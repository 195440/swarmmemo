package web

// A subject's reviews where another page shows the subject itself (C200):
// the agent page (/agent/FP, its agent:ACCOUNT subject) and an offering's
// page (/@HANDLE/NAME, its x402 subject). One block, the same review cards
// as /s/ID, read through the same subject.get, with the way to write one,
// the reward offer and the badge to copy.

import (
	"swarmmemo/internal/board"
	"swarmmemo/internal/reviews"
)

// reviewsBlockCards is how many review cards the block shows; the rest are
// on the subject's page.
const reviewsBlockCards = 3

// reviewsBlock is one subject's reviews as a block on another page.
type reviewsBlock struct {
	// Noun names the subject in the page's words: agent, offering.
	Noun string
	// URI and ID are the subject; URL its page once reviewed, else its
	// search page, where the first review is written (board.SubjectReviewURL).
	URI, ID, URL     string
	Exists           bool
	Counts, Verdicts string
	Lab              *board.ReviewCard
	Cards            []board.ReviewCard
	More             bool
	Reward           *board.ReviewRewardHint
	Embed            *badgeEmbed
}

// loadReviewsBlock reads sum's subject for a block; nil while reviews are
// off (sum nil) or when the read fails, so the page loses only the block.
func loadReviewsBlock(execute func(board.Command) (board.Result, error), sum *board.SubjectReviews, noun, badgeLink string) *reviewsBlock {
	if sum == nil || !reviews.ValidID(sum.ID) {
		return nil
	}
	res, err := execute(board.Command{Operation: "subject.get", Target: sum.Subject, Limit: reviewsBlockCards})
	if err != nil {
		return nil
	}
	b := &reviewsBlock{Noun: noun, URI: sum.Subject, ID: sum.ID, URL: sum.URL}
	info, _ := res.Data["subject"].(board.SubjectInfo)
	b.Exists = info.Exists
	b.URL = board.SubjectReviewURL(reviews.Subject{Type: info.Type, Key: info.Key, ID: info.ID}, info.Exists)
	agg, _ := res.Data["aggregate"].(board.ReviewAggregate)
	b.Counts, b.Verdicts = countsLine(agg)
	if lab, ok := res.Data["lab"].(board.ReviewCard); ok {
		b.Lab = &lab
	}
	b.Cards, _ = res.Data["reviews"].([]board.ReviewCard)
	b.More = agg.Reviews > len(b.Cards)
	b.Reward, _ = res.Data["review_reward"].(*board.ReviewRewardHint)
	if sum.Badge != "" {
		b.Embed = newBadgeEmbed(sum.Badge, badgeLink, sum.ID)
	}
	return b
}
