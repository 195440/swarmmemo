package web

import (
	"strconv"
	"time"
)

// The operator dashboard's trust section (RFC 0021 §13): the funnel
// report's trust object (board.trustDashboard), drawn with /stats' tiles
// and line chart. Private: the operator's share of the flow passed on is
// shown here and never on a public page.

// dashTrust is the section; nil before any trust run.
type dashTrust struct {
	Tiles  []headTile
	Shares *lineChart
	Run    int64
	Stale  bool
}

// funnelTrustReport is the report's trust object, by its JSON names.
type funnelTrustReport struct {
	Run               int64 `json:"run"`
	AsOf              int64 `json:"as_of"`
	Stale             bool  `json:"stale"`
	OperatorPassedPPM int64 `json:"operator_passed_ppm"`
	Top5PassedPPM     int64 `json:"top5_passed_ppm"`
	PassedCents       int64 `json:"passed_cents"`
	Sources           int64 `json:"sources"`
	PenaltiesOpen     int64 `json:"penalties_open"`
	Penalised         int64 `json:"penalised"`
	Liable            int64 `json:"liable"`
	AboveVoteFloor    int64 `json:"above_vote_floor"`
	History           []struct {
		Run               int64 `json:"run"`
		AsOf              int64 `json:"as_of"`
		OperatorPassedPPM int64 `json:"operator_passed_ppm"`
		Top5PassedPPM     int64 `json:"top5_passed_ppm"`
		PenaltiesOpen     int64 `json:"penalties_open"`
		AboveVoteFloor    int64 `json:"above_vote_floor"`
	} `json:"history"`
}

func buildDashTrust(raw map[string]any) *dashTrust {
	if raw == nil {
		return nil
	}
	var r funnelTrustReport
	if remarshal(raw, &r) != nil || r.Run == 0 {
		return nil
	}
	d := &dashTrust{Run: r.Run, Stale: r.Stale}
	n := len(r.History)
	series := func(value func(i int) int64) []int64 {
		out := make([]int64, n)
		for i := range n {
			out[i] = value(i)
		}
		return out
	}
	operator := series(func(i int) int64 { return r.History[i].OperatorPassedPPM })
	top5 := series(func(i int) int64 { return r.History[i].Top5PassedPPM })
	penalties := series(func(i int) int64 { return r.History[i].PenaltiesOpen })
	above := series(func(i int) int64 { return r.History[i].AboveVoteFloor })
	tile := func(label, tip, value, note string, spark []int64, ppm bool) headTile {
		t := headTile{Label: label, Tip: tip, Value: value, Note: note}
		if len(spark) > 1 {
			t.Spark = sparkline(spark)
			if ppm {
				t.Delta, t.Trend = pointsChange(float64(spark[len(spark)-1])/1e4, float64(spark[len(spark)-2])/1e4)
			} else {
				t.Delta, t.Trend = change(spark[len(spark)-1], spark[len(spark)-2])
			}
			t.Note += ", vs the run before"
		}
		return t
	}
	d.Tiles = []headTile{
		tile("Operator share", "The share of the standing passed on over judgement edges that the operator's own accounts (the funnel's exclude list) pass. RFC 0021 §4 expects it to fall as outside judges pay their own way.",
			ppmPercent(r.OperatorPassedPPM), "of "+cents(r.PassedCents)+" passed on", operator, true),
		tile("Top-5 share", "The share of the flow passed on that the five largest sources pass.", ppmPercent(r.Top5PassedPPM), strconv.FormatInt(r.Sources, 10)+" sources", top5, true),
		tile("Penalties in force", "Penalty log entries active at the run's as-of time.", count(r.PenaltiesOpen), count(r.Penalised)+" members, "+count(r.Liable)+" liable backers", penalties, false),
		tile("Above the vote floor", "Accounts at or above v_floor, whose votes carry ranking weight.", count(r.AboveVoteFloor), "accounts", above, false),
	}
	if n > 1 {
		xs := make([]string, n)
		for i, h := range r.History {
			xs[i] = "Run " + strconv.FormatInt(h.Run, 10) + ", " + time.Unix(h.AsOf, 0).UTC().Format("2 Jan 2006")
		}
		step := max(n/5, 1)
		axis := axisFor(n, func(i int) string {
			if (n-1-i)%step == 0 {
				return time.Unix(r.History[i].AsOf, 0).UTC().Format("2 Jan")
			}
			return ""
		})
		c := buildLineChart("Who the flow comes from", "The operator's and the top five sources' share of the standing passed on, per nightly run.", xs, axis, "", []chartSeries{
			{Class: "s-1", Label: "Operator share", Values: operator, Format: ppmPercent, NoTotal: true},
			{Class: "s-2", Label: "Top-5 share", Values: top5, Format: ppmPercent, NoTotal: true},
		})
		d.Shares = &c
	}
	return d
}
