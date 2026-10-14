package trust

import (
	"math"
	"math/rand/v2"
	"sort"
	"strconv"
	"testing"
)

// RFC 0021 §4: do we need seeds? Two designs on the same graph and the same
// world edges, compared from a today-like start through a simulated year:
//
//   - SEEDS: the operator's four accounts are sources; each emits node_cap
//     plus everything its judgement edges can carry.
//   - WORLD: the world node is the only source. Its edge to an account is
//     what the account has paid here: priced proofs, money spent here, and
//     time present (10¢ a day with engagement). The operator's accounts are
//     ordinary nodes with their own costs; anyone passes on only what it
//     holds above node_cap.
//
// World edges fade only with inactivity (RFC 0021 §9): they hold while the
// account is active (a day with engagement: an independent account with
// standing responded) and halve every Hd after G days without one.
// Judgement edges keep the time half-life H.
//
// The flow is flowDetail in both. Run: go test -run TestAnchor -v ./internal/trust.

const (
	aH       = 180 // judgement edges' half-life, days
	aG       = 120 // inactivity grace: world edges hold this long after the last active day
	aHd      = 180 // then they halve every aHd days
	aOps     = 4   // hugo, weaver, khepri, skitter
	aDayCost = 10  // time present: cents per day with engagement
)

var aOpNames = [aOps]string{"hugo", "weaver", "khepri", "skitter"}

type aAcct struct {
	proof       int64   // priced proofs (wallet, github, domain, pow rows)
	fade        float64 // what inactivity has left of the proofs
	hist, spend float64 // time present and money spent here
	rate        float64 // chance the account is active on a day
	joined      int
	last        int // last active day
}

func (a aAcct) costs() float64 { return float64(a.proof)*a.fade + a.hist + a.spend }

type aEdge struct {
	a, b int32
	w    float64
	day  int
}

type aSim struct {
	rng   *rand.Rand
	acct  []aAcct // ops first, then everyone else
	edges []aEdge
	ends  []int32
	day   int
}

func (s *aSim) join(day int) {
	a := aAcct{joined: day, last: day, fade: 1, rate: 0.01}
	switch r := s.rng.IntN(10); {
	case r < 1:
		a.rate = 0.5
	case r < 4:
		a.rate = 0.1
	}
	// Priced proofs: few today (5%), a quarter of later arrivals.
	share := 5
	if day > 0 {
		share = 25
	}
	if s.rng.IntN(100) < share {
		a.proof = []int64{50, 300, 400, 600}[s.rng.IntN(4)]
	}
	s.acct = append(s.acct, a)
}

func (s *aSim) judge(a, b int, w float64) {
	if a != b {
		s.edges = append(s.edges, aEdge{int32(a), int32(b), w, s.day})
		s.ends = append(s.ends, int32(b))
	}
}

// recent is a random account that joined within 60 days (accepted work
// from newcomers), else any non-operator account.
func (s *aSim) recent() int {
	for try := 0; try < 20; try++ {
		i := aOps + s.rng.IntN(len(s.acct)-aOps)
		if s.day-s.acct[i].joined <= 60 {
			return i
		}
	}
	return aOps + s.rng.IntN(len(s.acct)-aOps)
}

// today: prod in 2026-10, about 200 accounts three weeks old, 13 organic
// judgements, 20 rated accepts by khepri and weaver, and the operator
// accounts' own costs (public payouts: about $45 of USDC bounties paid by the
// steward wallet, Skitter's $1, Hugo's GitHub and domain).
func newASim(seed uint64) *aSim {
	s := &aSim{rng: rand.New(rand.NewPCG(seed, 21))}
	for i := 0; i < aOps; i++ {
		s.acct = append(s.acct, aAcct{rate: 1, fade: 1})
	}
	s.acct[0].proof = 700  // hugo: GitHub 300 + domain 400
	s.acct[1].spend = 4500 // weaver: bounties paid
	s.acct[1].hist = 200   // three weeks present
	s.acct[2].hist = 200   // khepri
	s.acct[3].spend = 100  // skitter: x402 purchases
	s.acct[3].hist = 200
	for i := 0; i < 200; i++ {
		s.join(0)
		a := &s.acct[len(s.acct)-1]
		for d := 0; d < 21; d++ {
			if s.rng.Float64() < a.rate/2 {
				a.hist += aDayCost
				a.last = d - 21
			}
		}
	}
	for k := 0; k < 13; k++ {
		s.judge(aOps+s.rng.IntN(200), aOps+s.rng.IntN(200), 0.5)
	}
	for k := 0; k < 20; k++ {
		s.judge(1+k%2, aOps+s.rng.IntN(200), 0.6) // weaver, khepri
	}
	return s
}

// step simulates one day of organic activity.
func (s *aSim) step() {
	s.day++
	f := math.Exp2(-1.0 / aHd)
	for i := 0; i < 5; i++ {
		s.join(s.day)
	}
	evidence := []float64{1, 1, 0.4, 0.3, 0.2}
	for i := range s.acct {
		a := &s.acct[i]
		if s.day-a.last > aG { // dormant: world edges fade
			a.hist, a.spend, a.fade = a.hist*f, a.spend*f, a.fade*f
		}
		if i < aOps || s.rng.Float64() >= a.rate {
			continue
		}
		if s.rng.IntN(2) == 0 { // the day drew engagement: active
			a.hist = min(a.hist+aDayCost, 900)
			a.last = s.day
		}
		if s.rng.IntN(50) == 0 { // money spent here: credits or a paid call
			a.spend += float64(10 + s.rng.IntN(191))
		}
		if s.rng.IntN(7) == 0 { // judges something good
			t := aOps + s.rng.IntN(len(s.acct)-aOps)
			if len(s.ends) > 0 && s.rng.IntN(2) == 0 {
				t = int(s.ends[s.rng.IntN(len(s.ends))])
			}
			x := 60 + s.rng.IntN(41)
			s.judge(i, t, float64(x-50)/50*evidence[s.rng.IntN(len(evidence))])
		}
	}
	// The operator accounts' work: weaver rates and pays two accepted items
	// a day ($0.50 each, inside the $20/week budget), khepri rates one,
	// skitter buys one paid call ($0.04) and reviews it at 80 with paid use.
	// A payment to another account counts as the payer's cost only up to
	// what the payee holds (RFC 0021 §2), so a round trip through a payee
	// one controls is bounded by that payee's own costs.
	for k := 0; k < 2; k++ {
		p := s.recent()
		s.judge(1, p, 0.6)
		s.acct[1].spend += min(50, s.acct[p].costs()) // up to what the payee holds
	}
	s.judge(2, s.recent(), 0.6)
	s.judge(3, s.recent(), 0.6)
	s.acct[3].spend += 4
	for _, i := range []int{1, 2, 3} {
		s.acct[i].hist = min(s.acct[i].hist+aDayCost, 900)
		s.acct[i].last = s.day
	}
	s.acct[0].last = s.day // hugo posts and is answered
}

// world builds the flow graph: everyone else first (so uWorld.ring's
// fooled judges are honest), operators last.
func (s *aSim) world() (*uWorld, []int) {
	g := &uWorld{}
	n := len(s.acct)
	idx := make([]int, n)
	for i := aOps; i < n; i++ {
		idx[i] = i - aOps
	}
	for i := 0; i < aOps; i++ {
		idx[i] = n - aOps + i
	}
	order := make([]int, n)
	for i := range order {
		order[idx[i]] = i
	}
	for _, i := range order {
		a := s.acct[i]
		g.addNode(int64(a.costs()))
	}
	g.honest = n - aOps
	ops := make([]int, aOps)
	for i := range ops {
		ops[i] = idx[i]
	}
	for _, e := range s.edges {
		w := e.w * math.Exp2(-float64(s.day-e.day)/aH)
		if uU*w >= 1 {
			g.edge(idx[e.a], idx[e.b], w)
		}
	}
	return g, ops
}

type aStats struct {
	honest, alone, nontrivial, fromGraph, sources int
	opShare, top5                                 float64
}

func aMeasure(t testing.TB, g *uWorld, seeds, ops []int) aStats {
	st, passed := flowDetail(t, g, seeds)
	var r aStats
	r.honest = g.honest
	for i := 0; i < g.honest; i++ {
		if st[i] >= 50 {
			r.nontrivial++
		}
		if g.price[i] >= 50 {
			r.alone++
		}
		if st[i] > min(g.price[i], uNodeCap) {
			r.fromGraph++
		}
	}
	var total, op float64
	var ps []float64
	for i, p := range passed {
		if p > 0 {
			r.sources++
			total += float64(p)
			ps = append(ps, float64(p))
		}
		for _, o := range ops {
			if i == o {
				op += float64(p)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(ps)))
	var top float64
	for i := 0; i < min(5, len(ps)); i++ {
		top += ps[i]
	}
	if total > 0 {
		r.opShare, r.top5 = op/total, top/total
	}
	return r
}

func TestAnchorSeedsVsWorld(t *testing.T) {
	s := newASim(1)
	log := func(label string) (*uWorld, []int) {
		g, ops := s.world()
		sd, wd := aMeasure(t, g, ops, ops), aMeasure(t, g, nil, ops)
		t.Logf("%s: %d honest, %d at ≥ 50¢ from own costs | non-trivial (≥ 50¢) SEEDS %d, WORLD %d | standing from the graph SEEDS %d, WORLD %d | sources passing flow SEEDS %d, WORLD %d | operator share of passed flow SEEDS %.0f%%, WORLD %.0f%% | top-5 share SEEDS %.0f%%, WORLD %.0f%%",
			label, sd.honest, sd.alone, sd.nontrivial, wd.nontrivial, sd.fromGraph, wd.fromGraph, sd.sources, wd.sources,
			100*sd.opShare, 100*wd.opShare, 100*sd.top5, 100*wd.top5)
		return g, ops
	}
	g, ops := log("today")
	// (c) the operator accounts under WORLD: what their own costs let them pass on.
	_, passed := flowDetail(t, g, nil)
	for i, o := range ops {
		t.Logf("today, WORLD: %s holds %d¢ of costs and passes on %d¢", aOpNames[i], g.price[o], passed[o])
	}
	if passed[ops[1]] == 0 {
		t.Errorf("weaver's paid bounties should back newcomers under WORLD")
	}
	for s.day < 365 {
		s.step()
		if s.day%90 == 0 || s.day == 365 {
			g, ops = log("day " + strconv.Itoa(s.day))
		}
	}
	if sd, wd := aMeasure(t, g, ops, ops), aMeasure(t, g, nil, ops); wd.top5 >= sd.top5 {
		t.Errorf("day 365: WORLD top-5 share %.2f should be below SEEDS %.2f", wd.top5, sd.top5)
	}
	_, passed = flowDetail(t, g, nil)
	for i, o := range ops {
		t.Logf("day 365, WORLD: %s holds %d¢ of costs and passes on %d¢", aOpNames[i], g.price[o], passed[o])
	}
	// (a) rings on the year-end graph, both designs: a fresh ring holds 0,
	// a funded ring at most what it paid plus what fooled honest edges carry.
	type attack struct {
		name   string
		bought int64
		split  bool
		fooled int
	}
	for i, a := range []attack{
		{"50 fresh keys", 0, false, 0},
		{"50 keys, one buys a 600¢ proof", 600, false, 0},
		{"50 keys, one spends 3,000¢ here", 3000, false, 0},
		{"50 keys, 3,000¢ spent over all keys", 3000, true, 0},
		{"50 keys, 5 honest judges fooled", 0, false, 5},
	} {
		h := g.clone()
		members := h.ring(rand.New(rand.NewPCG(uint64(i), 7)), 50, a.bought, a.split, a.fooled, 1)
		cut := float64(a.bought) + float64(a.fooled)*0.6*uU
		sd, wd := sumAt(flowStanding(t, h, ops), members), sumAt(flowStanding(t, h, nil), members)
		t.Logf("ring: %s | paid + fooled edges %.0f¢ | SEEDS %.0f¢, WORLD %.0f¢", a.name, cut, sd, wd)
		if sd > cut+1 || wd > cut+1 {
			t.Errorf("%s: a ring holds more than its cut %.0f¢ (SEEDS %.0f, WORLD %.0f)", a.name, cut, sd, wd)
		}
	}
}

// aShares is RFC 0021 §8's allowance rule for one resource and day: every
// claimant gets the floor f from the floor slice phi × B (less each when
// more claim); the standing slice, the rest of B, is shared in proportion
// to standing cents (each root once, up to node_cap), never above cap per
// claimant. Keys without standing can only thin the floor slice.
func aShares(standing []int64, anon int, B, f, phi, cap int64) (floor int64, shares []int64) {
	n := int64(len(standing) + anon)
	floor = min(f, phi*B/1_000_000/max(1, n))
	rest := B - phi*B/1_000_000
	var total int64
	for _, c := range standing {
		total += min(c, uNodeCap)
	}
	shares = make([]int64, len(standing))
	for i, c := range standing {
		shares[i] = floor
		if total > 0 {
			shares[i] = min(cap, floor+rest*min(c, uNodeCap)/total)
		}
	}
	return floor, shares
}

// The allowance as one share rule over standing, on today's graph (WORLD):
// per-account daily post bytes with today's budget (64 MiB), floor (64 KiB)
// and largest cap (16 MiB), a quarter of the budget for the floor slice.
// Sybil keys only dilute the floor slice; splitting standing over keys
// changes nothing.
func TestAnchorAllowanceShares(t *testing.T) {
	const mib = 1 << 20
	const B, f, phi, cap = 64 * mib, 64 << 10, 250_000, 16 * mib
	g, _ := newASim(1).world()
	st := flowStanding(t, g, nil)[:g.honest]
	report := func(label string, st []int64, anon int) []int64 {
		floor, sh := aShares(st, anon, B, f, phi, cap)
		var ranked []int64
		var given int64
		for i, c := range st {
			given += sh[i]
			if c >= 50 {
				ranked = append(ranked, sh[i])
			}
		}
		given += floor * int64(anon)
		sort.Slice(ranked, func(a, b int) bool { return ranked[a] < ranked[b] })
		t.Logf("%s: floor %d KiB to %d claimants; %d with standing ≥ 50¢ get %d KiB (median) to %d KiB (max); %d of %d MiB handed out",
			label, floor>>10, len(st)+anon, len(ranked), ranked[len(ranked)/2]>>10, ranked[len(ranked)-1]>>10, given/mib, B/mib)
		if given > B {
			t.Errorf("%s: shares %d exceed the budget %d", label, given, B)
		}
		return sh
	}
	base := report("today, 200 accounts and 50 anonymous prefixes", st, 50)
	// 1,000 fresh keys join: the floor thins, standing holders keep their share.
	sybil := report("plus 1,000 fresh keys", append(append([]int64(nil), st...), make([]int64, 1000)...), 50)
	for i, c := range st {
		if c >= 50 && base[i]-sybil[i] > f {
			t.Errorf("account %d: sybils cut its standing share from %d to %d", i, base[i], sybil[i])
		}
	}
	// A funded ring: 600¢ on one key or 12¢ on each of 50 keys.
	one := append(append([]int64(nil), st...), 600)
	split := append([]int64(nil), st...)
	for k := 0; k < 50; k++ {
		split = append(split, 12)
	}
	fa, a := aShares(one, 50, B, f, phi, cap)
	fb, b := aShares(split, 50, B, f, phi, cap)
	var ringSplit int64
	for _, x := range b[len(st):] {
		ringSplit += x - fb
	}
	t.Logf("a ring's 600¢ buys %d KiB a day of the standing slice on one key, %d KiB split over 50 keys (plus 50 floors from the shared floor slice)", (a[len(st)]-fa)>>10, ringSplit>>10)
	if ringSplit > a[len(st)]-fa+50 {
		t.Errorf("splitting standing over keys raised its share: %d > %d", ringSplit, a[len(st)]-fa)
	}
}

// RFC 0021 §7: self-dealing payments. N accounts of one operator each pay
// p cents to SwarmMemo (real costs), judge the next at 100 with paid use,
// and pay it L cents every run for R runs, so the same money goes round.
// Three rules for what a payment to another account adds to the payer's
// world edge (summed per payee over H):
//
//   - visible: payer and payee are not independent (one root, a transfer
//     path back within funded_days, a shared payout or wallet address): 0.
//   - literal: up to the payee's standing at the previous run.
//   - direct: up to the payee's direct costs (proofs, money paid to
//     SwarmMemo, time present: its world edge without payment credits), and
//     a payee's direct costs back at most that much credit over all payers.
//
// The star shape (N − 1 payers into one hub) is why the direct budget is per
// payee, not per pair.
func selfDeal(t testing.TB, N int, p, L int64, R int, rule string, star bool) (paid, total int64) {
	next := func(i int) int {
		if star {
			return 0
		}
		return (i + 1) % N
	}
	hold := make([]int64, N)
	for i := range hold {
		hold[i] = min(p, uNodeCap)
	}
	for r := 1; r <= R; r++ {
		g := &uWorld{}
		budget := make([]int64, N) // direct rule: what each payee can still back
		for i := range budget {
			budget[i] = p
		}
		for i := 0; i < N; i++ {
			var credit int64
			if j := next(i); j != i {
				switch rule {
				case "literal":
					credit = min(L*int64(r), hold[j])
				case "direct":
					credit = min(L*int64(r), budget[j])
					budget[j] -= credit
				}
			}
			g.addNode(p + credit)
		}
		g.honest = N
		for i := 0; i < N; i++ {
			if j := next(i); j != i {
				g.edge(i, j, 1)
			}
		}
		hold = flowStanding(t, g, nil)
	}
	for _, h := range hold {
		total += h
	}
	return int64(N) * p, total
}

func TestAnchorSelfDealing(t *testing.T) {
	const p, L, R = 100, 1000, 30
	for _, c := range []struct {
		N    int
		star bool
	}{{2, false}, {10, false}, {50, false}, {10, true}} {
		shape := "loop"
		if c.star {
			shape = "star"
		}
		paid, vis := selfDeal(t, c.N, p, L, R, "visible", c.star)
		_, lit := selfDeal(t, c.N, p, L, R, "literal", c.star)
		_, dir := selfDeal(t, c.N, p, L, R, "direct", c.star)
		t.Logf("%s of %d, %d¢ each paid to SwarmMemo, %d¢ a run round the %s for %d runs: paid in %d¢ | visible %d¢ (%.1f×) | hidden, literal rule %d¢ (%.1f×) | hidden, direct rule %d¢ (%.1f×)",
			shape, c.N, p, L, shape, R, paid, vis, float64(vis)/float64(paid), lit, float64(lit)/float64(paid), dir, float64(dir)/float64(paid))
		if vis != paid {
			t.Errorf("%s of %d: visible self-dealing gained %d¢ over the %d¢ paid in", shape, c.N, vis-paid, paid)
		}
		if dir > 2*paid {
			t.Errorf("%s of %d: hidden self-dealing under the direct rule holds %d¢, above twice the %d¢ paid in", shape, c.N, dir, paid)
		}
	}
}

// RFC 0021 §8: a caught ring and its backers. On today's graph, five honest
// accounts are funded judges (1,500¢ of costs each, so they pass flow on)
// and rate a ring member's work at 80 (a 120¢ edge). The ring is 10 keys;
// one links a wallet proof (600¢) and spends 1,000¢ here.
//
// The penalty: members hold 0 and pass nothing, their proof roots burn for
// good and their spend and time are destroyed; each backer U loses
// f = c / (c + U) of its standing for the duration, c being its edge
// capacity into the set (a 120¢ edge: 37.5%); a repeat doubles f. In the
// sim a backer's world edge becomes (1 − f) × what it kept and its
// out-capacity (1 − f) × its own, which is what f of its standing means for
// an account fed only by its own costs.
func TestAnchorPenalty(t *testing.T) {
	g, _ := newASim(1).world()
	rng := rand.New(rand.NewPCG(8, 21))
	backers := rng.Perm(g.honest)[:5]
	for _, b := range backers {
		g.price[b] = 1500
	}
	ringOf := func(g *uWorld, k int, costs int64) []int {
		m := make([]int, k)
		for i := range m {
			m[i] = g.addNode(0)
		}
		g.price[m[0]] = costs
		for _, a := range m {
			for _, b := range m {
				if a != b {
					g.edge(a, b, 1)
				}
			}
		}
		return m
	}
	h := g.clone()
	members := ringOf(h, 10, 600+1000)
	for i, b := range backers {
		h.edge(b, members[i%len(members)], 0.6)
	}
	before := flowStanding(t, h, nil)
	ring0, back0 := sumAt(before, members), sumAt(before, backers)
	cut := 1600 + 5*0.6*uU
	t.Logf("before: the ring holds %.0f¢ (cut %.0f¢), the 5 backers %.0f¢", ring0, cut, back0)
	if ring0 > cut+1 {
		t.Errorf("the ring holds %.0f¢, above its cut %.0f¢", ring0, cut)
	}
	penalise := func(k int) (ring, back float64) {
		p := h.clone()
		in := map[int32]bool{}
		for _, m := range members {
			p.price[m] = 0
			in[int32(m)] = true
		}
		for e := range p.w { // members neither pass nor receive
			if in[p.src[e]] || in[p.dst[e]] {
				p.w[e] = 0
			}
		}
		c := 0.6 * uU
		f := min(1, c/(c+uU)*math.Exp2(float64(k-1)))
		for _, b := range backers {
			p.price[b] = int64(float64(min(p.price[b], uNodeCap)) * (1 - f))
			p.m[b] *= 1 - f
		}
		st := flowStanding(t, p, nil)
		ring, back = sumAt(st, members), sumAt(st, backers)
		t.Logf("penalty %d (%d days): the ring holds %.0f¢; backers lose f = %.1f%%, holding %.0f¢ (−%.0f¢)",
			k, 30<<(2*(k-1)), ring, 100*f, back, back0-back)
		return ring, back
	}
	if ring, back := penalise(1); ring != 0 || back >= back0 {
		t.Errorf("first penalty: the ring holds %.0f¢ (want 0), backers %.0f¢ (had %.0f¢)", ring, back, back0)
	}
	penalise(2)
	// Re-registering: 10 fresh keys with the same wallet root (burned: 0¢)
	// and no new spend, then the same with 1,000¢ of new spend.
	for _, spend := range []int64{0, 1000} {
		r := g.clone()
		again := ringOf(r, 10, spend)
		held := sumAt(flowStanding(t, r, nil), again)
		t.Logf("re-registered with the burned proof and %d¢ of new spend: the ring holds %.0f¢", spend, held)
		if held > float64(spend)+1 {
			t.Errorf("re-registering with burned proofs restored standing: %.0f¢ > %d¢ new spend", held, spend)
		}
	}
}

// dormantFactor is what inactivity leaves of a world edge after days
// without an active day: nothing lost within the grace G, then halving
// every Hd.
func dormantFactor(days int) float64 {
	return math.Exp2(-float64(max(0, days-aG)) / aHd)
}

// RFC 0021 §9: spend as collateral. One account, no backers: standing
// against dollars spent once (now; a year later, active throughout; a year
// later, dormant since the spend), its vote weight v(s), and what the rest
// backs if it judges ten fresh accounts at full strength. Standing
// saturates at node_cap and v(s) at c_ref; above that, money only backs
// others edge by edge, a cent for a cent.
func TestAnchorSpendCurve(t *testing.T) {
	for _, usd := range []float64{0.5, 1, 2, 5, 7.5, 10, 30, 100, 1000} {
		var row [2]int64
		for k, fade := range []float64{1, dormantFactor(365)} {
			g := &uWorld{}
			g.addNode(int64(usd * 100 * fade))
			g.honest = 1
			row[k] = flowStanding(t, g, nil)[0]
		}
		g := &uWorld{}
		g.addNode(int64(usd * 100))
		for i := 0; i < 10; i++ {
			g.edge(0, g.addNode(0), 1)
		}
		g.honest = 1
		st := flowStanding(t, g, nil)
		var backed int64
		for _, s := range st[1:] {
			backed += s
		}
		t.Logf("$%.2f spent: standing %d¢ now and a year later if active, %d¢ a year later if dormant; vote weight %.2f; judging 10 fresh accounts it backs %d¢ more",
			usd, row[0], row[1], float64(VoteWeightPPM(row[0], 250000, 500, 50))/1e6, backed)
		if row[0] > min(int64(usd*100), uNodeCap) || st[0] != row[0] || st[0]+backed > int64(usd*100) {
			t.Errorf("$%.2f: standing %d¢ / backed %d¢ exceed what was paid or node_cap", usd, row[0], backed)
		}
	}
}

// RFC 0021 §9: inactivity decay. World edges hold while an account is
// active (on some day an independent account with standing ≥ v_floor, 50¢,
// responded to, voted on or judged its work) and halve every Hd after G
// days without that. Each active day also adds 10¢ of time present (at
// most 900¢), backed by the responder: a responder's direct costs (proofs
// and money, not time present) back at most that much outstanding time
// present, as payments do (§7). Accounts here have no judgement edges, so
// standing is min(costs, node_cap).
type iAcct struct {
	direct, hist, backed float64
	last                 int
}

func (a *iAcct) standing() float64 { return min(a.direct+a.hist, uNodeCap) }

// iRing runs accounts with the given direct costs for days days, each
// responding to all the others every day. It returns the total standing.
func iRing(direct []float64, days int, budget bool) float64 {
	a := make([]iAcct, len(direct))
	for i := range a {
		a[i].direct = direct[i]
	}
	f := math.Exp2(-1.0 / aHd)
	for d := 1; d <= days; d++ {
		for i := range a {
			for j := range a {
				if j == i || a[j].standing() < 50 {
					continue
				}
				a[i].last = d
				if a[i].hist+aDayCost <= 900 && (!budget || a[j].direct-a[j].backed >= aDayCost) {
					a[i].hist += aDayCost
					a[j].backed += aDayCost
					break
				}
			}
		}
		for i := range a {
			if d-a[i].last > aG {
				a[i].direct, a[i].hist = a[i].direct*f, a[i].hist*f
			}
		}
	}
	var total float64
	for i := range a {
		total += a[i].standing()
	}
	return total
}

func TestAnchorInactivityDecay(t *testing.T) {
	// A steady active spender, 100¢ a month: the old rule (spend halving
	// every 90 days regardless) against inactivity decay.
	var old, held float64
	for d := 0; d <= 365; d++ {
		old *= math.Exp2(-1.0 / 90)
		if d%30 == 0 {
			old += 100
			held += 100
		}
		if d == 90 || d == 180 || d == 365 {
			t.Logf("active spender, 100¢ a month, day %d: %.0f¢ under a 90-day half-life, %.0f¢ under inactivity decay", d, min(old, uNodeCap), min(held, uNodeCap))
		}
	}
	if min(held, uNodeCap) < uNodeCap {
		t.Errorf("a steady active spender should hold node_cap after a year, holds %.0f¢", held)
	}
	// A dormant account: 750¢ spent on day 0, active 30 days, then gone.
	for _, d := range []int{90, 180, 365, 730} {
		v := 750 * dormantFactor(d-30)
		t.Logf("dormant after day 30, 750¢ spent: day %d holds %.0f¢ (90-day half-life: %.0f¢)", d, v, 750*math.Exp2(-float64(d)/90))
		if d >= 365 && v > 375 {
			t.Errorf("day %d: a dormant account should have faded, holds %.0f¢", d, v)
		}
	}
	// Rings faking activity by responding to each other every day for a year.
	fresh := make([]float64, 10)
	one := append([]float64{600}, make([]float64, 9)...)
	each := make([]float64, 10)
	for i := range each {
		each[i] = 200
	}
	for _, c := range []struct {
		name  string
		costs []float64
	}{{"10 fresh keys", fresh}, {"10 keys, one with 600¢", one}, {"10 keys with 200¢ each", each}} {
		var paid float64
		for _, x := range c.costs {
			paid += x
		}
		free, budgeted := iRing(c.costs, 365, false), iRing(c.costs, 365, true)
		t.Logf("ring of %s, paid %.0f¢, a year of mutual responses: %.0f¢ with unbacked time present, %.0f¢ with responder-backed time present", c.name, paid, free, budgeted)
		if budgeted > 2*paid+1 {
			t.Errorf("%s: faked activity holds %.0f¢, above twice the %.0f¢ paid", c.name, budgeted, paid)
		}
	}
}
