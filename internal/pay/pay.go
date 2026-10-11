// Package pay is SwarmMemo's one payment rail with two wire protocols.
//
// The rail is what moves the money: one EIP-3009 transferWithAuthorization
// of USDC on Base, verified and settled by the operator's facilitator
// (services.TopupConfig.Verify and SettleVerified, which rebuild the
// facilitator request from parsed fields, never from the payer's echo). A
// wire is how a 402 is written: x402 v2 (PAYMENT-REQUIRED and
// PAYMENT-SIGNATURE) or MPP, the "Payment" HTTP authentication scheme
// (draft-httpauth-payment-01) with the evm charge method and
// type="authorization" (draft-evm-charge-00). Both wires carry the same
// signed authorization, so both settle on the same rail; MPP adds one rule,
// nonce = keccak256(challenge.id ‖ realm).
//
// The package is pure: no database, no network, no clock (callers pass
// now). It never holds a key: the MPP challenge secret is derived from the
// caller's quote key and only computes HMACs.
package pay

import (
	"errors"
	"net/http"
)

// Wire names, as recorded with each payment.
const (
	WireX402 = "x402"
	WireMPP  = "mpp"
)

// Ask is what we ask for, whatever the wire: Amount atomic units of the
// rail's asset to PayTo, good until Expires (Unix seconds).
type Ask struct {
	PayTo  EVMAddress
	Amount int64
	// Quote is the caller's own binding (top-ups and offerings issue
	// "EXPIRES.SALT.MAC" over every term): x402 carries it in
	// extra.quote, MPP in opaque {"q": Quote}, which the challenge id
	// covers. Either way the payer echoes it and the caller checks it.
	Quote   string
	Expires int64
	// MaxTimeout is x402's maxTimeoutSeconds.
	MaxTimeout int64
	// Resource and Description describe the priced resource: x402's
	// resource object; MPP shows Description (display only, unbound).
	Resource, Description string
	// Body is the request body the payment is bound to: MPP's digest
	// parameter (RFC 9530 sha-256). nil binds no body.
	Body []byte
	// Extensions is x402's v2 extensions object (bazaar discovery); nil
	// omits it. MPP ignores it.
	Extensions map[string]any
	// PaymentAuthorization asks an MPP client for its credential in the
	// Payment-Authorization header, so Authorization keeps a bearer token.
	PaymentAuthorization bool
}

// Auth is what a payer gave, whatever the wire: one EIP-3009
// authorization, and how to find the challenge it answers.
type Auth struct {
	Wire                    string // WireX402 or WireMPP
	From, To                EVMAddress
	Value                   int64
	ValidAfter, ValidBefore int64
	Nonce                   string // "0x" + 64 lowercase hex: the replay key on chain and in our tables
	Signature               string // "0x" + 130 lowercase hex
	// Quote is the caller's quote the payer echoed (see Ask.Quote); the
	// caller checks it before Bound.
	Quote string
	// Ref names the challenge: x402's quote, MPP's challenge id.
	Ref string
	// Field is the header the credential came in (canonical form).
	Field string

	x402 *X402Payment
	mpp  *mppEcho
}

// Payment is the authorization as an x402 exact payment, ready for the
// facilitator (services.TopupConfig.Verify and SettleVerified, whose body
// is our own requirement plus these authorization fields). For an x402
// credential it is exactly what ParseX402Payment read; for MPP it carries
// the authorization and the echoed quote.
func (a Auth) Payment() X402Payment {
	if a.x402 != nil {
		return *a.x402
	}
	return X402Payment{Scheme: "exact", Quote: a.Quote, From: a.From, To: a.To, Value: a.Value,
		ValidAfter: a.ValidAfter, ValidBefore: a.ValidBefore, Nonce: a.Nonce, Signature: a.Signature}
}

// Settlement is a settled payment, for the receipt.
type Settlement struct {
	Transaction string     // "0x" + 64 hex
	Payer       EVMAddress // the authorization's from
	Ref         string     // Auth.Ref
	At          int64      // Unix seconds
}

// Problem is a wire's error answer for a board error code: Status and an
// RFC 9457 body. The zero Problem means the wire adds nothing: the board's
// own error stands (x402).
type Problem struct {
	Status int
	Body   map[string]any
}

// Wire is one way of writing the 402 exchange.
type Wire interface {
	// Name is WireX402 or WireMPP.
	Name() string
	// Challenge adds the wire's challenge for a to h and returns the body
	// the wire would answer the 402 with.
	Challenge(a Ask, h http.Header) (map[string]any, error)
	// Credential is the one credential of this wire a request carries, and
	// the header it came in: ErrNoCredential when there is none,
	// ErrManyCredentials when there are several.
	Credential(h http.Header) (raw, field string, err error)
	// Parse reads a credential strictly and boundedly; every malformation
	// is a *Refusal with CodeInvalid.
	Parse(raw, field string) (Auth, error)
	// Bound holds x to exactly what a asks, at now: the challenge binding,
	// payTo, amount and asset, the nonce rule, and the time window. The
	// caller has already checked x.Quote against its key and rebuilt a
	// from it.
	Bound(x Auth, a Ask, now int64) error
	// Receipt adds the wire's receipt for s to h.
	Receipt(s Settlement, h http.Header) error
	// Problem is the wire's error answer for a board error code.
	Problem(code, detail string) Problem
}

var (
	// ErrNoCredential: the request carries no credential of this wire.
	ErrNoCredential = errors.New("pay: no payment credential")
	// ErrManyCredentials: the request carries more than one credential;
	// answer 400 (both specs).
	ErrManyCredentials = errors.New("pay: more than one payment credential")
)

// Detect is the one credential a request carries across wires: the wire
// it belongs to, the raw value and its header. A request with credentials
// of two wires, or two of one, is ErrManyCredentials; none is
// ErrNoCredential.
func Detect(h http.Header, wires ...Wire) (Wire, string, string, error) {
	var found Wire
	var raw, field string
	for _, w := range wires {
		r, f, err := w.Credential(h)
		switch {
		case errors.Is(err, ErrNoCredential):
			continue
		case err != nil:
			return nil, "", "", err
		case found != nil:
			return nil, "", "", ErrManyCredentials
		}
		found, raw, field = w, r, f
	}
	if found == nil {
		return nil, "", "", ErrNoCredential
	}
	return found, raw, field, nil
}

// Offered is one priced resource's 402 on every wire: each wire's body by
// name; the challenges themselves are in the header set Offer wrote to.
type Offered struct {
	Bodies map[string]map[string]any
}

// Body is the 402 body to send: x402's PaymentRequired object when x402
// is offered (x402 clients read it there), else the first other wire's.
func (o Offered) Body() map[string]any {
	if b, ok := o.Bodies[WireX402]; ok {
		return b
	}
	for _, b := range o.Bodies {
		return b
	}
	return nil
}

// Offer writes one priced resource's challenge on every wire to h (x402's
// PAYMENT-REQUIRED, MPP's WWW-Authenticate and Cache-Control: no-store)
// and returns their bodies. It is all or nothing: on an error h is left as
// it was.
func Offer(a Ask, h http.Header, wires ...Wire) (Offered, error) {
	o := Offered{Bodies: map[string]map[string]any{}}
	staged := http.Header{}
	for _, w := range wires {
		body, err := w.Challenge(a, staged)
		if err != nil {
			return Offered{}, err
		}
		o.Bodies[w.Name()] = body
	}
	for k, vs := range staged {
		if k == "Cache-Control" || k == "Payment-Required" {
			h.Del(k)
		}
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	return o, nil
}
