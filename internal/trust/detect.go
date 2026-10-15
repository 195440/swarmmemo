package trust

import (
	"sort"
	"strconv"
	"strings"
)

func evidenceID(kind string, D int64, members []string) string {
	return kind + "-" + sha256Hex([]byte(kind + "\n" + strconv.FormatInt(D, 10) + "\n" + strings.Join(members, ",")))[:24]
}

// detect runs the versioned detectors (RFC 0021 §8, RFC 0012 §4.5).
// Evidence is mechanical and public only: ledger transfers, claims, payments,
// proofs and the judgement graph. Reports, hides and model verdicts are
// never inputs.
//
// funnel: one account receives transfers from ≥ funnel_k distinct accounts in
// the last funnel_days, each of which spent ≤ funnel_spend_ppm of what it
// claimed on those days. Members: the recipient and those senders.
//
// ring: a strongly connected set of ≥ ring_min roots in which every member
// takes ≥ ring_inside_ppm of its inbound edge capacity from inside the set,
// and which shares an account with a funnel. Members: the roots' accounts.
//
// shared_wallet: two roots (keys and verified domains) that share one live
// wallet or payout address and judged or paid each other within
// funded_days. Members: those roots' accounts.
func (r *run) detect(g *Graph, set judgementSet, payments []Record, funded map[[2]string]bool, baseRoot func(string) string) []EvidenceOut {
	p, snap, asOf, D := r.p, r.snap, r.asOf, r.D
	d := p.Detectors
	out := []EvidenceOut{}
	accountsOf := map[string][]string{}
	for _, a := range r.accounts {
		root := r.rootOf(a)
		accountsOf[root] = append(accountsOf[root], a)
	}

	// funnel
	from := asOf - d.FunnelDays*day
	claimed := map[string]int64{}
	spent := map[string]int64{}
	for _, x := range snap.Claims {
		if x.Day >= D-d.FunnelDays && x.Day < D {
			claimed[x.Account] += x.Claimed
			spent[x.Account] += x.Spent
		}
	}
	senders := map[string]map[string]bool{}
	for _, x := range snap.Transfers {
		if x.CreatedAt < from || x.CreatedAt >= asOf || x.From == x.To || x.From == "" || x.To == "" || x.Amount <= 0 {
			continue
		}
		if senders[x.To] == nil {
			senders[x.To] = map[string]bool{}
		}
		senders[x.To][x.From] = true
	}
	recipients := make([]string, 0, len(senders))
	for x := range senders {
		recipients = append(recipients, x)
	}
	sort.Strings(recipients)
	inFunnel := map[string]string{}
	for _, to := range recipients {
		var farm []string
		for s := range senders[to] {
			if claimed[s] > 0 && spent[s]*1e6 <= d.FunnelSpendPPM*claimed[s] {
				farm = append(farm, s)
			}
		}
		if int64(len(farm)) < d.FunnelK {
			continue
		}
		members := append(farm, to)
		sort.Strings(members)
		ev := EvidenceOut{ID: evidenceID("funnel", D, members), Kind: "funnel", Members: members, DetectorVersion: d.Version,
			Detail: string(Canonical(map[string]any{"recipient": to, "senders": len(farm), "window_days": d.FunnelDays}))}
		for _, m := range members {
			if inFunnel[m] == "" {
				inFunnel[m] = ev.ID
			}
		}
		out = append(out, ev)
	}

	// ring
	if len(inFunnel) > 0 {
		edges := make([]gEdge, len(g.Src))
		for k := range g.Src {
			edges[k] = gEdge{g.Src[k], g.Dst[k], g.EdgeCap[k]}
		}
		for _, scc := range stronglyConnected(len(g.Labels), edges) {
			if int64(len(scc)) < d.RingMin {
				continue
			}
			inside := map[int32]bool{}
			for _, v := range scc {
				inside[v] = true
			}
			in, total := map[int32]int64{}, map[int32]int64{}
			for _, e := range edges {
				if inside[e.dst] {
					total[e.dst] += e.w
					if inside[e.src] {
						in[e.dst] += e.w
					}
				}
			}
			ok, funnel := true, ""
			var members []string
			for _, v := range scc {
				if total[v] == 0 || in[v]*1e6 < d.RingInsidePPM*total[v] {
					ok = false
				}
				members = append(members, accountsOf[g.Labels[v]]...)
			}
			sort.Strings(members)
			for _, m := range members {
				if id := inFunnel[m]; id != "" && (funnel == "" || id < funnel) {
					funnel = id
				}
			}
			if !ok || funnel == "" {
				continue
			}
			out = append(out, EvidenceOut{ID: evidenceID("ring", D, members), Kind: "ring", Members: members, DetectorVersion: d.Version,
				Detail: string(Canonical(map[string]any{"roots": len(scc), "funnel": funnel}))})
		}
	}

	// shared_wallet: base roots grouped by a live address they share.
	byAddress := map[string]map[string]bool{}
	note := func(address, account string) {
		if address == "" || account == "" {
			return
		}
		if byAddress[address] == nil {
			byAddress[address] = map[string]bool{}
		}
		byAddress[address][baseRoot(account)] = true
	}
	fresh := asOf - p.ProofFreshDays*day
	for _, x := range snap.Proofs {
		if x.Kind == "wallet" && x.State == "verified" && x.CheckedAt >= fresh && x.CheckedAt <= asOf && x.CreatedAt <= asOf && x.Root != "" {
			note(x.Root, x.Account)
		}
	}
	for _, x := range payments {
		if x.To != "" && x.LinkValue != "" && x.CreatedAt < asOf {
			note(x.LinkValue, x.To)
		}
	}
	addresses := make([]string, 0, len(byAddress))
	for a, roots := range byAddress {
		if len(roots) >= 2 {
			addresses = append(addresses, a)
		}
	}
	sort.Strings(addresses)
	if len(addresses) > 0 {
		// Interactions between base roots within funded_days: a judgement of
		// a subject the other owns, a payment or a transfer.
		since := asOf - p.HalfLifeDays*day
		touched := map[[2]string]bool{}
		touch := func(a, b string) {
			ra, rb := baseRoot(a), baseRoot(b)
			if ra != rb {
				touched[[2]string{ra, rb}], touched[[2]string{rb, ra}] = true, true
			}
		}
		for _, s := range set.subjects {
			for _, e := range set.bySubject[s] {
				if e.j.At < since {
					continue
				}
				for o := range set.owners[s] {
					touch(e.j.Author, o)
				}
			}
		}
		for k := range funded {
			touch(k[0], k[1])
		}
		for _, addr := range addresses {
			var roots []string
			for root := range byAddress[addr] {
				roots = append(roots, root)
			}
			sort.Strings(roots)
			hit := map[string]bool{}
			for i, a := range roots {
				for _, b := range roots[i+1:] {
					if touched[[2]string{a, b}] {
						hit[a], hit[b] = true, true
					}
				}
			}
			if len(hit) < 2 {
				continue
			}
			var members []string
			for _, a := range r.accounts {
				if hit[baseRoot(a)] {
					members = append(members, a)
				}
			}
			if len(members) == 0 {
				continue
			}
			out = append(out, EvidenceOut{ID: evidenceID("shared_wallet", D, members), Kind: "shared_wallet", Members: members, DetectorVersion: d.Version,
				Detail: string(Canonical(map[string]any{"address": addr, "roots": len(hit)}))})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// stronglyConnected returns the strongly connected components (Tarjan).
func stronglyConnected(n int, edges []gEdge) [][]int32 {
	adj := make([][]int32, n)
	for _, e := range edges {
		adj[e.src] = append(adj[e.src], e.dst)
	}
	index := make([]int32, n)
	low := make([]int32, n)
	on := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int32
	var out [][]int32
	var next int32
	var visit func(v int32)
	visit = func(v int32) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		on[v] = true
		for _, w := range adj[v] {
			if index[w] < 0 {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if on[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var comp []int32
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				on[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			out = append(out, comp)
		}
	}
	for v := int32(0); v < int32(n); v++ {
		if index[v] < 0 {
			visit(v)
		}
	}
	return out
}
