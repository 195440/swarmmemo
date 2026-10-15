package web

import (
	"fmt"
	"html"
	"html/template"
	"strconv"
	"strings"
	"time"
)

// The /stats charts are server-rendered SVG, so they draw without
// JavaScript. A chart is drawn in a 1000 by 100 viewBox stretched to the
// plot (preserveAspectRatio none, strokes kept at their width by
// vector-effect). Each drawn series carries its formatted values
// (data-values) and positions (data-ys, percent from the top), and a
// readout-only series its values alone; assets/stats.js turns these into
// a crosshair with every series' value at the hovered or focused x.
// Without it, the legend's totals, the aria-label and the tables under the
// charts carry the numbers; no per-point markup is repeated for it.
//
// Every SVG carries its own width, height, fill and stroke as attributes
// (currentColor, with opacity for the lighter marks), so a chart still
// draws as thin lines and light bands if the stylesheet fails to load; the
// stylesheet's class rules refine them.

const chartWidth = 1000

// chartSeries is one series of a chart.
type chartSeries struct {
	Class, Label string
	// Term is the glossary key explaining Label in the legend, if any.
	Term   string
	Values []int64
	Format func(int64) string
	// Alt draws the series against the second scale, labelled on the left.
	Alt bool
	// Readout series are not drawn; their values appear in the readout,
	// and with Legend also in the legend, with their totals.
	Readout, Legend bool
	// NoTotal leaves the series' total out of the legend, for a count
	// whose days do not add up (distinct agents per day).
	NoTotal bool
	// Bytes rounds the scale in whole KB or MB.
	Bytes bool
}

// lineChart is one figure of /stats.
type lineChart struct {
	Title, Note, Summary string
	Legend               []chartKey
	Max, Half            string
	AltMax, AltHalf      string
	// Xs are the x labels joined by "|", for the readout.
	Xs string
	// Partial marks a last x still filling (today, this hour, this week):
	// the readout names it "so far". A chart of whole runs or bins has none.
	Partial bool
	SVG     template.HTML
	Axis    []axisLabel
}

// chartKey is a legend entry: a drawn series with its total over the range.
type chartKey struct {
	Class, Label, Term, Total, Share string
	// Alt is on the second scale; Area a band; Split a readout series
	// listed with its total but not drawn.
	Alt, Area, Split bool
	// Key is the legend's mark, drawn like the series.
	Key template.HTML
}

// keySVG is the legend mark of a line or band of class cls.
func keySVG(cls string, area bool) template.HTML {
	mark := `<line class="line ` + cls + `" x1="0" x2="16" y1="5" y2="5"` + lineAttrs(cls) + `/>`
	if area {
		mark = `<rect class="band ` + cls + `" x="0" y="0" width="16" height="10" fill="currentColor" fill-opacity="` + bandOpacity(cls) + `"/>`
	}
	return template.HTML(`<svg class="key" width="16" height="10" viewBox="0 0 16 10" aria-hidden="true" focusable="false">` + mark + `</svg>`)
}

// lineAttrs are the presentation attributes of a line of class cls: the
// fallback the stylesheet refines.
func lineAttrs(cls string) string {
	out := ` fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"`
	switch cls {
	case "s-2":
		out += ` stroke-dasharray="6 4"`
	case "s-3":
		out += ` stroke-dasharray="2 3"`
	case "s-alt":
		out += ` stroke-dasharray=".5 5"`
	case "partial":
		out += ` stroke-opacity=".5" stroke-dasharray="2 4"`
	}
	return out
}

// bandOpacity is the fallback fill opacity of band class b-N.
func bandOpacity(cls string) string {
	switch cls {
	case "b-1":
		return ".9"
	case "b-2":
		return ".65"
	case "b-3":
		return ".45"
	case "b-4":
		return ".3"
	case "b-5":
		return ".18"
	}
	return ".08"
}

// chartOpen starts a plot's SVG; gridLines draws its grid at the top and
// middle, chartClose its baseline.
const (
	chartOpen  = `<svg class="chart-svg" width="100%" height="150" viewBox="0 0 1000 100" preserveAspectRatio="none" aria-hidden="true" focusable="false">`
	gridLines  = `<line class="grid" x1="0" x2="1000" y1="0" y2="0" stroke="currentColor" stroke-opacity=".15" vector-effect="non-scaling-stroke"/><line class="grid" x1="0" x2="1000" y1="50" y2="50" stroke="currentColor" stroke-opacity=".15" vector-effect="non-scaling-stroke"/>`
	chartClose = `<line class="baseline" x1="0" x2="1000" y1="100" y2="100" stroke="currentColor" stroke-opacity=".4" vector-effect="non-scaling-stroke"/></svg>`
)

// axisLabel is one tick label; a Minor one is left out on narrow screens.
// A Major one (the first tick of a UTC day on an hourly axis) never is.
type axisLabel struct {
	Left  float64
	Text  string
	Minor bool
	Major bool
}

// chartX is where point i of n sits, in viewBox units.
func chartX(i, n int) float64 {
	if n <= 1 {
		return chartWidth / 2
	}
	return float64(i) * chartWidth / float64(n-1)
}

// scaleTop rounds a peak up to a readable top of scale.
func scaleTop(peak int64, bytes bool) int64 {
	if bytes {
		for _, u := range []int64{1 << 20, 1 << 10} {
			if peak >= u {
				return niceCeiling((peak+u-1)/u) * u
			}
		}
	}
	return niceCeiling(peak)
}

func num(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

// axisFor labels the x positions where label returns text; on a long range,
// or with more than axisLabelsNarrow labels, every other label is minor, so a
// phone shows half of them (six date labels overlap at 320px).
func axisFor(n int, label func(int) string) []axisLabel {
	return axisMarked(n, func(i int) (string, bool) { return label(i), false })
}

// axisMarked is axisFor where label also says whether a label is Major.
func axisMarked(n int, label func(int) (string, bool)) []axisLabel {
	var out []axisLabel
	for i := range n {
		if text, major := label(i); text != "" {
			left := 50.0
			if n > 1 {
				left = float64(i) * 100 / float64(n-1)
			}
			out = append(out, axisLabel{Left: left, Text: text, Major: major})
		}
	}
	thinAxis(out, n > 60)
	return out
}

// thinAxis marks every other label minor when crowded (or always), so a
// phone shows half of them. A Major label stays, and its unmarked
// neighbours become minor, as a dated label is wider than an hour.
func thinAxis(out []axisLabel, always bool) {
	if !always && len(out) <= axisLabelsNarrow {
		return
	}
	for i := 1; i < len(out); i += 2 {
		out[i].Minor = true
	}
	for i, l := range out {
		if !l.Major {
			continue
		}
		out[i].Minor = false
		for _, j := range []int{i - 1, i + 1} {
			if j >= 0 && j < len(out) && !out[j].Major {
				out[j].Minor = true
			}
		}
	}
}

// hourAxis labels an hourly axis every stride hours, counted back from the
// last bucket. The first tick of each UTC day carries its date ("2 Jan",
// or "2 Jan 15:04" when the stride misses midnight) and is Major, so a
// phone always shows which day it is; the other ticks show the hour.
func hourAxis(starts []time.Time, stride int) []axisLabel {
	n := len(starts)
	lastDay := ""
	return axisMarked(n, func(i int) (string, bool) {
		if (n-1-i)%stride != 0 {
			return "", false
		}
		at := starts[i].UTC()
		day := at.Format("2 Jan")
		switch {
		case day == lastDay:
			return at.Format("15:04"), false
		case at.Hour() == 0 && at.Minute() == 0:
			lastDay = day
			return day, true
		default:
			lastDay = day
			return at.Format("2 Jan 15:04"), true
		}
	})
}

// axisLabelsNarrow is how many x labels a phone shows in full.
const axisLabelsNarrow = 5

func joinFormatted(values []int64, format func(int64) string) string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = format(v)
	}
	return strings.Join(out, "|")
}

// buildLineChart draws series as lines over xs, a light area under the
// first when it is alone. A series marked Alt is drawn against its own
// scale. The last x is still filling: with partial, its name in the legend
// ("Today so far"), each line reaches it as a faint dashed segment, with no
// area under it, so a day or hour not yet over never reads as a drop.
func buildLineChart(title, note string, xs []string, axis []axisLabel, partial string, series []chartSeries) lineChart {
	c := lineChart{Title: title, Note: note, Xs: strings.Join(xs, "|"), Axis: axis}
	var peak, altPeak int64
	var bytes, altBytes bool
	format := count
	drawn := 0
	for _, s := range series {
		if s.Readout {
			continue
		}
		drawn++
		for _, v := range s.Values {
			if s.Alt {
				altPeak = max(altPeak, v)
			} else {
				peak = max(peak, v)
			}
		}
		if s.Alt {
			altBytes = s.Bytes
		} else {
			bytes, format = s.Bytes, s.Format
		}
	}
	top, altTop := scaleTop(peak, bytes), scaleTop(altPeak, altBytes)
	c.Max, c.Half = format(top), format(top/2)
	if top%2 != 0 {
		c.Half = ""
	}
	n := len(xs)
	dashed := partial != "" && n > 1
	var b strings.Builder
	b.WriteString(chartOpen + gridLines)
	legend := []string{}
	var split []chartKey
	for _, s := range series {
		var total int64
		for _, v := range s.Values {
			total += v
		}
		if s.Readout {
			fmt.Fprintf(&b, `<g class="readout-only" data-label="%s" data-values="%s"></g>`, html.EscapeString(s.Label), html.EscapeString(joinFormatted(s.Values, s.Format)))
			if s.Legend {
				split = append(split, chartKey{Label: s.Label, Term: s.Term, Total: s.Format(total), Split: true})
				legend = append(legend, s.Label+" "+s.Format(total))
			}
			continue
		}
		scale := top
		if s.Alt {
			scale = altTop
			c.AltMax, c.AltHalf = s.Format(altTop), s.Format(altTop/2)
			if altTop%2 != 0 {
				c.AltHalf = ""
			}
		}
		points := make([]string, n)
		ys := make([]string, n)
		for i, v := range s.Values {
			y := 100 - min(float64(v)*100/float64(scale), 100)
			points[i] = num(chartX(i, n)) + "," + num(y)
			ys[i] = num(y)
		}
		whole := points
		if dashed {
			whole = points[:n-1]
		}
		if drawn == 1 {
			fmt.Fprintf(&b, `<polygon class="area %s" points="0,100 %s %s,100" fill="currentColor" fill-opacity=".06" stroke="none"/>`, s.Class, strings.Join(whole, " "), num(chartX(len(whole)-1, n)))
		}
		fmt.Fprintf(&b, `<polyline class="line %s" points="%s"%s vector-effect="non-scaling-stroke" data-label="%s" data-values="%s" data-ys="%s"/>`, s.Class, strings.Join(whole, " "), lineAttrs(s.Class), html.EscapeString(s.Label), html.EscapeString(joinFormatted(s.Values, s.Format)), strings.Join(ys, "|"))
		if dashed {
			fmt.Fprintf(&b, `<polyline class="line partial %s" points="%s %s"%s vector-effect="non-scaling-stroke"/>`, s.Class, points[n-2], points[n-1], lineAttrs("partial"))
		}
		key := chartKey{Class: s.Class, Label: s.Label, Term: s.Term, Total: s.Format(total), Alt: s.Alt, Key: keySVG(s.Class, false)}
		if s.NoTotal {
			key.Total = ""
		} else {
			legend = append(legend, s.Label+" "+s.Format(total))
		}
		c.Legend = append(c.Legend, key)
	}
	b.WriteString(chartClose)
	c.SVG = template.HTML(b.String())
	if drawn == 1 && len(split) == 0 {
		c.Legend = nil
	}
	c.Legend = append(c.Legend, split...)
	c.Summary = strings.TrimSpace(title+". "+note) + " Totals over the range: " + strings.Join(legend, ", ") + "."
	if dashed {
		c.Partial = true
		c.Legend = append(c.Legend, chartKey{Class: "partial", Label: partial, Key: keySVG("partial", false)})
		c.Summary += " The last point, " + strings.ToLower(partial) + ", is dashed."
	}
	c.Summary += " All numbers, at the bottom, lists each day."
	return c
}

// buildShareChart draws series as a 100% stacked area over xs: each band
// is the series' share of the x's total, so a change in the mix shows even
// while the totals move. The readout gives each count and its share.
func buildShareChart(title, note string, xs []string, axis []axisLabel, series []chartSeries) lineChart {
	c := lineChart{Title: title, Note: note, Xs: strings.Join(xs, "|"), Axis: axis, Max: "100%", Half: "50%"}
	n := len(xs)
	totals := make([]int64, n)
	var grand int64
	for _, s := range series {
		for i, v := range s.Values {
			totals[i] += v
			grand += v
		}
	}
	share := func(v, total int64) string { return percent(v, total) }
	var b strings.Builder
	b.WriteString(chartOpen)
	below := make([]float64, n) // cumulative share below the band, in percent
	legend := []string{}
	for _, s := range series {
		upper := make([]string, n)
		lower := make([]string, n)
		values := make([]string, n)
		var total int64
		for i, v := range s.Values {
			total += v
			h := 0.0
			if totals[i] > 0 {
				h = float64(v) * 100 / float64(totals[i])
			}
			x := num(chartX(i, n))
			lower[n-1-i] = x + "," + num(100-below[i])
			below[i] += h
			upper[i] = x + "," + num(100-below[i])
			values[i] = count(v) + " · " + share(v, totals[i])
			if totals[i] == 0 {
				values[i] = "0"
			}
		}
		fmt.Fprintf(&b, `<polygon class="band %s" points="%s %s" fill="currentColor" fill-opacity="%s" stroke="none" vector-effect="non-scaling-stroke" data-label="%s" data-values="%s"/>`, s.Class, strings.Join(upper, " "), strings.Join(lower, " "), bandOpacity(s.Class), html.EscapeString(s.Label), html.EscapeString(strings.Join(values, "|")))
		c.Legend = append(c.Legend, chartKey{Class: s.Class, Label: s.Label, Term: s.Term, Total: count(total), Share: share(total, grand), Area: true, Key: keySVG(s.Class, true)})
		legend = append(legend, s.Label+" "+count(total)+" ("+share(total, grand)+")")
	}
	b.WriteString(chartClose)
	c.SVG = template.HTML(b.String())
	c.Summary = title + ". " + note + " Over the range: " + strings.Join(legend, ", ") + "."
	return c
}

// sparkline draws values as a small line with a light area, for a stat
// tile; the tile's words carry the number, so it is decoration.
func sparkline(values []int64) template.HTML {
	if len(values) == 0 {
		return ""
	}
	var peak int64
	for _, v := range values {
		peak = max(peak, v)
	}
	peak = max(peak, 1)
	points := make([]string, len(values))
	for i, v := range values {
		x := 50.0
		if len(values) > 1 {
			x = float64(i) * 100 / float64(len(values)-1)
		}
		points[i] = num(x) + "," + num(22-float64(v)*20/float64(peak))
	}
	line := strings.Join(points, " ")
	return template.HTML(`<svg class="spark" width="100%" height="24" viewBox="0 0 100 24" preserveAspectRatio="none" aria-hidden="true" focusable="false"><polygon class="spark-area" points="0,24 ` + line + ` 100,24" fill="currentColor" fill-opacity=".08" stroke="none"/><polyline class="spark-line" points="` + line + `" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" vector-effect="non-scaling-stroke"/></svg>`)
}

// statsBar is a table cell's bar, width percent of the cell, sized and
// filled by its own attributes.
func statsBar(width float64) template.HTML {
	return template.HTML(`<svg class="via-bar" width="100%" height="8" aria-hidden="true" focusable="false"><rect height="100%" width="` + strconv.FormatFloat(max(0, min(width, 100)), 'f', 1, 64) + `%" fill="currentColor"/></svg>`)
}

// buildBarChart draws series as stacked bars, one bar per x, each series a
// band (its Class, b-1 to b-6) stacked from the baseline, labelled under
// its bar by labels ("" for none). A bar marked partial (a week not over
// yet, or cut by the range) is drawn faint and named partialLabel in the
// legend, so it never reads as a drop. Each band carries its values for
// the readout, like the share chart's.
func buildBarChart(title, note string, xs, labels []string, partial []bool, partialLabel string, series []chartSeries) lineChart {
	n := len(xs)
	c := lineChart{Title: title, Note: note, Xs: strings.Join(xs, "|")}
	totals := make([]int64, n)
	for _, s := range series {
		for i, v := range s.Values {
			totals[i] += v
		}
	}
	var peak, grand int64
	for _, t := range totals {
		peak, grand = max(peak, t), grand+t
	}
	top := scaleTop(peak, false)
	c.Max, c.Half = count(top), count(top/2)
	if top%2 != 0 {
		c.Half = ""
	}
	slot := float64(chartWidth) / float64(max(n, 1))
	for i, l := range labels {
		if l != "" {
			c.Axis = append(c.Axis, axisLabel{Left: (float64(i) + .5) * 100 / float64(max(n, 1)), Text: l})
		}
	}
	thinAxis(c.Axis, false)
	var b strings.Builder
	b.WriteString(chartOpen + gridLines)
	below := make([]float64, n)
	legend := []string{}
	anyPartial := false
	for _, s := range series {
		var total int64
		fmt.Fprintf(&b, `<g class="band %s" fill="currentColor" fill-opacity="%s" data-label="%s" data-values="%s">`, s.Class, bandOpacity(s.Class), html.EscapeString(s.Label), html.EscapeString(joinFormatted(s.Values, count)))
		for i, v := range s.Values {
			total += v
			if v <= 0 {
				continue
			}
			h := float64(v) * 100 / float64(top)
			below[i] += h
			faint := ""
			if i < len(partial) && partial[i] {
				faint, anyPartial = ` fill-opacity=".35"`, true
			}
			fmt.Fprintf(&b, `<rect x="%s" y="%s" width="%s" height="%s"%s vector-effect="non-scaling-stroke"/>`, num(float64(i)*slot+slot*.15), num(100-below[i]), num(slot*.7), num(h), faint)
		}
		b.WriteString(`</g>`)
		c.Legend = append(c.Legend, chartKey{Class: s.Class, Label: s.Label, Term: s.Term, Total: count(total), Share: percent(total, grand), Area: true, Key: keySVG(s.Class, true)})
		legend = append(legend, s.Label+" "+count(total))
	}
	b.WriteString(chartClose)
	c.SVG = template.HTML(b.String())
	c.Summary = title + ". " + note + " Over the range: " + strings.Join(legend, ", ") + "."
	if anyPartial {
		c.Partial = len(partial) > 0 && partial[len(partial)-1]
		c.Legend = append(c.Legend, chartKey{Class: "partial", Label: partialLabel, Key: template.HTML(`<svg class="key" width="16" height="10" viewBox="0 0 16 10" aria-hidden="true" focusable="false"><rect x="0" y="0" width="16" height="10" fill="currentColor" fill-opacity=".35"/></svg>`)})
		c.Summary += " Faint bars are " + strings.ToLower(partialLabel) + "."
	}
	return c
}
