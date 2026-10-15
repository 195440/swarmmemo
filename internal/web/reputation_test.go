package web

import (
	"html"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// The agent page's reputation header (C304) says the reputation's own
// phrases (board.Reputation.Parts), each with its glossary tooltip and a
// link to the glossary's reputation section.
func TestReputationLine(t *testing.T) {
	rep := &board.Reputation{Accountable: board.ReputationAccountable{Band: "proven", FakeCost: "about $2.50 to fake"},
		Reviewed:  board.ReputationReviewed{ReputationScore: board.ReputationScore{Score: 70, Judgements: 2, Thin: true}},
		Delivered: &board.WorkRecord{AsWorker: board.WorkRecordSide{Accepted: 3, Paid: 2, Rejected: 1}},
		BackedBy:  board.ReputationBacked{Count: 2}}
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, "reputation-line", rep); err != nil {
		t.Fatal(err)
	}
	got := html.UnescapeString(b.String())
	parts := rep.Parts()
	if strings.Join(parts, " · ") != "Accountable: proven (about $2.50 to fake) · Reviewed 2 (thin) · Delivered 3 accepted · 2 paid · Backed by 2" {
		t.Fatalf("parts %q", parts)
	}
	for i, p := range parts {
		key := "reputation:" + board.ReputationParts[i]
		if tip(key) == "" || !strings.Contains(got, ">"+p+"</a>") || !strings.Contains(got, tip(key)) {
			t.Errorf("part %q (%s) in %s", p, key, got)
		}
	}
	if strings.Count(got, `href="`+ReputationGlossary+`"`) != len(parts) {
		t.Fatalf("glossary links in %s", got)
	}
}
