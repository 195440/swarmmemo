package sim

import (
	"context"
	"math"
	"reflect"
	"sync"
	"testing"

	"swarmmemo/internal/trust"
)

// RFC 0021 §6: one half-life. The score's judgement decay (180 days) set to
// standing's (90) keeps the C303s score targets; the numbers are cited in
// the RFC.
func TestOneHalfLife(t *testing.T) {
	if testing.Short() {
		t.Skip("RFC 0021 simulation")
	}
	cfg := Config{Params: func(p *trust.Params) {
		jp := *p.Standing.Judgements
		jp.HalfLifeDays, jp.DayFactorPPM = p.Standing.HalfLifeDays, p.Standing.DayFactorPPM
		p.Standing.Judgements = &jp
	}}
	for _, name := range []string{"baseline", "griefing", "funded", "thin"} {
		sc, _ := ScenarioByName(name)
		r, err := Simulate(context.Background(), sc, 1, testSeeds, cfg)
		if err != nil {
			t.Fatal(err)
		}
		base := reportsFor(t)[name]
		t.Logf("%s: |score − q| %.1f → %.1f; griefed lift %.1f → %.1f; funded lift %.0f → %.0f; informed final %+.2f → %+.2f¢; conserved %v",
			name, mean(t, base, MetricAccuracy), mean(t, r, MetricAccuracy), base.Stats["lift.griefed"].Mean, r.Stats["lift.griefed"].Mean,
			base.Stats["lift.funded"].Mean, r.Stats["lift.funded"].Mean, mean(t, base, MetricMarkFinal+string(Informed)), mean(t, r, MetricMarkFinal+string(Informed)), r.Conserved)
		if !r.Conserved {
			t.Errorf("%s: minted", name)
		}
		if v, b := mean(t, r, MetricAccuracy), mean(t, base, MetricAccuracy); v > b+2 {
			t.Errorf("%s: |score − q| %.1f with one half-life, %.1f with two", name, v, b)
		}
	}
}

// The scenario invariants of RFC 0020 under rule 4 (C303b: trust parameter
// version 7, each run reading the previous run's carry): the acceptance
// targets of §9.1. Each scenario runs three seeds (about 0.2 s each); the
// reports are computed once and shared.

const testSeeds = 3

var (
	reportsOnce sync.Once
	reports     map[string]Report
	reportsErr  error
)

func reportsFor(t *testing.T) map[string]Report {
	t.Helper()
	reportsOnce.Do(func() {
		reports = map[string]Report{}
		for _, sc := range Scenarios() {
			r, err := Simulate(context.Background(), sc, 1, testSeeds, Config{})
			if err != nil {
				reportsErr = err
				return
			}
			reports[sc.Name] = r
		}
	})
	if reportsErr != nil {
		t.Fatal(reportsErr)
	}
	return reports
}

func mean(t *testing.T, r Report, metric string) float64 {
	t.Helper()
	s, ok := r.Stats[metric]
	if !ok {
		t.Fatalf("%s: no metric %s", r.Scenario.Name, metric)
	}
	return s.Mean
}

// No minting, always: every run pays at most what it forfeits and holds at
// most its seed mass, and all runs together pay at most what they forfeit.
func TestConservation(t *testing.T) {
	for name, r := range reportsFor(t) {
		if !r.Conserved {
			t.Errorf("%s: minted", name)
		}
		for _, res := range r.Results {
			if res.M[MetricPaid] > res.M[MetricForfeited] || res.M[MetricEscrow] < 0 {
				t.Errorf("%s seed %d: paid %.3f¢ of %.3f¢ forfeited, escrow %.3f¢", name, res.Seed, res.M[MetricPaid], res.M[MetricForfeited], res.M[MetricEscrow])
			}
		}
	}
}

// In an honest market the early, informed judges earn in final marks and
// contrarians lose.
func TestBaselineHonestEarlyEarns(t *testing.T) {
	r := reportsFor(t)["baseline"]
	if v := mean(t, r, MetricMarkFinal+string(Informed)); v <= 0 {
		t.Errorf("honest-informed: %.3f¢, want > 0", v)
	}
	if v := mean(t, r, MetricMarkFinal+string(Contrarian)); v >= 0 {
		t.Errorf("contrarian: %.3f¢, want < 0", v)
	}
}

// Herders (x = c0, read at entry) earn nothing, provisional or final.
func TestHerdersEarnAboutNothing(t *testing.T) {
	for name, r := range reportsFor(t) {
		informed := math.Abs(mean(t, r, MetricMarkFinal+string(Informed)))
		for _, m := range []string{MetricMarkR90, MetricMarkFinal} {
			if v := math.Abs(mean(t, r, m+string(Herder))); v > 0.02 || v > informed/4 && informed > 0.08 {
				t.Errorf("%s %s herder: %.3f¢ (honest-informed %.3f¢)", name, m, v, informed)
			}
		}
	}
}

// Attackers never hold more standing above their seed than they spent;
// sybils without standing get nothing; griefers and the collusive pair
// lose in final marks.
func TestRingsCaptureLessThanCost(t *testing.T) {
	for name, r := range reportsFor(t) {
		for _, k := range []Kind{Sybil, Funded, Griefer, Collusive} {
			if _, ok := r.Stats[MetricCapture+string(k)]; !ok {
				continue
			}
			capture, cost := mean(t, r, MetricCapture+string(k)), mean(t, r, MetricCost+string(k))
			if capture > 0 && capture >= cost {
				t.Errorf("%s %s: held %.1f¢ above seed for %.1f¢", name, k, capture, cost)
			}
		}
	}
	if r := reportsFor(t)["sybil"]; mean(t, r, MetricMarkFinal+string(Sybil)) != 0 || mean(t, r, MetricCapture+string(Sybil)) != 0 {
		t.Error("sybil keys without standing were marked or gained")
	}
	if v := mean(t, reportsFor(t)["griefing"], MetricMarkFinal+string(Griefer)); v >= 0 {
		t.Errorf("griefer: %.3f¢, want < 0", v)
	}
	if v := mean(t, reportsFor(t)["thin"], MetricMarkFinal+string(Collusive)); v >= 0 {
		t.Errorf("collusive pair: %.3f¢, want < 0", v)
	}
}

// The prior at the subject's mean weight (§2): the score finds q, and one
// griefer moves the three best subjects much less than the 42 points a
// full-weight prior let it.
func TestScoreFindsQualityAndResistsGriefing(t *testing.T) {
	if v := mean(t, reportsFor(t)["baseline"], MetricAccuracy); v > 15 {
		t.Errorf("baseline |score − q| %.1f, want ≤ 15", v)
	}
	if v := mean(t, reportsFor(t)["griefing"], "lift.griefed"); v < -25 {
		t.Errorf("griefed subjects %.1f below q, want > −25", v)
	}
	if v := mean(t, reportsFor(t)["thin"], MetricThin); v <= 0 {
		t.Error("a thin market labels no subject thin")
	}
}

// Deterministic: a seed is a world.
func TestDeterministic(t *testing.T) {
	sc, _ := ScenarioByName("thin")
	a, err := Run(context.Background(), sc, 7, Config{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Run(context.Background(), sc, 7, Config{})
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("seed 7 differs:\n%v\n%v", a.M, b.M)
	}
	c, _ := Run(context.Background(), sc, 8, Config{})
	if reflect.DeepEqual(a.M, c.M) {
		t.Fatal("seeds 7 and 8 gave the same world")
	}
}
