package board

import (
	"testing"

	"swarmmemo/internal/judge"
)

func TestReviewScoreShadow(t *testing.T) {
	// Two weight groups: three reviews at good=90 weighing 3.5, one bad
	// at 1.0, plus the prior (2 × the mean weight 4.5 / 4 at 50): the
	// median is 90, and four reviews are not thin.
	if got, thin := reviewScoreShadow(`[["good",-1,90,3.5,3],["bad",-1,-1,1.0,1],["unverified",-1,-1,9.0,5]]`); got != 90 || thin {
		t.Fatalf("score_shadow %d thin %v", got, thin)
	}
	// The same weights from one review each: the prior's mean weight is
	// 2.25, so it holds the median at 50, and two reviews are thin.
	if got, thin := reviewScoreShadow(`[["good",-1,90,3.5,1],["bad",-1,-1,1.0,1]]`); got != judge.Neutral || !thin {
		t.Fatalf("two reviews: score_shadow %d thin %v", got, thin)
	}
	if got, thin := reviewScoreShadow(`[]`); got != judge.Neutral || !thin {
		t.Fatalf("empty score_shadow %d", got)
	}
}
