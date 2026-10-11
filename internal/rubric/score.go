package rubric

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"sort"
	"time"
)

// Result is a scored subject. Its JSON (Encode) is canonical: the same
// rubric and observations give the same bytes.
type Result struct {
	Schema             string            `json:"schema"`
	Rubric             RubricRef         `json:"rubric"`
	Subject            Subject           `json:"subject"`
	AsOf               string            `json:"as_of"`
	ObservationsSHA256 string            `json:"observations_sha256"`
	Seed               string            `json:"seed"`
	Draws              int               `json:"draws"`
	Score              int               `json:"score"`
	Band               [2]int            `json:"band"`
	Grade              string            `json:"grade"`
	Confidence         string            `json:"confidence"`
	Verdict            string            `json:"verdict"`
	Reasons            []string          `json:"reasons"`
	Outlook            string            `json:"outlook"`
	Watch              bool              `json:"watch"`
	Stale              bool              `json:"stale"`
	Flags              []string          `json:"flags"`
	Cap                *int              `json:"cap,omitempty"`
	Uncapped           *Uncapped         `json:"uncapped,omitempty"`
	Dimensions         []DimResult       `json:"dimensions"`
	Checks             []CheckOut        `json:"checks"`
	Value              map[string]string `json:"value,omitempty"`
}

// RubricRef identifies the rubric a result was computed with.
type RubricRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// Uncapped is the score and band before gates capped them.
type Uncapped struct {
	Score int    `json:"score"`
	Band  [2]int `json:"band"`
}

// DimResult is one dimension's score; NA when every check in it is na.
type DimResult struct {
	ID     string  `json:"id"`
	Weight float64 `json:"weight"`
	NA     bool    `json:"na,omitempty"`
	Score  int     `json:"score"`
	Band   [2]int  `json:"band"`
}

// CheckOut is one check's evidence summary (the ?explain view).
type CheckOut struct {
	ID        string   `json:"id"`
	Dimension string   `json:"dimension"`
	Weight    float64  `json:"weight"`
	Kind      string   `json:"kind"`
	Metric    string   `json:"metric"`
	State     string   `json:"state"` // measured | unmeasured | na
	Reason    string   `json:"reason,omitempty"`
	N         int      `json:"n"`
	Pass      int      `json:"pass,omitempty"`
	Fail      int      `json:"fail,omitempty"`
	S         float64  `json:"s"`
	F         float64  `json:"f"`
	WeightSum float64  `json:"weight_sum"`
	Stat      *float64 `json:"stat,omitempty"` // a curve check's decayed statistic
	Point     float64  `json:"point"`          // posterior-mean score, 0–100
}

type checkState struct {
	c         *Check
	na        bool
	reason    string
	s, f      float64
	n         int
	pass      int
	fail      int
	events    []Event   // after revision reset, time order
	values    []float64 // curve: sorted by value
	cdf       []float64 // curve: cumulative decayed weight in value order
	weightSum float64
	lastAt    time.Time // newest event or sample counted
}

func (cs *checkState) measured() bool {
	if cs.c.Metric == "curve" {
		return len(cs.values) > 0 && cs.weightSum > 0
	}
	return cs.s+cs.f > 0
}

// prepare decays one window of evidence (window ≤ 0: all of it).
func prepare(r *Rubric, o *Observations, windowDays float64) []checkState {
	out := make([]checkState, len(r.Checks))
	for i := range r.Checks {
		c := &r.Checks[i]
		cs := checkState{c: c}
		obs, ok := o.Checks[c.ID]
		if ok && obs.NA {
			cs.na, cs.reason = true, obs.Reason
			out[i] = cs
			continue
		}
		factor := func(at time.Time, w float64) float64 {
			age := o.asOf.Sub(at).Hours() / 24
			if windowDays > 0 && age > windowDays {
				return 0
			}
			if w == 0 {
				w = 1
			}
			if !o.Subject.revChanged.IsZero() && at.Before(o.Subject.revChanged) {
				switch c.OnRevision {
				case "reset":
					return 0
				case "half":
					w *= 0.5
				}
			}
			return w * math.Pow(2, -age/c.HalfLife)
		}
		type vw struct{ v, w float64 }
		var pts []vw
		for _, e := range obs.Events {
			w := factor(e.at, e.Weight)
			if w == 0 {
				continue
			}
			cs.events = append(cs.events, e)
			if e.at.After(cs.lastAt) {
				cs.lastAt = e.at
			}
			cs.s += w * float64(e.Pass)
			cs.f += w * float64(e.Fail)
			cs.pass += e.Pass
			cs.fail += e.Fail
			cs.n++
		}
		for _, s := range obs.Samples {
			w := factor(s.at, s.Weight)
			if w == 0 {
				continue
			}
			pts = append(pts, vw{s.Value, w})
			if s.at.After(cs.lastAt) {
				cs.lastAt = s.at
			}
			cs.n++
		}
		sort.SliceStable(pts, func(a, b int) bool { return pts[a].v < pts[b].v })
		for _, p := range pts {
			cs.weightSum += p.w
			cs.values = append(cs.values, p.v)
			cs.cdf = append(cs.cdf, cs.weightSum)
		}
		if c.Metric == "rate" {
			cs.weightSum = cs.s + cs.f
		}
		out[i] = cs
	}
	return out
}

// eval maps x through the curve: log-space interpolation, clamped.
func (cv *Curve) eval(x float64) float64 {
	p := cv.Points
	if !(x > p[0][0]) {
		return p[0][1]
	}
	if x >= p[len(p)-1][0] {
		return p[len(p)-1][1]
	}
	for i := 1; i < len(p); i++ {
		if x <= p[i][0] {
			t := (math.Log(x) - math.Log(p[i-1][0])) / (math.Log(p[i][0]) - math.Log(p[i-1][0]))
			return p[i-1][1] + t*(p[i][1]-p[i-1][1])
		}
	}
	return p[len(p)-1][1]
}

func rateScore(c *Check, p float64) float64 {
	if c.Curve != nil {
		return c.Curve.eval(1 - p)
	}
	return 100 * p
}

// stat is the curve input over sorted values with cumulative weights.
func stat(input string, values, cdf []float64) float64 {
	total := cdf[len(cdf)-1]
	if input == "mean" {
		sum, prev := 0.0, 0.0
		for i, v := range values {
			sum += v * (cdf[i] - prev)
			prev = cdf[i]
		}
		return sum / total
	}
	q := map[string]float64{"p50": 0.5, "p90": 0.9, "p95": 0.95, "p99": 0.99}[input]
	target := q * total
	i := sort.SearchFloat64s(cdf, target)
	if i >= len(values) {
		i = len(values) - 1
	}
	return values[i]
}

func (cs *checkState) point() (score float64, st *float64) {
	c := cs.c
	if c.Metric == "rate" {
		return rateScore(c, (cs.s+0.5)/(cs.s+cs.f+1)), nil
	}
	if !cs.measured() {
		return 50, nil
	}
	v := stat(c.Curve.Input, cs.values, cs.cdf)
	return c.Curve.eval(v), &v
}

// draw is one Monte Carlo draw of the check's score.
func (cs *checkState) draw(g *rng, counts []float64) float64 {
	c := cs.c
	if c.Metric == "rate" {
		return rateScore(c, g.beta(cs.s+0.5, cs.f+0.5))
	}
	if !cs.measured() {
		return 100 * g.beta(0.5, 0.5)
	}
	// Weighted bootstrap: n picks by decayed weight, then the statistic
	// over the picks (each pick weight 1), in value order.
	n := len(cs.values)
	clear(counts[:n])
	total := cs.cdf[n-1]
	for range n {
		i := sort.SearchFloat64s(cs.cdf, g.uniform()*total)
		if i >= n {
			i = n - 1
		}
		counts[i]++
	}
	cum := counts[:n]
	run := 0.0
	cdf := make([]float64, 0, n)
	vals := make([]float64, 0, n)
	for i := range n {
		if cum[i] == 0 {
			continue
		}
		run += cum[i]
		cdf = append(cdf, run)
		vals = append(vals, cs.values[i])
	}
	return c.Curve.eval(stat(c.Curve.Input, vals, cdf))
}

// combine is the dimension scores and headline from per-check scores.
func combine(r *Rubric, states []checkState, scores []float64, dims []float64) (headline float64, present []bool) {
	present = make([]bool, len(r.Dimensions))
	hw, hs := 0.0, 0.0
	for d, dim := range r.Dimensions {
		w, s := 0.0, 0.0
		for i := range states {
			if states[i].na || states[i].c.Dimension != dim.ID || states[i].c.Weight == 0 {
				continue
			}
			w += states[i].c.Weight
			s += states[i].c.Weight * scores[i]
		}
		if w == 0 {
			continue
		}
		present[d] = true
		dims[d] = s / w
		hw += dim.Weight
		hs += dim.Weight * dims[d]
	}
	if hw == 0 {
		return 0, present
	}
	return hs / hw, present
}

func pointHeadline(r *Rubric, states []checkState) float64 {
	scores := make([]float64, len(states))
	for i := range states {
		if !states[i].na {
			scores[i], _ = states[i].point()
		}
	}
	h, _ := combine(r, states, scores, make([]float64, len(r.Dimensions)))
	return h
}

func percentile(sorted []float64, p float64) float64 {
	i := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

func round(x float64) int { return int(math.Floor(x + 0.5)) }

func round3(x float64) float64 { return math.Round(x*1000) / 1000 }

// Score computes the result. It reads no clock: time is o.AsOf.
func Score(r *Rubric, o *Observations) *Result {
	canon := o.Canonical()
	obsHash := sha256Hex(canon)
	seed := sha256.Sum256([]byte("swarmmemo.rubric.seed/1\n" + r.SHA256 + "\n" + obsHash))
	res := &Result{Schema: "swarmmemo.rubric.result/1", Rubric: RubricRef{r.ID, r.Version, r.SHA256},
		Subject: o.Subject, AsOf: o.AsOf, ObservationsSHA256: obsHash, Seed: hex.EncodeToString(seed[:]), Draws: r.Draws,
		Reasons: []string{}, Flags: []string{}, Value: o.Value}
	states := prepare(r, o, 0)

	// Monte Carlo.
	g := newRNG(seed)
	maxN := 0
	for i := range states {
		maxN = max(maxN, len(states[i].values))
	}
	counts := make([]float64, maxN)
	scores := make([]float64, len(states))
	dimDraws := make([][]float64, len(r.Dimensions))
	head := make([]float64, r.Draws)
	dims := make([]float64, len(r.Dimensions))
	var present []bool
	for d := range r.Draws {
		for i := range states {
			if states[i].na {
				continue
			}
			scores[i] = states[i].draw(g, counts)
		}
		head[d], present = combine(r, states, scores, dims)
		for k := range dims {
			dimDraws[k] = append(dimDraws[k], dims[k])
		}
	}
	slices.Sort(head)
	res.Score = round(percentile(head, 50))
	res.Band = [2]int{round(percentile(head, r.BandLo)), round(percentile(head, r.BandHi))}
	for k, dim := range r.Dimensions {
		dr := DimResult{ID: dim.ID, Weight: dim.Weight, NA: !present[k]}
		if present[k] {
			slices.Sort(dimDraws[k])
			dr.Score = round(percentile(dimDraws[k], 50))
			dr.Band = [2]int{round(percentile(dimDraws[k], r.BandLo)), round(percentile(dimDraws[k], r.BandHi))}
		}
		res.Dimensions = append(res.Dimensions, dr)
	}
	for i := range states {
		cs := &states[i]
		out := CheckOut{ID: cs.c.ID, Dimension: cs.c.Dimension, Weight: cs.c.Weight, Kind: cs.c.Kind, Metric: cs.c.Metric,
			N: cs.n, Pass: cs.pass, Fail: cs.fail, S: round3(cs.s), F: round3(cs.f), WeightSum: round3(cs.weightSum)}
		switch {
		case cs.na:
			out.State, out.Reason = "na", cs.reason
		case cs.measured():
			out.State = "measured"
		default:
			out.State = "unmeasured"
		}
		if !cs.na {
			p, st := cs.point()
			out.Point = math.Round(p*10) / 10
			if st != nil {
				v := math.Round(*st*10) / 10
				out.Stat = &v
			}
		}
		res.Checks = append(res.Checks, out)
	}

	// Gates.
	gateVerdicts := map[string]bool{}
	var capv *int
	for _, gt := range r.Gates {
		cs := &states[slices.IndexFunc(r.Checks, func(c Check) bool { return c.ID == gt.Check })]
		if cs.na || !gateFires(gt, cs, o.asOf) {
			continue
		}
		if !slices.Contains(res.Flags, gt.Flag) {
			res.Flags = append(res.Flags, gt.Flag)
		}
		if gt.Cap != nil && (capv == nil || *gt.Cap < *capv) {
			v := *gt.Cap
			capv = &v
		}
		if gt.Verdict != "" {
			gateVerdicts[gt.Verdict] = true
		}
		res.Watch = res.Watch || gt.Watch
		res.Stale = res.Stale || gt.Stale
	}
	sort.Strings(res.Flags)
	width := float64(res.Band[1] - res.Band[0])
	if capv != nil && (res.Score > *capv || res.Band[1] > *capv) {
		res.Uncapped = &Uncapped{res.Score, res.Band}
		res.Cap = capv
		res.Score = min(res.Score, *capv)
		res.Band = [2]int{min(res.Band[0], *capv), min(res.Band[1], *capv)}
	}
	for _, gr := range r.Grades {
		if float64(res.Band[0]) >= gr.Min {
			res.Grade = gr.Grade
			break
		}
	}
	res.Confidence = r.Confidence[len(r.Confidence)-1].Level
	for _, c := range r.Confidence {
		if width <= c.MaxWidth {
			res.Confidence = c.Level
			break
		}
	}

	// Watch: a revision change in the window, an open dispute, or a gate.
	if !o.Subject.revChanged.IsZero() && o.asOf.Sub(o.Subject.revChanged).Hours() <= r.WatchDays*24 {
		res.Watch = true
		res.Reasons = append(res.Reasons, "revision_changed")
	}
	if o.DisputeOpen {
		res.Watch = true
		res.Reasons = append(res.Reasons, "dispute_open")
	}

	// Verdict: down, then an avoid gate, then unverified, then the grade
	// and caution rules, else proceed.
	unverified := unverifiedReasons(r, states, o.asOf)
	switch {
	case gateVerdicts["down"]:
		res.Verdict = "down"
	case gateVerdicts["avoid"]:
		res.Verdict = "avoid"
	case len(unverified) > 0:
		res.Verdict = "unverified"
		res.Reasons = append(res.Reasons, unverified...)
	case slices.Contains(r.Verdict.AvoidGrades, res.Grade):
		res.Verdict = "avoid"
		res.Reasons = append(res.Reasons, "grade_"+res.Grade)
	case gateVerdicts["caution"] || slices.Contains(r.Verdict.CautionGrades, res.Grade) || slices.Contains(r.Verdict.CautionConf, res.Confidence):
		res.Verdict = "caution"
		if slices.Contains(r.Verdict.CautionGrades, res.Grade) {
			res.Reasons = append(res.Reasons, "grade_"+res.Grade)
		}
		if slices.Contains(r.Verdict.CautionConf, res.Confidence) {
			res.Reasons = append(res.Reasons, "confidence_"+res.Confidence)
		}
	default:
		res.Verdict = "proceed"
	}

	// Outlook: the recent window's point headline against the baseline's.
	recent := pointHeadline(r, prepare(r, o, r.Outlook.RecentDays))
	base := pointHeadline(r, prepare(r, o, r.Outlook.BaselineDays))
	switch d := recent - base; {
	case d > r.Outlook.Delta:
		res.Outlook = "improving"
	case d < -r.Outlook.Delta:
		res.Outlook = "declining"
	default:
		res.Outlook = "stable"
	}
	return res
}

func gateFires(gt Gate, cs *checkState, asOf time.Time) bool {
	if gt.Clear > 0 {
		lastFail := -1
		for i, e := range cs.events {
			if e.Fail > 0 {
				lastFail = i
			}
		}
		clean := 0
		for _, e := range cs.events[lastFail+1:] {
			if e.Pass > 0 && e.Fail == 0 {
				clean++
			}
		}
		if lastFail >= 0 && clean >= gt.Clear {
			return false
		}
	}
	switch gt.When {
	case "failed":
		return cs.f > 0
	case "fail_weight_gte":
		return cs.f > 0 && cs.f >= gt.Value
	case "failed_within_hours":
		for _, e := range cs.events {
			if e.Fail > 0 && asOf.Sub(e.at).Hours() <= gt.Value {
				return true
			}
		}
	case "failing_for_hours":
		// The trailing run of failure-only events started more than Value
		// hours ago, with no pass since.
		start := -1
		for i := len(cs.events) - 1; i >= 0; i-- {
			if cs.events[i].Pass > 0 {
				break
			}
			start = i
		}
		return start >= 0 && asOf.Sub(cs.events[start].at).Hours() > gt.Value
	}
	return false
}

func unverifiedReasons(r *Rubric, states []checkState, asOf time.Time) []string {
	var out []string
	v := r.Verdict
	var probe *checkState
	for i := range states {
		if states[i].c.ID == v.ProbeCheck {
			probe = &states[i]
		}
	}
	n := 0
	var first, last time.Time
	if probe != nil {
		for _, e := range probe.events {
			if first.IsZero() || e.at.Before(first) {
				first = e.at
			}
			if e.at.After(last) {
				last = e.at
			}
			n++
		}
	}
	if n < v.MinProbes || last.Sub(first).Hours() < v.MinSpanDays*24 {
		out = append(out, "few_probes")
	}
	if v.PaidWithinDays > 0 {
		paid := false
		for i := range states {
			cs := &states[i]
			if cs.c.Kind != "paid" || cs.na {
				continue
			}
			if !cs.lastAt.IsZero() && asOf.Sub(cs.lastAt).Hours() <= v.PaidWithinDays*24 {
				paid = true
			}
		}
		if !paid {
			out = append(out, "no_paid_call")
		}
	}
	return out
}

// Encode is the result's canonical JSON, newline-terminated.
func (res *Result) Encode() []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(res)
	return b.Bytes()
}

// ScoreFiles parses both files and returns the canonical result.
func ScoreFiles(rubricRaw, observationsRaw []byte) ([]byte, error) {
	r, err := ParseRubric(rubricRaw)
	if err != nil {
		return nil, err
	}
	o, err := ParseObservations(observationsRaw, r)
	if err != nil {
		return nil, err
	}
	return Score(r, o).Encode(), nil
}
