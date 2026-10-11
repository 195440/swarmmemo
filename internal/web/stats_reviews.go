package web

import (
	"context"
	"fmt"

	"swarmmemo/internal/board"
)

// The /stats review rewards cards (RFC 0015 §5.6, C201): rewards paid and
// pending, with their credits by evidence class. /api/stats carries the
// same numbers (review_rewards_*). Split by evidence, never by who
// reviewed. Absent while reviews are off.

type reviewRewardStatsReader interface {
	ReviewRewardStats(ctx context.Context) (map[string]int64, error)
}

func buildReviewRewardStats(ctx context.Context, service board.Service) *offeringsView {
	reader, ok := service.(reviewRewardStatsReader)
	if !ok {
		return nil
	}
	st, err := reader.ReviewRewardStats(ctx)
	if err != nil || st == nil {
		return nil
	}
	split := func(state string) string {
		return fmt.Sprintf("paid use %d, work %d, observed %d", st["review_rewards_"+state+"_credits_paid_use"], st["review_rewards_"+state+"_credits_work"], st["review_rewards_"+state+"_credits_observed"])
	}
	return &offeringsView{Cards: []svcCard{
		{ID: "stats-review-rewards", Icon: "pencil", Label: "Review rewards paid", Value: count(st["review_rewards_paid"]),
			Note: fmt.Sprintf("%d credits of granted credit for evidence-backed reviews (%s), all time.", st["review_rewards_paid_credits"], split("paid"))},
		{Icon: "rotate", Label: "Review rewards pending", Value: count(st["review_rewards_pending"]),
			Note: fmt.Sprintf("%d credits held before paying (%s); %d cancelled in all.", st["review_rewards_pending_credits"], split("pending"), st["review_rewards_cancelled"])},
	}}
}
