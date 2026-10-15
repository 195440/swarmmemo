package judge_test

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"swarmmemo/internal/judge"
	"swarmmemo/internal/trust"
)

// TestRFCWorkedNumbers reads RFC 0020 and checks that every worked number
// it states is what the code computes: the RFC is the single source, and
// editing either alone fails here.
func TestRFCWorkedNumbers(t *testing.T) {
	raw, err := os.ReadFile("../../docs/rfcs/0020-judgements-score-accuracy.md")
	if err != nil {
		t.Fatal(err)
	}
	rfc := strings.Join(strings.Fields(string(raw)), " ")
	const w = 700_000 // any common weight
	pts := func(xs ...int64) []judge.Point {
		var out []judge.Point
		for _, x := range xs {
			out = append(out, judge.Point{X: x, W: w})
		}
		return out
	}
	thin := func(n int64) string {
		if judge.Thin(n) {
			return " (thin)"
		}
		return ""
	}
	gain := func(x, c0, cT int64) string { return fmt.Sprintf("%+.2f", float64(judge.Gain(x, c0, cT, false))/1e6) }
	griefed := append(pts(80, 80, 80, 80, 80, 80), judge.Point{X: 0, W: 2 * w})
	for _, want := range []string{
		// §2
		fmt.Sprintf("`m = %d` pseudo-judgements at %d", judge.PriorWeight, judge.Neutral),
		fmt.Sprintf("fewer than `thin_below` = %d judgements with weight", judge.ThinBelow),
		fmt.Sprintf("one judge at 90 scores %d%s;", judge.Consensus(pts(90)), thin(1)),
		fmt.Sprintf("two judges at 90 and 94 of equal weight score %d%s, whatever that weight;", judge.Consensus(pts(90, 94)), thin(2)),
		fmt.Sprintf("a third at 92 makes it %d;", judge.Consensus(pts(90, 92, 94))),
		fmt.Sprintf("six judges at 80 weighing 1 and a griefer at 0 weighing 2: the score stays %d.", judge.Consensus(griefed)),
		// §3.1
		fmt.Sprintf("75 → you say 60 → it ends at 62: %s.", gain(60, 75, 62)),
		fmt.Sprintf("you say 99 at 80, and it ends at 99: %s.", gain(99, 80, 99)),
	} {
		if !strings.Contains(rfc, want) {
			t.Errorf("RFC 0020 does not say what the code computes:\n  %s", want)
		}
	}
	var keys map[string]any
	b, _ := json.Marshal(judge.Default)
	_ = json.Unmarshal(b, &keys)
	for k := range keys {
		if !strings.Contains(rfc, "`"+k+"`") {
			t.Errorf("RFC 0020 §4 does not name judge.Params %s", k)
		}
	}
}

// RFC 0020 §3.1's worked examples.
func TestGainWorkedExamples(t *testing.T) {
	for _, c := range []struct {
		name      string
		x, c0, cT int64
		want      int64
	}{
		{"moves the consensus towards where it ends: 75 → 60 → 62", 60, 75, 62, 110_000},
		{"repeating the consensus (herding) earns nothing", 75, 75, 62, 0},
		{"herding at the end point too", 62, 62, 62, 0},
		{"early and right: 80 → 99 → 99", 99, 80, 99, 190_000},
		{"the wrong direction costs: 75 → 90 → 62", 90, 75, 62, -150_000},
		{"overshooting past the end point", 40, 75, 62, -90_000},
	} {
		if got := judge.Gain(c.x, c.c0, c.cT, false); got != c.want {
			t.Errorf("%s: gain %d ppm, want %d", c.name, got, c.want)
		}
	}
	// Every gain is in [−1, 1].
	for x := int64(0); x <= 100; x += 5 {
		for c0 := int64(0); c0 <= 100; c0 += 5 {
			for cT := int64(0); cT <= 100; cT += 5 {
				if g := judge.Gain(x, c0, cT, false); g < -1e6 || g > 1e6 {
					t.Fatalf("gain(%d,%d,%d) = %d out of range", x, c0, cT, g)
				}
			}
		}
	}
}

// RFC 0020 §3.2: a coarse judgement settles by direction, as if it had named
// the end point on its side.
func TestGainCoarse(t *testing.T) {
	for _, c := range []struct {
		name      string
		x, c0, cT int64
		want      int64
	}{
		{"up, consensus rose", 100, 50, 70, 200_000},
		{"up, consensus fell", 100, 50, 30, -200_000},
		{"down, consensus fell", 0, 60, 20, 400_000},
		{"down, consensus rose", 0, 60, 80, -200_000},
		{"a vouch (80, coarse) is up", 80, 50, 70, 200_000},
		{"a work accept (75, coarse) is up", 75, 60, 40, -200_000},
		{"up at the top: nothing to move", 100, 100, 40, 0},
		{"unchanged consensus", 100, 55, 55, 0},
	} {
		if got := judge.Gain(c.x, c.c0, c.cT, true); got != c.want {
			t.Errorf("%s: gain %d ppm, want %d", c.name, got, c.want)
		}
	}
	// The same formula with x replaced by the end point when it moved the
	// judge's way.
	for c0 := int64(0); c0 <= 100; c0 += 10 {
		for cT := int64(0); cT <= 100; cT += 10 {
			if cT > c0 {
				if got, want := judge.Gain(100, c0, cT, true), judge.Gain(cT, c0, cT, false); got != want {
					t.Fatalf("coarse up (%d→%d) %d, numeric at the end point %d", c0, cT, got, want)
				}
			}
		}
	}
}

func TestConsensusWeightedMedianWithPrior(t *testing.T) {
	const full = 1_000_000
	for _, c := range []struct {
		name string
		pts  []judge.Point
		want int64
	}{
		{"no judgement: the prior", nil, 50},
		{"one judgement is held by the prior (2 × its own weight), whatever it weighs", []judge.Point{{100, 2 * full}}, 50},
		{"one full judgement stays at the prior", []judge.Point{{100, full}}, 50},
		{"two equal judgements: exactly half, the midpoint", []judge.Point{{100, full}, {100, full}}, 75},
		{"two thin opinions move it as far as two full ones", []judge.Point{{100, 200_000}, {100, 200_000}}, 75},
		{"three judgements outweigh the prior", []judge.Point{{90, full}, {90, full}, {90, full}}, 90},
		{"one extreme judge cannot drag it", []judge.Point{{70, full}, {72, full}, {74, full}, {76, full}, {0, full}}, 70},
		{"an exact half: the midpoint, rounded down", []judge.Point{{70, full}, {72, full}, {74, full}, {0, full}}, 60},
		{"weights, not counts: two heavy judges outweigh three light ones", []judge.Point{{80, 4 * full}, {85, 4 * full}, {20, full}, {20, full}, {20, full}}, 80},
		{"zero weight does not count", []judge.Point{{0, 0}, {0, 0}, {0, 0}}, 50},
	} {
		if got := judge.Consensus(c.pts); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
		if judge.Score(c.pts) != judge.Consensus(c.pts) {
			t.Errorf("%s: Score differs from Consensus", c.name)
		}
	}
	// Order does not matter, and the input is not modified.
	pts := []judge.Point{{10, 3}, {90, 7}, {40, 5}}
	a := judge.Consensus(pts)
	if b := judge.Consensus([]judge.Point{pts[2], pts[0], pts[1]}); a != b || pts[0].X != 10 {
		t.Fatal("consensus depends on order or modified its input")
	}
	// The prior scales with the subject: the same judgements at any common
	// weight give the same score (§2: 2 × their mean weight).
	for _, w := range []int64{1, 300_000, full, 7 * full} {
		if got := judge.Consensus([]judge.Point{{90, w}, {94, w}}); got != 70 {
			t.Fatalf("two judges at weight %d: %d, want 70", w, got)
		}
	}
	// Thin: below judge.ThinBelow weighted judgements.
	if !judge.Thin(0) || !judge.Thin(judge.ThinBelow-1) || judge.Thin(judge.ThinBelow) {
		t.Fatal("thin")
	}
	// Merged groups weigh the prior by the judgements they stand for: two
	// judgements at 100 merged into one point are still two.
	if g, s := judge.ScoreGrouped([]judge.Point{{100, 2 * full}}, 2), judge.Consensus([]judge.Point{{100, full}, {100, full}}); g != s || g != 75 {
		t.Fatalf("grouped %d, ungrouped %d", g, s)
	}
}

// c_T is the later judges' weighted median without the prior (C303s): the
// prior shrank thin outcomes to 50, so a judge who named the truth early
// lost to one who said 50. Herding still earns exactly nothing.
func TestOutcomeHasNoPrior(t *testing.T) {
	const full = 1_000_000
	for _, c := range []struct {
		name string
		pts  []judge.Point
		want int64
	}{
		{"no judgement: neutral", nil, 50},
		{"zero weight is no judgement", []judge.Point{{0, 0}}, 50},
		{"one judgement is the outcome", []judge.Point{{100, full}}, 100},
		{"thin weights are not shrunk", []judge.Point{{90, 300_000}, {94, 300_000}, {20, 200_000}}, 90},
		{"an exact half: the midpoint", []judge.Point{{70, full}, {80, full}}, 75},
	} {
		if got := judge.Outcome(c.pts); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	// Two later judges at 90 and 94 with free-use weights (0.3): a prior
	// inside c_T made it 50 and the early judge who said 92 at c0 = 50 lost
	// 0.42; without it c_T is 92 and she earns 0.42. A herder (x = c0) earns
	// 0. The score keeps the prior: 70, labelled thin.
	later := []judge.Point{{90, 300_000}, {94, 300_000}}
	if judge.Consensus(later) != 70 || !judge.Thin(2) {
		t.Fatal("the score of two thin judgements")
	}
	cT := judge.Outcome(later)
	if g := judge.Gain(92, 50, cT, false); cT != 92 || g != 420_000 {
		t.Fatalf("early, right: gain %d at c_T %d", g, cT)
	}
	if g := judge.Gain(50, 50, cT, false); g != 0 {
		t.Fatalf("herding earns %d", g)
	}
}

// TestMappingGolden is the golden table of RFC 0020 §1.
func TestMappingGolden(t *testing.T) {
	type row struct {
		act, input string
		x          int64
		coarse, ok bool
	}
	var got []row
	for _, c := range []struct {
		verdict     string
		score, good int64
	}{{"good", -1, -1}, {"mixed", -1, -1}, {"bad", -1, -1}, {"unusable", -1, -1}, {"unverified", -1, -1}, {"bad", -1, 64}, {"good", 91, 64}, {"unverified", 91, 64}, {"good", 140, -1}} {
		x, ok := judge.FromReview(c.verdict, c.score, c.good)
		got = append(got, row{"review", c.verdict + "/" + itoa(c.score) + "/" + itoa(c.good), x, false, ok})
	}
	for _, c := range [][2]int64{{1, 0}, {1, 95}, {1, 20}, {0, 0}} {
		x, coarse, ok := judge.FromVouch(c[0], c[1])
		got = append(got, row{"vouch", itoa(c[0]) + "/" + itoa(c[1]), x, coarse, ok})
	}
	for _, v := range []int64{1, -1, 0} {
		x, ok := judge.FromVote(v)
		got = append(got, row{"vote", itoa(v), x, true, ok})
	}
	for _, r := range []int64{-1, 0, 88} {
		x, coarse := judge.FromWorkAccept(r)
		got = append(got, row{"work_accept", itoa(r), x, coarse, true})
	}
	got = append(got, row{"witness", "verified", judge.FromWitness(true), true, true}, row{"witness", "failed", judge.FromWitness(false), true, true})
	want := []row{
		{"review", "good/-1/-1", 80, false, true},
		{"review", "mixed/-1/-1", 50, false, true},
		{"review", "bad/-1/-1", 20, false, true},
		{"review", "unusable/-1/-1", 0, false, true},
		{"review", "unverified/-1/-1", 0, false, false},
		{"review", "bad/-1/64", 64, false, true},
		{"review", "good/91/64", 91, false, true},
		{"review", "unverified/91/64", 0, false, false},
		{"review", "good/140/-1", 100, false, true},
		{"vouch", "1/0", 80, true, true},
		{"vouch", "1/95", 95, false, true},
		{"vouch", "1/20", 51, false, true},
		{"vouch", "0/0", 0, false, false},
		{"vote", "1", 100, true, true},
		{"vote", "-1", 0, true, true},
		{"vote", "0", 0, true, false},
		{"work_accept", "-1", 75, true, true},
		{"work_accept", "0", 0, false, true},
		{"work_accept", "88", 88, false, true},
		{"witness", "verified", 100, true, true},
		{"witness", "failed", 0, true, true},
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("§1 mapping changed:\n got %v\nwant %v", got, want)
	}
	// The table as data names every act.
	acts := map[string]bool{}
	for _, r := range judge.Mapping {
		acts[r.Act] = true
	}
	for _, a := range []string{judge.ActReview, judge.ActVouch, judge.ActVote, judge.ActWorkAccept, judge.ActWitness} {
		if !acts[a] {
			t.Errorf("judge.Mapping has no row for %s", a)
		}
	}
	// A coarse judgement counts as its side in the median.
	if (judge.Judgement{X: 80, Coarse: true}).MedianX() != 100 || (judge.Judgement{X: 20, Coarse: true}).MedianX() != 0 || (judge.Judgement{X: 80}).MedianX() != 80 {
		t.Fatal("MedianX")
	}
	// RFC 0021 §2: s(x) = (x − 50)/50 above 50, a coarse act at coarse_x,
	// a down judgement no edge.
	for _, c := range []struct {
		j    judge.Judgement
		want int64
	}{{judge.Judgement{X: 80}, 600_000}, {judge.Judgement{X: 100, Coarse: true}, 500_000}, {judge.Judgement{X: 0}, 0}, {judge.Judgement{X: 50}, 0},
		{judge.Judgement{X: 0, Coarse: true}, 0}} {
		if got := c.j.EdgePPM(75); got != c.want {
			t.Fatalf("EdgePPM(%+v) = %d, want %d", c.j, got, c.want)
		}
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestWeight(t *testing.T) {
	if w := judge.Weight(judge.EvidencePPM("paid_use"), 1e6, judge.RelationshipPPM("customer"), 1e6); w != 1e6 {
		t.Fatalf("full weight %d", w)
	}
	if w := judge.Weight(judge.EvidencePPM("opinion"), 500_000, judge.RelationshipPPM("competitor"), 500_000); w != 25_000 {
		t.Fatalf("0.2 × 0.5 × 0.5 × 0.5 = %d ppm", w)
	}
	if judge.Weight(judge.EvidencePPM("work"), 1e6, judge.RelationshipPPM("owner"), 1e6) != 0 {
		t.Fatal("an owner's judgement weighs 0")
	}
	if judge.DayFactorPPM != trust.DayFactor(judge.HalfLifeDays) {
		t.Fatalf("judge.DayFactorPPM %d, trust.DayFactor(%d) %d", judge.DayFactorPPM, judge.HalfLifeDays, trust.DayFactor(judge.HalfLifeDays))
	}
}

// OwnSubject (RFC 0020 §3.3): a judgement by an owner, or by an account of
// an owner's root, is no judgement; another root's is.
func TestOwnSubject(t *testing.T) {
	roots := map[string]string{"seller": "domain:x", "mate": "domain:x", "other": "domain:y"}
	rootOf := func(a string) string {
		if r, ok := roots[a]; ok {
			return r
		}
		return a
	}
	owners := slices.Values([]string{"seller"})
	for author, want := range map[string]bool{"seller": true, "mate": true, "other": false, "fresh": false} {
		if got := judge.OwnSubject(author, owners, rootOf); got != want {
			t.Errorf("%s: %v, want %v", author, got, want)
		}
	}
	if judge.OwnSubject("seller", slices.Values([]string(nil)), rootOf) {
		t.Error("a subject without owners is nobody's own")
	}
}
