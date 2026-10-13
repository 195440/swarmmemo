package web

import (
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// C255: reviews of an earlier version of an offering are listed under their
// own heading, labelled, and left out of the counts line.
func TestSubjectPageEarlierVersion(t *testing.T) {
	old := board.ReviewCard{ID: "ev1", Root: "ev1", Thread: "/e/ev1", Text: "Validated my schema.", Author: board.ReviewAuthor{Account: "acct", Handle: "first"}}
	old.Verdict, old.Relationship, old.Version, old.EarlierVersion, old.VersionNote = "good", "none", 1, true, board.EarlierVersionLabel
	agg := board.ReviewAggregate{EarlierVersion: 1}
	v := &subjectPage{Info: board.SubjectInfo{Version: 2}, Earlier: []board.ReviewCard{old}, Aggregate: agg}
	v.Info.ID, v.Info.URI, v.Info.URL, v.Info.Type, v.Info.Key = "x402-abc", "x402:POST https://swarmmemo.com/@tuancodes/conv", "/s/x402-abc", "x402", "POST https://swarmmemo.com/@tuancodes/conv"
	v.Counts, v.Verdicts = countsLine(agg)
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, "subject", &page{SubjectView: v}); err != nil {
		t.Fatal(err)
	}
	body := b.String()
	for _, want := range []string{"No reviews of this version yet. 1 more review is about an earlier version and not counted.", "<h2>About an earlier version</h2>", "it is at version 2",
		`<span class="badge review-version"`, ">about an earlier version</span>", "Validated my schema."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if counts, _ := countsLine(board.ReviewAggregate{Reviews: 2, Accounts: 2, EarlierVersion: 3}); !strings.HasSuffix(counts, " 3 more reviews are about an earlier version and not counted.") {
		t.Errorf("counts line %q", counts)
	}
}
