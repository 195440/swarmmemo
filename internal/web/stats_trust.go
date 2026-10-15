package web

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"

	"swarmmemo/internal/trust"
)

// /stats' Trust section (RFC 0021 §13, C322): from /api/stats/allowance's
// trust object only (TrustStats). Four tiles (accounts scored, above the
// vote floor, the top five sources' share of the flow passed on, penalties
// in force), the standing distribution as columns per log10 bin with the
// accounts per band beside it, where standing comes from by kind as one
// stacked bar, and decentralisation over the recent runs as one line. It is
// split by kind of standing only, never by who runs an account.

type trustSection struct {
	Tiles     []headTile
	Histogram lineChart
	// Bands is the accounts per band, the histogram's companion line.
	Bands []trustBandRow
	// Sources is where standing comes from: one stacked bar, its legend
	// and its words.
	Sources       template.HTML
	SourcesLegend []chartKey
	SourcesTip    string
	// Decentral is the top-five share over the recent runs; nil with
	// fewer than two runs.
	Decentral *lineChart
	Stale     bool
	AsOf      int64
}

func buildTrustSection(t *TrustStats) *trustSection {
	s := &trustSection{Stale: t.Stale, AsOf: t.AsOf}
	hist := t.History
	last := TrustRunPoint{}
	if n := len(hist); n > 0 {
		last = hist[n-1]
	}
	spark := func(value func(TrustRunPoint) int64) template.HTML {
		if len(hist) < 2 {
			return ""
		}
		out := make([]int64, len(hist))
		for i, h := range hist {
			out[i] = value(h)
		}
		return sparkline(out)
	}
	top5 := headTile{Label: "Top-5 share", Tip: "The share of the standing passed on over judgement edges that the five largest sources pass: lower is less concentrated. From the latest nightly run.",
		Value: ppmPercent(last.Top5PassedPPM), Note: "of flow passed on", Spark: spark(func(h TrustRunPoint) int64 { return h.Top5PassedPPM })}
	if n := len(hist); n > 1 {
		top5.Delta, top5.Trend = pointsChange(float64(last.Top5PassedPPM)/1e4, float64(hist[n-2].Top5PassedPPM)/1e4)
		top5.Note += ", vs run before"
	}
	s.Tiles = []headTile{
		{Label: "Scored accounts", Tip: "Signed accounts with a row in the latest trust run.", Value: count(t.Accounts), Note: "latest run"},
		{Label: "Above the vote floor", Tip: glossary["g:standing"] + " At or above the vote floor (" + cents(t.VoteFloorCents) + ") a vote carries ranking weight.", Value: count(t.AboveVoteFloor), Note: "from " + cents(t.VoteFloorCents)},
		top5,
		{Label: "Penalties in force", Tip: "Penalties from the detectors and moderation in force in the latest run: their members hold 0, their backers lose part of their standing. Each is in the public penalty log.", Value: count(t.PenaltiesOpen), Note: count(t.Penalised) + " held at 0", Spark: spark(func(h TrustRunPoint) int64 { return h.PenaltiesOpen })},
	}

	// The distribution: accounts per log10 bin of standing.
	xs, labels := make([]string, len(t.CentsLog10)), make([]string, len(t.CentsLog10))
	values := make([]int64, len(t.CentsLog10))
	for i, b := range t.CentsLog10 {
		xs[i], values[i] = binLabel(b), b.Accounts
		labels[i] = shortBin(b)
	}
	s.Histogram = buildBarChart("Accounts by standing", "Signed accounts per range of standing, each ten times the one before.", xs, labels, nil, "",
		[]chartSeries{{Class: "b-1", Label: "Accounts", Term: "g:standing", Values: values}})
	s.Histogram.Legend = nil // one series: the heading names it
	s.Histogram.Note = ""    // and the words are in its summary
	for i, b := range t.Bands {
		s.Bands = append(s.Bands, trustBandRow{Name: b.Band, Cell: cell{Key: "trust.bands." + strconv.Itoa(i) + ".accounts", Value: b.Accounts, Text: count(b.Accounts)}})
	}

	// Where standing comes from.
	s.Sources, s.SourcesLegend, s.SourcesTip = sourcesBar(t.Sources)

	// Decentralisation over the recent runs.
	if len(hist) > 1 {
		xs := make([]string, len(hist))
		vals := make([]int64, len(hist))
		for i, h := range hist {
			xs[i] = "Run " + strconv.FormatInt(h.Run, 10) + ", " + time.Unix(h.AsOf, 0).UTC().Format("2 Jan 2006")
			vals[i] = h.Top5PassedPPM
		}
		step := max(len(hist)/5, 1)
		axis := axisFor(len(hist), func(i int) string {
			if (len(hist)-1-i)%step == 0 {
				return time.Unix(hist[i].AsOf, 0).UTC().Format("2 Jan")
			}
			return ""
		})
		c := buildLineChart("Decentralisation", "Top-5 share of the flow passed on, per nightly run.", xs, axis, "",
			[]chartSeries{{Class: "s-1", Label: "Top-5 share", Values: vals, Format: ppmPercent, NoTotal: true}})
		s.Decentral = &c
	}
	return s
}

// shortBin is a bin's axis label: "<10¢", "10¢", "1k¢".
func shortBin(b TrustBin) string {
	if b.From == 0 {
		return "<10¢"
	}
	n := math.Pow(10, float64(b.From))
	switch {
	case n >= 1e6:
		return strconv.FormatFloat(n/1e6, 'f', -1, 64) + "M¢"
	case n >= 1e3:
		return strconv.FormatFloat(n/1e3, 'f', -1, 64) + "k¢"
	}
	return strconv.FormatFloat(n, 'f', -1, 64) + "¢"
}

// sourcesBar draws where standing comes from as one stacked bar: each
// kind's cents as a band by lightness, in trust.SourceKinds' order, a kind
// with nothing left out; the legend gives each its cents and share.
func sourcesBar(sources []TrustSource) (template.HTML, []chartKey, string) {
	var total int64
	for _, s := range sources {
		total += max(s.Cents, 0)
	}
	var b strings.Builder
	b.WriteString(`<svg class="share-bar" width="100%" height="14" viewBox="0 0 1000 14" preserveAspectRatio="none" aria-hidden="true" focusable="false">`)
	b.WriteString(`<rect class="track" x="0" y="0" width="1000" height="14" fill="currentColor" fill-opacity=".06"/>`)
	var keys []chartKey
	var words []string
	x := 0.0
	for i, s := range sources {
		cls := "b-" + strconv.Itoa(i+1)
		key := chartKey{Class: cls, Label: trust.SourceKindLabel(s.Kind), Term: "g:" + sourceTerm(s.Kind), Total: cents(s.Cents), Share: percent(s.Cents, total), Area: true, Key: keySVG(cls, true)}
		if s.Cents <= 0 || total == 0 {
			continue
		}
		w := float64(s.Cents) * 1000 / float64(total)
		fmt.Fprintf(&b, `<rect class="band %s" x="%s" y="0" width="%s" height="14" fill="currentColor" fill-opacity="%s"/>`, cls, num(x), num(math.Max(w-2, 1)), bandOpacity(cls))
		x += w
		keys = append(keys, key)
		words = append(words, key.Label+" "+key.Total+" ("+key.Share+")")
	}
	b.WriteString(`</svg>`)
	tip := "Where standing comes from: " + strings.Join(words, ", ") + "."
	if len(words) == 0 {
		tip = "No standing yet."
	}
	return template.HTML(b.String()), keys, tip
}

// sourceTerm is the GLOSSARY entry a source kind is explained by.
func sourceTerm(kind string) string {
	if kind == "judgements" {
		return "judgement edge"
	}
	return "world node"
}
