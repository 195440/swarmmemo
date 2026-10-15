package web

import (
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
	"swarmmemo/internal/trust"
)

// C334: "Your trust" shows the distance to the next band and Trust ladder
// tier and the cheapest route, from trust.get's climb, and says in one
// sentence why backers whose edges carried nothing add little yet.
func TestTrustBreakdownClimb(t *testing.T) {
	a := &breakdownAnswer{Agent: strings.Repeat("ab", 32), Standing: &trust.StandingView{StandingCents: 40, Band: "signed"}, NodeCapCents: 750, BackersTotal: 1,
		Backers: []trust.FlowPart{{Root: strings.Repeat("cd", 32), CapCents: 84, Cents: 0}}}
	a.Bands.Proven, a.Bands.Trusted = 200, 750
	a.Climb = board.Climb{NextBand: &trust.Milestone{Name: "proven", Cents: 200, GapCents: 160}, NextTier: &trust.Milestone{Name: "tier 1", Cents: 100, GapCents: 60},
		Route: []board.ClimbStep{{Kind: "wallet", Label: "Link a wallet", AddsCents: 600}}, PayCents: 60}
	a.Climb.Target = a.Climb.NextTier
	v := buildTrustBreakdown(a, time.Now())
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, "trust-breakdown", v); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{`data-key="climb.next_band.gap_cents" data-value="160">160¢ away`, "proven at 200¢", `data-key="climb.next_tier.gap_cents" data-value="60">60¢ away`, "tier 1 at 100¢",
		`Cheapest route: <a href="/me#standing-wallet" data-kind="wallet">Link a wallet</a> <span class="muted">up to &#43;600¢</span>; or pay <span data-key="climb.pay_cents" data-value="60">60¢</span>`,
		"Reviews from low-standing backers carry little yet: a backer passes on what it holds above 750¢ or 25% of what it keeps, whichever is more, shared by everyone it backs."} {
		if !strings.Contains(html, want) {
			t.Errorf("Your trust lacks %q", want)
		}
	}
	// Received standing, or trusted: no sentence and no climb.
	a.ReceivedCents, a.Climb = 20, board.Climb{}
	b.Reset()
	if err := templates.ExecuteTemplate(&b, "trust-breakdown", buildTrustBreakdown(a, time.Now())); err != nil {
		t.Fatal(err)
	}
	if html = b.String(); strings.Contains(html, "low-standing backers") || strings.Contains(html, "tb-climb") || strings.Contains(html, "Cheapest route") {
		t.Error("the climb or the low-backers sentence shows when it should not")
	}
}
