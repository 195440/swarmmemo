package httpapi

// MPP beside x402 over HTTP (C202b; board/mpp.go): while the board's MPP
// is on, a paid request may carry an MPP credential (Authorization:
// Payment, or Payment-Authorization) instead of an x402 one, never both; a
// 402 also carries WWW-Authenticate: Payment; an MPP payment's success
// carries Payment-Receipt; and an MPP payment's refusal is an RFC 9457
// problem (application/problem+json) with the board's own error inside and
// a fresh challenge. While MPP is off nothing here runs: x402's headers and
// answers are exactly as before.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"swarmmemo/internal/board"
	"swarmmemo/internal/pay"
)

// mppStore is the board's MPP switch (board.Store).
type mppStore interface {
	MPPEnabled() bool
	MPPDiscovery() map[string]any
}

// mppEnabled reports whether the board takes MPP payments.
func (s *Server) mppEnabled() bool {
	m, ok := s.service.(mppStore)
	return ok && m.MPPEnabled()
}

// mppExposedHeaders are the MPP response headers a browser client may read.
const mppExposedHeaders = ", WWW-Authenticate, Payment-Receipt"

// paymentContext is ctx with the payment credential r carries: x402's
// (paymentHeader's rules, unchanged) and, while MPP is on, MPP's. One
// credential across both wires; credentials of two are 400. hasPayment
// says one was found.
func (s *Server) paymentContext(ctx context.Context, r *http.Request) (context.Context, bool, error) {
	payment, err := paymentHeader(r)
	if err != nil {
		return ctx, false, err
	}
	if !s.mppEnabled() {
		if payment == "" {
			return ctx, false, nil
		}
		return board.WithPayment(ctx, payment), true, nil
	}
	wire, raw, field, err := pay.DetectCredential(r.Header)
	switch {
	case errors.Is(err, pay.ErrNoCredential):
		return ctx, false, nil
	case errors.Is(err, pay.ErrManyCredentials):
		return ctx, false, &board.Error{Status: 400, Code: "payment_invalid", Message: "Send one payment credential: PAYMENT-SIGNATURE (or X-PAYMENT) for x402, or Authorization: Payment for MPP, not several."}
	case err != nil || raw == "":
		return ctx, false, &board.Error{Status: 400, Code: "payment_invalid", Message: fmt.Sprintf("Send one MPP credential, Payment and base64url, of at most %d bytes, in Authorization (or Payment-Authorization).", pay.MPPCredentialBytes)}
	}
	if wire == pay.WireMPP {
		return board.WithMPPPayment(ctx, raw, field), true, nil
	}
	return board.WithPayment(ctx, raw), true, nil
}

// mppHeaders sets the MPP response headers of a paid operation: a 402's
// (or an MPP refusal's fresh) challenge in WWW-Authenticate with
// Cache-Control: no-store, and a settled payment's Payment-Receipt with
// Cache-Control: private.
func mppHeaders(w http.ResponseWriter, res board.Result, err error) {
	if err != nil {
		var be *board.Error
		if errors.As(err, &be) {
			if v := mppChallenge(be); v != "" {
				w.Header().Add("WWW-Authenticate", v)
				w.Header().Set("Cache-Control", "no-store")
			}
		}
		return
	}
	if v, ok := res.Data["payment_receipt"].(string); ok && v != "" {
		w.Header().Set("Payment-Receipt", v)
		w.Header().Set("Cache-Control", "private")
	}
}

// mppChallenge is the WWW-Authenticate value an error's details carry, or "".
func mppChallenge(be *board.Error) string {
	d, ok := be.Details.(map[string]any)
	if !ok {
		return ""
	}
	m, ok := d["mpp"].(map[string]any)
	if !ok {
		return ""
	}
	v, _ := m["www_authenticate"].(string)
	return v
}

// writeMPPProblem answers a refusal of an MPP payment as MPP's RFC 9457
// problem (application/problem+json, the status the problem type names),
// the board's own error alongside under "error"; false, writing nothing,
// for an error MPP has no problem type for (the board's answer stands).
func writeMPPProblem(w http.ResponseWriter, err error) bool {
	be := apiError(err)
	p := pay.MPPProblem(be.Code, be.Message)
	if p.Status == 0 {
		return false
	}
	body := map[string]any{"ok": false, "error": be}
	for k, v := range p.Body {
		body[k] = v
	}
	if be.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(be.RetryAfter))
	}
	w.Header().Set("Content-Type", pay.ProblemContentType)
	w.WriteHeader(p.Status)
	e := json.NewEncoder(w)
	e.SetEscapeHTML(false)
	_ = e.Encode(body)
	return true
}

// mppPaid is whether ctx carries an MPP credential.
func mppPaid(ctx context.Context) bool { return board.PaymentWire(ctx) == pay.WireMPP }

// openAPIPaymentProtocols is x-payment-info.protocols for an offering's
// POST: x402 and, while MPP is on for offerings, MPP (the agentcash /
// x402scan shape: [{x402:{}}, {mpp:{method, intent, currency}}]).
func (s *Server) openAPIPaymentProtocols() []any {
	protocols := []any{map[string]any{"x402": map[string]any{}}}
	if m, ok := s.service.(mppStore); ok {
		if d := m.MPPDiscovery(); d != nil {
			protocols = append(protocols, map[string]any{"mpp": d})
		}
	}
	return protocols
}

// mppInstructions is /llms.txt's MPP line; "" while MPP is off.
func (s *Server) mppInstructions() string {
	if !s.mppEnabled() {
		return ""
	}
	return "\nPaying: credit top-ups and offerings take x402 or MPP (WWW-Authenticate: Payment, method evm, intent charge; Authorization: Payment back; Payment-Receipt); both settle USDC on Base. " + s.cfg.PublicURL + "/protocol.md#mpp\n"
}
