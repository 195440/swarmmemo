package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/reviews"
)

// fakeReviews summarises one subject, as the board's reviews index would.
type fakeReviews struct {
	asked []string
	id    string
}

func (f *fakeReviews) ReviewSummaries(_ context.Context, _ allowance.Querier, subjects []string) (map[string]any, error) {
	f.asked = subjects
	return map[string]any{f.id: map[string]any{"reviews": 3, "url": "/s/" + f.id}}, nil
}

// While REVIEWS is on, each resource names its subject (x402:METHOD URL,
// query dropped) and carries its reviews summary once reviewed; off, neither.
func TestX402ResourcesNameSubjects(t *testing.T) {
	price, _ := reviews.Canonical(reviews.TypeX402, "GET https://example.com/paid", reviews.Options{})
	search, _ := reviews.Canonical(reviews.TypeX402, "POST https://example.com/paid", reviews.Options{})
	fake := &fakeReviews{id: price.ID}
	h := newX402HarnessAlongside(t, &fakeX402{price: 2000}, testX402Config(t, `{}`, testResources), nil, Deps{Reviews: fake})
	got, err := h.engine.Read(context.Background(), h.db, Request{Service: "x402", Data: `{"schema":1,"method":"resources"}`, Subject: testSubject}, h.now)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	for _, want := range []string{`"subject_id":"` + price.ID + `"`, `"reviews":{"reviews":3,"url":"/s/` + price.ID + `"}`, `"subject_id":"` + search.ID + `"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("resources lacks %s: %s", want, raw)
		}
	}
	if len(fake.asked) != 2 {
		t.Fatalf("summaries asked for %v", fake.asked)
	}
	off := newX402Harness(t, &fakeX402{price: 2000}, `{}`)
	got, err = off.engine.Read(context.Background(), off.db, Request{Service: "x402", Data: `{"schema":1,"method":"resources"}`, Subject: testSubject}, off.now)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ = json.Marshal(got); strings.Contains(string(raw), "subject_id") {
		t.Fatal("subject_id while REVIEWS is off")
	}
}
