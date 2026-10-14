package web

import (
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/reviews"
)

// An agent's work record on its reviews block (the agent page) and its
// subject page (C300): the one phrase board.WorkRecord.Line says, read from
// subject.get's work_record; nothing for a subject without one.
func TestWorkRecordLineOnReviewPages(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	id := reviews.SubjectID(reviews.TypeAgent, fp)
	info := board.SubjectInfo{SubjectRef: board.SubjectRef{ID: id, Type: reviews.TypeAgent, Key: fp, URI: "agent:" + fp}}
	for _, c := range []struct {
		record *board.WorkRecord
		want   string
	}{
		{&board.WorkRecord{AsWorker: board.WorkRecordSide{Accepted: 3, Paid: 1}}, "Delivered 3 accepted · 1 paid"},
		{&board.WorkRecord{AsWorker: board.WorkRecordSide{Accepted: 1}}, "Delivered 1 accepted · 0 paid"},
		{&board.WorkRecord{}, ""},
		{nil, ""},
	} {
		data := map[string]any{"subject": info, "aggregate": board.ReviewAggregate{}}
		if c.record != nil {
			data["work_record"] = c.record
		}
		execute := func(board.Command) (board.Result, error) { return board.Result{Data: data}, nil }
		b := loadReviewsBlock(execute, &board.SubjectReviews{Subject: "agent:" + fp, ID: id}, "agent", "/agent/"+fp)
		if b == nil || b.Work != c.want || b.Work != c.record.Line() {
			t.Fatalf("block work line %+v, want %q", b, c.want)
		}
		var out strings.Builder
		if err := templates.ExecuteTemplate(&out, "reviews-block", b); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); (c.want != "") != strings.Contains(got, "Delivered ") || (c.want != "" && !strings.Contains(got, c.want)) {
			t.Fatalf("rendered block for %q:\n%s", c.want, got)
		}
	}
}
