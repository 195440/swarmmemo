package trust

import (
	"context"
	"math"
	"math/rand/v2"
	"sort"
	"testing"
	"time"

	"swarmmemo/internal/judge"
)

// RFC 0021's experiment: one graph (accounts plus a "world" node whose edges
// to proof holders carry each proof's price), judgement edges weighted
// |x − 50| / 50 × evidence, and trust as flow from the operator's seeds.
// Two propagation rules on the same graph, with rings attached:
//
//   - PPR: personalized PageRank in rule 2's form (c = seed + received −
//     seed × T / S), a node passing α × W / (K + W) of what it holds; K = 0 is
//     plain EigenTrust with pre-trusted peers.
//   - FLOW: the existing Dinic (flow.go). Each node first keeps its own
//     source (price, or a seed's own cap) up to node_cap; the rest of every
//     source then flows over the judgement edges (capacity U × w × accuracy)
//     into the nodes' sinks, filled progressively (max-min fair), never
//     above node_cap. A sink edge never loses flow in Dinic, so judging
//     never lowers what a node keeps from its own sources.
//
// The numbers it logs are RFC 0021 §3 and §5; the assertions are the
// bounds the RFC claims. Run: go test -run TestUnified -v ./internal/trust.

const (
	uU       = 200 // cents a full-weight judgement edge can carry
	uNodeCap = 750 // cents a node can hold (θ1)
)

var uLevels = []int64{25, 50, 100, 200, 400, 750}

type uWorld struct {
	n      int
	honest int
	seeds  []int
	price  []int64   // world → node capacity: the node's priced proofs, cents
	m      []float64 // accuracy multiplier on the node's out-edges
	src    []int32
	dst    []int32
	w      []float64 // |x − 50| / 50 × evidence
}

func (g *uWorld) addNode(price int64) int {
	g.price = append(g.price, price)
	g.m = append(g.m, 1)
	g.n++
	return g.n - 1
}

func (g *uWorld) edge(a, b int, w float64) {
	g.src, g.dst, g.w = append(g.src, int32(a)), append(g.dst, int32(b)), append(g.w, w)
}

func (g *uWorld) clone() *uWorld {
	c := *g
	c.seeds = append([]int(nil), g.seeds...)
	c.price = append([]int64(nil), g.price...)
	c.m = append([]float64(nil), g.m...)
	c.src = append([]int32(nil), g.src...)
	c.dst = append([]int32(nil), g.dst...)
	c.w = append([]float64(nil), g.w...)
	return &c
}

// honestWorld: n honest accounts, 40% with priced proofs (the C144 rows),
// each judging 0-8 others (preferential), 4 operator seeds each rating 40
// accounts' work at 80 with work evidence (w = 0.6).
func honestWorld(seed uint64, n int) *uWorld {
	rng := rand.New(rand.NewPCG(seed, 21))
	g := &uWorld{}
	prices := []int64{50, 300, 400, 600}
	for i := 0; i < n; i++ {
		var p int64
		if rng.IntN(10) < 4 {
			p = prices[rng.IntN(4)]
			if rng.IntN(4) == 0 {
				p += prices[rng.IntN(4)]
			}
		}
		g.addNode(p)
	}
	g.honest = n
	var ends []int32
	evidence := []float64{1, 1, 0.4, 0.3, 0.2}
	for i := 0; i < n; i++ {
		for k := rng.IntN(9); k > 0; k-- {
			t := rng.IntN(n)
			if len(ends) > 0 && rng.IntN(2) == 0 {
				t = int(ends[rng.IntN(len(ends))])
			}
			if t == i {
				continue
			}
			x := 60 + rng.IntN(41) // honest judgements of good work
			g.edge(i, t, float64(x-50)/50*evidence[rng.IntN(len(evidence))])
			ends = append(ends, int32(t))
		}
	}
	for s := 0; s < 4; s++ {
		id := g.addNode(0)
		g.seeds = append(g.seeds, id)
		for k := 0; k < 40; k++ {
			g.edge(id, rng.IntN(n), 0.6)
		}
	}
	return g
}

// ring attaches k fresh keys that all judge each other 100 with paid use
// (w = 1), buys `bought` cents of proof on one key (or split evenly when
// split), and has `attack` honest accounts judge a member at 80 with work
// evidence (w = 0.6). It returns the members.
func (g *uWorld) ring(rng *rand.Rand, k int, bought int64, split bool, attack int, m float64) []int {
	members := make([]int, k)
	for i := range members {
		members[i] = g.addNode(0)
		g.m[members[i]] = m
	}
	if split {
		for _, a := range members {
			g.price[a] = bought / int64(k)
		}
	} else {
		g.price[members[0]] = bought
	}
	for _, a := range members {
		for _, b := range members {
			if a != b {
				g.edge(a, b, 1)
			}
		}
	}
	for i := 0; i < attack; i++ {
		g.edge(rng.IntN(g.honest), members[rng.IntN(k)], 0.6)
	}
	return members
}

// outCap is a node's total out-edge capacity in cents.
func (g *uWorld) outCap() []int64 {
	out := make([]int64, g.n)
	for e := range g.src {
		out[g.src[e]] += int64(float64(uU) * g.w[e] * g.m[g.src[e]])
	}
	return out
}

// sources is what each node emits: a seed's own cap plus everything its
// judgements can carry (seeds have no other mass), a priced node its price.
func (g *uWorld) sources(seeds []int) []int64 {
	out := g.outCap()
	s := append([]int64(nil), g.price...)
	for _, a := range seeds {
		s[a] += uNodeCap + out[a]
	}
	return s
}

// flowStanding is the FLOW rule.
func flowStanding(t testing.TB, g *uWorld, seeds []int) []int64 {
	out, _ := flowDetail(t, g, seeds)
	return out
}

// flowDetail is the FLOW rule, also returning what each source passed on
// to others: its flow from the super source (a source keeps node_cap first,
// so its own sink takes none of it).
func flowDetail(t testing.TB, g *uWorld, seeds []int) ([]int64, []int64) {
	src := g.sources(seeds)
	n := g.n
	own := make([]int64, n)
	S, T := n, n+1
	net := newNetwork(context.Background(), n+2, 1<<62, 0)
	in := make([]int, n)
	for i := 0; i < n; i++ {
		own[i] = min(src[i], uNodeCap)
		in[i] = -1
		if r := src[i] - own[i]; r > 0 {
			in[i] = net.add(S, i, r)
		}
	}
	for e := range g.src {
		if c := int64(float64(uU) * g.w[e] * g.m[g.src[e]]); c > 0 {
			net.add(int(g.src[e]), int(g.dst[e]), c)
		}
	}
	sink := make([]int, n)
	set := make([]int64, n)
	for i := 0; i < n; i++ {
		sink[i] = net.add(i, T, 0)
	}
	for _, L := range uLevels {
		for i := 0; i < n; i++ {
			want := max(0, min(L, uNodeCap)-own[i])
			net.cap[sink[i]] += want - set[i]
			set[i] = want
		}
		if err := net.maxflow(S, T); err != nil {
			t.Fatal(err)
		}
	}
	out, passed := make([]int64, n), make([]int64, n)
	for i := range out {
		out[i] = own[i] + net.flowOn(sink[i])
		if in[i] >= 0 {
			passed[i] = net.flowOn(in[i])
		}
	}
	return out, passed
}

// pprStanding is the PPR rule (rule 2's form), keep weight K in edge
// weight units; K = 0 is plain personalized PageRank.
func pprStanding(g *uWorld, seeds []int, alpha, K float64) []float64 {
	src := g.sources(seeds)
	n := g.n
	s := make([]float64, n)
	var S float64
	for i := range s {
		s[i] = float64(src[i])
		S += s[i]
	}
	W := make([]float64, n)
	for e := range g.src {
		W[g.src[e]] += g.w[e] * g.m[g.src[e]]
	}
	c := append([]float64(nil), s...)
	recv := make([]float64, n)
	for it := 0; it < 60; it++ {
		for i := range recv {
			recv[i] = 0
		}
		var T float64
		for e := range g.src {
			a := g.src[e]
			share := alpha * W[a] / (K + W[a]) * c[a] * g.w[e] * g.m[a] / W[a]
			recv[g.dst[e]] += share
			T += share
		}
		for i := range c {
			c[i] = s[i]*(1-T/S) + recv[i]
		}
	}
	return c
}

func sumAt[T int64 | float64](v []T, ids []int) float64 {
	var s float64
	for _, i := range ids {
		s += float64(v[i])
	}
	return s
}

func voteWeights[T int64 | float64](v []T, ids []int) float64 {
	st := StandingJudgements()
	var s float64
	for _, i := range ids {
		s += float64(st.VoteWeight(int64(v[i]))) / 1e6
	}
	return s
}

type uRow struct {
	name                 string
	flow, plain, keep    float64
	flowV, plainV, keepV float64
	cost                 float64
}

func TestUnifiedRingsFlowVsPPR(t *testing.T) {
	if testing.Short() {
		t.Skip("RFC 0021 simulation")
	}
	const n = 10000
	base := honestWorld(1, n)
	type attack struct {
		name   string
		k      int
		bought int64
		split  bool
		edges  int
		m      float64
	}
	attacks := []attack{
		{"ring of 50 fresh keys", 50, 0, false, 0, 1},
		{"ring of 50, one key buys 200¢", 50, 200, false, 0, 1},
		{"ring of 50, 200¢ split over all keys", 50, 200, true, 0, 1},
		{"ring of 50, 5 honest judges fooled", 50, 0, false, 5, 1},
		{"same, ring accuracy ×2", 50, 0, false, 5, 2},
		{"ring of 10, one key buys 600¢", 10, 600, false, 0, 1},
	}
	var rows []uRow
	for i, a := range attacks {
		g := base.clone()
		rng := rand.New(rand.NewPCG(uint64(i), 7))
		members := g.ring(rng, a.k, a.bought, a.split, a.edges, a.m)
		f := flowStanding(t, g, g.seeds)
		p := pprStanding(g, g.seeds, 0.85, 0)
		kp := pprStanding(g, g.seeds, 0.85, 10)
		cut := float64(a.bought) + float64(a.edges)*0.6*uU
		rows = append(rows, uRow{a.name, sumAt(f, members), sumAt(p, members), sumAt(kp, members),
			voteWeights(f, members), voteWeights(p, members), voteWeights(kp, members), cut})
		// FLOW's bound: a ring holds at most what crosses its cut (bought
		// proofs plus fooled honest edges' capacity).
		if got := sumAt(f, members); got > cut+1 {
			t.Errorf("%s: flow gave the ring %.0f¢ above its cut %.0f¢", a.name, got, cut)
		}
	}
	t.Logf("| attack | cut ¢ | FLOW ¢ | PPR α=.85 ¢ | PPR keep K=10 ¢ | vote weight FLOW / PPR / keep |")
	for _, r := range rows {
		t.Logf("| %s | %.0f | %.0f | %.0f | %.0f | %.2f / %.2f / %.2f |", r.name, r.cost, r.flow, r.plain, r.keep, r.flowV, r.plainV, r.keepV)
	}
	// Honest side and graph contribution.
	f := flowStanding(t, base, base.seeds)
	p := pprStanding(base, base.seeds, 0.85, 10)
	var reachedF, reachedP, noProof, noProofReached int
	var fs []float64
	for i := 0; i < base.honest; i++ {
		fs = append(fs, float64(f[i]))
		if float64(f[i]) > float64(base.price[i]) {
			reachedF++
		}
		if p[i] > float64(base.price[i])+0.5 {
			reachedP++
		}
		if base.price[i] == 0 {
			noProof++
			if f[i] >= 50 {
				noProofReached++
			}
		}
	}
	sort.Float64s(fs)
	t.Logf("honest: median FLOW standing %.0f¢; share above own proofs: FLOW %.1f%%, PPR keep %.1f%%; accounts with no proof reaching the vote floor (50¢) by FLOW: %.1f%%",
		fs[len(fs)/2], 100*float64(reachedF)/float64(base.honest), 100*float64(reachedP)/float64(base.honest), 100*float64(noProofReached)/float64(noProof))
	// Judging never costs a node what it keeps from its own sources.
	g := base.clone()
	before := flowStanding(t, g, g.seeds)
	rng := rand.New(rand.NewPCG(9, 9))
	var judged []int
	for i := 0; len(judged) < 100; i++ {
		if a := rng.IntN(base.honest); g.price[a] > 0 {
			judged = append(judged, a)
			for k := 0; k < 10; k++ {
				g.edge(a, rng.IntN(base.honest), 0.6)
			}
		}
	}
	after := flowStanding(t, g, g.seeds)
	var lost, maxLost float64
	for _, a := range judged {
		d := float64(before[a] - after[a])
		lost += d
		maxLost = max(maxLost, d)
		if after[a] < min(g.price[a], uNodeCap) {
			t.Errorf("node %d keeps %d¢ below its own %d¢ after judging", a, after[a], g.price[a])
		}
	}
	t.Logf("100 proof holders cast 10 more judgements each: mean change %.1f¢, worst %.0f¢ (never below own proofs)", -lost/100, -maxLost)
}

// Cost: one global run and one per-viewer run on 10k accounts; and a
// viewer's flow is bounded by its own judgements' capacity.
func TestUnifiedPerViewerCost(t *testing.T) {
	if testing.Short() {
		t.Skip("RFC 0021 simulation")
	}
	g := honestWorld(2, 10000)
	t0 := time.Now()
	flowStanding(t, g, g.seeds)
	global := time.Since(t0)
	rng := rand.New(rand.NewPCG(3, 3))
	var viewers []int
	for len(viewers) < 20 {
		viewers = append(viewers, rng.IntN(g.honest))
	}
	t1 := time.Now()
	var beyond int
	for _, v := range viewers {
		// The viewer replaces the operator seeds; the world node (prices) stays.
		vs := flowStanding(t, g, []int{v})
		gs := flowStanding(t, g, nil)
		for i := range vs {
			if vs[i] > gs[i] && i != v {
				beyond++
			}
		}
	}
	per := time.Since(t1) / time.Duration(2*len(viewers))
	t.Logf("10k accounts, %d edges: global run %v, per-viewer run %v; a viewer's trust reaches %.1f accounts on average (its own judgements' capacity bounds it)",
		len(g.src), global.Round(time.Millisecond), per.Round(time.Millisecond), float64(beyond)/float64(len(viewers)))
	if per > 5*time.Second {
		t.Errorf("per-viewer run %v: too slow to compute on read", per)
	}
}

// Accuracy as capacity: the multiplier from RFC 0020's gain (moved the
// consensus towards where it ended), shrunk towards 1, in [0.5, 2]. Herders
// stay at 1, early informed judges rise, contrarians fall. No conservation
// rule is needed: a multiplier bounds flow, it never creates any.
func TestUnifiedAccuracyMultiplier(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 5))
	type j struct {
		kind  string
		x, c0 int64
		at    int
	}
	kinds := []string{"informed", "late", "herder", "contrarian"}
	gains, agree := map[string][]float64{}, map[string][]float64{}
	for s := 0; s < 300; s++ {
		q := rng.Int64N(101)
		var js []j
		for k := 0; k < 8; k++ {
			// The C303s baseline mix: 20 informed, 15 late, 10 herders, 5 contrarians.
			r, kind := rng.IntN(50), "contrarian"
			switch {
			case r < 20:
				kind = "informed"
			case r < 35:
				kind = "late"
			case r < 45:
				kind = "herder"
			}
			js = append(js, j{kind: kind, at: 10 + rng.IntN(76)})
		}
		for i := range js {
			if js[i].kind == "informed" {
				js[i].at = rng.IntN(30)
			}
			if js[i].kind == "late" {
				js[i].at = 50 + rng.IntN(40)
			}
		}
		sort.Slice(js, func(a, b int) bool { return js[a].at < js[b].at })
		for i := range js {
			var pts []judge.Point
			for _, p := range js[:i] {
				pts = append(pts, judge.Point{X: p.x, W: 1e6})
			}
			sort.Slice(pts, func(a, b int) bool { return pts[a].X < pts[b].X })
			c0 := judge.Consensus(pts)
			est := min(100, max(0, q+int64(math.Round(10*rng.NormFloat64()))))
			switch js[i].kind {
			case "herder":
				js[i].x = c0
			case "contrarian":
				if est >= c0 {
					js[i].x = max(0, c0-25)
				} else {
					js[i].x = min(100, c0+25)
				}
			default:
				js[i].x = est
			}
			js[i].c0 = c0
		}
		for i := range js {
			var later []judge.Point
			for _, p := range js[i+1:] {
				later = append(later, judge.Point{X: p.x, W: 1e6})
			}
			if len(later) < judge.KMin {
				continue
			}
			sort.Slice(later, func(a, b int) bool { return later[a].X < later[b].X })
			cT := judge.Outcome(later)
			gains[js[i].kind] = append(gains[js[i].kind], float64(judge.Gain(js[i].x, js[i].c0, cT, false))/1e6)
			// The literal reading, "agreed with the consensus": 1 − |x − cT| / 50.
			agree[js[i].kind] = append(agree[js[i].kind], 1-math.Abs(float64(js[i].x-cT))/50)
		}
	}
	for _, k := range kinds {
		t.Logf("%s: agreement with the final consensus %+.3f (the literal rule)", k, uMean(agree[k]))
	}
	// Agreement pays a herder about what it pays an informed judge (0.78
	// against 0.81 here): copying is nearly free. The gain pays it nothing.
	if uMean(agree["herder"]) < 0.9*uMean(agree["informed"]) {
		t.Errorf("agreement: herder %.3f, informed %.3f; the RFC says they are close", uMean(agree["herder"]), uMean(agree["informed"]))
	}
	// The multiplier a judge with 6 marked judgements gets: 1 + 2 × mean
	// gain, shrunk by the score prior's 2 pseudo-judgements, in [0.5, 2].
	mult := func(g []float64) float64 { return min(2, max(0.5, 1+2*6*uMean(g)/(6+2))) }
	for _, k := range kinds {
		t.Logf("%s: mean gain %+.3f, multiplier after 6 marked judgements %.2f", k, uMean(gains[k]), mult(gains[k]))
	}
	if m := mult(gains["herder"]); math.Abs(m-1) > 0.02 {
		t.Errorf("herder multiplier %.2f, want 1", m)
	}
	if mult(gains["informed"]) <= 1 || mult(gains["contrarian"]) >= 1 {
		t.Errorf("informed %.2f should be > 1 and contrarian %.2f < 1", mult(gains["informed"]), mult(gains["contrarian"]))
	}
}

func uMean(x []float64) float64 {
	var s float64
	for _, v := range x {
		s += v
	}
	return s / float64(max(1, len(x)))
}
