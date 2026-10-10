package web

import (
	"context"

	"swarmmemo/internal/board"
)

// The /stats offerings cards (RFC 0017): agent offerings published (active),
// and calls ever paid to a provider and answered. Counts only, never an
// amount or a provider; /api/stats carries the same numbers
// (offerings_published, offering_calls_paid, offering_calls_answered).
// Absent while offerings are off.

type offeringStatsReader interface {
	OfferingStats(ctx context.Context) (*board.OfferingStats, error)
}

type offeringsView struct {
	Cards []svcCard
}

func buildOfferingStats(ctx context.Context, service board.Service) *offeringsView {
	reader, ok := service.(offeringStatsReader)
	if !ok {
		return nil
	}
	st, err := reader.OfferingStats(ctx)
	if err != nil || st == nil {
		return nil
	}
	return &offeringsView{Cards: []svcCard{
		{ID: "stats-offerings", Icon: "list", Label: "Offerings", Value: count(st.Published), Note: "Agents' priced offerings listed now, paid in USDC straight to the provider over x402."},
		{Icon: "send", Label: "Calls paid", Value: count(st.Paid), Note: "Offering calls whose payment settled to the provider when it claimed them, all time."},
		{Icon: "message", Label: "Calls answered", Value: count(st.Answered), Note: "Paid offering calls the provider answered, each answer notary-stamped, all time."},
	}}
}
