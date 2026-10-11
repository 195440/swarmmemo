package pay

import (
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

var lowerHexRE = regexp.MustCompile(`^0x[0-9a-f]+$`)

// FuzzMPPParse: the MPP credential parser never panics, and what it
// accepts is well formed and bounded; Bound never panics on it and never
// binds it to a challenge whose nonce it does not carry.
func FuzzMPPParse(f *testing.F) {
	w := testMPP(f)
	a := testAsk()
	good, _ := testCredential(f, w, a, nil)
	tok := strings.TrimPrefix(good, "Payment ")
	raw, _ := base64.RawURLEncoding.DecodeString(tok)
	f.Add(tok)
	f.Add(string(raw))
	f.Add("")
	f.Add("e30")
	f.Add(b64j(`{"challenge":{},"payload":{}}`))
	f.Add(b64j(`{"challenge":{"id":"a","realm":"r","method":"evm","intent":"charge","request":"e30","opaque":"e30"},"payload":{"type":"authorization"}}`))
	f.Fuzz(func(t *testing.T, s string) {
		x, err := w.Parse(s, "Authorization")
		if err != nil {
			if code(err) != CodeInvalid {
				t.Fatalf("refusal %v", err)
			}
			return
		}
		if len(s) > MPPCredentialBytes || x.Wire != WireMPP || x.Value <= 0 || x.From == (EVMAddress{}) || len(x.Nonce) != 66 || len(x.Signature) != 132 ||
			!lowerHexRE.MatchString(x.Nonce) || !lowerHexRE.MatchString(x.Signature) || x.ValidAfter < 0 || x.ValidBefore < 0 ||
			!challengeIDRE.MatchString(x.Ref) || (x.Quote != "" && !quoteRE.MatchString(x.Quote)) {
			t.Fatalf("accepted a malformed credential: %+v", x)
		}
		if w.Bound(x, a, testNow) == nil && x.Nonce != challengeNonceHex(x.Ref, testRealm) {
			t.Fatal("bound without the challenge nonce")
		}
	})
}

// FuzzParseMPPChallenges: the WWW-Authenticate parser never panics and
// returns only complete Payment challenges.
func FuzzParseMPPChallenges(f *testing.F) {
	h := http.Header{}
	_, _ = testMPP(f).Challenge(testAsk(), h)
	f.Add(h.Get("WWW-Authenticate"))
	f.Add(`Bearer realm="a", Payment id="x", realm="r", method="evm", intent="charge", request="e30"`)
	f.Add(`Payment id="a\"b", realm=r`)
	f.Add(`,,, Payment ,`)
	f.Fuzz(func(t *testing.T, v string) {
		cs, err := ParseMPPChallenges([]string{v})
		if err != nil {
			return
		}
		for _, c := range cs {
			for _, k := range []string{"id", "realm", "method", "intent", "request"} {
				if c.Params[k] == "" {
					t.Fatalf("challenge without %s: %q", k, v)
				}
			}
		}
	})
}

// FuzzX402WireParse: the moved x402 parser, through the wire.
func FuzzX402WireParse(f *testing.F) {
	w := NewX402(testRail)
	a := testAsk()
	f.Add(testX402Payment(a, "0x"+strings.Repeat("ab", 32), nil))
	f.Add("")
	f.Add("eyJ4NDAyVmVyc2lvbiI6Mn0=")
	f.Fuzz(func(t *testing.T, s string) {
		x, err := w.Parse(s, "Payment-Signature")
		if err != nil {
			if code(err) != CodeInvalid {
				t.Fatalf("refusal %v", err)
			}
			return
		}
		if len(s) > X402PaymentBytes || x.Value <= 0 || x.From == (EVMAddress{}) || !lowerHexRE.MatchString(x.Nonce) || len(x.Nonce) != 66 ||
			!quoteRE.MatchString(x.Quote) {
			t.Fatalf("accepted a malformed payment: %+v", x)
		}
		_ = w.Bound(x, a, testNow)
	})
}
