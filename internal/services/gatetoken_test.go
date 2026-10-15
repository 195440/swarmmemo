package services

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func gateTestKey() (ed25519.PrivateKey, string) {
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	return key, base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
}

func TestGateTokenSignVerify(t *testing.T) {
	key, pub := gateTestKey()
	p := GatePayload{ServiceID: "swarmmemo.com", Origin: "https://shop.example", Nonce: "n0nce-123", Challenge: strings.Repeat("c", 32),
		IssuedAt: 1790000000, ExpiresAt: 1790000300, Passed: true, MinCents: 0, Band: "anonymous", Proofs: []string{"pow"}}
	token, signed, ok := SignGateToken(key, p)
	if !ok || !strings.HasPrefix(token, GateTokenPrefix) || signed.Schema != GateSchema || signed.KeyID != keyID(key.Public().(ed25519.PublicKey)) {
		t.Fatalf("sign: %v %+v", ok, signed)
	}
	got, err := VerifyGateToken(pub, token, "https://shop.example", 1790000299)
	if err != nil || !got.Passed || got.Nonce != "n0nce-123" || got.Proofs[0] != "pow" {
		t.Fatalf("verify: %+v %v", got, err)
	}
	if _, err = VerifyGateToken(pub, token, "https://other.example", 1790000000); !errors.Is(err, ErrGateOrigin) {
		t.Fatalf("other origin: %v", err)
	}
	if _, err = VerifyGateToken(pub, token, "https://shop.example", 1790000300); !errors.Is(err, ErrGateExpired) {
		t.Fatalf("expired: %v", err)
	}
	// Another key, a flipped payload byte, a re-serialised payload, a cut token.
	_, other := func() (ed25519.PrivateKey, string) {
		k := ed25519.NewKeyFromSeed([]byte(strings.Repeat("x", 32)))
		return k, base64.RawURLEncoding.EncodeToString(k.Public().(ed25519.PublicKey))
	}()
	if _, err = VerifyGateToken(other, token, "https://shop.example", 1790000000); !errors.Is(err, ErrGateToken) {
		t.Fatalf("other key: %v", err)
	}
	payload, sig, _ := ParseGateToken(token)
	tampered := strings.Replace(payload, `"passed":true`, `"passed":false`, 1)
	forged := GateTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(tampered)) + "." + sig
	if _, err = VerifyGateToken(pub, forged, "https://shop.example", 1790000000); !errors.Is(err, ErrGateToken) {
		t.Fatalf("tampered: %v", err)
	}
	spaced := GateTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(payload, ",", ", ", 1))) + "." + sig
	if _, err = VerifyGateToken(pub, spaced, "https://shop.example", 1790000000); !errors.Is(err, ErrGateToken) {
		t.Fatalf("re-serialised: %v", err)
	}
	for _, bad := range []string{"", "smg1.", "smg2." + token[5:], token[:len(token)-3], token + ".x", strings.Repeat("a", GateTokenMax+1)} {
		if _, err = VerifyGateToken(pub, bad, "https://shop.example", 1790000000); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, _, ok = SignGateToken(nil, p); ok {
		t.Fatal("signed without a key")
	}
}

// TestGateTokenVector pins the format: a fixed key and payload give these
// exact bytes, which docs/rfcs/0022-gate-token.md shows.
func TestGateTokenVector(t *testing.T) {
	key, _ := gateTestKey()
	token, _, _ := SignGateToken(key, GatePayload{ServiceID: "swarmmemo.com", Origin: "https://shop.example", Nonce: "abcdefgh", Challenge: strings.Repeat("0", 32),
		IssuedAt: 1790000000, ExpiresAt: 1790000300, Passed: true, Band: "anonymous", Proofs: []string{"pow"}, Bits: 20})
	payload, _, _ := ParseGateToken(token)
	const want = `{"schema":"swarmmemo-gate/1","service_id":"swarmmemo.com","key_id":"139e3940e64b5491722088d9a0d741628fc826e09475d341a780acde3c4b8070","origin":"https://shop.example","nonce":"abcdefgh","challenge":"00000000000000000000000000000000","issued_at":1790000000,"expires_at":1790000300,"passed":true,"min_cents":0,"standing_cents":0,"band":"anonymous","proofs":["pow"],"subject":"","millis":0,"bits":20}`
	if payload != want {
		t.Fatalf("payload\n%s\nwant\n%s", payload, want)
	}
	// Ed25519 is deterministic: the whole token is fixed too (the seed is 32 zero bytes).
	const wantToken = "smg1.eyJzY2hlbWEiOiJzd2FybW1lbW8tZ2F0ZS8xIiwic2VydmljZV9pZCI6InN3YXJtbWVtby5jb20iLCJrZXlfaWQiOiIxMzllMzk0MGU2NGI1NDkxNzIyMDg4ZDlhMGQ3NDE2MjhmYzgyNmUwOTQ3NWQzNDFhNzgwYWNkZTNjNGI4MDcwIiwib3JpZ2luIjoiaHR0cHM6Ly9zaG9wLmV4YW1wbGUiLCJub25jZSI6ImFiY2RlZmdoIiwiY2hhbGxlbmdlIjoiMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAiLCJpc3N1ZWRfYXQiOjE3OTAwMDAwMDAsImV4cGlyZXNfYXQiOjE3OTAwMDAzMDAsInBhc3NlZCI6dHJ1ZSwibWluX2NlbnRzIjowLCJzdGFuZGluZ19jZW50cyI6MCwiYmFuZCI6ImFub255bW91cyIsInByb29mcyI6WyJwb3ciXSwic3ViamVjdCI6IiIsIm1pbGxpcyI6MCwiYml0cyI6MjB9.ZlivfDpy89IrVb9lDbrJkCYXjpfCnWQH_-Drl4ei532pUyfgAzTboded-pvBwDg_-1lowVYhdVoKZxvm9ekYDg"
	if token != wantToken {
		t.Fatalf("token\n%s\nwant\n%s", token, wantToken)
	}
}

func TestGateResultID(t *testing.T) {
	key, _ := gateTestKey()
	id, ok := SignGateResult(key, strings.Repeat("a", 64), 412, 1790000000)
	if !ok {
		t.Fatal("no id")
	}
	p, ok := VerifyGateResult(key, id)
	if !ok || p.Millis != 412 || p.Agent != strings.Repeat("a", 64) {
		t.Fatalf("%+v %v", p, ok)
	}
	for _, bad := range []string{"", "x", id + "x", "e30." + strings.Split(id, ".")[1], strings.Repeat("a", 600)} {
		if _, ok = VerifyGateResult(key, bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// C325-sec: the notary key signs receipts, journal seals and gate results
// too. None of them, wrapped as smg1, is a gate token (each names its own
// schema, and a payload with fields a token lacks fails strict decoding),
// and a token is no receipt.
func TestGateTokenDomainSeparation(t *testing.T) {
	key, pub := gateTestKey()
	wrap := func(payload, sig string) string {
		return GateTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + sig
	}
	for name, signed := range map[string]any{
		"notary receipt": NotaryPayload{Schema: NotarySchema, ServiceID: "swarmmemo.com", KeyID: keyID(key.Public().(ed25519.PublicKey)), Seq: 1, Time: 1790000000, Hash: strings.Repeat("a", 64)},
		"journal seal":   JournalSealPayload{Schema: JournalSealSchema, ServiceID: "swarmmemo.com", KeyID: keyID(key.Public().(ed25519.PublicKey)), Agent: "https://a.example", Time: 1790000000, Hash: strings.Repeat("a", 64)},
		"gate result":    GateResultPayload{Schema: GateResultSchema, Agent: "", Millis: 1, Time: 1790000000},
		// A payload of the token's own fields, but another schema.
		"wrong schema": GatePayload{Schema: GateResultSchema, KeyID: keyID(key.Public().(ed25519.PublicKey)), Origin: "https://a.example", ExpiresAt: 2, Passed: true, Proofs: []string{"pow"}},
	} {
		payload, sig := signPayload(key, signed)
		if _, err := VerifyGateToken(pub, wrap(payload, sig), "https://a.example", 1); !errors.Is(err, ErrGateToken) {
			t.Errorf("%s accepted as a gate token: %v", name, err)
		}
	}
	id, _ := SignGateResult(key, "", 1, 1790000000)
	if _, err := VerifyGateToken(pub, GateTokenPrefix+id, "https://a.example", 1); err == nil {
		t.Error("a result card ID is a token")
	}
	token, _, _ := SignGateToken(key, GatePayload{Origin: "https://a.example", ExpiresAt: 2, Proofs: []string{"pow"}})
	payload, sig, _ := ParseGateToken(token)
	if VerifyNotaryReceipt(pub, NotaryReceipt{Schema: NotarySchema, Payload: payload, Signature: sig}) {
		t.Error("a gate token is a notary receipt")
	}
	if _, ok := VerifyGateResult(key, base64.RawURLEncoding.EncodeToString([]byte(payload))+"."+sig); ok {
		t.Error("a gate token is a result card")
	}
}

func FuzzVerifyGateToken(f *testing.F) {
	key, pub := gateTestKey()
	token, _, _ := SignGateToken(key, GatePayload{Origin: "https://a.example", ExpiresAt: 2, Proofs: []string{"pow"}})
	f.Add(token)
	f.Add("smg1.e30.AAAA")
	f.Fuzz(func(t *testing.T, s string) {
		p, err := VerifyGateToken(pub, s, "https://a.example", 1)
		if err == nil && (p.Schema != GateSchema || p.Origin != "https://a.example") {
			t.Fatalf("accepted %q as %+v", s, p)
		}
	})
}
