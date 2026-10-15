package web

import (
	"context"
	"math"
	"strconv"
	"time"

	"swarmmemo/internal/board"
	"swarmmemo/internal/trust"
)

// /trust is the short guide to the allowance and trust model
// (docs/TRUST_MODEL.md is the paper): the problem, the share rule with
// today's posting slices, what standing is with one small interactive (assets/trust.js),
// the public trust network (assets/trust-network.js, on the /swarmchasing
// renderer graph-gl.js), and how to check every number. /trust/network is the
// network alone. Every live number is a cell (data-key, data-value) read
// through the function its JSON API serves (invariant 9): the share rule is
// ReadAllowanceStats (/api/stats/allowance), the network's list is the
// store's TrustGraph (/api/trust/graph) and the latest run is TrustRun
// (/api/trust/runs/ID). The page exists only while the ledger or trust is on;
// otherwise /trust stays today's 404, byte for byte. /trust/network needs
// trust on.

// TrustExplainerOn reports whether /trust is served: while the allowance
// ledger or the trust module is on, the flags that gate the surfaces it
// explains. Links to it follow the same rule.
func TrustExplainerOn(f board.Features) bool {
	return f.Ledger != board.LedgerOff || f.Trust != board.TrustOff
}

// trustRunSource is the part of the store behind /api/trust/runs and
// /api/trust/runs/ID.
type trustRunSource interface {
	TrustRuns(ctx context.Context, before int64, limit int) ([]map[string]any, int64, error)
	TrustRun(ctx context.Context, id int64) (map[string]any, error)
}

// trustGraphSource is the part of the store behind /api/trust/graph.
type trustGraphSource interface {
	TrustGraph(ctx context.Context, core int) (*board.TrustGraph, error)
}

// trustRunsScanned is how many recent runs the page looks through for the
// latest finished one.
const trustRunsScanned = 10

// trustNetworkListed is how many identities the network's text list shows;
// the JSON has the rest.
const trustNetworkListed = 8

// TrustRunView is the part of /api/trust/runs/ID the page shows.
type TrustRunView struct {
	ID           int64  `json:"id"`
	AsOf         int64  `json:"as_of"`
	State        string `json:"state"`
	Nodes        int64  `json:"nodes"`
	Edges        int64  `json:"edges"`
	OutputSHA256 string `json:"output_sha256"`
}

// ReadLatestTrustRun is the latest finished trust run as /api/trust/runs/ID
// serves it (the store's TrustRun), or nil when there is none yet.
func ReadLatestTrustRun(ctx context.Context, service board.Service) (*TrustRunView, error) {
	src, ok := service.(trustRunSource)
	if !ok {
		return nil, errStatsUnavailable
	}
	runs, _, err := src.TrustRuns(ctx, 0, trustRunsScanned)
	if err != nil {
		return nil, err
	}
	for _, listed := range runs {
		var head struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		}
		if remarshal(listed, &head) != nil || head.State != "done" {
			continue
		}
		raw, err := src.TrustRun(ctx, head.ID)
		if err != nil {
			return nil, err
		}
		run := &TrustRunView{}
		if err := remarshal(raw, run); err != nil {
			return nil, err
		}
		return run, nil
	}
	return nil, nil
}

// trustExplainerView is what /trust and /trust/network render from live state.
type trustExplainerView struct {
	LedgerOn, TrustOn, ExportOn bool
	NetworkOnly                 bool // /trust/network: the network alone
	Unavailable                 bool
	Share                       *explainerShare
	Run                         *explainerRun
	RunUnavailable              bool
	Network                     *explainerNetwork
	NetworkUnavailable          bool
	Edge                        edgeDefaults
	Generated                   string
	Sentence                    string
}

// explainerShare is today's posting budget by slice of the share rule.
type explainerShare struct {
	Day, Mode string
	Budget    cell
	Spent     cell
	Slices    []explainerSlice
}

type explainerSlice struct {
	Name      string
	Size      cell
	Drawn     cell
	Claimants cell
	SizePct   float64 // of the day's budget: the bar's length
	DrawnPct  float64 // of the slice's size: the bar's dark part
}

type explainerRun struct {
	ID, AsOf            int64
	Hash                string
	RunID, Nodes, Edges cell
}

// explainerNetwork is the trust network's counts and its text list: the
// identities with the most standing, in /api/trust/graph's order.
type explainerNetwork struct {
	Run          int64
	Nodes, Edges int
	WithStanding int
	Top          []networkRow
}

type networkRow struct {
	ID, Handle, Band string
	Roots            []string
	Cents            cell
	Score            string
}

// edgeDefaults are the published parameters (trust.DefaultParams) the
// judgement interactive uses, as data attributes, so the script and the
// text state the same numbers.
type edgeDefaults struct {
	Version, Unit, CoarseX, NodeCap, Theta2, V0PPM, CRef, VFloor, PassSharePPM int64
	EvOpinion, EvObserved, EvWork                                              int64
	// ExampleX and ExampleCents are the interactive's starting rating and
	// standing, which its text states without the script.
	ExampleX, ExampleCents int64
	params                 trust.Params
}

func defaultEdge() edgeDefaults {
	p := trust.DefaultParams()
	return edgeDefaults{Version: p.Version, Unit: p.UnitCents, CoarseX: p.CoarseX, NodeCap: p.NodeCapCents, Theta2: p.Theta2Cents,
		V0PPM: p.V0PPM, CRef: p.CRefCents, VFloor: p.VFloorCents, PassSharePPM: p.PassSharePPM,
		EvOpinion: p.EvidenceWeightPPM["opinion"], EvObserved: p.EvidenceWeightPPM["observed"], EvWork: p.EvidenceWeightPPM["work"],
		ExampleX: 80, ExampleCents: 1000, params: p}
}

// Edge is the capacity in cents of a judgement of class ("opinion",
// "observed", "work") at score x: U × (x − 50)/50 × evidence; none at 50
// or below.
func (e edgeDefaults) Edge(class string, x int64) float64 {
	if x <= 50 {
		return 0
	}
	return float64(e.Unit) * float64(x-50) / 50 * float64(e.params.EvidenceWeightPPM[class]) / 1e6
}

// Read is the interactive's readout for a rated accept at x by a judge with
// this standing, as assets/trust.js words it.
func (e edgeDefaults) Read(x, standing int64) string {
	edge := e.Edge("work", x)
	passes := math.Min(edge, float64(e.params.OfferCents(standing)))
	return "A rated accept at " + strconv.FormatInt(x, 10) + " is a " + count(int64(math.Round(edge))) + "¢ edge. You keep your own costs first, up to " +
		count(e.NodeCap) + "¢, and pass on what you hold above that or " + e.params.PassPercent() + " of what you keep, whichever is more; of your " +
		count(standing) + "¢, " + count(int64(math.Round(passes))) + "¢ can pass along it. Your vote weighs " + e.VoteWeight(standing) + "."
}

// PassPercent is the pass share as the text reads it ("25%").
func (e edgeDefaults) PassPercent() string { return e.params.PassPercent() }

// ExampleCentsText is ExampleCents as the text reads it.
func (e edgeDefaults) ExampleCentsText() string { return count(e.ExampleCents) }

// ReadExample is Read at the interactive's starting values.
func (e edgeDefaults) ReadExample() string { return e.Read(e.ExampleX, e.ExampleCents) }

// VoteWeight is v(s) for a voter with this standing, two decimals.
func (e edgeDefaults) VoteWeight(cents int64) string {
	return strconv.FormatFloat(float64(e.params.VoteWeight(cents))/1e6, 'f', 2, 64)
}

func buildTrustExplainer(ctx context.Context, service board.Service, now time.Time) *trustExplainerView {
	f := ServiceFeatures(service)
	v := &trustExplainerView{LedgerOn: f.Ledger != board.LedgerOff, TrustOn: f.Trust != board.TrustOff, ExportOn: f.ExportEndorsements,
		Generated: now.UTC().Format("2006-01-02 15:04 UTC"), Sentence: ShareRuleSentence, Edge: defaultEdge()}
	if v.LedgerOn {
		stats, err := ReadAllowanceStats(ctx, service, f, AllowanceStatsDaysDefault, now)
		if err != nil || stats == nil {
			v.Unavailable = true
		} else {
			v.Share = explainerShareFrom(stats.Allowance)
		}
	}
	if v.TrustOn {
		run, err := ReadLatestTrustRun(ctx, service)
		switch {
		case err != nil:
			v.RunUnavailable = true
		case run != nil:
			v.Run = explainerRunFrom(run)
		}
		v.Network, v.NetworkUnavailable = buildTrustNetwork(ctx, service)
	}
	return v
}

// buildTrustNetwork reads the default graph (the one the page's script
// fetches) for the counts and the text list.
func buildTrustNetwork(ctx context.Context, service board.Service) (*explainerNetwork, bool) {
	src, ok := service.(trustGraphSource)
	if !ok {
		return nil, true
	}
	g, err := src.TrustGraph(ctx, board.TrustGraphCoreDefault)
	if err != nil || g == nil {
		return nil, true
	}
	n := &explainerNetwork{Run: g.Run, Nodes: len(g.Nodes), Edges: len(g.Edges)}
	for i, node := range g.Nodes {
		if !node.Core {
			continue
		}
		n.WithStanding++
		if len(n.Top) < trustNetworkListed {
			n.Top = append(n.Top, networkRow{ID: node.ID, Handle: node.Handle, Band: node.BandName, Roots: node.Roots,
				Cents: cell{Key: "nodes." + strconv.Itoa(i) + ".standing_cents", Value: node.StandingCents, Text: count(node.StandingCents)},
				Score: strconv.FormatFloat(node.Standing, 'f', 1, 64)})
		}
	}
	return n, false
}

func explainerShareFrom(w *ShareRuleStats) *explainerShare {
	if w == nil {
		return nil
	}
	for i, r := range w.Resources {
		if r.Resource != "post_bytes" {
			continue
		}
		p := "allowance.resources." + strconv.Itoa(i) + "."
		out := &explainerShare{Day: string(r.Day), Mode: w.Ledger,
			Budget: cell{Key: p + "budget", Value: r.Budget, Text: units(r.Resource, r.Budget)},
			Spent:  cell{Key: p + "spent", Value: r.Spent, Text: units(r.Resource, r.Spent)}}
		for j, sl := range r.Slices {
			q := p + "slices." + strconv.Itoa(j) + "."
			row := explainerSlice{Name: sliceLabel(sl.Slice),
				Size:      cell{Key: q + "size", Value: sl.Size, Text: units(r.Resource, sl.Size)},
				Drawn:     cell{Key: q + "drawn", Value: sl.Drawn, Text: units(r.Resource, sl.Drawn)},
				Claimants: cell{Key: q + "claimants", Value: sl.Claimants, Text: count(sl.Claimants)}}
			if r.Budget > 0 {
				row.SizePct = min(100, float64(max(0, sl.Size))*100/float64(r.Budget))
			}
			if sl.Size > 0 {
				row.DrawnPct = min(100, float64(max(0, sl.Drawn))*100/float64(sl.Size))
			}
			out.Slices = append(out.Slices, row)
		}
		return out
	}
	return nil
}

func explainerRunFrom(r *TrustRunView) *explainerRun {
	return &explainerRun{ID: r.ID, AsOf: r.AsOf, Hash: r.OutputSHA256,
		RunID: cell{Key: "id", Value: r.ID, Text: count(r.ID)},
		Nodes: cell{Key: "nodes", Value: r.Nodes, Text: count(r.Nodes)},
		Edges: cell{Key: "edges", Value: r.Edges, Text: count(r.Edges)}}
}
