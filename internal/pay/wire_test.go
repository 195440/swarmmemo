package pay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// testX402Payment is an x402 v2 exact payment for a on the test rail, as a
// client writes it in PAYMENT-SIGNATURE.
func testX402Payment(a Ask, nonce string, mutate func(m map[string]any)) string {
	accepted, _ := json.Marshal(testRail.X402Accept(X402Requirement{PayTo: a.PayTo, Amount: a.Amount, Quote: a.Quote, Expires: a.Expires, MaxTimeout: a.MaxTimeout}))
	var acc map[string]any
	_ = json.Unmarshal(accepted, &acc)
	m := map[string]any{"x402Version": 2, "accepted": acc, "payload": map[string]any{
		"signature": testSignature,
		"authorization": map[string]any{"from": testFrom.String(), "to": a.PayTo.String(), "value": strconv.FormatInt(a.Amount, 10),
			"validAfter": "0", "validBefore": strconv.FormatInt(a.Expires, 10), "nonce": nonce}}}
	if mutate != nil {
		mutate(m)
	}
	raw, _ := json.Marshal(m)
	return base64.StdEncoding.EncodeToString(raw)
}

func TestX402Wire(t *testing.T) {
	w := NewX402(testRail)
	a := testAsk()
	a.Extensions = map[string]any{"bazaar": map[string]any{"discoverable": true}}
	h := http.Header{}
	body, err := w.Challenge(a, h)
	if err != nil {
		t.Fatal(err)
	}
	wantBody, wantHeader := testRail.X402PaymentRequired(X402Requirement{PayTo: a.PayTo, Amount: a.Amount, Quote: a.Quote, Expires: a.Expires, MaxTimeout: a.MaxTimeout},
		a.Resource, a.Description, a.Extensions)
	gotRaw, _ := json.Marshal(body)
	wantRaw, _ := json.Marshal(wantBody)
	if string(gotRaw) != string(wantRaw) || h.Get("PAYMENT-REQUIRED") != wantHeader || len(h) != 1 {
		t.Fatalf("x402 challenge differs from X402PaymentRequired: %s %v", gotRaw, h)
	}
	v := testX402Payment(a, "0x"+strings.Repeat("ab", 32), nil)
	for _, name := range []string{"PAYMENT-SIGNATURE", "X-PAYMENT"} {
		x, err := parseHeader(t, w, v, name)
		if err != nil || x.Wire != WireX402 || x.Quote != testQuote || x.Ref != testQuote || x.Field != http.CanonicalHeaderKey(name) {
			t.Fatalf("%s: %+v %v", name, x, err)
		}
		if err := w.Bound(x, a, testNow); err != nil {
			t.Fatal(err)
		}
		// Payment() is exactly what ParseX402Payment read.
		p, _ := ParseX402Payment(v)
		pj, _ := json.Marshal(p)
		xj, _ := json.Marshal(x.Payment())
		if string(pj) != string(xj) {
			t.Fatal("x402 Payment() is not the parsed payment")
		}
	}
	// paymentHeader's rules: one value, bounded.
	if _, _, err := w.Credential(http.Header{"Payment-Signature": {v}, "X-Payment": {v}}); !errors.Is(err, ErrManyCredentials) {
		t.Fatal("two x402 payments")
	}
	if _, _, err := w.Credential(http.Header{"Payment-Signature": {strings.Repeat("A", X402PaymentBytes+1)}}); code(err) != CodeInvalid {
		t.Fatal("oversized x402 payment")
	}
	if _, _, err := w.Credential(http.Header{"Authorization": {"Payment abc"}}); !errors.Is(err, ErrNoCredential) {
		t.Fatal("x402 read an MPP credential")
	}
	// Bound is CheckX402: the same refusals.
	for name, c := range map[string]struct {
		a    Ask
		now  int64
		want string
	}{
		"other amount": {func() Ask { b := a; b.Amount++; return b }(), testNow, CodeMismatch},
		"other payTo":  {func() Ask { b := a; b.PayTo = testOther; return b }(), testNow, CodeMismatch},
		"other quote":  {func() Ask { b := a; b.Quote = "x"; return b }(), testNow, CodeMismatch},
		"expired":      {a, testExpires + 1, CodeExpired},
	} {
		x, _ := w.Parse(v, "Payment-Signature")
		if got := code(w.Bound(x, c.a, c.now)); got != c.want {
			t.Errorf("%s: %q", name, got)
		}
	}
	// Receipt: the SettlementResponse board.paymentResponse writes.
	rh := http.Header{}
	tx := "0x" + strings.Repeat("ab", 32)
	if err := w.Receipt(Settlement{Transaction: tx, Payer: testFrom, Ref: testQuote, At: 1}, rh); err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(rh.Get("PAYMENT-RESPONSE"))
	if string(raw) != `{"network":"eip155:8453","payer":"`+testFrom.String()+`","success":true,"transaction":"`+tx+`"}` {
		t.Fatalf("x402 receipt %s", raw)
	}
}

// An x402 credential cannot satisfy an MPP challenge, and an MPP credential
// cannot satisfy an x402 challenge: not by header, not by parse, not by
// binding, and not by moving the signed authorization across.
func TestCrossWire(t *testing.T) {
	x402w, mppw := NewX402(testRail), Wire(testMPP(t))
	a := testAsk()
	x402v := testX402Payment(a, "0x"+strings.Repeat("ab", 32), nil)
	mppv, mppField := testCredential(t, mppw, a, nil)

	// Headers: each wire reads only its own.
	if _, _, err := mppw.Credential(http.Header{"Payment-Signature": {x402v}}); !errors.Is(err, ErrNoCredential) {
		t.Fatal("MPP read PAYMENT-SIGNATURE")
	}
	if _, _, err := x402w.Credential(http.Header{"Authorization": {mppv}}); !errors.Is(err, ErrNoCredential) {
		t.Fatal("x402 read Authorization")
	}
	wire, _, _, err := Detect(http.Header{"Payment-Signature": {x402v}}, x402w, mppw)
	if err != nil || wire.Name() != WireX402 {
		t.Fatal("detect x402")
	}
	wire, _, _, err = Detect(http.Header{"Authorization": {mppv}}, x402w, mppw)
	if err != nil || wire.Name() != WireMPP {
		t.Fatal("detect MPP")
	}
	if _, _, _, err = Detect(http.Header{"Authorization": {mppv}, "Payment-Signature": {x402v}}, x402w, mppw); !errors.Is(err, ErrManyCredentials) {
		t.Fatal("credentials of both wires must be refused")
	}
	if _, _, _, err = Detect(http.Header{"Authorization": {"Bearer t"}}, x402w, mppw); !errors.Is(err, ErrNoCredential) {
		t.Fatal("a bearer token is not a payment")
	}

	// Parse: each wire refuses the other's credential, wherever it is put.
	if _, err := mppw.Parse(x402v, "Authorization"); code(err) != CodeInvalid {
		t.Fatalf("MPP parsed an x402 payment: %v", err)
	}
	if _, err := mppw.Parse(strings.TrimRight(base64.RawURLEncoding.EncodeToString(mustB64(t, x402v)), "="), "Authorization"); code(err) != CodeInvalid {
		t.Fatalf("MPP parsed an x402 payment re-encoded base64url: %v", err)
	}
	if _, err := x402w.Parse(strings.TrimPrefix(mppv, "Payment "), "Payment-Signature"); code(err) != CodeInvalid {
		t.Fatalf("x402 parsed an MPP credential: %v", err)
	}

	// Bound: an Auth of one wire never binds on the other.
	xa, _ := x402w.Parse(x402v, "Payment-Signature")
	ma, _ := parseHeader(t, mppw, mppv, mppField)
	if code(mppw.Bound(xa, a, testNow)) != CodeInvalid || code(x402w.Bound(ma, a, testNow)) != CodeInvalid {
		t.Fatal("an Auth bound on the other wire")
	}

	// Moving the authorization: an x402-signed authorization (random
	// nonce) wrapped in an MPP credential for a valid challenge fails the
	// nonce rule.
	moved, field := testCredential(t, mppw, a, func(au *X402AuthorizationWire, _ map[string]string) { au.Nonce = "0x" + strings.Repeat("ab", 32) })
	mx, err := parseHeader(t, mppw, moved, field)
	if err != nil || code(mppw.Bound(mx, a, testNow)) != CodeMismatch {
		t.Fatalf("x402 authorization accepted as MPP: %v", err)
	}
}

func mustB64(t *testing.T, s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOffer(t *testing.T) {
	wires := []Wire{NewX402(testRail), testMPP(t)}
	h := http.Header{"Www-Authenticate": {`Bearer realm="api"`}, "Payment-Required": {"stale"}}
	a := testAsk()
	o, err := Offer(a, h, wires...)
	if err != nil {
		t.Fatal(err)
	}
	if o.Body()["x402Version"] != 2 || o.Bodies[WireMPP]["type"] != ProblemBase+"payment-required" {
		t.Fatalf("bodies %v", o.Bodies)
	}
	if v := h.Values("PAYMENT-REQUIRED"); len(v) != 1 || v[0] == "stale" {
		t.Fatalf("PAYMENT-REQUIRED %v", v)
	}
	if v := h.Values("WWW-Authenticate"); len(v) != 2 || v[0] != `Bearer realm="api"` || !strings.HasPrefix(v[1], "Payment id=") {
		t.Fatalf("WWW-Authenticate %v", v)
	}
	if h.Get("Cache-Control") != "no-store" {
		t.Fatal("no-store")
	}
	// Both challenges price the same thing.
	cs, _ := ParseMPPChallenges(h.Values("WWW-Authenticate"))
	req, _ := cs[0].Request()
	acc := o.Body()["accepts"].([]X402RequirementWire)[0]
	if req["amount"] != acc.Amount || req["recipient"] != acc.PayTo || req["currency"] != acc.Asset {
		t.Fatalf("x402 %+v vs MPP %v", acc, req)
	}
	// All or nothing.
	h2 := http.Header{}
	bad := a
	bad.Quote = "a b"
	if _, err := Offer(bad, h2, wires...); err == nil || len(h2) != 0 {
		t.Fatalf("a failed offer wrote %v", h2)
	}
	if b := (Offered{Bodies: map[string]map[string]any{WireMPP: {"a": 1}}}).Body(); b["a"] != 1 {
		t.Fatal("MPP-only body")
	}
}
