package sim

import (
	"context"
	"math"
	"reflect"
	"sync"
	"testing"
)

// The scenario invariants of C303s. Each scenario runs three seeds (about
// 0.3 s each); the reports are computed once and shared.

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

// Conservation, always: every run's settlement pays at most what it
// forfeited and never raises the total standing, and so does the proposed
// final settlement (cohorts against an escrow).
func TestConservation(t *testing.T) {
	for name, r := range reportsFor(t) {
		if !r.Conserved {
			t.Errorf("%s: minted", name)
		}
		for _, res := range r.Results {
			if res.M[MetricPaid] > res.M[MetricForfeited] || res.M[MetricFixedPaid] > res.M[MetricFixedForfeited] {
				t.Errorf("%s seed %d: paid more than forfeited: %v", name, res.Seed, res.M)
			}
		}
	}
}

// In an honest market, the early, informed judges earn (as built at R90 and
// under the proposed finality), and herders earn about nothing.
func TestBaselineHonestEarlyEarns(t *testing.T) {
	r := reportsFor(t)["baseline"]
	for _, m := range []string{MetricMarkR90, MetricMarkFixed} {
		if v := mean(t, r, m+string(Informed)); v <= 0 {
			t.Errorf("%s honest-informed: %.3f¢, want > 0", m, v)
		}
		if v := mean(t, r, m+string(Contrarian)); v >= 0 {
			t.Errorf("%s contrarian: %.3f¢, want < 0", m, v)
		}
	}
}

func TestHerdersEarnAboutNothing(t *testing.T) {
	for _, name := range []string{"baseline", "herding", "griefing"} {
		r := reportsFor(t)[name]
		informed := math.Abs(mean(t, r, MetricMarkR90+string(Informed)))
		for _, m := range []string{MetricMarkR90, MetricMarkFixed} {
			if v := math.Abs(mean(t, r, m+string(Herder))); v > 0.05 || v > informed/4 {
				t.Errorf("%s %s herder: %.3f¢ (honest-informed %.3f¢)", name, m, v, informed)
			}
		}
	}
}

// Attackers never capture more than they spent, and lose or get nothing in
// marks: sybils without standing get exactly nothing, griefers and the
// collusive pair lose.
func TestRingsCaptureLessThanCost(t *testing.T) {
	for name, r := range reportsFor(t) {
		for _, k := range []Kind{Sybil, Funded, Griefer, Collusive} {
			if _, ok := r.Stats[MetricCapture+string(k)]; !ok {
				continue
			}
			capture, capped, cost := mean(t, r, MetricCapture+string(k)), mean(t, r, MetricCaptureCapped+string(k)), mean(t, r, MetricCost+string(k))
			if capture > 0 && capture >= cost || capped > 0 && capped >= cost/10 {
				t.Errorf("%s %s: captured %.1f¢ (capped bonus %.1f¢) for %.1f¢", name, k, capture, capped, cost)
			}
		}
	}
	if r := reportsFor(t)["sybil"]; mean(t, r, MetricMarkR90+string(Sybil)) != 0 || mean(t, r, MetricCapture+string(Sybil)) != 0 {
		t.Error("sybil keys without standing were marked")
	}
	if v := mean(t, reportsFor(t)["griefing"], MetricMarkFixed+string(Griefer)); v >= 0 {
		t.Errorf("griefer: %.3f¢, want < 0", v)
	}
	if v := mean(t, reportsFor(t)["thin"], MetricMarkFixed+string(Collusive)); v >= 0 {
		t.Errorf("collusive pair: %.3f¢, want < 0", v)
	}
}

// Freezing marks at window exit with c0 read then (as RFC 0020 §3.3 reads)
// lets the window truncate c0 towards the prior; reading c0 at entry keeps
// herding at zero.
func TestFinalityKeepsC0AtEntry(t *testing.T) {
	for _, res := range reportsFor(t)["baseline"].Results {
		if v := res.M[MetricMarkFixed+string(Herder)]; math.Abs(v) > 0.02 {
			t.Errorf("seed %d: herder final mark with c0 at entry %.3f¢", res.Seed, v)
		}
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
