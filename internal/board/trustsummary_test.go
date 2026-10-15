package board

import (
	"testing"

	"swarmmemo/internal/trust"
)

// StandingSummary is the breakdown in one plain-English line, every number
// the breakdown's own (C320b): the MCP trust tool and the CLI lead with it.
func TestStandingSummaryLine(t *testing.T) {
	view := &trust.StandingView{StandingCents: 340, Band: "proven"}
	parts := trust.Parts{OwnCents: 290, ReceivedCents: 50, BackersTotal: 3}
	next := []StandingWay{{Kind: "wallet", Label: "Link a wallet", AddsCents: &StandingRange{Max: 600}}, {Kind: "github", Label: "Link GitHub", AddsCents: &StandingRange{Max: 300}}}
	if got, want := StandingSummary(view, parts, next, Climb{}), "Standing 340¢, proven (own costs 290¢, backed by 3 agents 50¢). Next: link a wallet (+~600¢)."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}
	view = &trust.StandingView{StandingCents: 1250, Band: "trusted", Stale: true}
	parts = trust.Parts{OwnCents: 750, ReceivedCents: 500, BackersTotal: 1, Penalty: &trust.PenaltyState{Role: "backer", EndsAt: 1760000000}}
	if got, want := StandingSummary(view, parts, []StandingWay{{Kind: "earned", Label: "Earned standing"}}, Climb{}), "Standing 1,250¢, trusted (own costs 750¢, backed by 1 agent 500¢). Under a penalty as a backer until 2025-10-09. Next: earned standing. The latest run is stale."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}
	if got, want := StandingSummary(&trust.StandingView{Band: "signed"}, trust.Parts{}, nil, Climb{}), "Standing 0¢, signed (own costs 0¢, no backers yet)."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}
	// With the climb (C334): the nearer target and its route's first step.
	view = &trust.StandingView{StandingCents: 40, Band: "signed"}
	ways := []StandingWay{
		{Kind: "wallet", Label: "Link a wallet", Priced: true, State: "none", Status: "enabled", AddsCents: &StandingRange{Max: 600}},
		{Kind: "money", Label: "Pay SwarmMemo", Priced: true, State: "none"},
	}
	climb := climbOf(trust.DefaultParams(), 40, ways)
	if got, want := StandingSummary(view, trust.Parts{OwnCents: 40}, next, climb), "Standing 40¢, signed (own costs 40¢, no backers yet). Next: tier 1 at 100¢, 60¢ away: link a wallet (+~600¢)."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}
}

// The climb (C334): the next band and Trust ladder tier from the one list
// (trust.LadderCents) and the bands' floors, and the cheapest route to the
// nearer from the ways standing.ways lists: free proofs not taken yet,
// largest first, then money for what they leave.
func TestClimb(t *testing.T) {
	p := trust.DefaultParams()
	ways := []StandingWay{
		{Kind: "pow", Label: "Proof of work", Priced: true, State: "none", Status: "enabled", AddsCents: &StandingRange{Max: 50}, Action: "pow"},
		{Kind: "wallet", Label: "Link a wallet", Priced: true, State: "verified", Status: "enabled", AddsCents: &StandingRange{Max: 600}},
		{Kind: "google", Label: "Link Google", Priced: true, State: "none", Status: "disabled-no-credentials", AddsCents: &StandingRange{Max: 50}},
		{Kind: "github", Label: "Link GitHub", Priced: true, State: "lapsed", Status: "enabled", AddsCents: &StandingRange{Max: 300}, Action: "github"},
		{Kind: "bond", Label: "Post a bond", Priced: true, State: "none", Status: "enabled", AddsCents: &StandingRange{Max: 500}},
		{Kind: "money", Label: "Pay SwarmMemo", Priced: true, State: "none", AddsCents: &StandingRange{Max: 750}, Action: "topup"},
		{Kind: "earned", Label: "Earned standing", Priced: true, State: "none"},
	}
	c := climbOf(p, 0, ways)
	if c.NextBand == nil || c.NextBand.Name != "proven" || c.NextBand.Cents != p.Theta2Cents || c.NextBand.GapCents != p.Theta2Cents {
		t.Fatalf("next band from 0¢: %+v", c.NextBand)
	}
	if c.NextTier == nil || c.NextTier.Name != "tier 1" || c.NextTier.Cents != trust.LadderCents[0] || c.Target != c.NextTier || c.PayCents != trust.LadderCents[0] {
		t.Fatalf("next tier from 0¢: %+v, target %+v, pay %d", c.NextTier, c.Target, c.PayCents)
	}
	if len(c.Route) != 1 || c.Route[0].Kind != "github" || c.Route[0].CostCents != 0 {
		t.Fatalf("route to 100¢: %+v", c.Route)
	}
	// 250¢: proven is passed, tier 2 (300¢) is 50¢ away, trusted 500¢.
	c = climbOf(p, 250, ways)
	if c.NextBand.Name != "trusted" || c.NextBand.GapCents != p.NodeCapCents-250 || c.NextTier.Name != "tier 2" || c.Target.GapCents != 50 {
		t.Fatalf("from 250¢: %+v %+v target %+v", c.NextBand, c.NextTier, c.Target)
	}
	// No free proof left: money pays the whole gap.
	c = climbOf(p, 250, ways[4:])
	if len(c.Route) != 1 || c.Route[0].Kind != "money" || c.Route[0].CostCents != 50 {
		t.Fatalf("money route: %+v", c.Route)
	}
	// Free proofs short of the gap: money for what they leave.
	c = climbOf(p, 0, append([]StandingWay{ways[0]}, ways[4:]...))
	if len(c.Route) != 2 || c.Route[0].Kind != "pow" || c.Route[1].Kind != "money" || c.Route[1].CostCents != 50 {
		t.Fatalf("pow then money: %+v", c.Route)
	}
	// Trusted and past the ladder: nothing next.
	if c = climbOf(p, p.NodeCapCents, ways); c.Target != nil || c.NextBand != nil || c.NextTier != nil || len(c.Route) != 0 {
		t.Fatalf("at trusted: %+v", c)
	}
}
