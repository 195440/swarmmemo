package web

import (
	"strings"
	"testing"
)

// The trust section (RFC 0021 §13): the operator's and the top five
// sources' share over the runs, penalties and accounts above the vote
// floor; absent before any run.
func TestFunnelDashboardTrust(t *testing.T) {
	rep := funnelFixture()
	if strings.Contains(renderFunnel(t, rep), `id="trust"`) {
		t.Fatal("a trust section before any run")
	}
	rep.Trust = map[string]any{"run": 12, "as_of": 1759190400, "stale": false, "operator_passed_ppm": 640000, "top5_passed_ppm": 810000, "passed_cents": 9000,
		"sources": 9, "penalties_open": 1, "penalised": 3, "liable": 2, "above_vote_floor": 14,
		"history": []map[string]any{
			{"run": 11, "as_of": 1759104000, "operator_passed_ppm": 700000, "top5_passed_ppm": 850000, "penalties_open": 0, "above_vote_floor": 12},
			{"run": 12, "as_of": 1759190400, "operator_passed_ppm": 640000, "top5_passed_ppm": 810000, "penalties_open": 1, "above_vote_floor": 14}}}
	page := renderFunnel(t, rep)
	for _, want := range []string{`id="trust"`, `>Operator share</span></dt><dd class="stat-value">64%</dd><dd class="stat-delta down">−6 pts</dd>`, ">Top-5 share</span>",
		">Penalties in force</span>", ">Above the vote floor</span>", `data-label="Operator share"`, `data-label="Top-5 share"`, "3 members, 2 liable backers"} {
		if !strings.Contains(page, want) {
			t.Errorf("trust section lacks %q", want)
		}
	}
}
