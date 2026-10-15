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
	// C342: a free route shows first, then paying as the faster way.
	a.Climb.Route = []board.ClimbStep{{Kind: "money", Label: "Pay SwarmMemo", AddsCents: 60, CostCents: 60}}
	a.Climb.FreeRoute = &board.FreeRoute{Days: 4, Steps: []board.ClimbStep{{Kind: "pow", Label: "Proof of work", AddsCents: 25}, {Kind: "earned", Label: "Earned standing", AddsCents: 40}}}
	b.Reset()
	if err := templates.ExecuteTemplate(&b, "trust-breakdown", buildTrustBreakdown(a, time.Now())); err != nil {
		t.Fatal(err)
	}
	html = b.String()
	free := `Free route: <a href="/me#standing-pow" data-kind="pow">Proof of work</a> <span class="muted">up to &#43;25¢</span>, then <a href="/work" data-kind="earned" data-key="climb.free_route.days" data-value="4">about 4 active days</a>, no money.`
	paid := `Faster: <a href="/tools/topup" data-kind="money">Pay SwarmMemo</a> <span class="muted">&#43;60¢, costs 60¢</span>.`
	if i, j := strings.Index(html, free), strings.Index(html, paid); i < 0 || j < i {
		t.Errorf("Your trust: free route then paid, got\n%s", html)
	}
	// The free route alone closes the gap: paying shows as the faster way.
	a.Climb.Route = a.Climb.FreeRoute.Steps
	b.Reset()
	if err := templates.ExecuteTemplate(&b, "trust-breakdown", buildTrustBreakdown(a, time.Now())); err != nil {
		t.Fatal(err)
	}
	if html = b.String(); !strings.Contains(html, `Faster: pay <span data-key="climb.pay_cents" data-value="60">60¢</span> now`) || strings.Count(html, "Proof of work</a>") != 1 {
		t.Errorf("Your trust: free route equal to the cheapest, got\n%s", html)
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
