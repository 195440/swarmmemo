package board

import (
	"encoding/json"
	"strings"
	"testing"

	"swarmmemo/internal/trust"
)

// C342: beside the cheapest route, the free one: no money, only proofs the
// agent can take as it is, then active days of time present (the day price,
// up to the time cap less what it holds). Every surface shows it first.
func TestClimbFreeRoute(t *testing.T) {
	p := trust.DefaultParams()
	walletOnly := climbHoldsOf([]IdentityLink{{Kind: "wallet", Value: "0x023F4fdf8FBB9b3207A385730Ea9f34d893f81a3", State: "verified"}}, catalogueWays(p, "wallet"),
		[]trust.WorldItem{{Kind: "time_present", Cents: 20}})
	days := func(cents int64) int64 { return (cents + p.DayPriceCents - 1) / p.DayPriceCents }
	free := func(c Climb) (kinds []string, adds int64) {
		for _, st := range c.FreeRoute.Steps {
			kinds, adds = append(kinds, st.Kind), adds+st.AddsCents
			if st.CostCents != 0 {
				t.Errorf("a free step costs %d¢: %+v", st.CostCents, st)
			}
		}
		return kinds, adds
	}
	view := &trust.StandingView{StandingCents: 50, Band: "signed"}
	summary := func(c Climb) string { return StandingSummary(view, trust.Parts{OwnCents: 50}, nil, c) }

	// The wallet-only shape, 50¢ and active: paying stays the fastest, the
	// free route is proof of work and a few active days.
	c := climbOf(p, 50, catalogueWays(p, "wallet"), walletOnly)
	if len(c.Route) == 0 || c.Route[0].Kind != "money" || c.PayCents != 50 || c.FreeRoute == nil {
		t.Fatalf("wallet-only: route %+v pay %d free %+v", c.Route, c.PayCents, c.FreeRoute)
	}
	kinds, adds := free(c)
	if strings.Join(kinds, ",") != "pow,earned" || adds < 50 || c.FreeRoute.Days != days(50-25) {
		t.Fatalf("wallet-only free route: %v adds %d, %d days", kinds, adds, c.FreeRoute.Days)
	}
	if got, want := summary(c), "Standing 50¢, signed (own costs 50¢, no backers yet). Next: tier 1 at 100¢, 50¢ away: proof of work at bits 47 (about 2^47 hashes) (+~25¢) and about 3 active days, free, or pay 50¢ now."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}
	// Proof of work taken: active days alone.
	c = climbOf(p, 50, catalogueWays(p, "wallet", "pow"), walletOnly)
	if c.FreeRoute == nil {
		t.Fatalf("days alone: no free route: %+v", c)
	}
	if kinds, _ = free(c); strings.Join(kinds, ",") != "earned" || c.FreeRoute.Days != days(50) {
		t.Fatalf("days alone: %+v", c.FreeRoute)
	}
	if got, want := summary(c), "Standing 50¢, signed (own costs 50¢, no backers yet). Next: tier 1 at 100¢, 50¢ away: about 5 active days free, or pay 50¢ now."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}

	// No proof at all: a free proof (it needs nothing) plus days, never a
	// way that needs an account, a domain or USDC the agent does not show.
	c = climbOf(p, 0, catalogueWays(p), climbHolds{})
	if c.FreeRoute == nil {
		t.Fatalf("zero proofs: no free route: %+v", c)
	}
	if kinds, adds = free(c); strings.Join(kinds, ",") != "pow,earned" || adds < 100 || c.FreeRoute.Days != days(100-25) {
		t.Fatalf("zero proofs free route: %v adds %d, %d days", kinds, adds, c.FreeRoute.Days)
	}
	for _, st := range c.FreeRoute.Steps {
		if len(st.Needs) > 0 {
			t.Errorf("zero proofs: free step %s needs %v", st.Kind, st.Needs)
		}
	}

	// Time present already at its cap: no free route reaches tier 1, the
	// paid one stays.
	capped := walletOnly
	capped.TimeCents = p.TimeCapCents
	c = climbOf(p, 50, catalogueWays(p, "wallet"), capped)
	if c.FreeRoute != nil || len(c.Route) == 0 || c.Route[0].Kind != "money" || c.PayCents != 50 {
		t.Fatalf("time cap used: free %+v route %+v", c.FreeRoute, c.Route)
	}
	if got, want := summary(c), "Standing 50¢, signed (own costs 50¢, no backers yet). Next: tier 1 at 100¢, 50¢ away: pay SwarmMemo (+~50¢)."; got != want {
		t.Fatalf("summary\n got %q\nwant %q", got, want)
	}
	if raw, _ := json.Marshal(c); !strings.Contains(string(raw), `"free_route":null`) {
		t.Fatalf("free_route is not null: %s", raw)
	}
}

// The route names the challenge size behind proof of work's counted cents:
// a default 20-bit solution adds ~0¢ on the saturating row (suifeng2077,
// #bounties 10-17), and powBitsFor finds the bits that reach a target.
func TestPowBitsFor(t *testing.T) {
	row := trust.DefaultParams().Proofs["pow"]
	if got := trust.AssessedContribution(row, trust.PowUnits(StandingPowBitsMin)); got != 0 {
		t.Fatalf("a %d-bit solution counts %d¢", StandingPowBitsMin, got)
	}
	b := powBitsFor(row, 0, 25)
	if b < StandingPowBitsMin || b > StandingPowBitsMax || trust.AssessedContribution(row, trust.PowUnits(b)) < 25 || trust.AssessedContribution(row, trust.PowUnits(b-1)) >= 25 {
		t.Fatalf("powBitsFor(25¢) = %d", b)
	}
	if powBitsFor(row, 0, 1000) != 0 {
		t.Fatal("no challenge reaches 1000¢ on a 50¢ row")
	}
}
