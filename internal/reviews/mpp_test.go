package reviews

import (
	"encoding/json"
	"strings"
	"testing"

	"swarmmemo/internal/rubric"
)

// An mpp subject is keyed like an x402 one (METHOD https://host/path, the
// same canonicalisation), so the two name one endpoint: siblings, with
// different ids, each with its own reviews.
func TestMPPSubjects(t *testing.T) {
	for _, c := range []struct{ in, key string }{
		{"mpp:get HTTPS://Openrouter.MPP.tempo.xyz:443/v1/./models?x=1#f", "GET https://openrouter.mpp.tempo.xyz/v1/models"},
		{"mpp:POST https://api.example.com/a/%7Euser/../b/", "POST https://api.example.com/a/b/"},
		{"mpp:POST https://swarmmemo.com/@Atlas/Translate", "POST https://swarmmemo.com/@atlas/translate"},
	} {
		s, err := Resolve(c.in, opt)
		if err != nil || s.Type != TypeMPP || s.Key != c.key {
			t.Fatalf("%s: %+v %v", c.in, s, err)
		}
		x, err := Canonical(TypeX402, strings.TrimPrefix(c.in, "mpp:"), opt)
		if err != nil || x.Key != s.Key || x.ID == s.ID {
			t.Fatalf("%s: the x402 sibling %+v (%v), mpp %+v", c.in, x, err, s)
		}
		sibs := s.Siblings()
		if len(sibs) != 1 || sibs[0] != x {
			t.Fatalf("%s: siblings %+v, want %+v", c.in, sibs, x)
		}
		if back := x.Siblings(); len(back) != 1 || back[0] != s {
			t.Fatalf("%s: x402's siblings %+v", c.in, back)
		}
		if p, ok := s.Parent(); !ok || p.Type != TypeDomain || s.Host() == "" {
			t.Fatalf("%s: parent %+v %v", c.in, p, ok)
		}
	}
	if s, _ := Canonical(TypeDomain, "example.com", opt); len(s.Siblings()) != 0 {
		t.Fatal("a domain has no siblings")
	}
	// The x402 rules hold for mpp: people, IPs and bad methods are refused.
	for in, code := range map[string]string{
		"mpp:GET https://x.com/someone":      "subject_refused",
		"mpp:GET https://10.0.0.1/v1":        "subject_refused",
		"mpp:FETCH https://api.example.com/": "invalid_subject",
		"mpp:GET http://api.example.com/":    "invalid_subject",
		"mpp:GET https://swarmmemo.com/@bob": "subject_refused",
	} {
		_, err := Resolve(in, opt)
		if e, ok := err.(*Error); !ok || e.Code != code {
			t.Errorf("%s: %v, want %s", in, err, code)
		}
	}
	// An offering is a subject under both types; OfferingPath reads either.
	for _, typ := range []string{TypeX402, TypeMPP} {
		s, err := Canonical(typ, "POST https://swarmmemo.com/@atlas/translate", opt)
		if alias, name, ok := OfferingPath(s, opt); err != nil || !ok || alias != "atlas" || name != "translate" {
			t.Fatalf("%s offering: %v %s %s %v", typ, err, alias, name, ok)
		}
	}
}

// The MPP rubric: the x402 rubric's format, dimensions, weights, gates and
// grades; conformance checks of its own; served and scored as one.
func TestMPPRubric(t *testing.T) {
	r := LookupRubric("mpp-endpoint", "")
	x := LookupRubric("x402-endpoint", "")
	if r == nil || x == nil || r.Version != "1.0.0-draft.1" || r.Path() != "/rubrics/mpp-endpoint/1.0.0-draft.1.json" || r.Doc.Title != "MPP endpoint" ||
		len(r.SubjectTypes) != 1 || r.SubjectTypes[0] != TypeMPP {
		t.Fatalf("rubric %+v", r)
	}
	var a, b map[string]json.RawMessage
	_ = json.Unmarshal(r.Raw, &a)
	_ = json.Unmarshal(x.Raw, &b)
	for _, k := range []string{"schema", "dimensions", "decay", "gates", "grades", "confidence", "verdict", "outlook", "watch", "monte_carlo", "value"} {
		if string(a[k]) != string(b[k]) {
			t.Errorf("%s differs from the x402 rubric's", k)
		}
	}
	sums, conformance := map[string]float64{}, 0.0
	xc := map[string]rubric.Check{}
	for _, c := range x.Checks {
		xc[c.ID] = c
	}
	for _, c := range r.Checks {
		sums[c.Dimension] += c.Weight
		if c.ID[0] == 'C' {
			conformance += c.Weight
			continue
		}
		if o := xc[c.ID]; o.Weight != c.Weight || o.Dimension != c.Dimension || o.Kind != c.Kind {
			t.Errorf("%s: %+v, x402 %+v", c.ID, c, o)
		}
	}
	for d, s := range sums {
		if s != 100 {
			t.Errorf("checks of %s weigh %v", d, s)
		}
	}
	if conformance != 36 || r.Check("C6").Kind != "paid" || r.Check("C7") != nil || !strings.Contains(r.CheckDoc("C5").Measure, "problem+json") ||
		!strings.Contains(r.CheckDoc("C1").Measure, "WWW-Authenticate: Payment") || !strings.Contains(r.CheckDoc("C6").Measure, "Payment-Receipt") {
		t.Fatalf("conformance %v, C checks %+v", conformance, r.Doc.Checks)
	}
	obs := `{"schema":"swarmmemo.observations/1","rubric":"mpp-endpoint","subject":{"type":"mpp","key":"GET https://api.example.com/v1/search"},"as_of":"2026-10-11T12:00:00Z","checks":{"R1":{"events":[{"at":"2026-10-10T12:00:00Z","pass":10,"fail":0}]},"C1":{"events":[{"at":"2026-10-10T12:00:00Z","pass":1,"fail":0}]},"C6":{"na":true,"reason":"no rail for tempo"}}}`
	out, err := rubric.ScoreFiles(r.Raw, []byte(obs))
	if err != nil || !strings.Contains(string(out), `"rubric":{"id":"mpp-endpoint","version":"1.0.0-draft.1"`) {
		t.Fatalf("score: %v %s", err, out)
	}
}
