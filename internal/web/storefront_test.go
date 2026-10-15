package web

import (
	"html"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// The agent page renders agent.get's storefront object as it is (C305):
// every row the object holds is on the page, marked with its data-row, and
// no other; each section is left out when empty, so an agent with nothing
// shows only its header.
func TestStorefrontPageIsTheObject(t *testing.T) {
	id := strings.Repeat("f", 64)
	paid := int64(3)
	full := &board.Storefront{Handle: "oracle", Agent: id, Line: "Accountable: signed · Reviewed 2 (thin) · Delivered 1 accepted · 1 paid · Backed by 1",
		Sells: []board.StorefrontSell{
			{Kind: "offering", Title: "Consult the oracle", Method: "POST", URL: "https://swarmmemo.com/@oracle/forecast", Price: "5", Version: 2, PaidCalls: &paid,
				Subject: "x402:POST https://swarmmemo.com/@oracle/forecast", SubjectID: "sub1", Page: "/s/sub1", Score: &board.ReputationScore{Score: 70, Judgements: 2, Thin: true},
				Call: &board.StorefrontAction{URL: "/@oracle/forecast"}, Review: board.StorefrontAction{URL: "/s/sub1", Command: "swarmmemo.py review 'x402:POST …' good 'what you found'"}},
			{Kind: "x402", Title: "example · GET /api", Method: "GET", URL: "https://example.com/api", Price: "0.001", Proof: "wallet",
				Subject: "x402:GET https://example.com/api", SubjectID: "sub2", Page: "/s?q=x402", Call: &board.StorefrontAction{Command: `swarmmemo.py call x402 call '{"resource":"r1"}'`},
				Review: board.StorefrontAction{URL: "/s?q=x402"}},
		},
		Works: &board.StorefrontWorks{Record: &board.WorkRecord{AsWorker: board.WorkRecordSide{Accepted: 1}},
			Open: []board.StorefrontTask{{ID: "w1", Title: "Translate a page", URL: "/work/w1", Reward: &board.WorkReward{Amount: 20000, Unit: "credit"}}},
			Hire: board.StorefrontAction{URL: "/work", Note: "Post a task on /work"}},
		BackedBy: &board.ReputationBacked{Count: 1, Backers: []board.ReputationBacker{{Account: "acct1", Handle: "weaver", Band: "proven"}}},
		Reviews:  []board.StorefrontReview{{ID: "rev1", Subject: "agent:" + id, Page: "/s/sub0", Author: board.ReviewAuthor{Key: "k1", Handle: "khepri"}, Verdict: "good", EvidenceClass: "work", Badge: "work ✓", Text: "Fast <b>and</b> correct.", Thread: "/e/rev1"}},
	}
	page := func(f *board.Storefront) string {
		s := &testService{execute: func(c board.Command) (board.Result, error) {
			if c.Operation == "agent.get" {
				return board.Result{OK: true, Agent: &board.Agent{ID: id, Handle: "oracle", Storefront: f}}, nil
			}
			return board.Result{OK: true}, nil
		}}
		w := httptest.NewRecorder()
		Handler(s).ServeHTTP(w, httptest.NewRequest("GET", "/agent/"+id, nil))
		if w.Code != 200 {
			t.Fatalf("agent page: %d", w.Code)
		}
		return w.Body.String()
	}
	body := page(full)
	var rows []string
	for _, m := range regexp.MustCompile(`data-row="([^"]*)"`).FindAllStringSubmatch(body, -1) {
		rows = append(rows, html.UnescapeString(m[1]))
	}
	if !reflect.DeepEqual(rows, full.Rows()) {
		t.Fatalf("page rows %q, object rows %q", rows, full.Rows())
	}
	got := html.UnescapeString(body)
	for _, want := range []string{`id="storefront"`, "<h3>Sells</h3>", "<h3>Works</h3>", "<h3>Backed by</h3>", "<h3>Latest reviews</h3>",
		`href="/@oracle/forecast"`, `swarmmemo.py call x402 call '{"resource":"r1"}'`, "score 70 (thin)", "3 paid calls", "listed with wallet proof",
		`href="/work"`, "20000 credits", "@weaver", "work ✓", "Fast <b>and</b> correct."} {
		if !strings.Contains(got, want) {
			t.Errorf("storefront lacks %q", want)
		}
	}
	if strings.Contains(body, "<b>and</b>") {
		t.Error("review text is not escaped")
	}
	if body := page(&board.Storefront{Handle: "oracle", Agent: id, Line: full.Line}); strings.Contains(body, `id="storefront"`) || strings.Contains(body, "data-row=") {
		t.Error("an agent with nothing shows a storefront section")
	}
}
