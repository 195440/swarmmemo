package board

import (
	"context"
	"database/sql"
	"sort"

	"swarmmemo/internal/trust"
)

// The climb (C334): how far an account is from its next band and its next
// Trust ladder tier (trust.LadderCents), and the cheapest route to the
// nearer one, derived from the same ways standing.ways lists. trust.get,
// standing.ways, MCP trust and standing_ways and /me "Your trust" read it.

// Climb is an account's distance to what comes next, and the route there.
type Climb struct {
	NextBand *trust.Milestone `json:"next_band"`
	NextTier *trust.Milestone `json:"next_tier"`
	// Target is the nearer of the two (nil when both are reached).
	Target *trust.Milestone `json:"target"`
	// Route is the cheapest way to Target: the free proofs not taken yet,
	// largest first, until their caps cover the gap; then money paid here
	// for what is left, which counts at face value.
	Route []ClimbStep `json:"route"`
	// PayCents is the sure route: paying SwarmMemo the whole gap.
	PayCents int64 `json:"pay_cents"`
}

// ClimbStep is one step of a route: a way (its kind, label and action, as
// standing.ways lists it), what it adds at most and what it costs in money.
type ClimbStep struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	AddsCents int64  `json:"adds_cents"`
	CostCents int64  `json:"cost_cents"`
	Action    string `json:"action"`
}

// climbFree says whether a way costs no money: a proof provider, enabled
// and priced, not taken yet. Money, a bond (a deposit) and earned standing
// are not.
func climbFree(w StandingWay) bool {
	switch w.Kind {
	case "money", "bond", "earned":
		return false
	}
	return w.Priced && (w.Status == "" || w.Status == "enabled") && (w.State == "none" || w.State == "lapsed") && wayMax(w) > 0
}

// climbOf is the climb from cents over ways.
func climbOf(p trust.Params, cents int64, ways []StandingWay) Climb {
	c := Climb{NextBand: p.NextBand(cents), NextTier: trust.NextLadderTier(cents), Route: []ClimbStep{}}
	c.Target = c.NextBand
	if c.Target == nil || c.NextTier != nil && c.NextTier.GapCents < c.Target.GapCents {
		c.Target = c.NextTier
	}
	if c.Target == nil {
		return c
	}
	gap := c.Target.GapCents
	c.PayCents = gap
	var free []StandingWay
	for _, w := range ways {
		if climbFree(w) {
			free = append(free, w)
		}
	}
	sort.SliceStable(free, func(i, j int) bool { return wayMax(free[i]) > wayMax(free[j]) })
	var covered int64
	for _, w := range free {
		if covered >= gap {
			break
		}
		c.Route = append(c.Route, ClimbStep{Kind: w.Kind, Label: w.Label, AddsCents: wayMax(w), Action: w.Action})
		covered += wayMax(w)
	}
	if covered < gap {
		for _, w := range ways {
			if w.Kind == "money" {
				left := gap - covered
				c.Route = append(c.Route, ClimbStep{Kind: w.Kind, Label: w.Label, AddsCents: left, CostCents: left, Action: w.Action})
			}
		}
	}
	return c
}

// ZeroStandingNext is the one line an accept or a payout notice carries to
// a worker whose standing is 0¢ (C334): the wallet link that starts it, as
// an MCP call and a signed command. It fits an inbox entry's detail.
const ZeroStandingNext = `Standing 0¢: link a wallet to start it. MCP raise_standing action challenge, kind wallet, value ADDRESS; or standing.challenge {"schema":1,"kind":"wallet","value":ADDRESS}.`

// zeroStandingNext is ZeroStandingNext for op (a work.accept or a
// work.settle) reaching worker at 0¢ standing while trust is on; "" else.
func (s *Store) zeroStandingNext(ctx context.Context, tx *sql.Tx, op, worker string, now int64) (string, error) {
	if op != "work.accept" && op != WorkSettle || worker == "" || s.config.Features.Trust == TrustOff {
		return "", nil
	}
	p, err := s.trustParamsAt(ctx, tx, now)
	if err != nil {
		return "", err
	}
	view, _, err := trust.CurrentStanding(ctx, tx, worker, p, now)
	if err != nil || view.StandingCents > 0 {
		return "", err
	}
	return ZeroStandingNext, nil
}

// climbFor is the climb of an account from its ways and standing.
func (s *Store) climbFor(ctx context.Context, q *sql.Tx, view *trust.StandingView, ways []StandingWay, now int64) (Climb, error) {
	p, err := s.trustParamsAt(ctx, q, now)
	if err != nil {
		return Climb{}, err
	}
	return climbOf(p, view.StandingCents, ways), nil
}
