package judge_test

import (
	"encoding/json"
	"math/rand"
	"testing"

	"swarmmemo/internal/judge"
	"swarmmemo/internal/trust"
)

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
		{"one judgement cannot leave the prior's half: midpoint", []judge.Point{{100, 2 * full}}, 75},
		{"one full judgement stays at the prior", []judge.Point{{100, full}}, 50},
		{"three full judgements outweigh the prior", []judge.Point{{90, full}, {90, full}, {90, full}}, 90},
		{"one extreme judge cannot drag it", []judge.Point{{70, full}, {72, full}, {74, full}, {76, full}, {0, full}}, 70},
		{"an exact half: the midpoint, rounded down", []judge.Point{{70, full}, {72, full}, {74, full}, {0, full}}, 60},
		{"weights, not counts", []judge.Point{{20, 5 * full}, {90, full}, {90, full}}, 20},
		{"zero weight does not count", []judge.Point{{0, 0}, {0, 0}, {0, 0}}, 50},
		{"thin opinion stays near neutral", []judge.Point{{100, 200_000}, {100, 200_000}}, 50},
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
	// Two later judges at 90 and 94 with free-use weights (0.3): the
	// prior made c_T 50 and the early judge who said 92 at c0 = 50 lost
	// 0.42; without it c_T is 92 and she earns 0.42. A herder (x = c0) earns 0.
	later := []judge.Point{{90, 300_000}, {94, 300_000}}
	if judge.Consensus(later) != 50 {
		t.Fatal("the prior no longer dominates thin judgements in the score")
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
	if w, opp := (judge.Judgement{X: 80}).EdgeWeightPPM(); w != 600_000 || opp {
		t.Fatalf("edge weight %d %v", w, opp)
	}
	if w, opp := (judge.Judgement{X: 0}).EdgeWeightPPM(); w != 1_000_000 || !opp {
		t.Fatalf("edge weight %d %v", w, opp)
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

// TestSettleConserves is the conservation property (RFC 0020 §3.3): over
// random runs, Σ paid ≤ Σ forfeited, losses are taken whole, no gain is paid
// more than it earned or with the wrong sign, and the marks sum to ≤ 0.
func TestSettleConserves(t *testing.T) {
	rng := rand.New(rand.NewSource(20))
	for run := 0; run < 2000; run++ {
		n := rng.Intn(40)
		raw := make([]int64, n)
		for i := range raw {
			switch rng.Intn(4) {
			case 0:
				raw[i] = 0
			case 1:
				raw[i] = -rng.Int63n(1e9)
			default:
				raw[i] = judge.Mark(rng.Int63n(1e12), rng.Int63n(2e6+1)-1e6)
			}
		}
		checkSettle(t, raw)
	}
	checkSettle(t, nil)
	checkSettle(t, []int64{5, 7})                // only gains: nothing paid
	checkSettle(t, []int64{-5, -7})              // only losses: all returned
	checkSettle(t, []int64{1 << 61, -(1 << 61)}) // large values
}

func FuzzSettle(f *testing.F) {
	f.Add(int64(10), int64(-3), int64(7))
	f.Fuzz(func(t *testing.T, a, b, c int64) {
		// Marks are bounded by stakes (a share of standing in millicents).
		a, b, c = a%(1<<50), b%(1<<50), c%(1<<50)
		checkSettle(t, []int64{a, b, c, -a / 2})
	})
}

func checkSettle(t *testing.T, raw []int64) {
	t.Helper()
	s := judge.Settle(raw)
	var paid, forfeited, total int64
	for i, m := range s.Marks {
		switch {
		case raw[i] < 0:
			if m != max(raw[i], -(1<<62)) {
				t.Fatalf("a loss is not taken whole: %d → %d", raw[i], m)
			}
			forfeited -= m
		case m < 0 || m > raw[i]:
			t.Fatalf("a gain %d paid %d", raw[i], m)
		default:
			paid += m
		}
		total += m
	}
	if paid != s.Paid || forfeited != s.Forfeited || paid > forfeited || total > 0 || s.Returned != forfeited-paid || s.Returned < 0 {
		t.Fatalf("not conserved: paid %d forfeited %d total %d settlement %+v", paid, forfeited, total, s)
	}
}
