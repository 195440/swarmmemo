package pay

// The x402 v2 server wire (docs.x402.org, HTTP transport), scheme "exact"
// on EVM (EIP-3009): the PaymentRequired object we issue, the strict parser
// of a PAYMENT-SIGNATURE (or X-PAYMENT) payload, and the check that a
// payment is exactly what we asked for. Moved here from
// internal/services/x402_topup.go (C202a) without behaviour change: the
// golden file services/testdata/x402_golden.json, recorded before the move,
// pins the bytes. services keeps its names (TopupConfig.PaymentRequiredWith,
// ParseTopupPayment, CheckRequirement) as thin wrappers.

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// x402 bounds.
const (
	// X402PaymentBytes bounds a payment header (base64), and the same
	// payment carried in a command's data (services.TopupPaymentBytes).
	X402PaymentBytes = 8 << 10
	// x402HeaderBytes bounds any base64 header we decode.
	x402HeaderBytes = 16 << 10
	// x402RawFieldBytes bounds the resource and extensions objects a payer
	// echoes.
	x402RawFieldBytes = 4 << 10
	// ValidBeforeMargin is how long an authorization must still be valid
	// when it reaches us, so it cannot lapse while it settles (seconds).
	ValidBeforeMargin = 30
)

// Rail is what moves the money, whatever the wire: one EIP-3009 token on
// one EVM network, settled by the facilitator. Network is CAIP-2
// ("eip155:8453"); AssetName and AssetVersion are the token's EIP-712
// domain ("USD Coin", "2").
type Rail struct {
	Network                 string
	Asset                   EVMAddress
	AssetName, AssetVersion string
}

var caip2EVMRE = regexp.MustCompile(`^eip155:([1-9][0-9]{0,15})$`)

// ChainID is the EIP-155 chain id of the rail's network.
func (r Rail) ChainID() (int64, bool) {
	m := caip2EVMRE.FindStringSubmatch(r.Network)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	return n, err == nil
}

// X402Requirement is any payment requirement this server issues on the
// rail: Amount atomic units to PayTo, good until Expires, Quote binding it,
// MaxTimeout its maxTimeoutSeconds. A top-up's pays the operator; an
// offering's (RFC 0017) pays the provider's linked wallet within its claim
// window.
type X402Requirement struct {
	PayTo      EVMAddress
	Amount     int64
	Quote      string
	Expires    int64
	MaxTimeout int64
}

// X402ExtraWire is a requirement's "extra": the token domain and our quote.
type X402ExtraWire struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Quote   string `json:"quote"`
}

// X402RequirementWire is an x402 v2 PaymentRequirements object.
type X402RequirementWire struct {
	Scheme            string        `json:"scheme"`
	Network           string        `json:"network"`
	Amount            string        `json:"amount"`
	Asset             string        `json:"asset"`
	PayTo             string        `json:"payTo"`
	MaxTimeoutSeconds int64         `json:"maxTimeoutSeconds"`
	Extra             X402ExtraWire `json:"extra"`
}

// X402Accept is q as an x402 v2 PaymentRequirements object.
func (r Rail) X402Accept(q X402Requirement) X402RequirementWire {
	return X402RequirementWire{Scheme: "exact", Network: r.Network, Amount: strconv.FormatInt(q.Amount, 10), Asset: r.Asset.String(),
		PayTo: q.PayTo.String(), MaxTimeoutSeconds: q.MaxTimeout, Extra: X402ExtraWire{Name: r.AssetName, Version: r.AssetVersion, Quote: q.Quote}}
}

// X402PaymentRequired is the x402 v2 PaymentRequired object for q, and its
// base64 encoding for the PAYMENT-REQUIRED header, with the x402 v2
// extensions object (e.g. "bazaar" discovery metadata); nil omits it.
func (r Rail) X402PaymentRequired(q X402Requirement, resourceURL, description string, extensions map[string]any) (map[string]any, string) {
	body := map[string]any{
		"x402Version": 2,
		"error":       "payment required",
		"resource":    map[string]any{"url": resourceURL, "description": description, "mimeType": "application/json"},
		"accepts":     []X402RequirementWire{r.X402Accept(q)},
	}
	if extensions != nil {
		body["extensions"] = extensions
	}
	raw, _ := json.Marshal(body)
	return body, base64.StdEncoding.EncodeToString(raw)
}

// X402Payment is a parsed x402 v2 payment payload for the exact EVM scheme:
// what the payer accepted, and its signed EIP-3009 authorization
// (services.TopupPayment).
type X402Payment struct {
	// Accepted is the requirement the payer says it accepted.
	Scheme, Network, Asset, PayTo, Amount string
	Name, Version, Quote                  string
	// The authorization.
	From, To                EVMAddress
	Value                   int64
	ValidAfter, ValidBefore int64
	Nonce                   string // "0x" + 64 lowercase hex
	Signature               string // "0x" + 130 lowercase hex
	// Resource is the payload's resource object as the payer sent it
	// (bounded, never trusted), echoed to the facilitator; nil when absent.
	Resource json.RawMessage
}

// Refusal is a wire's refusal of a payment; Code is the board's public
// error code (payment_invalid, payment_mismatch, payment_expired,
// payment_challenge). Nothing has moved when a wire refuses.
type Refusal struct{ Code string }

func (e *Refusal) Error() string { return "pay: " + e.Code }

func refusal(code string) error { return &Refusal{Code: code} }

// Board error codes a wire's refusal carries.
const (
	CodeInvalid   = "payment_invalid"   // malformed credential
	CodeMismatch  = "payment_mismatch"  // not what we asked: amount, payTo, asset, nonce
	CodeExpired   = "payment_expired"   // challenge or authorization out of time
	CodeChallenge = "payment_challenge" // MPP: not a challenge we issued, or tampered
)

var (
	evmNonceRE     = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
	evmSignatureRE = regexp.MustCompile(`^0x[0-9a-fA-F]{130}$`)
	unixSecondsRE  = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})$`)
	quoteRE        = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	// atomicAmountRE is services' atomicAmountRE: a positive integer that
	// fits an int64 with room.
	atomicAmountRE = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
	txHashRE       = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

type x402PayloadWire struct {
	Version    json.Number     `json:"x402Version"`
	Resource   json.RawMessage `json:"resource"`
	Accepted   json.RawMessage `json:"accepted"`
	Payload    json.RawMessage `json:"payload"`
	Extensions json.RawMessage `json:"extensions"`
}

type x402AcceptedWire struct {
	Scheme            string          `json:"scheme"`
	Network           string          `json:"network"`
	Amount            string          `json:"amount"`
	Asset             string          `json:"asset"`
	PayTo             string          `json:"payTo"`
	MaxTimeoutSeconds json.Number     `json:"maxTimeoutSeconds"`
	Extra             json.RawMessage `json:"extra"`
}

type x402AcceptedExtraWire struct {
	Name                string `json:"name"`
	Version             string `json:"version"`
	Quote               string `json:"quote"`
	AssetTransferMethod string `json:"assetTransferMethod"`
}

// X402AuthorizationWire is an EIP-3009 authorization as x402 (and MPP's
// evm "authorization" payload) writes it: addresses and decimal strings.
type X402AuthorizationWire struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Value       string `json:"value"`
	ValidAfter  string `json:"validAfter"`
	ValidBefore string `json:"validBefore"`
	Nonce       string `json:"nonce"`
}

// X402ExactPayload is the x402 "exact" EVM payload.
type X402ExactPayload struct {
	Signature     string                `json:"signature"`
	Authorization X402AuthorizationWire `json:"authorization"`
}

// ParseX402Payment reads a PAYMENT-SIGNATURE (or X-PAYMENT) header value:
// base64 of one x402 v2 payment payload for the exact scheme, every field
// well formed. The payload and its authorization are strict (no unknown
// field); the envelope's resource and extensions are bounded and never
// trusted. Any malformation is payment_invalid.
func ParseX402Payment(header string) (X402Payment, error) {
	invalid := refusal(CodeInvalid)
	header = strings.TrimSpace(header)
	if header == "" || len(header) > X402PaymentBytes {
		return X402Payment{}, invalid
	}
	raw, ok := DecodeBase64JSON(header, x402HeaderBytes)
	if !ok || len(raw) > X402PaymentBytes {
		return X402Payment{}, invalid
	}
	var w x402PayloadWire
	if strictObject(raw, &w) != nil || w.Version.String() != "2" || !IsObject(w.Accepted) || !IsObject(w.Payload) {
		return X402Payment{}, invalid
	}
	if (len(w.Resource) > 0 && string(w.Resource) != "null" && (!IsObject(w.Resource) || len(w.Resource) > x402RawFieldBytes)) ||
		(len(w.Extensions) > 0 && string(w.Extensions) != "null" && (!IsObject(w.Extensions) || len(w.Extensions) > x402RawFieldBytes)) {
		return X402Payment{}, invalid
	}
	var acc x402AcceptedWire
	if strictObject(w.Accepted, &acc) != nil || !IsObject(acc.Extra) {
		return X402Payment{}, invalid
	}
	var extra x402AcceptedExtraWire
	if strictObject(acc.Extra, &extra) != nil || !quoteRE.MatchString(extra.Quote) {
		return X402Payment{}, invalid
	}
	var pl X402ExactPayload
	if strictObject(w.Payload, &pl) != nil {
		return X402Payment{}, invalid
	}
	a := pl.Authorization
	p := X402Payment{Scheme: acc.Scheme, Network: acc.Network, Asset: acc.Asset, PayTo: acc.PayTo, Amount: acc.Amount,
		Name: extra.Name, Version: extra.Version, Quote: extra.Quote}
	if extra.AssetTransferMethod != "" && extra.AssetTransferMethod != "eip3009" {
		return X402Payment{}, invalid
	}
	if !parseAuthorization(a, pl.Signature, &p.From, &p.To, &p.Value, &p.ValidAfter, &p.ValidBefore, &p.Nonce, &p.Signature) {
		return X402Payment{}, invalid
	}
	if IsObject(w.Resource) {
		p.Resource = w.Resource
	}
	return p, nil
}

// parseAuthorization reads an EIP-3009 authorization and its signature as
// both wires write them: from a non-zero address, to any address, value a
// positive integer, the window Unix seconds, nonce 32 bytes and signature
// 65 bytes of hex (lowered).
func parseAuthorization(a X402AuthorizationWire, signature string, from, to *EVMAddress, value, after, before *int64, nonce, sig *string) bool {
	var ok bool
	if *from, ok = ParseEVMAddress(a.From); !ok || *from == (EVMAddress{}) {
		return false
	}
	if *to, ok = ParseEVMAddress(a.To); !ok {
		return false
	}
	if !atomicAmountRE.MatchString(a.Value) || !unixSecondsRE.MatchString(a.ValidAfter) || !unixSecondsRE.MatchString(a.ValidBefore) {
		return false
	}
	*value, _ = strconv.ParseInt(a.Value, 10, 64)
	*after, _ = strconv.ParseInt(a.ValidAfter, 10, 64)
	*before, _ = strconv.ParseInt(a.ValidBefore, 10, 64)
	if !evmNonceRE.MatchString(a.Nonce) || !evmSignatureRE.MatchString(signature) {
		return false
	}
	*nonce, *sig = strings.ToLower(a.Nonce), strings.ToLower(signature)
	return true
}

// CheckX402 holds p to exactly what q asked: the exact scheme on our
// network, our asset and token domain, q.PayTo in both the accepted
// requirement and the authorization, the amount to the unit, the quote we
// issued, and an authorization valid now and for long enough to settle.
// The quote's binding to the account and its expiry are the caller's (it
// holds the key); q carries what it verified.
func (r Rail) CheckX402(p X402Payment, q X402Requirement, now int64) error {
	mismatch := refusal(CodeMismatch)
	if p.Scheme != "exact" || p.Network != r.Network || p.Quote != q.Quote {
		return mismatch
	}
	if asset, ok := ParseEVMAddress(p.Asset); !ok || asset != r.Asset {
		return mismatch
	}
	if payTo, ok := ParseEVMAddress(p.PayTo); !ok || q.PayTo == (EVMAddress{}) || payTo != q.PayTo || p.To != q.PayTo {
		return mismatch
	}
	if (p.Name != "" && p.Name != r.AssetName) || (p.Version != "" && p.Version != r.AssetVersion) {
		return mismatch
	}
	if p.Amount != strconv.FormatInt(q.Amount, 10) || p.Value != q.Amount {
		return mismatch
	}
	return checkWindow(q.Expires, p.ValidAfter, p.ValidBefore, now)
}

// checkWindow is the time rule both wires share: the challenge (quote) not
// expired, the authorization valid now and for ValidBeforeMargin more
// seconds, and its window not empty.
func checkWindow(expires, validAfter, validBefore, now int64) error {
	if expires < now || validBefore < now+ValidBeforeMargin || validAfter > now || validAfter >= validBefore {
		return refusal(CodeExpired)
	}
	return nil
}
