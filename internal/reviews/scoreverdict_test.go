package reviews

import (
	"testing"

	"swarmmemo/internal/judge"
)

// ScoreBands agree with judge.VerdictX (RFC 0020's one verdict map): each
// verdict's x reads back as that verdict, and every score reads as the
// verdict whose x is nearest, a tie going to the better verdict.
func TestScoreBandsAgreeWithJudge(t *testing.T) {
	if len(ScoreBands) != len(judge.VerdictX) {
		t.Fatalf("bands %v, judge verdicts %v", ScoreBands, judge.VerdictX)
	}
	for v, x := range judge.VerdictX {
		if got := ScoreVerdict(int(x)); got != v {
			t.Errorf("x %d (%s) reads as %s", x, v, got)
		}
	}
	for score := judge.Min; score <= judge.Max; score++ {
		best, bestDist := "", int64(1<<62)
		for _, v := range Verdicts { // best first, so a tie keeps the better one
			x, ok := judge.VerdictX[v]
			if !ok {
				continue
			}
			d := x - int64(score)
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				best, bestDist = v, d
			}
		}
		if got := ScoreVerdict(score); got != best {
			t.Errorf("score %d reads as %s, nearest judge verdict %s", score, got, best)
		}
	}
}
