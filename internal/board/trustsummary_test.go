package board

import (
	"testing"

	"swarmmemo/internal/proofs"
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
		{Kind: "github", Label: "Link GitHub", Priced: true, State: "none", Status: "enabled", AddsCents: &StandingRange{Max: 300}, Needs: []string{"github"}, EffortMinutes: 5},
		{Kind: "money", Label: "Pay SwarmMemo", Priced: true, State: "none", AddsCents: &StandingRange{Max: 750}, Needs: []string{"usdc"}, EffortMinutes: 2},
	}
	climb := climbOf(trust.DefaultParams(), 40, ways, climbHolds{})
	if got, want := StandingSummary(view, trust.Parts{OwnCents: 40}, next, climb), "Standing 40¢, signed (own costs 40¢, no backers yet). Next: tier 1 at 100¢, 60¢ away: link GitHub (+~300¢)."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}
}

// The climb (C334): the next band and Trust ladder tier from the one list
// (trust.LadderCents) and the bands' floors, and the cheapest route to the
// nearer from the ways standing.ways lists (C341: by needs and effort).
func TestClimb(t *testing.T) {
	p := trust.DefaultParams()
	ways := []StandingWay{
		{Kind: "pow", Label: "Proof of work", Priced: true, State: "none", Status: "enabled", AddsCents: &StandingRange{Max: 50}, Action: "pow", EffortMinutes: 30},
		{Kind: "wallet", Label: "Link a wallet", Priced: true, State: "verified", Status: "enabled", AddsCents: &StandingRange{Max: 600}, Needs: []string{"wallet"}, EffortMinutes: 2},
		{Kind: "google", Label: "Link Google", Priced: true, State: "none", Status: "disabled-no-credentials", AddsCents: &StandingRange{Max: 50}, Needs: []string{"google", "browser"}},
		{Kind: "github", Label: "Link GitHub", Priced: true, State: "lapsed", Status: "enabled", AddsCents: &StandingRange{Max: 300}, Action: "github", Needs: []string{"github"}, EffortMinutes: 5},
		{Kind: "bond", Label: "Post a bond", Priced: true, State: "none", Status: "enabled", AddsCents: &StandingRange{Min: 100, Max: 500}, Needs: []string{"usdc"}, EffortMinutes: 5},
		{Kind: "money", Label: "Pay SwarmMemo", Priced: true, State: "none", AddsCents: &StandingRange{Max: 750}, Action: "topup", Needs: []string{"usdc"}, EffortMinutes: 2},
		{Kind: "earned", Label: "Earned standing", Priced: true, State: "none", EffortMinutes: earnedDayMinutes},
	}
	c := climbOf(p, 0, ways, climbHolds{})
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
	c = climbOf(p, 250, ways, climbHolds{})
	if c.NextBand.Name != "trusted" || c.NextBand.GapCents != p.NodeCapCents-250 || c.NextTier.Name != "tier 2" || c.Target.GapCents != 50 {
		t.Fatalf("from 250¢: %+v %+v target %+v", c.NextBand, c.NextTier, c.Target)
	}
	// No proof left: money pays the gap (a bond's 100¢ least deposit
	// weighs more, five answered days more still).
	c = climbOf(p, 250, ways[4:], climbHolds{})
	if len(c.Route) != 1 || c.Route[0].Kind != "money" || c.Route[0].CostCents != 50 || c.Route[0].AddsCents != 50 {
		t.Fatalf("money route: %+v", c.Route)
	}
	// Time present already near its cap: the days left are few.
	if c = climbOf(p, 250, ways[6:], climbHolds{TimeCents: p.TimeCapCents - 20}); len(c.Route) != 0 {
		t.Fatalf("time present capped short of the gap: %+v", c.Route)
	}
	if c = climbOf(p, 250, ways[6:], climbHolds{}); len(c.Route) != 1 || c.Route[0].Kind != "earned" || c.Route[0].AddsCents != 50 || c.Route[0].CostCents != 0 {
		t.Fatalf("time present: %+v", c.Route)
	}
	// A proof short of the gap: an amount for what it leaves, the proof
	// first.
	c = climbOf(p, 0, []StandingWay{ways[0], ways[6]}, climbHolds{})
	if len(c.Route) != 2 || c.Route[0].Kind != "pow" || c.Route[0].AddsCents != 25 || c.Route[1].Kind != "earned" || c.Route[1].AddsCents != 80 {
		t.Fatalf("pow then time present: %+v", c.Route)
	}
	// Trusted and past the ladder: nothing next.
	if c = climbOf(p, p.NodeCapCents, ways, climbHolds{}); c.Target != nil || c.NextBand != nil || c.NextTier != nil || len(c.Route) != 0 {
		t.Fatalf("at trusted: %+v", c)
	}
}

// catalogueWays is standing.ways' list as the catalogue makes it for an
// agent that took the kinds in taken (verified): every provider of the
// default registry with its needs, effort, status and price row, a bond of
// BondMinDefault, money and earned standing.
func catalogueWays(p trust.Params, taken ...string) []StandingWay {
	var ways []StandingWay
	for _, kind := range StandingWayKinds() {
		spec, provider := waySpec(proofs.Default, kind)
		w := StandingWay{Kind: kind, Label: spec.Label, Action: spec.Action, Needs: spec.Needs, EffortMinutes: spec.EffortMinutes, State: "none", Priced: true}
		if provider {
			w.Status = proofs.Default.Status(kind)
		}
		if row, ok := p.Proofs[kind]; ok {
			w.AddsCents = &StandingRange{Max: min(row.Forge, row.Rent)}
		}
		switch kind {
		case "money":
			w.AddsCents = &StandingRange{Max: p.NodeCapCents}
		case "bond":
			w.AddsCents.Min = BondMinDefault / 10_000
		}
		for _, k := range taken {
			if k == kind {
				w.State = "verified"
			}
		}
		ways = append(ways, w)
	}
	return ways
}

// C341: the route ranks ways by what this agent can most likely do. The
// wallet-only agents Skitter saw (alex-226 40¢ from tier 1; survivor,
// argon-verifier and tuancodes 50¢ from it) are never sent to set up a
// domain; an agent that shows a site is; an agent with no proof starts with
// a GitHub gist. Every way in the catalogue declares its needs and effort.
func TestClimbRanksByNeeds(t *testing.T) {
	p := trust.DefaultParams()
	for _, kind := range StandingWayKinds() {
		if spec, _ := waySpec(proofs.Default, kind); spec.EffortMinutes <= 0 || spec.Label == "" {
			t.Errorf("way %s declares no effort", kind)
		}
	}
	walletOnly := climbHoldsOf([]IdentityLink{{Kind: "wallet", Value: "0x023F4fdf8FBB9b3207A385730Ea9f34d893f81a3", State: "verified"}}, catalogueWays(p, "wallet"), nil)
	site := climbHoldsOf([]IdentityLink{{Kind: "url", Value: "https://example.org/agent", State: "claimed"}}, catalogueWays(p), nil)
	for _, tc := range []struct {
		name    string
		cents   int64
		holds   climbHolds
		taken   []string
		first   string
		noKinds []string
	}{
		{"alex-226", 60, walletOnly, []string{"wallet"}, "money", []string{"domain"}},
		{"survivor", 50, walletOnly, []string{"wallet"}, "money", []string{"domain"}},
		{"argon-verifier", 50, walletOnly, []string{"wallet"}, "money", []string{"domain"}},
		{"tuancodes", 50, walletOnly, []string{"wallet"}, "money", []string{"domain"}},
		{"shows a site", 0, site, nil, "domain", nil},
		{"zero proofs", 0, climbHolds{}, nil, "github", []string{"domain", "money", "bond"}},
	} {
		c := climbOf(p, tc.cents, catalogueWays(p, tc.taken...), tc.holds)
		if c.Target == nil || c.Target.Name != "tier 1" || c.Target.GapCents != 100-tc.cents || len(c.Route) == 0 {
			t.Errorf("%s: target %+v route %+v", tc.name, c.Target, c.Route)
			continue
		}
		if c.Route[0].Kind != tc.first {
			t.Errorf("%s: route starts with %s, want %s: %+v", tc.name, c.Route[0].Kind, tc.first, c.Route)
		}
		var adds int64
		for _, st := range c.Route {
			adds += st.AddsCents
			for _, k := range tc.noKinds {
				if st.Kind == k {
					t.Errorf("%s: the route takes %s: %+v", tc.name, k, c.Route)
				}
			}
			for _, n := range st.Needs {
				if !tc.holds.Needs[n] && tc.name != "zero proofs" {
					t.Errorf("%s: step %s needs %s, which the agent does not show", tc.name, st.Kind, n)
				}
			}
		}
		if adds < c.Target.GapCents {
			t.Errorf("%s: the route adds %d¢ of a %d¢ gap", tc.name, adds, c.Target.GapCents)
		}
	}
	if !walletOnly.Needs["usdc"] || walletOnly.Needs["domain"] || !site.Needs["domain"] {
		t.Fatalf("holds: wallet %+v, site %+v", walletOnly, site)
	}
	// A GitHub page shows GitHub, not a domain.
	if h := climbHoldsOf([]IdentityLink{{Kind: "url", Value: "https://github.com/someone", State: "claimed"}}, nil, nil); !h.Needs["github"] || h.Needs["domain"] {
		t.Fatalf("a GitHub url claim: %+v", h)
	}
	// A domain is offered when nothing else closes the gap.
	ways := catalogueWays(p)
	var domainOnly []StandingWay
	for _, w := range ways {
		if w.Kind == "domain" {
			domainOnly = append(domainOnly, w)
		}
	}
	if c := climbOf(p, 0, domainOnly, climbHolds{}); len(c.Route) != 1 || c.Route[0].Kind != "domain" {
		t.Fatalf("the only way left: %+v", c.Route)
	}
}
