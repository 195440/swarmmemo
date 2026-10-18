package services

// Credit top-ups: SwarmMemo as an x402 server. An agent asks to top up N
// credits (1 credit = 1 micro-USDC); the board answers 402 with one x402 v2
// payment requirement (exact, EIP-3009, our network and asset, the
// operator's receiving address, amount N exactly, a short-lived quote bound
// to the agent's account); the agent retries with a signed
// transferWithAuthorization in PAYMENT-SIGNATURE (or X-PAYMENT), and the
// board credits paid credit only once the operator's facilitator has
// settled it on chain.
//
// This file is the pure, money-free part: the operator's config, the strict
// parser of a payment header, the check that a payment is exactly what we
// asked for, and the facilitator client (POST /verify, then /settle). It
// never holds a key: we only receive, and the facilitator broadcasts.

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"swarmmemo/internal/pay"
	"swarmmemo/internal/useragent"
)

// Top-up bounds and defaults, in credits (micro-USDC).
const (
	// TopupMinDefault is the smallest top-up by default: 0.10 USDC.
	TopupMinDefault = 100_000
	// TopupMaxDefault is the largest single top-up by default: 5 USDC.
	TopupMaxDefault = 5_000_000
	// TopupAccountDailyDefault is what one account may top up per UTC day
	// by default: 10 USDC.
	TopupAccountDailyDefault = 10_000_000
	// TopupBoardDailyDefault is what the whole board takes in top-ups per
	// UTC day, every account together, by default: 100 USDC.
	TopupBoardDailyDefault = 100_000_000
	// TopupCeiling bounds every limit the config sets (1,000 USDC): a typo
	// cannot open an unbounded inflow.
	TopupCeiling = 1_000_000_000
	// TopupQuoteSeconds is how long a payment requirement is good for.
	TopupQuoteSeconds = 300
	// TopupPaymentBytes bounds a payment header (base64), and the same
	// payment carried in a command's data.
	TopupPaymentBytes = pay.X402PaymentBytes
	// topupFacilitatorBytes bounds a facilitator answer we read.
	topupFacilitatorBytes = 16 << 10
	// topupFacilitatorTimeout bounds each facilitator call; settling waits
	// for the chain, so it is generous.
	topupFacilitatorTimeout = 30 * time.Second
	// topupValidBeforeMargin is how long an authorization must still be
	// valid when it reaches us, so it cannot lapse while it settles.
	topupValidBeforeMargin = pay.ValidBeforeMargin
	// X402ValidBeforeMargin is topupValidBeforeMargin for a caller that
	// settles later (offerings): an authorization must still be valid this
	// long when it is settled.
	X402ValidBeforeMargin = topupValidBeforeMargin
)

// Agent offerings (RFC 0017): bounds of an offering's price and of its claim
// window, which is its payment requirement's maxTimeoutSeconds.
const (
	// OfferingPriceFloor and OfferingPriceCeiling bound an offering's price
	// in micro-USDC: 0.01 to 100 USDC, the operator's per-call cap, which
	// the config's offerings.max_price may only lower.
	OfferingPriceFloor   = 10_000
	OfferingPriceCeiling = 100_000_000
	// OfferingTimeoutFloor and OfferingTimeoutCeiling bound the claim window
	// in seconds. The default facilitator (PayAI) documents no maximum
	// maxTimeoutSeconds, so the ceiling is the RFC's 3600 unless the
	// config's offerings.max_timeout_seconds names the facilitator's smaller
	// one; a longer claim window is clamped to it.
	OfferingTimeoutFloor   = 60
	OfferingTimeoutCeiling = 3600
)

// TopupConfig is the loaded, validated top-up configuration
// (LoadTopupConfig). Amounts are credits, which are the asset's atomic
// units: the asset must be a 6-decimal USD token (USDC).
type TopupConfig struct {
	Network      string // CAIP-2, "eip155:8453"
	Asset        EVMAddress
	AssetName    string // its EIP-712 domain name, "USD Coin"
	AssetVersion string // its EIP-712 domain version, "2"
	// PayTo is the operator's receiving address. No key for it is ever
	// loaded: SwarmMemo only receives.
	PayTo          EVMAddress
	FacilitatorURL string
	// Min and Max bound one top-up; AccountDaily is what one account may
	// top up per UTC day, BoardDaily what every account together may.
	Min, Max, AccountDaily, BoardDaily int64
	// OfferingMaxPrice bounds one offering call's price (RFC 0017), in
	// micro-USDC; OfferingMaxTimeout is the facilitator's largest
	// maxTimeoutSeconds, to which a claim window is clamped.
	OfferingMaxPrice, OfferingMaxTimeout int64
	facilitatorToken                     string
	client                               *http.Client
}

type topupConfigFile struct {
	Schema               json.Number `json:"schema"`
	Enabled              bool        `json:"enabled"`
	Network              string      `json:"network"`
	Asset                string      `json:"asset"`
	AssetName            string      `json:"asset_name"`
	AssetVersion         string      `json:"asset_version"`
	PayTo                string      `json:"pay_to"`
	FacilitatorURL       string      `json:"facilitator_url"`
	FacilitatorTokenFile string      `json:"facilitator_token_file"`
	Limits               struct {
		Min          string `json:"min"`
		Max          string `json:"max"`
		AccountDaily string `json:"account_daily"`
		BoardDaily   string `json:"board_daily"`
	} `json:"limits"`
	// Offerings (RFC 0017), optional: the per-call cap and the
	// facilitator's largest maxTimeoutSeconds.
	Offerings struct {
		MaxPrice          string      `json:"max_price"`
		MaxTimeoutSeconds json.Number `json:"max_timeout_seconds"`
	} `json:"offerings"`
}

// ErrTopupDisabled is LoadTopupConfig's answer for a config with "enabled"
// false: top-ups stay off.
var ErrTopupDisabled = errors.New("topup: the config says enabled: false")

// LoadTopupConfig reads the operator's TOPUP_CONFIG file and, when it names
// one, the facilitator's bearer token file (owner-only, like a key), for
// credit top-ups: "enabled" must be true. Errors name the field, never a
// secret.
func LoadTopupConfig(path string) (*TopupConfig, error) {
	raw, err := readTopupConfig(path)
	if err != nil {
		return nil, err
	}
	return ParseTopupConfig(raw, readPrivateFile)
}

// LoadFacilitatorConfig is LoadTopupConfig for agent offerings (RFC 0017):
// only the shared x402 fields (network, asset, asset_name, asset_version,
// facilitator_url, facilitator_token_file, offerings). "enabled", pay_to and
// limits govern credit top-ups alone and are not read, so offerings run
// with top-ups off.
func LoadFacilitatorConfig(path string) (*TopupConfig, error) {
	raw, err := readTopupConfig(path)
	if err != nil {
		return nil, err
	}
	return ParseFacilitatorConfig(raw, readPrivateFile)
}

func readTopupConfig(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("topup: TOPUP_CONFIG is not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("topup: read config: %w", err)
	}
	return raw, nil
}

func badTopupField(field string) error {
	return fmt.Errorf("topup: config field %s is missing or invalid", field)
}

// ParseTopupConfig validates a config already read, for credit top-ups;
// readKey reads the facilitator token file (nil: the config must not name
// one).
func ParseTopupConfig(raw []byte, readKey func(string) ([]byte, error)) (*TopupConfig, error) {
	var f topupConfigFile
	if err := StrictObject(raw, &f); err != nil {
		return nil, errors.New("topup: config is not strict JSON with only the documented fields")
	}
	if !f.Enabled {
		return nil, ErrTopupDisabled
	}
	c, err := parseFacilitatorFields(&f, readKey)
	if err != nil {
		return nil, err
	}
	bad := badTopupField
	var ok bool
	if c.PayTo, ok = ParseEVMAddress(f.PayTo); !ok || c.PayTo == (EVMAddress{}) || c.PayTo == c.Asset {
		return nil, bad("pay_to (the operator's receiving address)")
	}
	limits := []struct {
		text, name string
		def        int64
		dst        *int64
	}{
		{f.Limits.Min, "limits.min", TopupMinDefault, &c.Min},
		{f.Limits.Max, "limits.max", TopupMaxDefault, &c.Max},
		{f.Limits.AccountDaily, "limits.account_daily", TopupAccountDailyDefault, &c.AccountDaily},
		{f.Limits.BoardDaily, "limits.board_daily", TopupBoardDailyDefault, &c.BoardDaily},
	}
	for _, l := range limits {
		*l.dst = l.def
		if l.text == "" {
			continue
		}
		n, ok := parseUnits(l.text, 6)
		if !ok || n <= 0 || n > TopupCeiling {
			return nil, bad(l.name + " (a positive USDC amount, at most " + formatUnits(TopupCeiling, 6) + ")")
		}
		*l.dst = n
	}
	if c.Min > c.Max || c.Max > c.AccountDaily || c.AccountDaily > c.BoardDaily {
		return nil, errors.New("topup: limits must satisfy min <= max <= account_daily <= board_daily")
	}
	return c, nil
}

// ParseFacilitatorConfig validates a config already read, for agent
// offerings: the shared fields only, whatever "enabled" says (see
// LoadFacilitatorConfig). The result has no PayTo and no top-up limits.
func ParseFacilitatorConfig(raw []byte, readKey func(string) ([]byte, error)) (*TopupConfig, error) {
	var f topupConfigFile
	if err := StrictObject(raw, &f); err != nil {
		return nil, errors.New("topup: config is not strict JSON with only the documented fields")
	}
	return parseFacilitatorFields(&f, readKey)
}

// parseFacilitatorFields validates the fields top-ups and offerings share.
func parseFacilitatorFields(f *topupConfigFile, readKey func(string) ([]byte, error)) (*TopupConfig, error) {
	bad := badTopupField
	if f.Schema.String() != "1" {
		return nil, bad("schema (must be 1)")
	}
	c := &TopupConfig{AssetName: f.AssetName, AssetVersion: f.AssetVersion}
	if !x402NetworkRE.MatchString(f.Network) {
		return nil, bad(`network (CAIP-2 "eip155:CHAIN_ID")`)
	}
	c.Network = f.Network
	var ok bool
	if c.Asset, ok = ParseEVMAddress(f.Asset); !ok || c.Asset == (EVMAddress{}) {
		return nil, bad("asset")
	}
	if !x402TokenNameRE.MatchString(f.AssetName) {
		return nil, bad("asset_name")
	}
	if !x402TokenNameRE.MatchString(f.AssetVersion) {
		return nil, bad("asset_version")
	}
	if err := checkX402URL(f.FacilitatorURL); err != nil {
		return nil, bad("facilitator_url (" + err.Error() + ")")
	}
	c.FacilitatorURL = strings.TrimRight(f.FacilitatorURL, "/")
	if f.FacilitatorTokenFile != "" {
		if readKey == nil {
			return nil, bad("facilitator_token_file")
		}
		token, err := readKey(f.FacilitatorTokenFile)
		if err != nil {
			return nil, fmt.Errorf("topup: facilitator_token_file: %w", err)
		}
		c.facilitatorToken = strings.TrimSpace(string(token))
		if c.facilitatorToken == "" || len(c.facilitatorToken) > 4096 || strings.ContainsAny(c.facilitatorToken, "\r\n\t ") || !utf8.ValidString(c.facilitatorToken) {
			return nil, bad("facilitator_token_file (one token on one line)")
		}
	}
	c.OfferingMaxPrice, c.OfferingMaxTimeout = OfferingPriceCeiling, OfferingTimeoutCeiling
	if f.Offerings.MaxPrice != "" {
		n, ok := parseUnits(f.Offerings.MaxPrice, 6)
		if !ok || n < OfferingPriceFloor || n > OfferingPriceCeiling {
			return nil, bad("offerings.max_price (a USDC amount from " + formatUnits(OfferingPriceFloor, 6) + " to " + formatUnits(OfferingPriceCeiling, 6) + ")")
		}
		c.OfferingMaxPrice = n
	}
	if f.Offerings.MaxTimeoutSeconds != "" {
		n, err := strconv.ParseInt(f.Offerings.MaxTimeoutSeconds.String(), 10, 64)
		if err != nil || n < OfferingTimeoutFloor || n > OfferingTimeoutCeiling {
			return nil, bad(fmt.Sprintf("offerings.max_timeout_seconds (the facilitator's largest maxTimeoutSeconds, %d to %d)", OfferingTimeoutFloor, OfferingTimeoutCeiling))
		}
		c.OfferingMaxTimeout = n
	}
	return c, nil
}

// UseTestFacilitator sends every facilitator call to url through client:
// an httptest server stands in for the facilitator. Tests only; nothing in
// a config file or the environment sets it.
func (c *TopupConfig) UseTestFacilitator(client *http.Client, url string) {
	c.client, c.FacilitatorURL = client, strings.TrimRight(url, "/")
}

// httpClient is the facilitator client: through dial (the board's SSRF-safe
// dialer), no proxy, no redirect, bounded in time.
func (c *TopupConfig) httpClient(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Client {
	if c.client != nil {
		return c.client
	}
	t := &http.Transport{
		Proxy:                  nil,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  topupFacilitatorTimeout,
		MaxResponseHeaderBytes: 64 << 10,
		DisableKeepAlives:      true,
	}
	if dial != nil {
		t.DialContext = dial
	}
	return &http.Client{Transport: t, Timeout: topupFacilitatorTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// TopupRequirement is one payment requirement we issue: Amount credits to
// PayTo, good until Expires, with Quote binding it to one account.
type TopupRequirement struct {
	Amount  int64
	Quote   string
	Expires int64
}

// X402Requirement is any payment requirement this server issues on the
// configured network and asset: Amount atomic units to PayTo, good until
// Expires, Quote binding it, MaxTimeout its maxTimeoutSeconds. A top-up's
// pays the operator for TopupQuoteSeconds; an offering's (RFC 0017) pays
// the provider's linked wallet within its claim window. It is
// pay.X402Requirement (C202a: the x402 server wire lives in internal/pay).
type X402Requirement = pay.X402Requirement

// Rail is the configured network and asset as internal/pay's rail, which
// both payment wires (x402 and MPP) share.
func (c *TopupConfig) Rail() pay.Rail {
	return pay.Rail{Network: c.Network, Asset: c.Asset, AssetName: c.AssetName, AssetVersion: c.AssetVersion}
}

// topup is r as the general requirement: our receiving address.
func (c *TopupConfig) topup(r TopupRequirement) X402Requirement {
	return X402Requirement{PayTo: c.PayTo, Amount: r.Amount, Quote: r.Quote, Expires: r.Expires, MaxTimeout: TopupQuoteSeconds}
}

type topupRequirementWire = pay.X402RequirementWire

// wire is r as an x402 v2 PaymentRequirements object.
func (c *TopupConfig) wire(r X402Requirement) topupRequirementWire { return c.Rail().X402Accept(r) }

// PaymentRequired is the x402 v2 PaymentRequired object for r, and its
// base64 encoding for the PAYMENT-REQUIRED header.
func (c *TopupConfig) PaymentRequired(r TopupRequirement, resourceURL, description string) (map[string]any, string) {
	return c.PaymentRequiredFor(c.topup(r), resourceURL, description)
}

// PaymentRequiredFor is PaymentRequired for any requirement we issue.
func (c *TopupConfig) PaymentRequiredFor(r X402Requirement, resourceURL, description string) (map[string]any, string) {
	return c.PaymentRequiredWith(r, resourceURL, description, nil)
}

// PaymentRequiredWith is PaymentRequiredFor with the x402 v2 extensions
// object (e.g. "bazaar" discovery metadata); nil omits it.
func (c *TopupConfig) PaymentRequiredWith(r X402Requirement, resourceURL, description string, extensions map[string]any) (map[string]any, string) {
	return c.Rail().X402PaymentRequired(r, resourceURL, description, extensions)
}

// TopupPayment is a parsed x402 v2 payment payload for the exact EVM
// scheme: what the payer accepted, and its signed EIP-3009 authorization
// (pay.X402Payment).
type TopupPayment = pay.X402Payment

// TopupError is a refusal of a top-up payment: Code is the public error
// code, Definite says nothing can have settled (no money moved).
type TopupError struct {
	Code     string
	Reason   string // a sanitised facilitator reason, or ""
	Definite bool
}

func (e *TopupError) Error() string {
	if e.Reason != "" {
		return "topup: " + e.Code + " (" + e.Reason + ")"
	}
	return "topup: " + e.Code
}

// TopupFacilitatorUnavailable is the reason of a payment the facilitator
// could not verify (down, refusing our credentials, or answering nonsense):
// nothing moved, so the same payment may be presented again.
const TopupFacilitatorUnavailable = "facilitator_unavailable"

func topupRefusal(code string) error { return &TopupError{Code: code, Definite: true} }

// topupFromPay is a wire's refusal as a top-up refusal: the same code,
// Definite (a wire refuses before anything can move).
func topupFromPay(err error) error {
	if err == nil {
		return nil
	}
	var r *pay.Refusal
	if errors.As(err, &r) {
		return topupRefusal(r.Code)
	}
	return topupRefusal("payment_invalid")
}

// ParseTopupPayment reads a PAYMENT-SIGNATURE (or X-PAYMENT) header value:
// base64 of one x402 v2 payment payload for the exact scheme, every field
// well formed (pay.ParseX402Payment). Any malformation is payment_invalid.
func ParseTopupPayment(header string) (TopupPayment, error) {
	p, err := pay.ParseX402Payment(header)
	if err != nil {
		return TopupPayment{}, topupFromPay(err)
	}
	return p, nil
}

// Check holds p to exactly what r asked: the exact scheme on our network,
// our asset and token domain, our receiving address in both the accepted
// requirement and the authorization, the amount to the unit, the quote we
// issued, and an authorization valid now and for long enough to settle.
// The quote's binding to the account and its expiry are the caller's
// (it holds the key); r carries what it verified.
func (c *TopupConfig) Check(p TopupPayment, r TopupRequirement, now int64) error {
	return c.CheckRequirement(p, c.topup(r), now)
}

// CheckRequirement is Check for any requirement we issue: r.PayTo is the
// recipient the accepted requirement and the authorization must both name.
func (c *TopupConfig) CheckRequirement(p TopupPayment, r X402Requirement, now int64) error {
	return topupFromPay(c.Rail().CheckX402(p, r, now))
}

// facilitatorBody is the /verify and /settle request: our own requirement,
// never the payer's echo of it, and the payload rebuilt from what we parsed.
func (c *TopupConfig) facilitatorBody(p TopupPayment, r X402Requirement) ([]byte, error) {
	req := c.wire(r)
	payload := map[string]any{
		"x402Version": 2,
		"accepted":    req,
		"payload": x402ExactPayload{Signature: p.Signature, Authorization: x402AuthorizationWire{
			From: p.From.String(), To: p.To.String(), Value: strconv.FormatInt(p.Value, 10),
			ValidAfter: strconv.FormatInt(p.ValidAfter, 10), ValidBefore: strconv.FormatInt(p.ValidBefore, 10), Nonce: p.Nonce}},
	}
	if p.Resource != nil {
		payload["resource"] = p.Resource
	}
	return json.Marshal(map[string]any{"x402Version": 2, "paymentPayload": payload, "paymentRequirements": req})
}

// TopupSettlement is a settled payment.
type TopupSettlement struct {
	Transaction string // "0x" + 64 lowercase hex
	Payer       EVMAddress
}

// Settle has the facilitator verify p against r and then settle it. It
// holds no database connection: the caller records the outcome after.
// A *TopupError with Definite set means nothing settled (a refusal the
// facilitator gave, or a failure before settling was asked); without it
// the outcome is unknown (the facilitator was asked to settle and gave no
// usable answer), and no credit may follow until the operator resolves it.
func (c *TopupConfig) Settle(ctx context.Context, dial func(ctx context.Context, network, addr string) (net.Conn, error), p TopupPayment, r TopupRequirement) (TopupSettlement, error) {
	req := c.topup(r)
	if err := c.Verify(ctx, dial, p, req); err != nil {
		return TopupSettlement{}, err
	}
	return c.SettleVerified(ctx, dial, p, req)
}

// Verify has the facilitator verify p against r (POST /verify) and nothing
// more: no money moves. Every refusal is a Definite *TopupError; Reason
// TopupFacilitatorUnavailable says it could not be verified, so the same
// payment may be presented again.
func (c *TopupConfig) Verify(ctx context.Context, dial func(ctx context.Context, network, addr string) (net.Conn, error), p TopupPayment, r X402Requirement) error {
	body, err := c.facilitatorBody(p, r)
	if err != nil {
		return &TopupError{Code: "payment_unsettled", Reason: "encode", Definite: true}
	}
	var v struct {
		IsValid       *bool  `json:"isValid"`
		InvalidReason string `json:"invalidReason"`
		Payer         string `json:"payer"`
	}
	status, err := c.post(ctx, c.httpClient(dial), "/verify", body, &v)
	switch {
	case err != nil || status >= 500 || v.IsValid == nil:
		// Verifying moves nothing: the payer may present it again.
		return &TopupError{Code: "payment_unsettled", Reason: TopupFacilitatorUnavailable, Definite: true}
	case !*v.IsValid:
		return &TopupError{Code: "payment_rejected", Reason: sanitizeReason(v.InvalidReason), Definite: true}
	case v.Payer != "" && !samePayer(v.Payer, p.From):
		return &TopupError{Code: "payment_rejected", Reason: "payer_mismatch", Definite: true}
	}
	return nil
}

// SettleVerified has the facilitator settle p against r (POST /settle),
// which broadcasts the transfer; call it for a payment Verify accepted.
// Errors are Settle's.
func (c *TopupConfig) SettleVerified(ctx context.Context, dial func(ctx context.Context, network, addr string) (net.Conn, error), p TopupPayment, r X402Requirement) (TopupSettlement, error) {
	body, err := c.facilitatorBody(p, r)
	if err != nil {
		return TopupSettlement{}, &TopupError{Code: "payment_unsettled", Reason: "encode", Definite: true}
	}
	var s struct {
		Success     *bool  `json:"success"`
		ErrorReason string `json:"errorReason"`
		Transaction string `json:"transaction"`
		Network     string `json:"network"`
		Payer       string `json:"payer"`
	}
	status, err := c.post(ctx, c.httpClient(dial), "/settle", body, &s)
	switch {
	case err != nil || status >= 500 || s.Success == nil:
		return TopupSettlement{}, &TopupError{Code: "payment_unsettled", Reason: "settle_unknown"}
	case !*s.Success:
		return TopupSettlement{}, &TopupError{Code: "payment_rejected", Reason: sanitizeReason(s.ErrorReason), Definite: true}
	case !txHashRE.MatchString(s.Transaction) || (s.Network != "" && s.Network != c.Network) || (s.Payer != "" && !samePayer(s.Payer, p.From)):
		// It says settled, but not in terms we can record: money may have
		// moved, so this is for the operator, never a credit.
		return TopupSettlement{}, &TopupError{Code: "payment_unsettled", Reason: "settle_malformed"}
	}
	return TopupSettlement{Transaction: strings.ToLower(s.Transaction), Payer: p.From}, nil
}

func samePayer(s string, want EVMAddress) bool {
	a, ok := ParseEVMAddress(s)
	if !ok {
		// Mixed case that is not a valid checksum: compare it lowered.
		a, ok = ParseEVMAddress(strings.ToLower(s))
	}
	return ok && a == want
}

func sanitizeReason(s string) string {
	if reasonRE.MatchString(s) {
		return s
	}
	return "unspecified"
}

// post sends body to the facilitator's path and decodes a bounded JSON
// answer into dst; it returns the HTTP status. A 4xx answer is decoded too:
// facilitators state refusals that way.
func (c *TopupConfig) post(ctx context.Context, client *http.Client, path string, body []byte, dst any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, topupFacilitatorTimeout)
	defer cancel()
	u, err := url.Parse(c.FacilitatorURL + path)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", useragent.For("x402-topup"))
	if c.facilitatorToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.facilitatorToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, topupFacilitatorBytes+1))
	if err != nil {
		return resp.StatusCode, err
	}
	if len(raw) > topupFacilitatorBytes || resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return resp.StatusCode, errors.New("topup: facilitator answer too large or redirected")
	}
	if !boundedJSON(raw, topupFacilitatorBytes, dst) {
		return resp.StatusCode, errors.New("topup: facilitator answer is not a JSON object")
	}
	return resp.StatusCode, nil
}

// FormatUSDC is credits as a USDC amount: 100000 is "0.1".
func FormatUSDC(credits int64) string { return formatUnits(credits, 6) }
