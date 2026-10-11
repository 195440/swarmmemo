package reviews

import (
	"net/url"
	"strings"
	"testing"
)

// FuzzResolve: canonicalisation never panics, and a canonical subject is a
// fixed point with a well-formed id, a printable ASCII key within its cap and
// no query, fragment or credentials.
func FuzzResolve(f *testing.F) {
	for _, seed := range []string{
		"x402:GET https://api.example.com/v1/search?q=1", "post https://a.example.com:8443/%7e/x/../y/", "https://Shop.example.co.uk/x",
		"domain:a.b.example.com", "mcp:io.github.o/s", "mcp:https://m.example.com/sse", "pkg:npm/@s/n@1", "pkg:pypi/A_b.C",
		strings.Repeat("a", 64), "mailto:x@y.z", "x402:GET https://u:p@x.example/", "site:https://[::1]/", "domain:x.local",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		s, err := Resolve(raw, opt)
		if err != nil {
			if _, ok := err.(*Error); !ok {
				t.Fatalf("%q: untyped error %v", raw, err)
			}
			return
		}
		if !ValidID(s.ID) || s.ID != SubjectID(s.Type, s.Key) || len(s.Key) > KeyMaxBytes || !printableASCIIOrSpace(s.Key) {
			t.Fatalf("%q: bad subject %+v", raw, s)
		}
		link := s.Key
		if s.Type == TypeX402 {
			_, link, _ = strings.Cut(s.Key, " ")
		}
		if u, err := url.Parse(link); strings.ContainsAny(s.Key, "?#") || (strings.HasPrefix(link, "https://") && (err != nil || u.User != nil || u.Hostname() != s.Host())) {
			t.Fatalf("%q: key keeps a query, fragment or credentials: %q", raw, s.Key)
		}
		again, err := Resolve(s.URI(), opt)
		if err != nil || again != s {
			t.Fatalf("%q: %q is not a fixed point: %+v %v", raw, s.URI(), again, err)
		}
	})
}

// FuzzParseReview: data.review parsing never panics, and whatever it accepts
// is within every cap.
func FuzzParseReview(f *testing.F) {
	r := LookupRubric("x402-endpoint", "")
	for _, seed := range []string{
		`{"subject":"x402:GET https://api.example.com/v1/search","verdict":"good"}`,
		`{"subject":"x402:GET https://api.example.com/v1/search","verdict":"good","relationship":"lab","rubric":{"id":"x402-endpoint","version":"` + r.Version + `","sha256":"` + r.SHA256 + `"},"score":84,"band":[78,89],"grade":"B","flags":["injection"],"evidence":["call:3f2a9c0e5b7d41a2a8e6c1f09d3b7e54"],"observations":[{"check":"R4","pass":3,"fail":0,"evidence":[0]},{"check":"R3","value":420},{"check":"H4","na":true}]}`,
		`{"subject":"domain:example.com","verdict":"bad","dimensions":{"works":0,"safe":100},"relationship":"outside_source","evidence":["stamp:` + strings.Repeat("a", 64) + `"],"source":{"name":"S","url":"https://s.example/","score":7.5}}`,
		`{"subject":"x","verdict":null}`, `{"a":1,"a":2}`, `[1]`, `{"subject":"agent:` + strings.Repeat("0", 64) + `","verdict":"good","record":{"sha256":"` + strings.Repeat("1", 64) + `"}}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		v, err := Parse(raw, opt)
		if err != nil {
			return
		}
		if len(v.Evidence) > MaxEvidence || len(v.Observations) > MaxObservations || len(v.Flags) > MaxFlags {
			t.Fatalf("caps exceeded: %+v", v)
		}
		if v.Score != nil && (*v.Score < 0 || *v.Score > 100) || v.Relationship != "lab" && (v.Score != nil || v.Band != nil || v.Grade != "" || v.Flags != nil) {
			t.Fatalf("lab fields: %+v", v)
		}
		if (v.Source != nil) != (v.Relationship == "outside_source") {
			t.Fatalf("source: %+v", v)
		}
		for _, o := range v.Observations {
			for _, c := range o.Cites {
				if c < 0 || c >= len(v.Evidence) {
					t.Fatalf("cite %d of %d", c, len(v.Evidence))
				}
			}
		}
		if again, err := Resolve(v.Subject.URI(), opt); err != nil || again != v.Subject {
			t.Fatalf("subject %+v not canonical", v.Subject)
		}
	})
}
