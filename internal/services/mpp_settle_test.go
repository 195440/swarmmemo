package services

// MPP on the x402 rail (C202a): a credential for an MPP challenge carries a
// real EIP-3009 signature whose nonce is keccak256(challenge.id ‖ realm),
// and settles through the same facilitator calls (Verify, SettleVerified)
// with the same request body an x402 payment of that authorization makes.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"swarmmemo/internal/pay"
)

// mppSign answers the Payment challenge in h for the payer s: it reads the
// challenge as a client does, signs the authorization the request asks for
// with nonce = the challenge nonce, and returns the Authorization value.
func mppSign(t *testing.T, s X402Signer, d EIP712Domain, h http.Header, validBefore int64) string {
	t.Helper()
	cs, err := pay.ParseMPPChallenges(h.Values("WWW-Authenticate"))
	if err != nil || len(cs) != 1 {
		t.Fatalf("challenges %v %v", cs, err)
	}
	req, err := cs[0].Request()
	if err != nil {
		t.Fatal(err)
	}
	to, ok := ParseEVMAddress(req["recipient"].(string))
	value, err := strconv.ParseInt(req["amount"].(string), 10, 64)
	if !ok || err != nil {
		t.Fatal("request")
	}
	nonceHex := cs[0].Nonce()
	var nonce [32]byte
	if b, err := hex.DecodeString(nonceHex[2:]); err != nil || copy(nonce[:], b) != 32 {
		t.Fatal("nonce")
	}
	a := TransferAuthorization{From: s.Address(), To: to, Value: value, ValidAfter: 0, ValidBefore: validBefore, Nonce: nonce}
	sig, err := s.SignAuthorization(d, a)
	if err != nil {
		t.Fatal(err)
	}
	v, err := cs[0].Credential(pay.X402AuthorizationWire{From: a.From.String(), To: a.To.String(), Value: strconv.FormatInt(a.Value, 10),
		ValidAfter: "0", ValidBefore: strconv.FormatInt(validBefore, 10), Nonce: nonceHex}, "0x"+hex.EncodeToString(sig[:]), "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMPPSettlesOnTheX402Rail(t *testing.T) {
	cfg, err := ParseTopupConfig([]byte(topupTestConfig), nil)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	signer, err := signerFromKeyBytes(key)
	if err != nil {
		t.Fatal(err)
	}
	domain := EIP712Domain{Name: cfg.AssetName, Version: cfg.AssetVersion, ChainID: 8453, VerifyingContract: cfg.Asset}
	mppw, err := pay.NewMPP(cfg.Rail(), "swarmmemo.example", []byte("quote key: 32 bytes of test data"))
	if err != nil {
		t.Fatal(err)
	}
	wires := []pay.Wire{pay.NewX402(cfg.Rail()), mppw}
	const now = int64(1_760_180_000)
	r := TopupRequirement{Amount: 250_000, Quote: strconv.FormatInt(now+TopupQuoteSeconds, 10) + ".c2FsdA.bWFj", Expires: now + TopupQuoteSeconds}
	ask := pay.Ask{PayTo: cfg.PayTo, Amount: r.Amount, Quote: r.Quote, Expires: r.Expires, MaxTimeout: TopupQuoteSeconds,
		Resource: "https://swarmmemo.example/v1/command", Description: "Top up"}

	// 402 with both wires; the payer answers MPP.
	h := http.Header{}
	if _, err := pay.Offer(ask, h, wires...); err != nil {
		t.Fatal(err)
	}
	cred := mppSign(t, signer, domain, h, r.Expires)
	req := http.Header{"Authorization": {cred}}
	wire, raw, field, err := pay.Detect(req, wires...)
	if err != nil || wire.Name() != pay.WireMPP {
		t.Fatalf("detect %v", err)
	}
	x, err := wire.Parse(raw, field)
	if err != nil {
		t.Fatal(err)
	}
	// The caller checks the echoed quote with its own key (board), then binds.
	if x.Quote != r.Quote {
		t.Fatal("quote echo")
	}
	if err := wire.Bound(x, ask, now); err != nil {
		t.Fatal(err)
	}
	p := x.Payment()

	// The signature is a real EIP-3009 signature over the challenge nonce.
	var nonce [32]byte
	b, _ := hex.DecodeString(p.Nonce[2:])
	copy(nonce[:], b)
	want := pay.ChallengeNonce(x.Ref, "swarmmemo.example")
	if nonce != want {
		t.Fatal("nonce is not keccak256(id ‖ realm)")
	}
	var sig [65]byte
	sb, _ := hex.DecodeString(p.Signature[2:])
	copy(sig[:], sb)
	digest := TransferAuthorization{From: p.From, To: p.To, Value: p.Value, ValidAfter: p.ValidAfter, ValidBefore: p.ValidBefore, Nonce: nonce}.Digest(domain)
	if who, ok := recoverAddress(digest, sig); !ok || who != signer.Address() {
		t.Fatal("signature does not recover to the payer")
	}

	// The facilitator body is our requirement plus the authorization:
	// byte for byte what an x402 payment of the same authorization sends.
	mppBody, err := cfg.facilitatorBody(p, cfg.topup(r))
	if err != nil {
		t.Fatal(err)
	}
	x402v := topupTestPayment(cfg, r, func(m map[string]any) {
		au := m["payload"].(map[string]any)["authorization"].(map[string]any)
		au["from"], au["validAfter"], au["validBefore"], au["nonce"] = p.From.String(), "0", strconv.FormatInt(p.ValidBefore, 10), p.Nonce
		m["payload"].(map[string]any)["signature"] = p.Signature
	})
	xp, err := ParseTopupPayment(x402v)
	if err != nil {
		t.Fatal(err)
	}
	x402Body, _ := cfg.facilitatorBody(xp, cfg.topup(r))
	if !bytes.Equal(mppBody, x402Body) {
		t.Fatalf("facilitator bodies differ:\n mpp  %s\n x402 %s", mppBody, x402Body)
	}

	// Verify then SettleVerified, through a stand-in facilitator that checks
	// what it is sent the way the x402 reference facilitator does (EIP-3009:
	// to = payTo, value = amount, a 32-byte nonce, nothing else about it).
	tx := "0x" + strings.Repeat("ab", 32)
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		body, _ := io.ReadAll(q.Body)
		var f struct {
			PaymentPayload struct {
				Payload struct {
					Authorization x402AuthorizationWire `json:"authorization"`
				} `json:"payload"`
			} `json:"paymentPayload"`
			PaymentRequirements topupRequirementWire `json:"paymentRequirements"`
		}
		if json.Unmarshal(body, &f) != nil {
			w.WriteHeader(400)
			return
		}
		au := f.PaymentPayload.Payload.Authorization
		ok := strings.EqualFold(au.To, f.PaymentRequirements.PayTo) && au.Value == f.PaymentRequirements.Amount && len(au.Nonce) == 66
		seen = append(seen, q.URL.Path+" "+au.Nonce)
		switch {
		case q.URL.Path == "/verify":
			_, _ = w.Write([]byte(`{"isValid":` + strconv.FormatBool(ok) + `,"payer":"` + au.From + `"}`))
		case q.URL.Path == "/settle" && ok:
			_, _ = w.Write([]byte(`{"success":true,"transaction":"` + tx + `","network":"eip155:8453","payer":"` + au.From + `"}`))
		default:
			_, _ = w.Write([]byte(`{"success":false,"errorReason":"invalid"}`))
		}
	}))
	defer srv.Close()
	cfg.UseTestFacilitator(srv.Client(), srv.URL)
	if err := cfg.Verify(context.Background(), nil, p, cfg.topup(r)); err != nil {
		t.Fatal(err)
	}
	st, err := cfg.SettleVerified(context.Background(), nil, p, cfg.topup(r))
	if err != nil || st.Transaction != tx || st.Payer != signer.Address() {
		t.Fatalf("settle %+v %v", st, err)
	}
	if len(seen) != 2 || seen[0] != "/verify "+p.Nonce || seen[1] != "/settle "+p.Nonce {
		t.Fatalf("facilitator saw %v", seen)
	}

	// The receipt, after settlement.
	rh := http.Header{}
	if err := wire.Receipt(pay.Settlement{Transaction: st.Transaction, Payer: st.Payer, Ref: x.Ref, At: now + 5}, rh); err != nil {
		t.Fatal(err)
	}
	rec, _ := base64.RawURLEncoding.DecodeString(rh.Get("Payment-Receipt"))
	var m map[string]any
	if json.Unmarshal(rec, &m) != nil || m["challengeId"] != x.Ref || m["reference"] != tx || m["status"] != "success" || m["chainId"] != float64(8453) {
		t.Fatalf("receipt %s", rec)
	}
}
