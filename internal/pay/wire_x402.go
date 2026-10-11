package pay

// x402Wire is the x402 v2 server wire behind the Wire interface: the code
// in x402.go (moved from services unchanged), with the header names the
// HTTP API already uses.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
)

// x402 header names (x402 v2 HTTP transport; X-PAYMENT is v1's name, which
// many clients still send).
const (
	headerX402Required  = "PAYMENT-REQUIRED"
	headerX402Signature = "PAYMENT-SIGNATURE"
	headerX402V1Payment = "X-PAYMENT"
	headerX402Response  = "PAYMENT-RESPONSE"
)

type x402Wire struct{ rail Rail }

// NewX402 is the x402 wire on rail.
func NewX402(rail Rail) Wire { return x402Wire{rail: rail} }

func (w x402Wire) Name() string { return WireX402 }

func (w x402Wire) requirement(a Ask) X402Requirement {
	return X402Requirement{PayTo: a.PayTo, Amount: a.Amount, Quote: a.Quote, Expires: a.Expires, MaxTimeout: a.MaxTimeout}
}

// Challenge sets PAYMENT-REQUIRED and returns the PaymentRequired object:
// byte for byte what services.TopupConfig.PaymentRequiredWith answers.
func (w x402Wire) Challenge(a Ask, h http.Header) (map[string]any, error) {
	body, header := w.rail.X402PaymentRequired(w.requirement(a), a.Resource, a.Description, a.Extensions)
	h.Set(headerX402Required, header)
	return body, nil
}

// Credential is PAYMENT-SIGNATURE or X-PAYMENT, one value, at most
// X402PaymentBytes (httpapi's paymentHeader rule).
func (w x402Wire) Credential(h http.Header) (string, string, error) {
	var values, fields []string
	for _, name := range []string{headerX402Signature, headerX402V1Payment} {
		for _, v := range h.Values(name) {
			values, fields = append(values, v), append(fields, http.CanonicalHeaderKey(name))
		}
	}
	switch {
	case len(values) == 0:
		return "", "", ErrNoCredential
	case len(values) > 1:
		return "", "", ErrManyCredentials
	case len(values[0]) > X402PaymentBytes:
		return "", "", refusal(CodeInvalid)
	}
	return values[0], fields[0], nil
}

// Parse is ParseX402Payment.
func (w x402Wire) Parse(raw, field string) (Auth, error) {
	p, err := ParseX402Payment(raw)
	if err != nil {
		return Auth{}, err
	}
	return Auth{Wire: WireX402, From: p.From, To: p.To, Value: p.Value, ValidAfter: p.ValidAfter, ValidBefore: p.ValidBefore,
		Nonce: p.Nonce, Signature: p.Signature, Quote: p.Quote, Ref: p.Quote, Field: field, x402: &p}, nil
}

// Bound is CheckX402 against a's requirement. A credential of another wire
// is not an x402 payment.
func (w x402Wire) Bound(x Auth, a Ask, now int64) error {
	if x.Wire != WireX402 || x.x402 == nil {
		return refusal(CodeInvalid)
	}
	return w.rail.CheckX402(*x.x402, w.requirement(a), now)
}

// Receipt sets PAYMENT-RESPONSE: base64 of the SettlementResponse, the same
// object board.paymentResponse writes for a credited top-up.
func (w x402Wire) Receipt(s Settlement, h http.Header) error {
	if !txHashRE.MatchString(s.Transaction) {
		return errors.New("pay: receipt without a transaction")
	}
	raw, err := json.Marshal(map[string]any{"success": true, "transaction": s.Transaction, "network": w.rail.Network, "payer": s.Payer.String()})
	if err != nil {
		return err
	}
	h.Set(headerX402Response, base64.StdEncoding.EncodeToString(raw))
	return nil
}

// Problem: x402 defines no error body of its own; the board's stands.
func (w x402Wire) Problem(code, detail string) Problem { return Problem{} }
