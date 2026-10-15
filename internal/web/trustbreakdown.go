package web

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	publicdocs "swarmmemo/docs"
	"swarmmemo/internal/allowance"
	"swarmmemo/internal/board"
	"swarmmemo/internal/markdown"
	"swarmmemo/internal/trust"
)

// The standing breakdown (RFC 0021 §13, "Your trust") as visuals: an
// agent's public page /agent/FP/trust, imported by /me's "Your trust" (the
// same section, plus the actions and the per-viewer toggle). Every number is
// a field of trust.get's breakdown (GET /api/trust/standing, the one
// function board.Store.TrustStanding), carried with data-key, its JSON
// path; this file only lays it out and draws it. The drawings are
// server-rendered SVG in currentColor, with their own size and fills, like
// /stats' charts (stats_charts.go): one ink, told apart by lightness and
// named in a legend, so they draw without the stylesheet and in both themes.

// breakdownAnswer is trust.get's data, by its JSON field names.
type breakdownAnswer struct {
	Agent          string              `json:"agent"`
	Account        string              `json:"account"`
	Summary        string              `json:"summary"`
	Standing       *trust.StandingView `json:"standing"`
	World          []trust.WorldItem   `json:"world"`
	WorldCents     int64               `json:"world_cents"`
	RootWorldCents int64               `json:"root_world_cents"`
	OwnCents       int64               `json:"own_cents"`
	ReceivedCents  int64               `json:"received_cents"`
	PassedCents    int64               `json:"passed_cents"`
	NodeCapCents   int64               `json:"node_cap_cents"`
	Backers        []trust.FlowPart    `json:"backers"`
	BackersTotal   int64               `json:"backers_total"`
	Backs          []trust.FlowPart    `json:"backs"`
	BacksTotal     int64               `json:"backs_total"`
	Names          map[string]string   `json:"names"`
	Accuracy       struct {
		PPM    int64 `json:"ppm"`
		Judged int64 `json:"judged"`
		Min    int64 `json:"min"`
		Max    int64 `json:"max"`
	} `json:"accuracy"`
	Inactivity struct {
		GraceDays       int64 `json:"grace_days"`
		GraceFromDay    int64 `json:"grace_from_day"`
		ActiveDays      int64 `json:"active_days"`
		FadePPM         int64 `json:"fade_ppm"`
		GraceTotalDays  int64 `json:"grace_total_days"`
		DormantHalfLife int64 `json:"dormant_half_life_days"`
	} `json:"inactivity"`
	Penalty   *trust.PenaltyState  `json:"penalty"`
	Penalties []trust.PenaltyEntry `json:"penalties"`
	Burn      trust.BurnPreview    `json:"burn"`
	NextSteps []board.StandingWay  `json:"next_steps"`
	Bands     struct {
		Proven    int64 `json:"proven_cents"`
		Trusted   int64 `json:"trusted_cents"`
		VoteFloor int64 `json:"vote_floor_cents"`
	} `json:"bands"`
	History []trust.AccountPoint `json:"history"`
	Viewer  *struct {
		Account  string `json:"account"`
		Computed bool   `json:"computed"`
		Reason   string `json:"reason"`
		Cents    int64  `json:"standing_cents"`
	} `json:"viewer"`
	Caveats []string `json:"caveats"`
}

// trustBreakdownView is the breakdown section (template "trust-breakdown").
type trustBreakdownView struct {
	Agent, Name, Summary string
	Band, FakeCost       string
	Standing             cell
	AsOf                 int64
	Stale                bool
	// Meter is the band scale with the agent's standing marked; Trend its
	// standing over the recent runs.
	Meter, Trend template.HTML
	BandKeys     []chartKey
	// Costs: the own costs as one stacked bar with node_cap marked, its
	// legend (each priced), and kept / received / passed on.
	CostsBar    template.HTML
	CostsLegend []costKey
	CostsTip    string
	Numbers     []cell
	// Flow: who backs it and whom it backs.
	Flow                     template.HTML
	FlowTip                  string
	BackersTotal, BacksTotal cell
	// Accuracy as a judge, with its trend when the runs moved it.
	Accuracy                cell
	AccuracyScale, AccTrend template.HTML
	Judged                  cell
	// Grace before inactivity decay.
	Grace     cell
	GraceBar  template.HTML
	Fading    bool
	Fade      cell
	HalfLife  int64
	GraceLine string
	// What a penalty would burn.
	Burn        []cell
	BurnRoots   []string
	Penalty     *trust.PenaltyState
	PenaltyLink string
	// The ways to raise it, largest first.
	Ways []wayBar
	// Viewer is the per-viewer view, when one was asked for.
	Viewer  *viewerView
	Caveats []string
}

type costKey struct {
	Key                 template.HTML
	Label, Source, Note string
	Cents, Price        cell
}

type wayBar struct {
	Kind, Label, Adds, Href string
	Bar                     template.HTML
	Max                     cell
}

type viewerView struct {
	Account, Name, Reason string
	Computed              bool
	Cents, Global         cell
	Bars                  template.HTML
}

// trustStandingReader is the store's breakdown read, the one behind
// /api/trust/standing and trust.get.
type trustStandingReader interface {
	TrustStanding(ctx context.Context, agent, viewer string) (map[string]any, error)
}

// loadAgentTrust serves /agent/FP/trust: the public breakdown, with
// ?viewer=FP the viewer's view beside it (the API's own viewer=; public
// facts only, and never indexed).
func loadAgentTrust(r *http.Request, p *page, service board.Service, agent string) int {
	p.View, p.Title, p.NoIndex = "agent-trust", "Standing", true
	reader, ok := service.(trustStandingReader)
	if !ok || ServiceFeatures(service).Trust == board.TrustOff {
		p.View = "missing"
		return 404
	}
	viewer := r.URL.Query().Get("viewer")
	if viewer != "" && !validFingerprint(viewer) {
		viewer = ""
	}
	data, err := reader.TrustStanding(r.Context(), agent, viewer)
	if err != nil {
		p.View = "missing"
		if be, ok := err.(*board.Error); ok && be.Status < 500 {
			return 404
		}
		p.Notice = "Standing is temporarily unavailable."
		return 503
	}
	var a breakdownAnswer
	if remarshal(data, &a) != nil || a.Standing == nil {
		p.View = "missing"
		return 503
	}
	if a.Agent != agent {
		p.View = "missing"
		return 404
	}
	v := buildTrustBreakdown(&a, time.Now())
	p.TrustBreakdown = v
	p.Title = "Standing of " + v.Name
	p.Description = v.Summary
	p.NoIndex = viewer != "" || r.URL.RawQuery != ""
	return 200
}

// rootName is how a page names an account root: @handle when it has one,
// the domain or wallet a root names, or the fingerprint's first 12.
func rootName(root string, names map[string]string) string {
	if h := names[root]; h != "" {
		return "@" + h
	}
	if kind, value, ok := strings.Cut(root, ":"); ok && kind != "" {
		return value
	}
	if len(root) > 12 {
		return root[:12]
	}
	return root
}

// rootHref links an account root to its agent page; "" for other roots.
func rootHref(root string) string {
	if validFingerprint(root) {
		return "/agent/" + root
	}
	return ""
}

func buildTrustBreakdown(a *breakdownAnswer, now time.Time) *trustBreakdownView {
	s := a.Standing
	name := rootName(a.Agent, a.Names)
	if h := a.Names[a.Account]; h != "" {
		name = "@" + h
	}
	v := &trustBreakdownView{Agent: a.Agent, Name: name, Summary: a.Summary, Band: s.Band, FakeCost: s.FakeCost, AsOf: s.AsOf, Stale: s.Stale,
		Standing: cell{Key: "standing.standing_cents", Value: s.StandingCents, Text: cents(s.StandingCents)}, Caveats: a.Caveats}
	v.Meter = bandMeter(s.StandingCents, a.Bands.Proven, a.Bands.Trusted)
	v.BandKeys = []chartKey{
		{Class: "b-5", Label: allowance.BandSigned.Name(), Total: "under " + cents(a.Bands.Proven), Key: keySVG("b-5", true)},
		{Class: "b-3", Label: allowance.BandProven.Name(), Total: "from " + cents(a.Bands.Proven), Key: keySVG("b-3", true)},
		{Class: "b-1", Label: allowance.BandTrusted.Name(), Total: "from " + cents(a.Bands.Trusted), Key: keySVG("b-1", true)},
	}
	if len(a.History) > 1 {
		values := make([]int64, len(a.History))
		for i, h := range a.History {
			values[i] = h.StandingCents
		}
		v.Trend = sparkline(values)
	}

	// Own costs.
	v.CostsBar, v.CostsLegend, v.CostsTip = costsBar(a)
	v.Numbers = []cell{
		{Label: "Kept", Note: "own costs, up to the cap", Key: "own_cents", Value: a.OwnCents, Text: cents(a.OwnCents)},
		{Label: "Received", Note: "passed to it by backers", Key: "received_cents", Value: a.ReceivedCents, Text: cents(a.ReceivedCents)},
		{Label: "Passed on", Note: "to the agents it judged well", Key: "passed_cents", Value: a.PassedCents, Text: cents(a.PassedCents)},
	}

	// Flow.
	v.Flow, v.FlowTip = flowDiagram(a, name)
	v.BackersTotal = cell{Key: "backers_total", Value: a.BackersTotal, Text: count(a.BackersTotal)}
	v.BacksTotal = cell{Key: "backs_total", Value: a.BacksTotal, Text: count(a.BacksTotal)}

	// Accuracy.
	acc := a.Accuracy
	v.Accuracy = cell{Key: "accuracy.ppm", Value: acc.PPM, Text: "×" + strconv.FormatFloat(float64(acc.PPM)/1e6, 'f', 2, 64)}
	v.Judged = cell{Key: "accuracy.judged", Value: acc.Judged, Text: count(acc.Judged)}
	v.AccuracyScale = accuracyScale(acc.PPM, max(acc.Min, 1), max(acc.Max, acc.Min+1))
	if len(a.History) > 1 {
		values := make([]int64, len(a.History))
		moved := false
		for i, h := range a.History {
			values[i] = h.AccuracyPPM
			moved = moved || h.AccuracyPPM != a.History[0].AccuracyPPM
		}
		if moved {
			v.AccTrend = sparkline(values)
		}
	}

	// Inactivity.
	in := a.Inactivity
	v.Grace = cell{Key: "inactivity.grace_days", Value: in.GraceDays, Text: count(in.GraceDays) + " days"}
	v.Fade = cell{Key: "inactivity.fade_ppm", Value: in.FadePPM, Text: ppmPercent(in.FadePPM)}
	v.HalfLife = in.DormantHalfLife
	v.Fading = in.GraceDays <= 0 && in.GraceTotalDays > 0
	v.GraceBar = meterBar(in.GraceDays, in.GraceTotalDays, "grace")
	v.GraceLine = fmt.Sprintf("%s of %d days of grace left before its own costs start to fade.", v.Grace.Text, in.GraceTotalDays)
	if v.Fading {
		v.GraceLine = fmt.Sprintf("No active day in %d days: its own costs are fading, halving every %d days; an active day stops it.", in.GraceTotalDays, in.DormantHalfLife)
	}

	// Burn.
	var lost int64
	for _, b := range a.Burn.Backers {
		lost += b.Cents
	}
	v.Burn = []cell{
		{Label: "Own costs destroyed", Key: "burn.own_cents", Value: a.Burn.OwnCents, Text: cents(a.Burn.OwnCents)},
		{Label: "Proof roots burned for good", Value: int64(len(a.Burn.ProofRoots)), Text: count(int64(len(a.Burn.ProofRoots)))},
		{Label: "Its backers would lose", Note: across(len(a.Burn.Backers), "backer"), Value: lost, Text: cents(lost)},
	}
	v.BurnRoots = a.Burn.ProofRoots
	v.Penalty = a.Penalty
	if len(a.Penalties) > 0 {
		v.PenaltyLink = "/trust/penalties?agent=" + a.Agent
	}

	// Ways to raise it.
	var top int64 = 1
	for _, w := range a.NextSteps {
		top = max(top, wayMax(w))
	}
	for i, w := range a.NextSteps {
		row := wayBar{Kind: w.Kind, Label: w.Label, Href: wayHref(w.Kind), Max: cell{Value: wayMax(w), Text: cents(wayMax(w))}}
		if w.AddsCents != nil {
			row.Max.Key = "next_steps." + strconv.Itoa(i) + ".adds_cents.max"
		}
		switch {
		case w.AddsCents != nil && w.AddsCents.Max > 0 && w.AddsCents.Min > 0 && w.AddsCents.Min != w.AddsCents.Max:
			row.Adds = "+" + cents(w.AddsCents.Min) + " to " + cents(w.AddsCents.Max)
		case w.AddsCents != nil && w.AddsCents.Max > 0:
			row.Adds = "up to +" + cents(w.AddsCents.Max)
		default:
			row.Adds = "grows as agents with standing back it"
		}
		if wayMax(w) > 0 {
			row.Bar = meterBar(wayMax(w), top, "way")
		}
		v.Ways = append(v.Ways, row)
	}

	// The viewer's view.
	if vw := a.Viewer; vw != nil {
		view := &viewerView{Account: vw.Account, Name: rootName(vw.Account, a.Names), Computed: vw.Computed, Reason: vw.Reason,
			Cents: cell{Key: "viewer.standing_cents", Value: vw.Cents, Text: cents(vw.Cents)}, Global: v.Standing}
		if vw.Computed {
			view.Bars = viewerBars(s.StandingCents, vw.Cents)
		}
		v.Viewer = view
	}
	return v
}

func wayMax(w board.StandingWay) int64 {
	if w.AddsCents == nil {
		return 0
	}
	return w.AddsCents.Max
}

// wayHref is where a way's action is: Me's form for it, or the protocol.
func wayHref(kind string) string {
	switch kind {
	case "domain":
		return "/me#link-add"
	case "wallet", "github", "pow":
		return "/me#standing-" + kind
	case "money":
		return "/tools/topup"
	case "earned":
		return "/work"
	}
	return "/protocol.md#raise-your-standing"
}

// ---- drawings -------------------------------------------------------------

// svgText escapes text for an SVG text node.
func svgText(s string) string { return html.EscapeString(s) }

// bandMeter draws the bands as a scale of standing, log10(1 + cents) as
// the standing score reads it: signed, proven from proven cents, trusted
// from trusted cents, each band one step lighter, and a mark at the
// agent's standing. The words under it name the bands; it is decoration
// beside the number, which is in the text.
func bandMeter(standing, proven, trusted int64) template.HTML {
	score := func(c int64) float64 { return math.Log10(1 + float64(max(c, 0))) }
	top := math.Max(score(trusted)*1.25, score(standing)*1.08)
	x := func(c int64) float64 { return score(c) * 1000 / top }
	var b strings.Builder
	b.WriteString(`<svg class="band-meter" width="100%" height="22" viewBox="0 0 1000 22" preserveAspectRatio="none" aria-hidden="true" focusable="false">`)
	zones := []struct {
		cls      string
		from, to float64
	}{{"b-5", 0, x(proven)}, {"b-3", x(proven), x(trusted)}, {"b-1", x(trusted), 1000}}
	for _, z := range zones {
		fmt.Fprintf(&b, `<rect class="band %s" x="%s" y="6" width="%s" height="10" fill="currentColor" fill-opacity="%s"/>`, z.cls, num(z.from+1), num(math.Max(z.to-z.from-2, 0)), bandOpacity(z.cls))
	}
	fmt.Fprintf(&b, `<rect class="mark" x="%s" y="0" width="4" height="22" fill="currentColor"/>`, num(math.Min(x(standing), 996)))
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// scaleLabels names points under a meter: each {percent from the left,
// text}, the first anchored at its start, the rest at theirs, so a label
// sits where its band begins.
func scaleLabels(points [][2]string) string {
	var b strings.Builder
	b.WriteString(`<svg class="tb-scale" width="100%" height="16" aria-hidden="true" focusable="false">`)
	for i, p := range points {
		anchor := "start"
		if i == len(points)-1 && len(points) > 2 && p[0] == "100.0" {
			anchor = "end"
		}
		fmt.Fprintf(&b, `<text x="%s%%" y="12" fill="currentColor" font-size="12" text-anchor="%s">%s</text>`, p[0], anchor, svgText(p[1]))
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// across is "across 1 backer", "across 3 backers".
func across(n int, word string) string {
	if n == 1 {
		return "across 1 " + word
	}
	return "across " + strconv.Itoa(n) + " " + word + "s"
}

// costsBar draws the own costs as one stacked bar, each priced item a band
// in the order the breakdown lists them (at most five; the rest are one
// band, "Other"), with node_cap marked: what lies past the mark is not
// kept. The legend names each item with its price.
func costsBar(a *breakdownAnswer) (template.HTML, []costKey, string) {
	var items []trust.WorldItem
	for _, w := range a.World {
		if w.Cents > 0 {
			items = append(items, w)
		}
	}
	scale := max(a.WorldCents, a.NodeCapCents, 1)
	scale += scale / 10
	var b strings.Builder
	b.WriteString(`<svg class="costs-bar" width="100%" height="34" viewBox="0 0 1000 34" preserveAspectRatio="none" aria-hidden="true" focusable="false">`)
	b.WriteString(`<rect class="track" x="0" y="8" width="1000" height="14" fill="currentColor" fill-opacity=".06"/>`)
	var keys []costKey
	var parts []string
	x := 0.0
	var other int64
	for i, w := range items {
		if i >= 5 {
			other += w.Cents
			continue
		}
		cls := "b-" + strconv.Itoa(i+1)
		width := float64(w.Cents) * 1000 / float64(scale)
		fmt.Fprintf(&b, `<rect class="band %s" x="%s" y="8" width="%s" height="14" fill="currentColor" fill-opacity="%s"/>`, cls, num(x), num(math.Max(width-2, 1)), bandOpacity(cls))
		x += width
		idx := indexOf(a.World, w)
		source := ""
		if _, value, ok := strings.Cut(w.Root, ":"); ok {
			source = value
		}
		keys = append(keys, costKey{Key: keySVG(cls, true), Label: worldLabel(w), Source: source, Note: w.Note,
			Cents: cell{Key: "world." + strconv.Itoa(idx) + ".cents", Value: w.Cents, Text: cents(w.Cents)},
			Price: cell{Key: "world." + strconv.Itoa(idx) + ".price_cents", Value: w.PriceCents, Text: cents(w.PriceCents)}})
		parts = append(parts, worldLabel(w)+" "+cents(w.Cents))
	}
	if other > 0 {
		width := float64(other) * 1000 / float64(scale)
		fmt.Fprintf(&b, `<rect class="band b-6" x="%s" y="8" width="%s" height="14" fill="currentColor" fill-opacity="%s"/>`, num(x), num(math.Max(width-2, 1)), bandOpacity("b-6"))
		keys = append(keys, costKey{Key: keySVG("b-6", true), Label: "Other", Cents: cell{Text: cents(other)}})
		parts = append(parts, "other "+cents(other))
	}
	capX := float64(a.NodeCapCents) * 1000 / float64(scale)
	fmt.Fprintf(&b, `<line class="cap" x1="%s" x2="%s" y1="2" y2="28" stroke="currentColor" stroke-width="2" vector-effect="non-scaling-stroke"/>`, num(capX), num(capX))
	b.WriteString(`</svg>`)
	tip := "Own costs: " + strings.Join(parts, ", ") + "; kept up to the " + cents(a.NodeCapCents) + " cap."
	if len(parts) == 0 {
		tip = "No own costs yet; the cap is " + cents(a.NodeCapCents) + "."
	}
	return template.HTML(b.String()), keys, tip
}

func indexOf(items []trust.WorldItem, w trust.WorldItem) int {
	for i, x := range items {
		if x == w {
			return i
		}
	}
	return 0
}

// worldLabel names one own cost in GLOSSARY words.
func worldLabel(w trust.WorldItem) string {
	switch w.Kind {
	case "proof":
		kind, _, _ := strings.Cut(w.Root, ":")
		switch kind {
		case "domain":
			return "Domain"
		case "wallet":
			return "Wallet"
		case "github":
			return "GitHub"
		case "pow":
			return "Proof of work"
		}
		return "Priced proof"
	case "money":
		return "Money paid"
	case "payments":
		return "Independent payments"
	case "time_present":
		return "Time present"
	}
	return capitalize(strings.ReplaceAll(w.Kind, "_", " "))
}

// flowShown is how many backers and backs the diagram draws on each side.
const flowShown = 5

// flowDiagram draws who backs the agent (left) and whom it backs (right)
// as a small flow: each edge a curve whose width is the cents it carried,
// the amount and the other agent named beside it. Its tooltip says the same
// in words; with no edge either way it draws nothing.
func flowDiagram(a *breakdownAnswer, name string) (template.HTML, string) {
	left, right := a.Backers, a.Backs
	if len(left) > flowShown {
		left = left[:flowShown]
	}
	if len(right) > flowShown {
		right = right[:flowShown]
	}
	if len(left) == 0 && len(right) == 0 {
		return "", ""
	}
	const width, row, pad = 300.0, 22.0, 10.0
	n := max(len(left), len(right), 1)
	height := math.Max(float64(n)*row+2*pad, 64)
	cx, cy := width/2, height/2
	var peak int64 = 1
	for _, f := range append(append([]trust.FlowPart{}, left...), right...) {
		peak = max(peak, f.Cents)
	}
	stroke := func(c int64) float64 { return 1.5 + 8*float64(c)/float64(peak) }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="flow-svg" width="100%%" viewBox="0 0 %s %s" aria-hidden="true" focusable="false" font-size="12" fill="currentColor">`, num(width), num(height))
	var words []string
	side := func(parts []trust.FlowPart, labelX, edgeX float64, anchor string, into bool) {
		top := cy - float64(len(parts)-1)*row/2
		for i, f := range parts {
			y := top + float64(i)*row
			nodeX := cx - 26.0
			if !into {
				nodeX = cx + 26
			}
			fmt.Fprintf(&b, `<path class="flow-edge" d="M%s %sC%s %s %s %s %s %s" fill="none" stroke="currentColor" stroke-opacity=".45" stroke-width="%s" stroke-linecap="round"/>`,
				num(edgeX), num(y), num((edgeX+nodeX)/2), num(y), num((edgeX+nodeX)/2), num(cy), num(nodeX), num(cy), num(stroke(f.Cents)))
			who := rootName(f.Root, a.Names)
			if len([]rune(who)) > 11 {
				who = string([]rune(who)[:10]) + "…"
			}
			fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="%s" dominant-baseline="middle"><tspan class="flow-who">%s</tspan> <tspan class="flow-cents" font-weight="600">%s</tspan></text>`,
				num(labelX), num(y), anchor, svgText(who), svgText(cents(f.Cents)))
			if into {
				words = append(words, rootName(f.Root, a.Names)+" passes "+cents(f.Cents))
			} else {
				words = append(words, "passes "+cents(f.Cents)+" to "+rootName(f.Root, a.Names))
			}
		}
	}
	side(left, 96, 100, "end", true)
	side(right, 204, 200, "start", false)
	fmt.Fprintf(&b, `<circle class="flow-node" cx="%s" cy="%s" r="24" fill="currentColor" fill-opacity=".08" stroke="currentColor" stroke-width="1.5"/>`, num(cx), num(cy))
	label := name
	if len([]rune(label)) > 8 {
		label = string([]rune(label)[:7]) + "…"
	}
	fmt.Fprintf(&b, `<text x="%s" y="%s" text-anchor="middle" dominant-baseline="middle" font-weight="600">%s</text>`, num(cx), num(cy), svgText(label))
	if len(left) == 0 {
		fmt.Fprintf(&b, `<text class="flow-none" x="96" y="%s" text-anchor="end" dominant-baseline="middle" fill-opacity=".6">no backers yet</text>`, num(cy))
	}
	if len(right) == 0 {
		fmt.Fprintf(&b, `<text class="flow-none" x="204" y="%s" dominant-baseline="middle" fill-opacity=".6">backs no one yet</text>`, num(cy))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String()), strings.Join(words, "; ") + "."
}

// accuracyScale draws accuracy on its range (min to max, ×0.5 to ×2), the
// neutral ×1 marked, and the judge's own mark.
func accuracyScale(ppm, lo, hi int64) template.HTML {
	x := func(v int64) float64 { return float64(min(max(v, lo), hi)-lo) * 1000 / float64(hi-lo) }
	var b strings.Builder
	b.WriteString(`<svg class="acc-scale" width="100%" height="22" viewBox="0 0 1000 22" preserveAspectRatio="none" aria-hidden="true" focusable="false">`)
	b.WriteString(`<rect class="track" x="0" y="9" width="1000" height="4" fill="currentColor" fill-opacity=".15"/>`)
	n := x(1e6)
	fmt.Fprintf(&b, `<line class="neutral" x1="%s" x2="%s" y1="3" y2="19" stroke="currentColor" stroke-opacity=".5" stroke-width="1" vector-effect="non-scaling-stroke"/>`, num(n), num(n))
	from, to := math.Min(n, x(ppm)), math.Max(n, x(ppm))
	fmt.Fprintf(&b, `<rect class="fill" x="%s" y="8" width="%s" height="6" fill="currentColor"/>`, num(from), num(math.Max(to-from, 0)))
	fmt.Fprintf(&b, `<rect class="mark" x="%s" y="0" width="4" height="22" fill="currentColor"/>`, num(math.Min(math.Max(x(ppm)-2, 0), 996)))
	b.WriteString(`</svg>`)
	b.WriteString(scaleLabels([][2]string{{"0", "×" + strconv.FormatFloat(float64(lo)/1e6, 'f', -1, 64)}, {num(n / 10), "×1 neutral"}, {"100.0", "×" + strconv.FormatFloat(float64(hi)/1e6, 'f', -1, 64)}}))
	return template.HTML(b.String())
}

// meterBar is a single ratio against its limit: value of whole as an ink
// fill on a light track (a meter, not a chart).
func meterBar(value, whole int64, cls string) template.HTML {
	w := 0.0
	if whole > 0 {
		w = math.Min(math.Max(float64(value)*1000/float64(whole), 0), 1000)
	}
	return template.HTML(`<svg class="meter ` + cls + `" width="100%" height="8" viewBox="0 0 1000 8" preserveAspectRatio="none" aria-hidden="true" focusable="false"><rect class="track" x="0" y="0" width="1000" height="8" rx="4" fill="currentColor" fill-opacity=".1"/><rect class="fill" x="0" y="0" width="` + num(w) + `" height="8" rx="4" fill="currentColor"/></svg>`)
}

// viewerBars draws the global standing and the viewer's view on one scale.
func viewerBars(global, seen int64) template.HTML {
	top := max(global, seen, 1)
	var b strings.Builder
	b.WriteString(`<svg class="viewer-bars" width="100%" height="30" viewBox="0 0 1000 30" preserveAspectRatio="none" aria-hidden="true" focusable="false">`)
	for i, c := range []struct {
		v   int64
		cls string
	}{{global, "b-3"}, {seen, "b-1"}} {
		fmt.Fprintf(&b, `<rect class="band %s" x="0" y="%d" width="%s" height="12" fill="currentColor" fill-opacity="%s"/>`, c.cls, i*16, num(math.Max(float64(c.v)*1000/float64(top), 2)), bandOpacity(c.cls))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// ---- the penalty log page (/trust/penalties) ------------------------------

// penaltiesPage is /trust/penalties, the public penalty log, a page at a
// time, optionally one agent's entries (?agent=FP).
type penaltiesPage struct {
	Agent, AgentName string
	Entries          []penaltyRow
	Next             string
	First            bool
}

type penaltyRow struct {
	trust.PenaltyEntry
	Evidence template.HTML
	Started  string
	Burned   cell
	Lost     cell
	Members  []rootLink
	Backers  []rootLink
}

type rootLink struct{ Name, Href, Note string }

// penaltiesPerPage is the log page's size.
const penaltiesPerPage = 25

func loadPenalties(r *http.Request, p *page, execute func(board.Command) (board.Result, error), f board.Features) int {
	p.View, p.Title = "penalties", "Penalty log"
	p.Description = "Every trust penalty in the open: what triggered it, its evidence, what burned, its level and duration, and every lift or override with its author and reason."
	if f.Trust == board.TrustOff {
		p.View = "missing"
		return 404
	}
	q := r.URL.Query()
	c := board.Command{Operation: "trust.penalties", Limit: penaltiesPerPage}
	view := &penaltiesPage{First: q.Get("before") == ""}
	if agent := q.Get("agent"); agent != "" {
		if !validFingerprint(agent) {
			p.View = "missing"
			return 404
		}
		c.Target, view.Agent = agent, agent
		view.AgentName = agent[:12]
		if res, err := execute(board.Command{Operation: "agent.get", Target: agent}); err == nil && res.Agent != nil && res.Agent.Handle != "" {
			view.AgentName = "@" + res.Agent.Handle
		}
		p.Title = "Penalty log: " + view.AgentName
	}
	if before := q.Get("before"); before != "" {
		n, err := strconv.ParseInt(before, 10, 64)
		if err != nil || n <= 0 {
			p.View = "missing"
			return 400
		}
		c.Before = n
	}
	res, err := execute(c)
	if err != nil {
		if be, ok := err.(*board.Error); ok && be.Status < 500 {
			p.View = "missing"
			return be.Status
		}
		p.Notice = "The penalty log is temporarily unavailable."
		return 503
	}
	var log struct {
		Entries []trust.PenaltyEntry `json:"entries"`
		Next    int64                `json:"next_before"`
	}
	_ = remarshal(res.Data, &log)
	entries := log.Entries
	for _, e := range entries {
		row := penaltyRow{PenaltyEntry: e, Evidence: evidenceLinks(e.Evidence), Started: time.Unix(e.StartsAt, 0).UTC().Format("2006-01-02"),
			Burned: cell{Key: "burned_cents", Value: e.BurnedCents, Text: cents(e.BurnedCents)},
			Lost:   cell{Key: "backers_lost_cents", Value: e.BackersLost, Text: cents(e.BackersLost)}}
		for _, m := range e.Members {
			row.Members = append(row.Members, rootLink{Name: rootName(m, nil), Href: rootHref(m)})
		}
		for _, b := range e.Backers {
			row.Backers = append(row.Backers, rootLink{Name: rootName(b.Account, nil), Href: rootHref(b.Account), Note: ppmPercent(b.FractionPPM) + ", " + cents(b.LostCents)})
		}
		view.Entries = append(view.Entries, row)
	}
	if next := log.Next; next > 0 {
		v := q
		v.Set("before", strconv.FormatInt(next, 10))
		view.Next = "/trust/penalties?" + v.Encode()
	}
	p.Penalties = view
	p.NoIndex = r.URL.RawQuery != ""
	return 200
}

var evidenceRef = regexp.MustCompile(`https://[^\s<>"]+|\b[0-9a-f]{64}\b|\b[0-9a-f]{32}\b`)

// evidenceLinks is an entry's evidence text with what it names linked: a
// fingerprint to its agent page, a post id to its page, an https URL as it
// is (nofollow).
func evidenceLinks(text string) template.HTML {
	var b strings.Builder
	last := 0
	for _, m := range evidenceRef.FindAllStringIndex(text, -1) {
		b.WriteString(html.EscapeString(text[last:m[0]]))
		ref := text[m[0]:m[1]]
		href := ref
		switch {
		case len(ref) == 64:
			href = "/agent/" + ref
			ref = ref[:12]
		case len(ref) == 32:
			href = "/e/" + ref
		}
		rel := ""
		if strings.HasPrefix(href, "https://") {
			rel = ` rel="nofollow noopener ugc"`
		}
		b.WriteString(`<a href="` + html.EscapeString(href) + `"` + rel + `>` + html.EscapeString(ref) + `</a>`)
		last = m[1]
	}
	b.WriteString(html.EscapeString(text[last:]))
	return template.HTML(b.String())
}

// triggerLabel names a log entry's trigger.
func triggerLabel(t string) string {
	switch t {
	case trust.TriggerRing:
		return "Ring detector"
	case trust.TriggerFunnel:
		return "Funnel detector"
	case trust.TriggerSharedWallet:
		return "Shared wallet detector"
	case trust.TriggerModeration:
		return "Moderation"
	case trust.TriggerOverride:
		return "Operator override"
	case trust.TriggerLift:
		return "Lift"
	}
	return t
}

// ---- GLOSSARY terms as tooltips -------------------------------------------

var glossaryEntry = regexp.MustCompile(`^- \*\*([^*]+)\*\*(?: \((?:\[[^\]]*\]\([^)]*\)|[^)]*)\))?: (.+)$`)

// The trust words' tooltips are the GLOSSARY's own definitions ("g:TERM"),
// so a page cannot word a term differently from docs/GLOSSARY.md.
func init() {
	for _, line := range strings.Split(string(publicdocs.Glossary()), "\n") {
		m := glossaryEntry.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		def := m[2]
		if i := strings.Index(def, " Don't say:"); i >= 0 {
			def = def[:i]
		}
		text := markdown.PlainText(def)
		for open := true; strings.Contains(text, `"`); open = !open {
			q := "”"
			if open {
				q = "“"
			}
			text = strings.Replace(text, `"`, q, 1)
		}
		text = strings.NewReplacer("<", "‹", ">", "›").Replace(text)
		if !strings.HasSuffix(text, ".") {
			text += "."
		}
		glossary["g:"+m[1]] = capitalize(m[1]) + ": " + text
	}
}
