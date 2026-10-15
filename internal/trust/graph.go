package trust

import (
	"context"
	"sort"

	"swarmmemo/internal/judge"
)

// Graph is one run's flow graph (RFC 0021 §2): nodes are roots, each with
// its world edge (what it paid here, after penalties) and its node cap;
// edges are the judgement edges' capacities between roots. Amounts are
// millicents. A run keeps its final graph (trust_run_graphs) so per-viewer
// standing (§5) is computed on read from the same graph.
type Graph struct {
	Labels  []string          `json:"labels"`
	RootOf  map[string]string `json:"root_of"` // account → its root's label
	World   []int64           `json:"world"`
	Cap     []int64           `json:"cap"`
	Src     []int32           `json:"src"`
	Dst     []int32           `json:"dst"`
	EdgeCap []int64           `json:"edge_cap"`
	Levels  []int64           `json:"levels"`
	MaxWork int64             `json:"max_work"`

	Index map[string]int `json:"-"`
	// recent are the judgement edges made within the backer window, by
	// author account (a penalty's backers, §8).
	recent []recentEdge
}

type recentEdge struct {
	author string
	dst    int
	cap    int64
	at     int64
}

// gEdge is one edge between node indices, weight w (detectors).
type gEdge struct {
	src, dst int32
	w        int64
}

// flowLevels are the sink levels of the progressive filling, max-min fair:
// U/8, U/4, U/2, U, 2U and node_cap (each at most node_cap), ascending.
func flowLevels(U, nodeCap int64) []int64 {
	var out []int64
	for _, l := range []int64{U / 8, U / 4, U / 2, U, 2 * U, nodeCap} {
		l = min(l, nodeCap)
		if l > 0 && (len(out) == 0 || l > out[len(out)-1]) {
			out = append(out, l)
		}
	}
	return out
}

// newGraph makes the nodes: every account's root, with the sum of its
// accounts' world edges. A member of an active penalty holds nothing (world
// and cap 0); a backer's world and cap are scaled by 1 − f.
func (r *run) newGraph(worldMC map[string]int64, member map[string]*logEntry, liable map[string]PenaltyState) *Graph {
	g := &Graph{RootOf: map[string]string{}, Index: map[string]int{}, MaxWork: r.p.MaxWork}
	world := map[string]int64{}
	for _, a := range r.accounts {
		root := r.rootOf(a)
		g.RootOf[a] = root
		world[root] += worldMC[a]
	}
	for root := range world {
		g.Labels = append(g.Labels, root)
	}
	sort.Strings(g.Labels)
	nodeCap := r.p.NodeCapCents * mc
	for i, root := range g.Labels {
		g.Index[root] = i
		w, c := world[root], nodeCap
		switch l, ok := liable[root]; {
		case member[root] != nil:
			w, c = 0, 0
		case ok:
			w, c = mulDiv(w, 1e6-l.FractionPPM, 1e6), mulDiv(c, 1e6-l.FractionPPM, 1e6)
		}
		g.World = append(g.World, w)
		g.Cap = append(g.Cap, c)
	}
	g.Levels = flowLevels(r.p.UnitCents*mc, nodeCap)
	return g
}

// setEdges builds the judgement edges from the set's current judgements,
// with acc as each author's accuracy multiplier.
func (g *Graph) setEdges(r *run, set judgementSet, acc func(string) int64, member map[string]*logEntry, liable map[string]PenaltyState) {
	p := r.p
	U := p.UnitCents * mc
	type key struct{ src, dst int }
	sum := map[key]int64{}
	g.recent = g.recent[:0]
	from := r.asOf - p.Penalty.BackerEdgeDays*day
	for _, s := range set.subjects {
		var dsts []int
		seen := map[int]bool{}
		for _, o := range set.sortedOwners(s) {
			root := r.rootOf(o)
			if i, ok := g.Index[root]; ok && !seen[i] && member[root] == nil {
				seen[i] = true
				dsts = append(dsts, i)
			}
		}
		sort.Ints(dsts)
		for _, e := range set.bySubject[s] {
			if e.withdraw || e.closeIdx >= 0 {
				continue
			}
			strength := e.j.EdgePPM(p.CoarseX)
			if strength <= 0 {
				continue // down judgements make no edge
			}
			srcRoot := r.rootOf(e.j.Author)
			src, ok := g.Index[srcRoot]
			if !ok || member[srcRoot] != nil {
				continue
			}
			var to []int
			for _, d := range dsts {
				if d != src {
					to = append(to, d)
				}
			}
			if len(to) == 0 {
				continue
			}
			c := mulDiv(U, strength, 1e6)
			c = mulDiv(c, p.evidencePPM(e.j), 1e6)
			c = mulDiv(c, judge.RelationshipPPM(e.j.Relationship), 1e6)
			c = mulDiv(c, r.cv.decay(p.DayFactorPPM, r.D-dayOf(e.j.At)), 1e6)
			c = mulDiv(c, acc(e.j.Author), 1e6)
			share := c / int64(len(to))
			if share <= 0 {
				continue
			}
			for _, d := range to {
				sum[key{src, d}] += share
				if e.j.At >= from {
					g.recent = append(g.recent, recentEdge{author: e.j.Author, dst: d, cap: share, at: e.j.At})
				}
			}
		}
	}
	keys := make([]key, 0, len(sum))
	for k := range sum {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].src != keys[j].src {
			return keys[i].src < keys[j].src
		}
		return keys[i].dst < keys[j].dst
	})
	g.Src, g.Dst, g.EdgeCap = g.Src[:0], g.Dst[:0], g.EdgeCap[:0]
	for _, k := range keys {
		c := sum[k]
		if l, ok := liable[g.Labels[k.src]]; ok {
			c = mulDiv(c, 1e6-l.FractionPPM, 1e6)
		}
		if c <= 0 {
			continue
		}
		g.Src = append(g.Src, int32(k.src))
		g.Dst = append(g.Dst, int32(k.dst))
		g.EdgeCap = append(g.EdgeCap, c)
	}
}

// flowResult is one flow over the graph.
type flowResult struct {
	Standing, Own, Passed []int64
	edge                  []int64
	work                  int64
}

// flow is the capacity-bounded max-flow from the world (and extra, per
// node, for a viewer's view): each node keeps min(world + extra, cap) of
// its own; the rest flows from the source over the judgement edges into
// the nodes' sinks, raised level by level up to each cap. It is specified
// exactly, for the verifier: network nodes 0..n−1, source n, sink n+1;
// insertion order: source → node by node, judgement edges in (src, dst)
// order, node → sink by node; then each level raises the sinks and runs
// network.maxflow. work is the network's work count after the flow.
func (g *Graph) flow(ctx context.Context, extra []int64, maxWork, work int64) (flowResult, error) {
	n := len(g.Labels)
	res := flowResult{Standing: make([]int64, n), Own: make([]int64, n), Passed: make([]int64, n), edge: make([]int64, len(g.Src)), work: work}
	if n == 0 {
		return res, nil
	}
	S, T := n, n+1
	net := newNetwork(ctx, n+2, maxWork, work)
	in := make([]int, n)
	for i := 0; i < n; i++ {
		w := g.World[i]
		if extra != nil {
			w += extra[i]
		}
		res.Own[i] = min(w, g.Cap[i])
		in[i] = -1
		if rest := w - res.Own[i]; rest > 0 {
			in[i] = net.add(S, i, rest)
		}
	}
	ids := make([]int, len(g.Src))
	for k := range g.Src {
		ids[k] = net.add(int(g.Src[k]), int(g.Dst[k]), g.EdgeCap[k])
	}
	sink := make([]int, n)
	for i := 0; i < n; i++ {
		sink[i] = net.add(i, T, 0)
	}
	set := make([]int64, n)
	for _, L := range g.Levels {
		for i := 0; i < n; i++ {
			want := max(0, min(L, g.Cap[i])-res.Own[i])
			net.cap[sink[i]] += want - set[i]
			set[i] = want
		}
		if err := net.maxflow(S, T); err != nil {
			res.work = net.work
			return res, err
		}
	}
	for i := 0; i < n; i++ {
		res.Standing[i] = res.Own[i] + net.flowOn(sink[i])
		if in[i] >= 0 {
			res.Passed[i] = net.flowOn(in[i])
		}
	}
	for k, e := range ids {
		res.edge[k] = net.flowOn(e)
	}
	res.work = net.work
	return res, nil
}

type nodeFlow struct {
	other int
	cap   int64
	flow  int64
}

// edgeFlows lists each node's in-edges and out-edges with their flow.
func (g *Graph) edgeFlows(res flowResult) (in, out [][]nodeFlow) {
	in, out = make([][]nodeFlow, len(g.Labels)), make([][]nodeFlow, len(g.Labels))
	for k := range g.Src {
		s, d := int(g.Src[k]), int(g.Dst[k])
		in[d] = append(in[d], nodeFlow{s, g.EdgeCap[k], res.edge[k]})
		out[s] = append(out[s], nodeFlow{d, g.EdgeCap[k], res.edge[k]})
	}
	return in, out
}

// FlowPartsMax bounds a score's backers and backs lists.
const FlowPartsMax = 20

// topFlows is a node's edges as flow parts, largest flow first.
func topFlows(list []nodeFlow, g *Graph) ([]FlowPart, int64) {
	sorted := append([]nodeFlow(nil), list...)
	sort.Slice(sorted, func(i, j int) bool {
		x, y := sorted[i], sorted[j]
		if x.flow != y.flow {
			return x.flow > y.flow
		}
		if x.cap != y.cap {
			return x.cap > y.cap
		}
		return g.Labels[x.other] < g.Labels[y.other]
	})
	out := []FlowPart{}
	for _, f := range sorted[:min(FlowPartsMax, len(sorted))] {
		out = append(out, FlowPart{Root: g.Labels[f.other], CapCents: f.cap / mc, Cents: f.flow / mc})
	}
	return out, int64(len(list))
}

// recentCapInto is, per author account outside roots, the capacity of its
// judgement edges into roots made at or after from (§8's c).
func (g *Graph) recentCapInto(roots map[string]bool, from int64) map[string]int64 {
	out := map[string]int64{}
	for _, e := range g.recent {
		if e.at >= from && roots[g.Labels[e.dst]] && !roots[g.RootOf[e.author]] {
			out[e.author] += e.cap
		}
	}
	return out
}

// burnPreview is what a penalty on node i would burn now.
func (g *Graph) burnPreview(i int, res flowResult, r *run, proofRootsOf map[string][]string, U int64) BurnPreview {
	b := BurnPreview{OwnCents: g.World[i] / mc, ProofRoots: []string{}, Backers: []BackerAtRisk{}}
	roots := map[string]bool{}
	for _, a := range r.accounts {
		if g.RootOf[a] == g.Labels[i] {
			for _, pr := range proofRootsOf[a] {
				roots[pr] = true
			}
		}
	}
	for pr := range roots {
		b.ProofRoots = append(b.ProofRoots, pr)
	}
	sort.Strings(b.ProofRoots)
	c := map[int]int64{}
	from := r.asOf - r.p.Penalty.BackerEdgeDays*day
	for _, e := range g.recent {
		if e.dst == i && e.at >= from {
			if j, ok := g.Index[g.RootOf[e.author]]; ok && j != i {
				c[j] += e.cap
			}
		}
	}
	for j, cap := range c {
		f := mulDiv(cap, 1e6, cap+U)
		b.Backers = append(b.Backers, BackerAtRisk{Root: g.Labels[j], FractionPPM: f, Cents: mulDiv(res.Standing[j], f, 1e6) / mc})
	}
	sort.Slice(b.Backers, func(x, y int) bool {
		if b.Backers[x].Cents != b.Backers[y].Cents {
			return b.Backers[x].Cents > b.Backers[y].Cents
		}
		return b.Backers[x].Root < b.Backers[y].Root
	})
	b.Backers = b.Backers[:min(FlowPartsMax, len(b.Backers))]
	return b
}

// Reindex rebuilds the label index of a graph read back from storage.
func (g *Graph) Reindex() {
	g.Index = make(map[string]int, len(g.Labels))
	for i, l := range g.Labels {
		g.Index[l] = i
	}
}

// ViewerStanding is RFC 0021 §5's per-viewer standing: the run's flow with
// the viewer's root as one more source, emitting node_cap plus its
// judgement edges' capacity. It orders what the viewer reads and never
// gates. It returns each root's standing in cents from the viewer's view,
// and ok false when the viewer has no root in the graph (then the global
// standing is the view).
func (g *Graph) ViewerStanding(ctx context.Context, viewer string) (map[string]int64, bool, error) {
	root, ok := g.RootOf[viewer]
	if !ok {
		return nil, false, nil
	}
	v := g.Index[root]
	extra := make([]int64, len(g.Labels))
	extra[v] = g.Cap[v]
	for k := range g.Src {
		if int(g.Src[k]) == v {
			extra[v] += g.EdgeCap[k]
		}
	}
	res, err := g.flow(ctx, extra, g.MaxWork, 0)
	if err != nil {
		return nil, true, err
	}
	out := make(map[string]int64, len(g.Labels))
	for i, l := range g.Labels {
		out[l] = res.Standing[i] / mc
	}
	return out, true, nil
}
