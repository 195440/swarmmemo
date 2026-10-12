package board

import (
	"testing"

	"swarmmemo/internal/find"
	"swarmmemo/internal/reviews"
	"swarmmemo/internal/services"
)

// mpp: and x402: name one endpoint: each subject keeps its reviews, the
// subject page shows its sibling, and Find shows the endpoint once, with
// the sibling's reviews beside its own.
func TestMPPSubjectSiblings(t *testing.T) {
	s := reviewsStore(t)
	alice, bob := keyFor(97), keyFor(98)
	register(t, s, alice)
	register(t, s, bob)
	const endpoint = "GET https://weatherly.example.com/v1/forecast"
	x402URI, mppURI := "x402:"+endpoint, "mpp:"+endpoint
	run(t, s, reviewPost(t, alice, x402URI, map[string]any{}, ""))
	run(t, s, reviewPost(t, bob, x402URI, map[string]any{}, ""))
	run(t, s, reviewPost(t, alice, mppURI, map[string]any{"verdict": "bad"}, ""))
	run(t, s, reviewPost(t, bob, mppURI, map[string]any{"verdict": "bad"}, ""))
	x, m := reviewSubjectOf(t, x402URI), reviewSubjectOf(t, mppURI)
	if x.Key != m.Key || x.ID == m.ID || m.Type != reviews.TypeMPP {
		t.Fatalf("subjects %+v %+v", x, m)
	}
	// The page of each shows the other, with its own reviews' summary.
	for _, c := range []struct {
		uri  string
		want reviews.Subject
	}{{x402URI, m}, {mppURI, x}} {
		info := readSubjectData(t, s, c.uri)["subject"].(SubjectInfo)
		if len(info.Siblings) != 1 || info.Siblings[0].ID != c.want.ID || info.Siblings[0].Type != c.want.Type || info.Siblings[0].Reviews == nil || info.Siblings[0].Reviews.Reviews != 2 {
			t.Fatalf("%s siblings %+v", c.uri, info.Siblings)
		}
	}
	if agg := readSubjectData(t, s, mppURI)["aggregate"].(ReviewAggregate); agg.Reviews != 2 || agg.ByVerdict["bad"] != 2 {
		t.Fatalf("mpp reviews are its own: %+v", agg)
	}
	// Find: one result for the endpoint, its sibling beside it.
	hits := run(t, s, Command{Operation: "find", Query: "weatherly"}).Data["results"].([]find.Hit)
	if len(hits) != 1 {
		t.Fatalf("one endpoint, one result: %+v", hits)
	}
	h := hits[0]
	if h.Kind != find.KindX402 || h.SubjectID != x.ID || len(h.Also) != 1 || h.Also[0].SubjectID != m.ID || h.Also[0].Type != "mpp" ||
		h.Also[0].Reviews == nil || h.Also[0].Reviews.Count != 2 || h.Also[0].Reviews.Word == "" {
		t.Fatalf("hit %+v also %+v", h, h.Also)
	}
	// Only the mpp subject reviewed: the result is the mpp one.
	other := "mpp:POST https://cards.example.net/v1/issue"
	run(t, s, reviewPost(t, alice, other, map[string]any{}, ""))
	run(t, s, reviewPost(t, bob, other, map[string]any{}, ""))
	hits = run(t, s, Command{Operation: "find", Query: "cards", Data: `{"schema":1,"protocol":"any"}`}).Data["results"].([]find.Hit)
	if len(hits) != 1 || hits[0].Kind != find.KindMPP || hits[0].SubjectID != reviewSubjectOf(t, other).ID || len(hits[0].Also) != 0 {
		t.Fatalf("mpp only: %+v", hits)
	}
	// subjects.list filters by the new type.
	list := run(t, s, Command{Operation: "subjects.list", Kind: "mpp"}).Data["subjects"].([]SubjectListing)
	if len(list) != 2 {
		t.Fatalf("mpp subjects: %+v", list)
	}
	fails(t, s, Command{Operation: "find", Data: `{"schema":1,"protocol":"tempo"}`}, "invalid_find")
	if c := s.FindCapabilities(); len(c["protocols"].([]string)) != 2 {
		t.Fatalf("capabilities protocols: %v", c["protocols"])
	}
}

// An MPP listing as a Find doc (C219): its service-and-path name, its line
// while no summary is served, the served summary once there is one, and
// its price text; the id stays the listing's.
func TestFindMPPDoc(t *testing.T) {
	l := services.X402Listing{ID: "api-openfunnel-dev-bulk-0123456789abcdef", Kind: services.KindMPP, Method: "POST", URL: "https://api.openfunnel.dev/v1/bulk",
		MaxPrice: -1, Decimals: 6, Name: "openfunnel · POST /v1/bulk", Line: "MPP charge endpoint of openfunnel, paid in EURC", PriceText: "0.5 EURC a call"}
	d := findX402Doc(l, subjectQuality{})
	if d.ID != l.ID || d.Name != l.Name || d.Description != l.Line || d.PriceText != l.PriceText || d.Price != find.Unpriced {
		t.Fatalf("doc %+v", d)
	}
	l.Summary = "Bulk company lookup"
	if d = findX402Doc(l, subjectQuality{}); d.Description != "Bulk company lookup" {
		t.Fatalf("summary first: %q", d.Description)
	}
	x := services.X402Listing{ID: "exa-search", Kind: services.KindX402, MaxPrice: -1, Name: "ignored", Line: "ignored"}
	if d = findX402Doc(x, subjectQuality{}); d.Name != "exa search" || d.Description != "" || d.PriceText != "" {
		t.Fatalf("x402 doc changed: %+v", d)
	}
}
