// Package rubric is the review lab's deterministic scoring library: it reads
// a rubric (which checks exist, which of the four universal dimensions each
// one feeds, its weight, metric and decay), takes a subject's observations
// and computes the headline score, its 80% band, the grade, confidence,
// verdict, outlook and flags.
//
// It is pure: no network, no model, no clock (the evaluation time is the
// observations' as_of), and its random draws come from a PRNG seeded by the
// hash of the rubric and the observations, so anyone holding both files gets
// byte-identical output (swarmmemo rubric score RUBRIC OBSERVATIONS).
//
// The method, in order:
//
//  1. Per check, events decay with w = 2^(-age/h) (h the check's half-life,
//     or its kind's default); s = Σ w·pass, f = Σ w·fail. A rate check's
//     posterior is Beta(s+½, f+½) (Jeffreys); never measured is Beta(½, ½).
//  2. A rate check scores 100·p, or curve(1-p) when it has a curve. A curve
//     check takes the decayed samples' quantile (p50, p95, mean) and maps it
//     through control points interpolated in log space, clamped at the ends.
//  3. A dimension is the weighted mean of its checks; an `na` check drops
//     out and the rest renormalize. The headline is the weighted mean of the
//     dimensions present.
//  4. Draws: per draw, every rate check samples its Beta and every curve
//     check bootstraps its samples (by decayed weight). The score is the
//     median draw, the band the 10th and 90th percentiles (nearest rank).
//  5. Gates cap the score and the band and add flags; the grade comes from
//     the band's lower edge, confidence from its width; the verdict and the
//     outlook follow the rubric's rules (see Score).
//
// Determinism: the PRNG is xoshiro256** whose state is the 32 bytes of
// SHA-256("swarmmemo.rubric.seed/1\n" + rubric_sha256 + "\n" +
// observations_sha256) read as four big-endian words; uniforms are the top
// 53 bits; normals are Box-Muller (cos branch only); Gamma is
// Marsaglia-Tsang (with the u^(1/a) boost below a = 1); Beta is X/(X+Y).
package rubric

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Limits keep parsing and scoring bounded whatever the input.
const (
	MaxRubricBytes       = 1 << 20
	MaxObservationsBytes = 4 << 20
	MaxChecks            = 64
	MaxDimensions        = 16
	MaxCurvePoints       = 16
	MaxGates             = 32
	MaxEventsPerCheck    = 10000
	MaxSamplesPerCheck   = 2000
	MaxEventsTotal       = 50000
	MaxCount             = 1000000
	DefaultDraws         = 4000
	MaxDraws             = 20000
)

// Dimensions every rubric scores, in display order (Hugo 2026-10-11).
var Dimensions = []string{"works", "as_described", "safe", "good"}

var (
	idRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	checkRE   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,15}$`)
	versionRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]{1,32})?$`)
	flagRE    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
	typeRE    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// Rubric is a parsed, validated rubric file.
type Rubric struct {
	Schema       string
	ID           string
	Version      string
	SubjectTypes []string
	Dimensions   []Dimension
	Checks       []Check
	Gates        []Gate
	Grades       []Grade // descending by Min
	Confidence   []ConfidenceLevel
	Verdict      VerdictRules
	Outlook      OutlookRules
	WatchDays    float64
	Draws        int
	BandLo       float64            // percentile, e.g. 10
	BandHi       float64            // percentile, e.g. 90
	HalfLife     map[string]float64 // kind → days
	SHA256       string             // of the file's bytes
}

// Dimension is one of the universal dimensions with its headline weight.
type Dimension struct {
	ID     string
	Weight float64
}

// Check is one rubric check.
type Check struct {
	ID         string
	Dimension  string
	Weight     float64
	Kind       string // free | paid
	Metric     string // rate | curve
	HalfLife   float64
	OnRevision string // keep | half | reset
	Curve      *Curve
}

// Curve maps a metric to 0–100: log-space interpolation between points with
// ascending positive x, clamped beyond the ends. Input names the statistic:
// p50, p95, mean (curve checks) or fail_rate (rate checks).
type Curve struct {
	Input  string
	Points [][2]float64
}

// Gate is a rule that caps the score and raises a flag.
type Gate struct {
	Flag    string
	Check   string
	When    string // failed | failed_within_hours | fail_weight_gte | failing_for_hours
	Value   float64
	Clear   int // passes after the last failure that clear it (0: never)
	Cap     *int
	Verdict string // "" | caution | avoid | down
	Watch   bool
	Stale   bool
}

// Grade is a letter and the band lower edge it needs.
type Grade struct {
	Grade string
	Min   float64
}

// ConfidenceLevel is a label and the widest band it allows.
type ConfidenceLevel struct {
	Level    string
	MaxWidth float64
}

// VerdictRules are the verdict's thresholds.
type VerdictRules struct {
	ProbeCheck     string
	MinProbes      int
	MinSpanDays    float64
	PaidWithinDays float64 // 0: no paid requirement
	AvoidGrades    []string
	CautionGrades  []string
	CautionConf    []string
}

// OutlookRules compare a recent window's headline with a baseline window's.
type OutlookRules struct {
	RecentDays   float64
	BaselineDays float64
	Delta        float64
}

// ---- the file format, read tolerantly ----

type rawRubric struct {
	Schema       string          `json:"schema"`
	ID           string          `json:"id"`
	Version      string          `json:"version"`
	SubjectTypes []string        `json:"subject_types"`
	Dimensions   json.RawMessage `json:"dimensions"`
	Checks       []rawCheck      `json:"checks"`
	Gates        []rawGate       `json:"gates"`
	Grades       json.RawMessage `json:"grades"`
	Confidence   json.RawMessage `json:"confidence"`
	Verdict      *struct {
		ProbeCheck     string   `json:"probe_check"`
		MinProbes      *int     `json:"min_probes"`
		MinSpanDays    *float64 `json:"min_span_days"`
		PaidWithinDays *float64 `json:"paid_within_days"`
		AvoidGrades    []string `json:"avoid_grades"`
		CautionGrades  []string `json:"caution_grades"`
		CautionConf    []string `json:"caution_confidence"`
	} `json:"verdict"`
	Outlook *struct {
		RecentDays   float64 `json:"recent_days"`
		BaselineDays float64 `json:"baseline_days"`
		Delta        float64 `json:"delta"`
	} `json:"outlook"`
	Watch *struct {
		RevisionDays float64 `json:"revision_days"`
	} `json:"watch"`
	MonteCarlo *struct {
		Draws int       `json:"draws"`
		Band  []float64 `json:"band"`
	} `json:"monte_carlo"`
	Decay map[string]float64 `json:"decay"`
	// Documentation only: shown on the method page, never scored.
	Title       json.RawMessage `json:"title"`
	Description json.RawMessage `json:"description"`
	URL         json.RawMessage `json:"url"`
	Changelog   json.RawMessage `json:"changelog"`
	Value       json.RawMessage `json:"value"`
	Notes       json.RawMessage `json:"notes"`
}

type rawCheck struct {
	ID          string          `json:"id"`
	Dimension   string          `json:"dimension"`
	Weight      *float64        `json:"weight"`
	Kind        string          `json:"kind"`
	Metric      string          `json:"metric"`
	Type        string          `json:"type"` // alias of metric
	HalfLife    *float64        `json:"half_life_days"`
	OnRevision  string          `json:"on_revision"`
	Curve       json.RawMessage `json:"curve"`
	CurvePoints json.RawMessage `json:"curve_points"` // alias: points without an input
	CurveInput  string          `json:"curve_input"`
	// Documentation only.
	Title       json.RawMessage `json:"title"`
	Measure     json.RawMessage `json:"measure"`
	Description json.RawMessage `json:"description"`
	Validity    json.RawMessage `json:"validity"`
	Note        json.RawMessage `json:"note"`
}

type rawGate struct {
	Flag    string   `json:"flag"`
	Check   string   `json:"check"`
	When    string   `json:"when"`
	Value   *float64 `json:"value"`
	Clear   int      `json:"clear_after_passes"`
	Cap     *int     `json:"cap"`
	Verdict string   `json:"verdict"`
	Watch   bool     `json:"watch"`
	Stale   bool     `json:"stale"`
}

func bad(format string, a ...any) error { return fmt.Errorf("rubric: "+format, a...) }

func decodeStrict(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.More() {
		return errors.New("trailing data")
	}
	return nil
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// ParseRubric reads and validates a rubric file. Unknown top-level and check
// fields are refused, so a typo cannot silently change a score.
func ParseRubric(raw []byte) (*Rubric, error) {
	if len(raw) > MaxRubricBytes {
		return nil, bad("file over %d bytes", MaxRubricBytes)
	}
	var in rawRubric
	if err := decodeStrict(raw, &in); err != nil {
		return nil, bad("%v", err)
	}
	sum := sha256.Sum256(raw)
	r := &Rubric{Schema: in.Schema, ID: in.ID, Version: in.Version, SubjectTypes: in.SubjectTypes, SHA256: hex.EncodeToString(sum[:]),
		Draws: DefaultDraws, BandLo: 10, BandHi: 90, WatchDays: 7,
		HalfLife: map[string]float64{"free": 14, "paid": 30}}
	if in.Schema != "" && in.Schema != "swarmmemo.rubric/1" {
		return nil, bad("schema %q", in.Schema)
	}
	if !idRE.MatchString(r.ID) {
		return nil, bad("id")
	}
	if !versionRE.MatchString(r.Version) {
		return nil, bad("version (semver)")
	}
	if len(r.SubjectTypes) == 0 || len(r.SubjectTypes) > 8 {
		return nil, bad("subject_types")
	}
	for _, t := range r.SubjectTypes {
		if !typeRE.MatchString(t) {
			return nil, bad("subject_types")
		}
	}
	for k, v := range in.Decay {
		k = strings.TrimSuffix(k, "_half_life_days")
		if (k != "free" && k != "paid") || !finite(v) || v <= 0 || v > 3650 {
			return nil, bad("decay")
		}
		r.HalfLife[k] = v
	}
	var err error
	if r.Dimensions, err = parseDimensions(in.Dimensions); err != nil {
		return nil, err
	}
	dims := map[string]bool{}
	for _, d := range r.Dimensions {
		dims[d.ID] = true
	}
	if len(in.Checks) == 0 || len(in.Checks) > MaxChecks {
		return nil, bad("checks: 1 to %d", MaxChecks)
	}
	seen := map[string]bool{}
	for _, c := range in.Checks {
		ch, err := r.parseCheck(c, dims)
		if err != nil {
			return nil, err
		}
		if seen[ch.ID] {
			return nil, bad("duplicate check %s", ch.ID)
		}
		seen[ch.ID] = true
		r.Checks = append(r.Checks, ch)
	}
	for _, d := range r.Dimensions {
		w := 0.0
		for _, c := range r.Checks {
			if c.Dimension == d.ID {
				w += c.Weight
			}
		}
		if w <= 0 {
			return nil, bad("dimension %s has no weighted check", d.ID)
		}
	}
	if len(in.Gates) > MaxGates {
		return nil, bad("gates: at most %d", MaxGates)
	}
	for _, g := range in.Gates {
		gate, err := parseGate(g, seen)
		if err != nil {
			return nil, err
		}
		r.Gates = append(r.Gates, gate)
	}
	if r.Grades, err = parseGrades(in.Grades); err != nil {
		return nil, err
	}
	if r.Confidence, err = parseConfidence(in.Confidence); err != nil {
		return nil, err
	}
	r.Verdict = VerdictRules{MinProbes: 8, MinSpanDays: 3, PaidWithinDays: 30, AvoidGrades: []string{"F"}, CautionGrades: []string{"C", "D"}, CautionConf: []string{"low"}}
	if v := in.Verdict; v != nil {
		r.Verdict.ProbeCheck = v.ProbeCheck
		if v.MinProbes != nil {
			r.Verdict.MinProbes = *v.MinProbes
		}
		if v.MinSpanDays != nil {
			r.Verdict.MinSpanDays = *v.MinSpanDays
		}
		if v.PaidWithinDays != nil {
			r.Verdict.PaidWithinDays = *v.PaidWithinDays
		}
		if v.AvoidGrades != nil {
			r.Verdict.AvoidGrades = v.AvoidGrades
		}
		if v.CautionGrades != nil {
			r.Verdict.CautionGrades = v.CautionGrades
		}
		if v.CautionConf != nil {
			r.Verdict.CautionConf = v.CautionConf
		}
	}
	if r.Verdict.ProbeCheck == "" {
		r.Verdict.ProbeCheck = r.Checks[0].ID
	}
	if !seen[r.Verdict.ProbeCheck] || r.Verdict.MinProbes < 0 || r.Verdict.MinProbes > MaxCount ||
		!finite(r.Verdict.MinSpanDays) || r.Verdict.MinSpanDays < 0 || r.Verdict.MinSpanDays > 3650 ||
		!finite(r.Verdict.PaidWithinDays) || r.Verdict.PaidWithinDays < 0 || r.Verdict.PaidWithinDays > 3650 {
		return nil, bad("verdict")
	}
	r.Outlook = OutlookRules{RecentDays: 14, BaselineDays: 30, Delta: 5}
	if o := in.Outlook; o != nil {
		r.Outlook = OutlookRules{RecentDays: o.RecentDays, BaselineDays: o.BaselineDays, Delta: o.Delta}
	}
	if o := r.Outlook; !finite(o.RecentDays) || !finite(o.BaselineDays) || !finite(o.Delta) || o.RecentDays <= 0 || o.BaselineDays < o.RecentDays || o.BaselineDays > 3650 || o.Delta < 0 || o.Delta > 100 {
		return nil, bad("outlook")
	}
	if w := in.Watch; w != nil {
		if !finite(w.RevisionDays) || w.RevisionDays < 0 || w.RevisionDays > 3650 {
			return nil, bad("watch")
		}
		r.WatchDays = w.RevisionDays
	}
	if mc := in.MonteCarlo; mc != nil {
		if mc.Draws != 0 {
			r.Draws = mc.Draws
		}
		if mc.Band != nil {
			if len(mc.Band) != 2 {
				return nil, bad("monte_carlo.band")
			}
			r.BandLo, r.BandHi = mc.Band[0], mc.Band[1]
		}
	}
	if r.Draws < 100 || r.Draws > MaxDraws || !finite(r.BandLo) || !finite(r.BandHi) || r.BandLo <= 0 || r.BandHi >= 100 || r.BandLo >= r.BandHi {
		return nil, bad("monte_carlo")
	}
	return r, nil
}

func parseDimensions(raw json.RawMessage) ([]Dimension, error) {
	var out []Dimension
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0:
		return nil, bad("dimensions missing")
	case raw[0] == '[':
		var list []struct {
			ID     string   `json:"id"`
			Weight *float64 `json:"weight"`
		}
		if err := decodeStrict(raw, &list); err != nil {
			return nil, bad("dimensions: %v", err)
		}
		for _, d := range list {
			if d.Weight == nil {
				return nil, bad("dimension %q weight", d.ID)
			}
			out = append(out, Dimension{d.ID, *d.Weight})
		}
	case raw[0] == '{':
		// {"works": 45, ...} or {"works": {"weight": 45}, ...}: ordered by
		// the universal order, then by name.
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, bad("dimensions: %v", err)
		}
		for id, v := range m {
			var w float64
			if json.Unmarshal(v, &w) != nil {
				var o struct {
					Weight *float64 `json:"weight"`
				}
				if decodeStrict(v, &o) != nil || o.Weight == nil {
					return nil, bad("dimension %q weight", id)
				}
				w = *o.Weight
			}
			out = append(out, Dimension{id, w})
		}
		sort.Slice(out, func(i, j int) bool {
			a, b := slices.Index(Dimensions, out[i].ID), slices.Index(Dimensions, out[j].ID)
			if a < 0 {
				a = len(Dimensions)
			}
			if b < 0 {
				b = len(Dimensions)
			}
			if a != b {
				return a < b
			}
			return out[i].ID < out[j].ID
		})
	default:
		return nil, bad("dimensions")
	}
	if len(out) == 0 || len(out) > MaxDimensions {
		return nil, bad("dimensions: 1 to %d", MaxDimensions)
	}
	seen := map[string]bool{}
	for _, d := range out {
		if !idRE.MatchString(d.ID) || seen[d.ID] || !finite(d.Weight) || d.Weight <= 0 || d.Weight > 1000 {
			return nil, bad("dimension %q", d.ID)
		}
		seen[d.ID] = true
	}
	return out, nil
}

func (r *Rubric) parseCheck(c rawCheck, dims map[string]bool) (Check, error) {
	ch := Check{ID: c.ID, Dimension: c.Dimension, Kind: c.Kind, Metric: c.Metric, OnRevision: c.OnRevision}
	if ch.Metric == "" {
		ch.Metric = c.Type
	}
	if !checkRE.MatchString(ch.ID) {
		return ch, bad("check id %q", c.ID)
	}
	if !dims[ch.Dimension] {
		return ch, bad("check %s: dimension %q", ch.ID, c.Dimension)
	}
	if c.Weight == nil || !finite(*c.Weight) || *c.Weight < 0 || *c.Weight > 1000 {
		return ch, bad("check %s: weight", ch.ID)
	}
	ch.Weight = *c.Weight
	if ch.Kind != "free" && ch.Kind != "paid" {
		return ch, bad("check %s: kind free|paid", ch.ID)
	}
	if ch.Metric != "rate" && ch.Metric != "curve" {
		return ch, bad("check %s: metric rate|curve", ch.ID)
	}
	switch ch.OnRevision {
	case "":
		ch.OnRevision = "keep"
	case "keep", "half", "reset":
	default:
		return ch, bad("check %s: on_revision keep|half|reset", ch.ID)
	}
	ch.HalfLife = r.HalfLife[ch.Kind]
	if c.HalfLife != nil {
		ch.HalfLife = *c.HalfLife
	}
	if !finite(ch.HalfLife) || ch.HalfLife <= 0 || ch.HalfLife > 3650 {
		return ch, bad("check %s: half_life_days", ch.ID)
	}
	curveRaw := c.Curve
	if len(bytes.TrimSpace(curveRaw)) == 0 && len(bytes.TrimSpace(c.CurvePoints)) > 0 {
		curveRaw = c.CurvePoints
	}
	if len(bytes.TrimSpace(curveRaw)) > 0 && string(bytes.TrimSpace(curveRaw)) != "null" {
		cv, err := parseCurve(curveRaw, c.CurveInput)
		if err != nil {
			return ch, bad("check %s: %v", ch.ID, err)
		}
		ch.Curve = cv
	}
	if ch.Metric == "curve" && ch.Curve == nil {
		return ch, bad("check %s: a curve metric needs a curve", ch.ID)
	}
	if ch.Curve != nil {
		if ch.Metric == "curve" && ch.Curve.Input == "" {
			ch.Curve.Input = "p95"
		}
		if ch.Metric == "rate" && ch.Curve.Input == "" {
			ch.Curve.Input = "fail_rate"
		}
		ok := map[string]bool{"p50": true, "p90": true, "p95": true, "p99": true, "mean": true}[ch.Curve.Input]
		if ch.Metric == "rate" {
			ok = ch.Curve.Input == "fail_rate"
		}
		if !ok {
			return ch, bad("check %s: curve input %q", ch.ID, ch.Curve.Input)
		}
	}
	return ch, nil
}

// parseCurve accepts {"input": "p95", "points": [[300,100],[1000,90]]},
// a bare [[x,score],…] list, or points as [{"x":…, "score":…}].
func parseCurve(raw json.RawMessage, input string) (*Curve, error) {
	raw = bytes.TrimSpace(raw)
	cv := &Curve{Input: input}
	var pts json.RawMessage
	if raw[0] == '{' {
		var o struct {
			Input  string          `json:"input"`
			Points json.RawMessage `json:"points"`
			Space  string          `json:"space"`
		}
		if err := decodeStrict(raw, &o); err != nil {
			return nil, err
		}
		if o.Space != "" && o.Space != "log" {
			return nil, errors.New("curve space must be log")
		}
		if o.Input != "" {
			cv.Input = o.Input
		}
		pts = o.Points
	} else {
		pts = raw
	}
	var pairs [][]float64
	if err := json.Unmarshal(pts, &pairs); err != nil {
		pairs = nil
		var objs []struct {
			X     *float64 `json:"x"`
			Score *float64 `json:"score"`
		}
		if err2 := decodeStrict(pts, &objs); err2 != nil {
			return nil, errors.New("curve points")
		}
		for _, o := range objs {
			if o.X == nil || o.Score == nil {
				return nil, errors.New("curve points")
			}
			pairs = append(pairs, []float64{*o.X, *o.Score})
		}
	}
	if len(pairs) < 2 || len(pairs) > MaxCurvePoints {
		return nil, fmt.Errorf("curve: 2 to %d points", MaxCurvePoints)
	}
	for i, p := range pairs {
		if len(p) != 2 || !finite(p[0]) || !finite(p[1]) || p[0] <= 0 || p[0] > 1e12 || p[1] < 0 || p[1] > 100 {
			return nil, errors.New("curve point: x > 0, 0 ≤ score ≤ 100")
		}
		if i > 0 && p[0] <= pairs[i-1][0] {
			return nil, errors.New("curve x must ascend")
		}
		cv.Points = append(cv.Points, [2]float64{p[0], p[1]})
	}
	return cv, nil
}

func parseGate(g rawGate, checks map[string]bool) (Gate, error) {
	out := Gate{Flag: g.Flag, Check: g.Check, When: g.When, Clear: g.Clear, Cap: g.Cap, Verdict: g.Verdict, Watch: g.Watch, Stale: g.Stale}
	if !flagRE.MatchString(out.Flag) {
		return out, bad("gate flag %q", g.Flag)
	}
	if !checks[out.Check] {
		return out, bad("gate %s: check %q", out.Flag, g.Check)
	}
	if g.Value != nil {
		out.Value = *g.Value
	}
	switch out.When {
	case "failed":
	case "failed_within_hours", "failing_for_hours", "fail_weight_gte":
		if g.Value == nil || !finite(out.Value) || out.Value < 0 || out.Value > 1e6 {
			return out, bad("gate %s: value", out.Flag)
		}
	default:
		return out, bad("gate %s: when failed|failed_within_hours|failing_for_hours|fail_weight_gte", out.Flag)
	}
	if out.Clear < 0 || out.Clear > 1000 {
		return out, bad("gate %s: clear_after_passes", out.Flag)
	}
	if out.Cap != nil && (*out.Cap < 0 || *out.Cap > 100) {
		return out, bad("gate %s: cap", out.Flag)
	}
	switch out.Verdict {
	case "", "caution", "avoid", "down":
	default:
		return out, bad("gate %s: verdict caution|avoid|down", out.Flag)
	}
	return out, nil
}

func parseGrades(raw json.RawMessage) ([]Grade, error) {
	raw = bytes.TrimSpace(raw)
	var out []Grade
	switch {
	case len(raw) == 0:
		out = []Grade{{"A", 90}, {"B", 80}, {"C", 70}, {"D", 60}, {"F", 0}}
	case raw[0] == '[':
		var list []struct {
			Grade string   `json:"grade"`
			Min   *float64 `json:"min"`
		}
		if err := decodeStrict(raw, &list); err != nil {
			return nil, bad("grades: %v", err)
		}
		for _, g := range list {
			if g.Min == nil {
				return nil, bad("grade %q min", g.Grade)
			}
			out = append(out, Grade{g.Grade, *g.Min})
		}
	case raw[0] == '{':
		var m map[string]float64
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, bad("grades: %v", err)
		}
		for g, v := range m {
			out = append(out, Grade{g, v})
		}
	default:
		return nil, bad("grades")
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Min != out[j].Min {
			return out[i].Min > out[j].Min
		}
		return out[i].Grade < out[j].Grade
	})
	if len(out) == 0 || len(out) > 12 {
		return nil, bad("grades: 1 to 12")
	}
	seen := map[string]bool{}
	for i, g := range out {
		if g.Grade == "" || len(g.Grade) > 4 || seen[g.Grade] || !finite(g.Min) || g.Min < 0 || g.Min > 100 || (i > 0 && g.Min == out[i-1].Min) {
			return nil, bad("grade %q", g.Grade)
		}
		seen[g.Grade] = true
	}
	if out[len(out)-1].Min != 0 {
		return nil, bad("grades: the lowest grade needs min 0")
	}
	return out, nil
}

func parseConfidence(raw json.RawMessage) ([]ConfidenceLevel, error) {
	raw = bytes.TrimSpace(raw)
	var out []ConfidenceLevel
	switch {
	case len(raw) == 0:
		return []ConfidenceLevel{{"high", 10}, {"medium", 25}, {"low", 100}}, nil
	case raw[0] == '[':
		var list []struct {
			Level    string   `json:"level"`
			MaxWidth *float64 `json:"max_width"`
		}
		if err := decodeStrict(raw, &list); err != nil {
			return nil, bad("confidence: %v", err)
		}
		for _, c := range list {
			w := 100.0
			if c.MaxWidth != nil {
				w = *c.MaxWidth
			}
			out = append(out, ConfidenceLevel{c.Level, w})
		}
	case raw[0] == '{':
		var m map[string]float64
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, bad("confidence: %v", err)
		}
		for l, w := range m {
			out = append(out, ConfidenceLevel{l, w})
		}
	default:
		return nil, bad("confidence")
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].MaxWidth != out[j].MaxWidth {
			return out[i].MaxWidth < out[j].MaxWidth
		}
		return out[i].Level < out[j].Level
	})
	if len(out) == 0 || len(out) > 8 {
		return nil, bad("confidence: 1 to 8 levels")
	}
	for i, c := range out {
		if !typeRE.MatchString(c.Level) || !finite(c.MaxWidth) || c.MaxWidth < 0 || c.MaxWidth > 100 || (i > 0 && c.MaxWidth == out[i-1].MaxWidth) {
			return nil, bad("confidence level %q", c.Level)
		}
	}
	return out, nil // the widest level also takes anything wider
}

func (r *Rubric) check(id string) *Check {
	for i := range r.Checks {
		if r.Checks[i].ID == id {
			return &r.Checks[i]
		}
	}
	return nil
}
