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
//     time present (10¢ a day with engagement, decayed with H). The
//     operator's accounts are ordinary nodes with their own costs; anyone
//     passes on only what it holds above node_cap.
//
// The flow is flowDetail in both. Run: go test -run TestAnchor -v ./internal/trust.

const (
	aH       = 90 // the one half-life, days
	aOps     = 4  // hugo, weaver, khepri, skitter
	aDayCost = 10 // time present: cents per day with engagement
)

var aOpNames = [aOps]string{"hugo", "weaver", "khepri", "skitter"}

type aAcct struct {
	proof       int64   // priced proofs (wallet, github, domain, pow rows)
	hist, spend float64 // time present and money spent here, decayed with H
	rate        float64 // chance the account is active on a day
	joined      int
}

func (a aAcct) costs() float64 { return float64(a.proof) + a.hist + a.spend }

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
	a := aAcct{joined: day, rate: 0.01}
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
		s.acct = append(s.acct, aAcct{rate: 1})
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
	f := math.Exp2(-1.0 / aH)
	for i := 0; i < 5; i++ {
		s.join(s.day)
	}
	evidence := []float64{1, 1, 0.4, 0.3, 0.2}
	for i := range s.acct {
		a := &s.acct[i]
		a.hist, a.spend = a.hist*f, a.spend*f
		if i < aOps || s.rng.Float64() >= a.rate {
			continue
		}
		if s.rng.IntN(2) == 0 { // the day drew engagement
			a.hist = min(a.hist+aDayCost, 900)
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
	}
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
