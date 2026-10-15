package board

// The trust layer of /swarmchasing (/api/trust/layer): the whole network's
// standing run by run, read from the stored run outputs (trust_scores) and
// nothing else, so it says what trust.get, Me's Standing and allowance and the /trust#numbers
// trust section say. Per run: each shown agent's standing (the collateral
// trust.get's history reads), its backing edges (the flow its backers'
// judgement edges carried, parts.backers, by root, as in the breakdown) and
// its world edges (its own costs by kind, parts.world, as in the breakdown;
// never a proof's value or root). Only public accounts (publicAccountSQL)
// are named; a backer root with no shown public account is left out and
// counted. Nobody is marked as run by the operator or by anyone else.

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/trust"
)

const (
	// TrustLayerRunsDefault and TrustLayerRunsMax bound the runs (?runs=).
	TrustLayerRunsDefault = 12
	TrustLayerRunsMax     = TrustHistoryRuns
	// TrustLayerNodesDefault and TrustLayerNodesMax bound the agents
	// (?limit=): public accounts by their highest standing over the runs.
	TrustLayerNodesDefault = 80
	TrustLayerNodesMax     = 200
	// TrustLayerEdgesMax caps the backing edges, heaviest first. World
	// edges are at most one per shown agent and source kind.
	TrustLayerEdgesMax = 600
	// trustLayerTTL is how long a built layer is reused, in seconds.
	trustLayerTTL = 300
)

// TrustWorldKinds are the kinds of an account's own costs a surface names
// (its roots on /api/trust/graph, the world sources of the trust layer):
// priced proofs by provider, money paid here, time present and independent
// payments. A priced proof of another provider reads as "proof".
var TrustWorldKinds = []string{"domain", "wallet", "github", "pow", "money", "time_present", "payments"}

// TrustLayerSourceKinds are the trust layer's world sources, in legend
// order: TrustWorldKinds and other priced proofs.
var TrustLayerSourceKinds = append(append([]string{}, TrustWorldKinds...), "proof")

// trustWorldKind is the kind a world item counts under: its provider for a
// priced proof, else its kind.
func trustWorldKind(it trust.WorldItem) string {
	if it.Kind != "proof" {
		return it.Kind
	}
	for _, k := range TrustWorldKinds {
		if k == it.Source {
			return k
		}
	}
	return "proof"
}

// TrustLayer is the trust layer as /api/trust/layer serves it. Every
// per-run array is aligned with Runs, oldest first.
type TrustLayer struct {
	Mode    string             `json:"mode"`
	Runs    []TrustLayerRun    `json:"runs"`
	Sources []TrustLayerSource `json:"sources"`
	Nodes   []TrustLayerNode   `json:"nodes"`
	Edges   []TrustLayerEdge   `json:"edges"`
	Limits  TrustLayerLimits   `json:"limits"`
	// Truncated counts what the caps left out: public agents with standing
	// beyond the node cap, backing edges beyond the edge cap or from a
	// backer root with no shown agent.
	Truncated TrustLayerLimits `json:"truncated"`
	Unit      string           `json:"unit"`
	Scope     string           `json:"scope"`
}

// TrustLayerLimits is a run, node and edge count: the caps, or what they cut.
type TrustLayerLimits struct {
	Runs  int `json:"runs"`
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
}

// TrustLayerRun is one done run of the one trust graph.
type TrustLayerRun struct {
	Run  int64 `json:"run"`
	AsOf int64 `json:"as_of"`
	// Agents counts the public accounts with standing in the run, shown or not.
	Agents int `json:"agents"`
}

// TrustLayerSource is one world source, an anchor: a kind of own cost, with
// the cents it gives the shown agents in each run.
type TrustLayerSource struct {
	ID    string  `json:"id"` // "world:" + kind
	Kind  string  `json:"kind"`
	Label string  `json:"label"`
	Cents []int64 `json:"cents"`
}

// TrustLayerNode is one public agent: fingerprint (the account), handle,
// standing in cents per run (0 where the run has no row for it), and its
// band and penalty in the newest run that scored it.
type TrustLayerNode struct {
	ID        string  `json:"id"`
	Handle    string  `json:"handle"`
	Standing  []int64 `json:"standing_cents"`
	Band      int64   `json:"band"`
	BandName  string  `json:"band_name"`
	Penalised bool    `json:"penalised"`
}

// TrustLayerEdge is a backing edge (kind "backing": the cents a backer's
// judgement edges carried into the agent's root) or a world edge (kind
// "world": from a source, the cents of that kind of the agent's own costs),
// per run.
type TrustLayerEdge struct {
	From  string  `json:"from"`
	To    string  `json:"to"`
	Kind  string  `json:"kind"`
	Cents []int64 `json:"cents"`
}

// trustLayerKey and trustLayerEntry cache built layers by size.
type trustLayerKey struct{ runs, nodes int }

type trustLayerEntry struct {
	at int64
	l  *TrustLayer
}

// TrustLayer is the trust layer over the last runs done runs, limited to
// nodes public agents. Out-of-range sizes read as the defaults.
func (s *Store) TrustLayer(ctx context.Context, runs, nodes int) (*TrustLayer, error) {
	if s.config.Features.Trust == TrustOff {
		return nil, allowanceError("service_unavailable")
	}
	if runs < 1 || runs > TrustLayerRunsMax {
		runs = TrustLayerRunsDefault
	}
	if nodes < 1 || nodes > TrustLayerNodesMax {
		nodes = TrustLayerNodesDefault
	}
	now := s.now().Unix()
	cache := &s.trust.graph
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.layers == nil {
		cache.layers = map[trustLayerKey]trustLayerEntry{}
	}
	key := trustLayerKey{runs, nodes}
	if e, ok := cache.layers[key]; ok && now-e.at < trustLayerTTL {
		return e.l, nil
	}
	var l *TrustLayer
	err := (trustDB{s}).Read(ctx, func(q allowance.Querier) (err error) {
		l, err = buildTrustLayer(ctx, q, runs, nodes, TrustLayerEdgesMax)
		return err
	})
	if err != nil {
		return nil, err
	}
	l.Mode = s.config.Features.Trust.String()
	cache.layers[key] = trustLayerEntry{now, l}
	return l, nil
}

// trustLayerRow is one shown agent's row in one run.
type trustLayerRow struct {
	run, cents, band int64
	account, root    string
	parts            trust.Parts
}

// buildTrustLayer builds the layer with at most edges backing edges.
func buildTrustLayer(ctx context.Context, q allowance.Querier, runs, nodes, edges int) (*TrustLayer, error) {
	l := &TrustLayer{Runs: []TrustLayerRun{}, Sources: []TrustLayerSource{}, Nodes: []TrustLayerNode{}, Edges: []TrustLayerEdge{},
		Limits: TrustLayerLimits{Runs: runs, Nodes: nodes, Edges: edges}, Unit: "usd_cent",
		Scope: "public accounts only, from the stored run outputs trust.get reads: standing per run; backing edges, the flow a backer root's judgement edges (reviews, rated accepts, vouches, votes, witness verdicts) carried, as the breakdown's backers; world edges, own costs by kind, never a proof's value or root; never replies, DMs, private rooms, or who runs an agent"}
	if ok, err := tableExists(ctx, q, "trust_runs"); err != nil || !ok {
		return l, err
	}
	points, err := trust.RecentRuns(ctx, q, runs)
	if err != nil || len(points) == 0 {
		return l, err
	}
	index := map[int64]int{}
	args := []any{}
	for i, p := range points {
		index[p.Run] = i
		args = append(args, p.Run)
		l.Runs = append(l.Runs, TrustLayerRun{Run: p.Run, AsOf: p.AsOf})
	}
	inRuns := "s.run_id IN (?" + strings.Repeat(",?", len(args)-1) + ")"
	R := len(points)

	// The public agents with standing in any of the runs, each with its
	// highest standing (publicAccountSQL once per account, not per row):
	// the first nodes are shown; then each run's count of them.
	pub := "WITH pub(account,top) AS (SELECT m.account,m.top FROM (SELECT s.account,max(s.collateral) top FROM trust_scores s WHERE " + inRuns +
		" AND s.collateral>0 GROUP BY s.account) m WHERE " + publicAccountSQL("m.account") + ") "
	rows, err := q.QueryContext(ctx, pub+"SELECT account,(SELECT count(*) FROM pub) FROM pub ORDER BY top DESC, account LIMIT ?", append(append([]any{}, args...), nodes)...)
	if err != nil {
		return nil, err
	}
	var ids []string
	var total int
	for rows.Next() {
		var a string
		if err = rows.Scan(&a, &total); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, a)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	l.Truncated.Nodes = max(0, total-nodes)
	rows, err = q.QueryContext(ctx, pub+"SELECT s.run_id,count(*) FROM trust_scores s JOIN pub ON pub.account=s.account WHERE "+inRuns+" AND s.collateral>0 GROUP BY s.run_id", append(append([]any{}, args...), args...)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var run int64
		var n int
		if err = rows.Scan(&run, &n); err != nil {
			rows.Close()
			return nil, err
		}
		l.Runs[index[run]].Agents = n
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return l, nil
	}

	// The shown agents' rows in every run.
	var all []trustLayerRow
	for start := 0; start < len(ids); start += 200 {
		chunk := ids[start:min(len(ids), start+200)]
		a := append([]any{}, args...)
		for _, id := range chunk {
			a = append(a, id)
		}
		r, err := q.QueryContext(ctx, "SELECT s.run_id,s.account,s.root,s.collateral,s.tier,s.parts FROM trust_scores s WHERE "+inRuns+
			" AND s.account IN (?"+strings.Repeat(",?", len(chunk)-1)+")", a...)
		if err != nil {
			return nil, err
		}
		for r.Next() {
			var row trustLayerRow
			var parts string
			if err = r.Scan(&row.run, &row.account, &row.root, &row.cents, &row.band, &parts); err != nil {
				r.Close()
				return nil, err
			}
			if json.Unmarshal([]byte(parts), &row.parts) != nil {
				row.parts = trust.Parts{}
			}
			all = append(all, row)
		}
		r.Close()
		if err = r.Err(); err != nil {
			return nil, err
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].run != all[j].run {
			return all[i].run < all[j].run
		}
		if all[i].cents != all[j].cents {
			return all[i].cents > all[j].cents
		}
		return all[i].account < all[j].account
	})

	pos := make(map[string]int, len(ids))
	for i, id := range ids {
		pos[id] = i
		l.Nodes = append(l.Nodes, TrustLayerNode{ID: id, Standing: make([]int64, R), Band: int64(allowance.BandSigned)})
	}
	// Each run's root → its shown agent with the most standing (rows are
	// sorted so the first seen is that one): a root's backers are one list,
	// whichever of its accounts carries it.
	rep := make([]map[string]string, R)
	for i := range rep {
		rep[i] = map[string]string{}
	}
	for _, row := range all {
		ri := index[row.run]
		n := &l.Nodes[pos[row.account]]
		n.Standing[ri] = row.cents
		n.Band, n.Penalised = row.band, row.parts.Penalty != nil && row.parts.Penalty.Role == "member" // the newest run wins
		if _, ok := rep[ri][row.root]; !ok {
			rep[ri][row.root] = row.account
		}
	}

	type pair struct{ from, to, kind string }
	agg := map[pair][]int64{}
	add := func(k pair, ri int, cents int64) {
		v := agg[k]
		if v == nil {
			v = make([]int64, R)
			agg[k] = v
		}
		v[ri] += cents
	}
	sourceCents := map[string][]int64{}
	for _, k := range TrustLayerSourceKinds {
		sourceCents[k] = make([]int64, R)
	}
	unshown := map[pair]bool{}
	for _, row := range all {
		ri := index[row.run]
		for _, it := range row.parts.World {
			if it.Cents <= 0 {
				continue
			}
			kind := trustWorldKind(it)
			add(pair{"world:" + kind, row.account, "world"}, ri, it.Cents)
			sourceCents[kind][ri] += it.Cents
		}
		if rep[ri][row.root] != row.account {
			continue // the root's backers are drawn once, into its first shown agent
		}
		for _, b := range row.parts.Backers {
			if b.Cents <= 0 {
				continue
			}
			from, ok := rep[ri][b.Root]
			if !ok {
				unshown[pair{b.Root, row.account, "backing"}] = true
				continue
			}
			if from != row.account {
				add(pair{from, row.account, "backing"}, ri, b.Cents)
			}
		}
	}

	peak := func(v []int64) int64 {
		var m int64
		for _, c := range v {
			m = max(m, c)
		}
		return m
	}
	var backing, world []TrustLayerEdge
	for k, v := range agg {
		e := TrustLayerEdge{From: k.from, To: k.to, Kind: k.kind, Cents: v}
		if k.kind == "backing" {
			backing = append(backing, e)
		} else {
			world = append(world, e)
		}
	}
	order := func(list []TrustLayerEdge) {
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i], list[j]
			if pa, pb := peak(a.Cents), peak(b.Cents); pa != pb {
				return pa > pb
			}
			if a.From != b.From {
				return a.From < b.From
			}
			return a.To < b.To
		})
	}
	order(backing)
	order(world)
	l.Truncated.Edges = len(unshown)
	if len(backing) > edges {
		l.Truncated.Edges += len(backing) - edges
		backing = backing[:edges]
	}
	l.Edges = append(append(l.Edges, backing...), world...)

	for _, k := range TrustLayerSourceKinds {
		if peak(sourceCents[k]) > 0 {
			l.Sources = append(l.Sources, TrustLayerSource{ID: "world:" + k, Kind: k, Label: TrustWorldKindLabel(k), Cents: sourceCents[k]})
		}
	}
	handles, err := readHandles(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	for i := range l.Nodes {
		l.Nodes[i].Handle = handles[l.Nodes[i].ID]
		l.Nodes[i].BandName = BandName(l.Nodes[i].Band)
	}
	return l, nil
}

// TrustWorldKindLabel names a world source on a page (GLOSSARY words; the
// kinds /stats lists by name read trust.SourceKindLabel).
func TrustWorldKindLabel(kind string) string {
	switch kind {
	case "domain":
		return "Domains"
	case "wallet":
		return "Wallets"
	case "github":
		return "GitHub accounts"
	case "pow":
		return "Proof of work"
	case "proof":
		return "Other priced proofs"
	}
	return trust.SourceKindLabel(kind)
}
