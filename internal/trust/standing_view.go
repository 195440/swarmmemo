package trust

import (
	"context"
	"math"
	"strconv"

	"swarmmemo/internal/allowance"
)

// StandingView is an account's standing as reads show it (the record,
// trust.get, MCP, the agent page, reputation): the score log₁₀(1 + C), the
// cents C, the dollar figure, the band and v(s), and the run it comes from.
// Never a boolean and never a rank.
type StandingView struct {
	Standing      float64 `json:"standing"`
	StandingCents int64   `json:"standing_cents"`
	FakeCost      string  `json:"fake_cost"`
	Band          string  `json:"band"`
	VoteWeightPPM int64   `json:"vote_weight_ppm"`
	Root          string  `json:"root"`
	Run           int64   `json:"run"`
	AsOf          int64   `json:"as_of"`
	ParamsVersion int64   `json:"params_version"`
	Stale         bool    `json:"stale"`
}

// NewStandingView makes the view of an account's score in run (nil: the
// account has no row in that run, so standing 0, band signed).
func NewStandingView(score *Score, account string, run LatestRun, p Params, now int64) *StandingView {
	v := &StandingView{Band: allowance.BandSigned.Name(), FakeCost: FakeCostText(0), Root: account, Run: run.ID, AsOf: run.AsOf,
		ParamsVersion: run.Version, Stale: run.ID == 0 || run.Stale(now)}
	if score != nil {
		v.StandingCents, v.Root = score.Cents, score.Root
		v.Band, v.VoteWeightPPM = p.Band(score.Cents).Name(), p.VoteWeight(score.Cents)
		v.Standing, v.FakeCost = StandingScore(score.Cents), FakeCostText(score.Cents)
	}
	return v
}

// Text is the quiet line the agent page shows: "Standing 2.4 (about $2.50
// to fake)".
func (v *StandingView) Text() string {
	return "Standing " + strconv.FormatFloat(math.Round(v.Standing*10)/10, 'f', 1, 64) + " (" + FakeCostText(v.StandingCents) + ")"
}

// CurrentStanding reads an account's standing in the latest done run; the
// view says standing 0 (and stale) before any run.
func CurrentStanding(ctx context.Context, q allowance.Querier, account string, p Params, now int64) (*StandingView, *Score, error) {
	run, _, err := Latest(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	score, ok, err := Current(ctx, q, account)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return NewStandingView(nil, account, run, p, now), nil, nil
	}
	return NewStandingView(&score, account, run, p, now), &score, nil
}
