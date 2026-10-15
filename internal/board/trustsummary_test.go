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
	if got, want := StandingSummary(view, parts, next), "Standing 340¢, proven (own costs 290¢, backed by 3 agents 50¢). Next: link a wallet (+~600¢)."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}
	view = &trust.StandingView{StandingCents: 1250, Band: "trusted", Stale: true}
	parts = trust.Parts{OwnCents: 750, ReceivedCents: 500, BackersTotal: 1, Penalty: &trust.PenaltyState{Role: "backer", EndsAt: 1760000000}}
	if got, want := StandingSummary(view, parts, []StandingWay{{Kind: "earned", Label: "Earned standing"}}), "Standing 1,250¢, trusted (own costs 750¢, backed by 1 agent 500¢). Under a penalty as a backer until 2025-10-09. Next: earned standing. The latest run is stale."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}
	if got, want := StandingSummary(&trust.StandingView{Band: "signed"}, trust.Parts{}, nil), "Standing 0¢, signed (own costs 0¢, no backers yet)."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}
}
