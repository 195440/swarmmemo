package services

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
)

// The SwarmMemo Gate's token (C325, docs/rfcs/0022-gate-token.md): what a
// visitor that passed a gate hands the site, and the site checks offline
// with the notary's published key (GET /api/notary/key, or
// /v1/gate/status). It is signed exactly as notary receipts are
// (signPayload): one key, one canonical form, one check.
//
//	smg1.BASE64URL(PAYLOAD).BASE64URL(SIGNATURE)
//
// PAYLOAD is GatePayload's canonical JSON (fields in struct order, no
// spaces, no HTML escaping) and SIGNATURE the Ed25519 signature over exactly
// those bytes. A verifier never re-serialises: it checks the signature over
// the payload bytes, then reads them.

// GateSchema names the token payload; a breaking change is a new name.
const GateSchema = "swarmmemo-gate/1"

// GateTokenPrefix starts every token: the format and its version.
const GateTokenPrefix = "smg1."

// GateTokenMax bounds a token a verifier reads.
const GateTokenMax = 2048

// GatePayload is what a token states.
type GatePayload struct {
	Schema    string `json:"schema"`
	ServiceID string `json:"service_id"`
	KeyID     string `json:"key_id"`
	// Origin is the site the token is for, and Nonce the site's nonce (or
	// the challenge ID when the site gave none): a site accepts a token for
	// its own origin and a nonce it issued, once.
	Origin    string `json:"origin"`
	Nonce     string `json:"nonce"`
	Challenge string `json:"challenge"`
	IssuedAt  int64  `json:"issued_at"`
	ExpiresAt int64  `json:"expires_at"`
	// Passed is true when a proof held and the standing is at least
	// MinCents. An identity below the site's minimum gets a token with
	// passed false, so the site can say how far it is.
	Passed        bool   `json:"passed"`
	MinCents      int64  `json:"min_cents"`
	StandingCents int64  `json:"standing_cents"`
	Band          string `json:"band"`
	// Proofs are what held: pow, agent_task, wallet or identity, and the
	// identity's verified link kinds (domain, github, ...).
	Proofs []string `json:"proofs"`
	// Subject is the agent's fingerprint (identity), wallet:eip155:1:ADDRESS
	// (wallet), or empty (work or the agent task without a key).
	Subject string `json:"subject"`
	// Millis is the agent task's time, from issue to answer; 0 otherwise.
	Millis int64 `json:"millis"`
	// Bits is the leading zero bits the proof of work met (the challenge's,
	// with its network's escalation); 0 without work. A site that wants
	// harder work than the default checks it: the caller, not the site,
	// asks for the challenge.
	Bits int64 `json:"bits"`
}

// Gate token errors.
var (
	ErrGateToken   = errors.New("gate token: malformed or not signed by this key")
	ErrGateExpired = errors.New("gate token: expired")
	ErrGateOrigin  = errors.New("gate token: issued for another origin")
)

// SignGateToken signs p with the notary key; ok is false without a key. It
// fills Schema and KeyID.
func SignGateToken(key ed25519.PrivateKey, p GatePayload) (string, GatePayload, bool) {
	if len(key) != ed25519.PrivateKeySize {
		return "", p, false
	}
	p.Schema, p.KeyID = GateSchema, keyID(key.Public().(ed25519.PublicKey))
	if p.Proofs == nil {
		p.Proofs = []string{}
	}
	payload, signature := signPayload(key, p)
	return GateTokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + signature, p, true
}

// ParseGateToken splits a token into its payload bytes and signature,
// without checking either.
func ParseGateToken(token string) (payload, signature string, err error) {
	if len(token) > GateTokenMax {
		return "", "", ErrGateToken
	}
	rest, ok := strings.CutPrefix(token, GateTokenPrefix)
	if !ok {
		return "", "", ErrGateToken
	}
	body, sig, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(sig, ".") {
		return "", "", ErrGateToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != body {
		return "", "", ErrGateToken
	}
	return string(raw), sig, nil
}

// VerifyGateToken checks a token offline against the notary's public key
// (base64url, as published): the signature over the payload, its schema and
// key, that it is for origin, and that now is before it expires. The caller
// still checks Passed, the nonce it issued (once) and any standing it needs.
func VerifyGateToken(publicKey, token, origin string, now int64) (GatePayload, error) {
	payload, signature, err := ParseGateToken(token)
	if err != nil {
		return GatePayload{}, err
	}
	var p GatePayload
	id, ok := verifyPayload(publicKey, payload, signature, &p)
	if !ok || p.Schema != GateSchema || p.KeyID != id {
		return GatePayload{}, ErrGateToken
	}
	if p.Origin != origin {
		return p, ErrGateOrigin
	}
	if now >= p.ExpiresAt {
		return p, ErrGateExpired
	}
	return p, nil
}

// GateResultSchema names a shareable agent-task result (/gate/agent).
const GateResultSchema = "swarmmemo-gate-result/1"

// GateResultPayload is a passed agent task as its card shows it: who (an
// agent fingerprint, or empty), how fast, when. It is not a token: no
// origin, no nonce, and nothing a site accepts.
type GateResultPayload struct {
	Schema string `json:"schema"`
	Agent  string `json:"agent"`
	Millis int64  `json:"millis"`
	Time   int64  `json:"time"`
}

// SignGateResult is a result's public ID: the payload and its signature,
// each base64url, joined by a dot.
func SignGateResult(key ed25519.PrivateKey, agent string, millis, now int64) (string, bool) {
	if len(key) != ed25519.PrivateKeySize {
		return "", false
	}
	payload, signature := signPayload(key, GateResultPayload{Schema: GateResultSchema, Agent: agent, Millis: millis, Time: now})
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + signature, true
}

// VerifyGateResult reads a result ID signed by key.
func VerifyGateResult(key ed25519.PrivateKey, id string) (GateResultPayload, bool) {
	if len(key) != ed25519.PrivateKeySize || len(id) > 512 {
		return GateResultPayload{}, false
	}
	body, sig, ok := strings.Cut(id, ".")
	if !ok {
		return GateResultPayload{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != body {
		return GateResultPayload{}, false
	}
	var p GateResultPayload
	pub := base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	if _, ok := verifyPayload(pub, string(raw), sig, &p); !ok || p.Schema != GateResultSchema {
		return GateResultPayload{}, false
	}
	return p, true
}
