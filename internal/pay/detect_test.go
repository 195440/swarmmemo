package pay

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestDetectCredential(t *testing.T) {
	x402v := testX402Payment(testAsk(), "0x"+strings.Repeat("ab", 32), nil)
	for name, c := range map[string]struct {
		h          http.Header
		wire, raw  string
		field      string
		many, none bool
	}{
		"x402":            {h: http.Header{"Payment-Signature": {x402v}}, wire: WireX402, raw: x402v, field: "Payment-Signature"},
		"x402 v1":         {h: http.Header{"X-Payment": {x402v}}, wire: WireX402, raw: x402v, field: "X-Payment"},
		"mpp":             {h: http.Header{"Authorization": {"Payment abc_-1"}}, wire: WireMPP, raw: "abc_-1", field: "Authorization"},
		"mpp other field": {h: http.Header{"Payment-Authorization": {"payment abc"}}, wire: WireMPP, raw: "abc", field: "Payment-Authorization"},
		"bearer":          {h: http.Header{"Authorization": {"Bearer tok"}}, none: true},
		"none":            {h: http.Header{}, none: true},
		"both wires":      {h: http.Header{"Payment-Signature": {x402v}, "Authorization": {"Payment abc"}}, many: true},
		"two mpp":         {h: http.Header{"Authorization": {"Payment abc"}, "Payment-Authorization": {"Payment abc"}}, many: true},
		"two x402":        {h: http.Header{"Payment-Signature": {x402v}, "X-Payment": {x402v}}, many: true},
	} {
		w, raw, field, err := DetectCredential(c.h)
		switch {
		case c.many:
			if !errors.Is(err, ErrManyCredentials) {
				t.Errorf("%s: %v", name, err)
			}
		case c.none:
			if !errors.Is(err, ErrNoCredential) {
				t.Errorf("%s: %v", name, err)
			}
		case err != nil || w != c.wire || raw != c.raw || field != c.field:
			t.Errorf("%s: %q %q %q %v", name, w, raw, field, err)
		}
	}
	if _, _, _, err := DetectCredential(http.Header{"Payment-Authorization": {"Bearer x"}}); code(err) != CodeInvalid {
		t.Fatal("Payment-Authorization of another scheme")
	}
	if tok, ok := MPPToken("Payment abc"); !ok || tok != "abc" {
		t.Fatal("MPPToken")
	}
	for _, v := range []string{x402v, "Payment ", "Bearer abc", ""} {
		if _, ok := MPPToken(v); ok {
			t.Fatalf("MPPToken(%q)", v)
		}
	}
	if p := MPPProblem(CodeMismatch, "d"); p.Status != 402 || p.Body["type"] != ProblemBase+"verification-failed" || p.Body["detail"] != "d" {
		t.Fatalf("%+v", p)
	}
	if p := MPPProblem("topup_amount", ""); p.Status != 0 {
		t.Fatal("unmapped code")
	}
}
