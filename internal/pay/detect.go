package pay

// Helpers for callers that hold no rail (the HTTP adapter): which wire a
// request's credential belongs to, the MPP credential as a value
// data.payment can carry, and MPP's problem table.

import "net/http"

// DetectCredential is Detect over both wires for a caller that holds no
// rail: the wire name (WireX402 or WireMPP), the raw credential and the
// header it came in. Credentials of both wires, or two of one, are
// ErrManyCredentials (answer 400); none is ErrNoCredential; an oversized
// credential, or a Payment-Authorization value of another scheme, is a
// *Refusal (CodeInvalid). Authorization with another scheme (Bearer) is
// not a payment.
func DetectCredential(h http.Header) (wire, raw, field string, err error) {
	w, raw, field, err := Detect(h, x402Wire{}, mppWire{})
	if err != nil {
		return "", "", "", err
	}
	return w.Name(), raw, field, nil
}

// MPPToken is the credential in an Authorization-style value whose scheme
// is Payment ("Payment " + base64url), as a command's data.payment carries
// an MPP credential; false for anything else (an x402 payload is base64
// with no space, so the two never collide).
func MPPToken(v string) (string, bool) {
	tok, ok := paymentToken(v)
	return tok, ok && tok != ""
}

// ParseMPPCredential is the MPP wire's Parse for a caller that holds no
// wire: it needs none (a credential sealed at intake is read again at
// settle this way, whatever MPP's flag says by then). Parsing binds
// nothing: Bound did, at intake.
func ParseMPPCredential(raw, field string) (Auth, error) { return parseMPP(raw, field) }

// MPPProblem is MPP's RFC 9457 answer for a board payment error code
// (mppWire.Problem); the zero Problem for a code it has no type for.
func MPPProblem(code, detail string) Problem { return mppWire{}.Problem(code, detail) }

// MPPAuthorizationField is the header an MPP credential answers in when
// the challenge names none, and the field a data.payment credential
// counts as.
const MPPAuthorizationField = headerAuthorization
