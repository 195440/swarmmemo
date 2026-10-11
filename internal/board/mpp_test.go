package board

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	paywire "swarmmemo/internal/pay"
	"swarmmemo/internal/services"
)

// MPP beside x402 (C202b) on the real engine: credentials signed for real
// (EIP-3009, nonce = keccak256(challenge.id ‖ realm)) settle through a
// stand-in facilitator that checks what the x402 reference facilitator
// does, every refusal comes before the facilitator, and with MPP off
// nothing changes.

const mppRealm = "swarmmemo.com" // the test signer's SERVICE_ID

// mppFacilitator stands in for the facilitator: /verify and /settle accept
// an authorization whose to is the requirement's payTo, whose value is its
// amount and whose nonce is 32 bytes, echo its payer, and record each
// nonce they were shown.
type mppFacilitator struct {
	mu        sync.Mutex
	verifies  int
	settles   int
	seen      []string
	tx        int
	lastPayTo string
}

func (f *mppFacilitator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		PaymentPayload struct {
			Payload struct {
				Authorization map[string]string `json:"authorization"`
			} `json:"payload"`
		} `json:"paymentPayload"`
		PaymentRequirements map[string]any `json:"paymentRequirements"`
	}
	_ = json.Unmarshal(body, &req)
	au := req.PaymentPayload.Payload.Authorization
	payTo := fmt.Sprint(req.PaymentRequirements["payTo"])
	ok := strings.EqualFold(au["to"], payTo) && au["value"] == fmt.Sprint(req.PaymentRequirements["amount"]) && len(au["nonce"]) == 66
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, r.URL.Path+" "+au["nonce"])
	f.lastPayTo = payTo
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/verify":
		f.verifies++
		fmt.Fprintf(w, `{"isValid":%t,"payer":"%s"}`, ok, au["from"])
	case "/settle":
		f.settles++
		if !ok {
			_, _ = w.Write([]byte(`{"success":false,"errorReason":"invalid"}`))
			return
		}
		f.tx++
		fmt.Fprintf(w, `{"success":true,"transaction":"0x%064x","network":"eip155:8453","payer":"%s"}`, f.tx, au["from"])
	default:
		http.NotFound(w, r)
	}
}

func (f *mppFacilitator) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.verifies, f.settles
}

func mppConfig(t *testing.T, h http.Handler) *services.TopupConfig {
	t.Helper()
	cfg, err := services.ParseTopupConfig([]byte(`{"schema":1,"enabled":true,"network":"eip155:8453","asset":"`+topupUSDC+`","asset_name":"USD Coin","asset_version":"2",
"pay_to":"`+topupPayTo+`","facilitator_url":"https://facilitator.example.com"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg.UseTestFacilitator(srv.Client(), srv.URL)
	return cfg
}

// mppSigner is a fresh payer wallet.
func mppSigner(t *testing.T) services.X402Signer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payer.key")
	if _, err := services.WriteX402Key(path); err != nil {
		t.Fatal(err)
	}
	s, err := services.LoadX402Signer(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// mppChallengeOf is the WWW-Authenticate value a 402's details carry.
func mppChallengeOf(t *testing.T, e *Error) string {
	t.Helper()
	d, _ := e.Details.(map[string]any)
	m, _ := d["mpp"].(map[string]any)
	v, _ := m["www_authenticate"].(string)
	if v == "" {
		t.Fatalf("no MPP challenge in %+v", e)
	}
	return v
}

func mppParse(t *testing.T, challenge string) paywire.MPPChallenge {
	t.Helper()
	cs, err := paywire.ParseMPPChallenges([]string{challenge})
	if err != nil || len(cs) != 1 {
		t.Fatalf("challenge %q: %v %v", challenge, cs, err)
	}
	return cs[0]
}

// mppOpaqueQuote is the quote a challenge's opaque carries.
func mppOpaqueQuote(t *testing.T, c paywire.MPPChallenge) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(c.Params["opaque"])
	var m map[string]string
	if err != nil || json.Unmarshal(raw, &m) != nil {
		t.Fatalf("opaque %q", c.Params["opaque"])
	}
	return m["q"]
}

// mppPay answers challenge as an MPP client does: it signs the EIP-3009
// authorization the request asks for, nonce keccak256(id ‖ realm), valid
// until validBefore, and returns the Authorization value.
func mppPay(t *testing.T, signer services.X402Signer, cfg *services.TopupConfig, challenge string, validBefore int64) string {
	t.Helper()
	c := mppParse(t, challenge)
	req, err := c.Request()
	if err != nil {
		t.Fatal(err)
	}
	to, ok := services.ParseEVMAddress(req["recipient"].(string))
	value, err := strconv.ParseInt(req["amount"].(string), 10, 64)
	if !ok || err != nil {
		t.Fatalf("request %v", req)
	}
	nonceHex := c.Nonce()
	var nonce [32]byte
	if b, err := hex.DecodeString(nonceHex[2:]); err != nil || copy(nonce[:], b) != 32 {
		t.Fatal("nonce")
	}
	domain := services.EIP712Domain{Name: cfg.AssetName, Version: cfg.AssetVersion, ChainID: 8453, VerifyingContract: cfg.Asset}
	sig, err := signer.SignAuthorization(domain, services.TransferAuthorization{From: signer.Address(), To: to, Value: value, ValidBefore: validBefore, Nonce: nonce})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Credential(paywire.X402AuthorizationWire{From: signer.Address().String(), To: to.String(), Value: strconv.FormatInt(value, 10),
		ValidAfter: "0", ValidBefore: strconv.FormatInt(validBefore, 10), Nonce: nonceHex}, "0x"+hex.EncodeToString(sig[:]), "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// mppHeader is ctx carrying v ("Payment …") in field.
func mppHeader(v, field string) context.Context {
	return WithMPPPayment(testContext, strings.TrimPrefix(v, "Payment "), field)
}

// mppEdit is credential v with its echoed challenge changed by edit.
func mppEdit(t *testing.T, v string, edit func(challenge map[string]any)) string {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(v, "Payment "))
	var m map[string]any
	if err != nil || json.Unmarshal(raw, &m) != nil {
		t.Fatal("credential")
	}
	edit(m["challenge"].(map[string]any))
	out, _ := json.Marshal(m)
	return "Payment " + base64.RawURLEncoding.EncodeToString(out)
}

func mppTopupStore(t *testing.T, cfg *services.TopupConfig, mpp bool) *Store {
	t.Helper()
	if _, _, ok := ledgerTestOverride(); ok {
		t.Skip("the ledger mode is overridden for the whole suite")
	}
	s, err := Open(filepath.Join(t.TempDir(), "mpp.sqlite"), Config{ServiceID: mppRealm, Features: Features{Ledger: LedgerOn, MPP: mpp}, Topup: cfg, EchoSimulate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.now = func() time.Time { return time.Unix(testTime, 0) }
	return s
}

func mppQuote(t *testing.T, s *Store, key ed25519.PrivateKey, amount int64) *Error {
	t.Helper()
	_, e := topupExec(s, testContext, signed(key, Command{Operation: "credits.topup", Amount: amount}))
	if e == nil || e.Code != "payment_required" || e.Status != 402 {
		t.Fatalf("quote: want 402 payment_required, got %+v", e)
	}
	return e
}

// x402Accepted is the requirement in a 402's PAYMENT-REQUIRED value.
func x402Accepted(t *testing.T, e *Error) map[string]any {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(e.Details.(map[string]any)["payment_required"].(string))
	var required struct {
		Accepts []map[string]any `json:"accepts"`
	}
	if err != nil || json.Unmarshal(raw, &required) != nil || len(required.Accepts) != 1 {
		t.Fatalf("PAYMENT-REQUIRED: %v", err)
	}
	return required.Accepts[0]
}

func TestMPPTopupEndToEnd(t *testing.T) {
	f := &mppFacilitator{}
	cfg := mppConfig(t, f)
	s := mppTopupStore(t, cfg, true)
	key := keyFor(202)
	register(t, s, key)
	signer := mppSigner(t)
	caps := s.TopupCapabilities()
	if p, _ := caps["protocols"].([]string); len(p) != 2 || p[1] != "mpp" || caps["mpp"].(map[string]any)["realm"] != mppRealm {
		t.Fatalf("capabilities %v", caps)
	}

	// One 402, both wires, one quote.
	e := mppQuote(t, s, key, 1_000_000)
	challenge := mppChallengeOf(t, e)
	c := mppParse(t, challenge)
	req, _ := c.Request()
	accepted := x402Accepted(t, e)
	if c.Params["realm"] != mppRealm || c.Params["method"] != "evm" || c.Params["intent"] != "charge" || req["recipient"] != topupPayTo || req["amount"] != "1000000" ||
		mppOpaqueQuote(t, c) != accepted["extra"].(map[string]any)["quote"] || !strings.Contains(e.Message, "Or pay with MPP") {
		t.Fatalf("challenge %v request %v x402 %v", c.Params, req, accepted)
	}

	// Pay over MPP, in Authorization.
	cred := mppPay(t, signer, cfg, challenge, testTime+300)
	cmd := signed(key, Command{Operation: "credits.topup", Amount: 1_000_000, RequestID: "mpp-topup-1"})
	res, e := topupExec(s, mppHeader(cred, "Authorization"), cmd)
	if e != nil {
		t.Fatalf("pay: %+v", e)
	}
	topup := res.Data["topup"].(map[string]any)
	if topup["state"] != "credited" || topup["protocol"] != "mpp" || topup["payer"] != signer.Address().String() || res.Data["payment_response"] != nil {
		t.Fatalf("receipt %v", res.Data)
	}
	raw, err := base64.RawURLEncoding.DecodeString(res.Data["payment_receipt"].(string))
	var receipt map[string]any
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt["challengeId"] != c.Params["id"] || receipt["reference"] != topup["transaction"] ||
		receipt["status"] != "success" || receipt["method"] != "evm" || receipt["chainId"] != float64(8453) || receipt["timestamp"] != time.Unix(testTime, 0).UTC().Format(time.RFC3339) {
		t.Fatalf("Payment-Receipt %s", raw)
	}
	// The row records the wire and the challenge; the nonce is the
	// challenge's, and the facilitator saw exactly it.
	var protocol, ref, nonce string
	if err = s.db.QueryRow("SELECT protocol,payment_ref,auth_nonce FROM credit_topups WHERE id=?", topup["id"]).Scan(&protocol, &ref, &nonce); err != nil ||
		protocol != "mpp" || ref != c.Params["id"] || nonce != c.Nonce() {
		t.Fatalf("row %s %s %s %v", protocol, ref, nonce, err)
	}
	if v, st := f.counts(); v != 1 || st != 1 || f.seen[0] != "/verify "+c.Nonce() || f.seen[1] != "/settle "+c.Nonce() || !strings.EqualFold(f.lastPayTo, topupPayTo) {
		t.Fatalf("facilitator %d %d %v", v, st, f.seen)
	}
	if got := paidBalance(t, s, key); got != 1_000_000 {
		t.Fatalf("paid balance %d", got)
	}
	// An exact retry: the same top-up and receipt; nothing settles again.
	again, e := topupExec(s, mppHeader(cred, "Authorization"), cmd)
	if e != nil || again.Data["payment_receipt"] != res.Data["payment_receipt"] {
		t.Fatalf("retry %+v %v", e, again.Data)
	}

	// Refusals, all before the facilitator, each with a fresh challenge.
	refused := func(name string, ctx context.Context, c Command, code string) {
		t.Helper()
		v0, _ := f.counts()
		_, e := topupExec(s, ctx, c)
		if e == nil || e.Code != code {
			t.Fatalf("%s: want %s, got %+v", name, code, e)
		}
		if v, _ := f.counts(); v != v0 {
			t.Fatalf("%s reached the facilitator", name)
		}
		if fresh := mppChallengeOf(t, e); fresh == challenge || mppParse(t, fresh).Params["id"] == c.RequestID {
			t.Fatalf("%s: no fresh challenge", name)
		}
	}
	refused("replayed", mppHeader(cred, "Authorization"), signed(key, Command{Operation: "credits.topup", Amount: 1_000_000}), "payment_replayed")
	// Bound to another amount: the quote is for 200000.
	small := mppPay(t, signer, cfg, mppChallengeOf(t, mppQuote(t, s, key, 200_000)), testTime+300)
	refused("other amount", mppHeader(small, "Authorization"), signed(key, Command{Operation: "credits.topup", Amount: 300_000}), "payment_mismatch")
	// Bound to another quote: opaque swapped for another challenge's.
	other := mppParse(t, mppChallengeOf(t, mppQuote(t, s, key, 200_000)))
	swapped := mppEdit(t, small, func(ch map[string]any) { ch["opaque"] = other.Params["opaque"] })
	refused("other quote", mppHeader(swapped, "Authorization"), signed(key, Command{Operation: "credits.topup", Amount: 200_000}), "payment_challenge")
	// Another agent's quote.
	stranger := keyFor(203)
	register(t, s, stranger)
	refused("other agent", mppHeader(small, "Authorization"), signed(stranger, Command{Operation: "credits.topup", Amount: 200_000}), "payment_mismatch")
	// The header the challenge did not name.
	refused("other field", mppHeader(small, "Payment-Authorization"), signed(key, Command{Operation: "credits.topup", Amount: 200_000}), "payment_challenge")
	// A request changed under the id.
	tampered := mppEdit(t, small, func(ch map[string]any) { ch["request"] = c.Params["request"] })
	refused("other request", mppHeader(tampered, "Authorization"), signed(key, Command{Operation: "credits.topup", Amount: 200_000}), "payment_mismatch")
	refused("malformed", WithMPPPayment(testContext, "bm90IGpzb24", "Authorization"), signed(key, Command{Operation: "credits.topup", Amount: 200_000}), "payment_invalid")
	// Expired: a challenge issued 250 seconds earlier (good for 300), paid
	// 60 seconds after it lapsed.
	s.now = func() time.Time { return time.Unix(testTime-250, 0) }
	stale := mppPay(t, signer, cfg, mppChallengeOf(t, mppQuote(t, s, key, 200_000)), testTime+200)
	s.now = func() time.Time { return time.Unix(testTime+60, 0) }
	refused("expired", mppHeader(stale, "Authorization"), signed(key, Command{Operation: "credits.topup", Amount: 200_000}), "payment_expired")
	s.now = func() time.Time { return time.Unix(testTime, 0) }
	if _, st := f.counts(); st != 1 {
		t.Fatalf("%d settlements", st)
	}

	// The same credential in data.payment (signed commands, MCP).
	res = run(t, s, signed(key, Command{Operation: "credits.topup", Amount: 200_000, Data: `{"schema":1,"payment":"` + small + `"}`}))
	if res.Data["topup"].(map[string]any)["protocol"] != "mpp" || res.Data["payment_receipt"] == nil {
		t.Fatalf("data.payment MPP %v", res.Data)
	}
	// A header and a different data.payment.
	fails(t, s, signed(key, Command{Operation: "credits.topup", Amount: 200_000, Data: `{"schema":1,"payment":"` + small + `"}`}), "payment_replayed")
	if _, e := topupExec(s, WithPayment(testContext, "eA=="), signed(key, Command{Operation: "credits.topup", Amount: 200_000, Data: `{"schema":1,"payment":"` + small + `"}`})); e == nil || e.Code != "payment_invalid" {
		t.Fatalf("header and data differ: %+v", e)
	}

	// x402 still works beside it, unchanged.
	x := x402Accepted(t, mppQuote(t, s, key, 100_000))
	res = run(t, s, paidCommand(key, 100_000, pay(t, x, 9, func(a map[string]any) { a["from"] = signer.Address().String() })))
	if tu := res.Data["topup"].(map[string]any); tu["state"] != "credited" || tu["protocol"] != nil || res.Data["payment_response"] == nil || res.Data["payment_receipt"] != nil {
		t.Fatalf("x402 beside MPP %v", res.Data)
	}
	if n := sqlCount(t, s, "SELECT count(*) FROM credit_topups WHERE protocol='x402' AND payment_ref=''"); n != 1 {
		t.Fatalf("%d x402 rows", n)
	}
	if got := paidBalance(t, s, key); got != 1_300_000 {
		t.Fatalf("paid balance %d", got)
	}
}

func mppOfferingStore(t *testing.T, cfg *services.TopupConfig, mpp bool) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "offerings.sqlite"), Config{ServiceID: mppRealm, Offerings: cfg, Features: Features{MPP: mpp}, EchoSimulate: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.stopOfferings(); _ = s.Close() })
	s.now = func() time.Time { return time.Unix(testTime, 0) }
	return s
}

func offeringQuoteErr(t *testing.T, s *Store, ctx context.Context, c Command) *Error {
	t.Helper()
	_, e := topupExec(s, ctx, c)
	if e == nil || e.Status != 402 || e.Code != "payment_required" {
		t.Fatalf("quote: want 402 payment_required, got %+v", e)
	}
	return e
}

func TestMPPOfferingEndToEnd(t *testing.T) {
	f := &mppFacilitator{}
	cfg := mppConfig(t, f)
	s := mppOfferingStore(t, cfg, true)
	key := keyFor(204)
	provider(t, s, key, "pythia")
	publish(t, s, key, listing("oracle", nil))
	signer := mppSigner(t)
	if caps := s.OfferingsCapabilities(); caps["mpp"] == nil || len(caps["protocols"].([]string)) != 2 {
		t.Fatalf("capabilities %v", caps)
	}
	if d := s.MPPDiscovery(); d["method"] != "evm" || d["intent"] != "charge" || !strings.EqualFold(d["currency"].(string), topupUSDC) {
		t.Fatalf("discovery %v", d)
	}

	e := offeringQuoteErr(t, s, testContext, buyCmd("pythia/oracle", question, ""))
	challenge := mppChallengeOf(t, e)
	c := mppParse(t, challenge)
	req, _ := c.Request()
	accepted := x402Accepted(t, e)
	if req["recipient"] != offeringWallet || req["amount"] != "5000000" || mppOpaqueQuote(t, c) != accepted["extra"].(map[string]any)["quote"] {
		t.Fatalf("challenge request %v, x402 %v", req, accepted)
	}
	// Too short an authorization for the claim window.
	short := mppPay(t, signer, cfg, challenge, testTime+60)
	if _, e := topupExec(s, mppHeader(short, "Authorization"), buyCmd("pythia/oracle", question, "")); e == nil || e.Code != "payment_expired" {
		t.Fatalf("short authorization: %+v", e)
	}
	// Another input under the same challenge: the quote binds the input.
	cred := mppPay(t, signer, cfg, challenge, testTime+930)
	if _, e := topupExec(s, mppHeader(cred, "Authorization"), buyCmd("pythia/oracle", `{"question":"Other?"}`, "")); e == nil || e.Code != "payment_mismatch" || mppChallengeOf(t, e) == challenge {
		t.Fatalf("other input: %+v", e)
	}
	if v, _ := f.counts(); v != 0 {
		t.Fatal("a refusal reached the facilitator")
	}

	// Pay: verified at intake, no receipt yet (nothing settled).
	res, e2 := topupExec(s, mppHeader(cred, "Authorization"), buyCmd("pythia/oracle", question, ""))
	if e2 != nil {
		t.Fatalf("buy: %+v", e2)
	}
	call := callOf(t, res)
	id := call["call"].(string)
	poll, _ := call["poll"].(string)
	if call["state"] != "authorized" || call["protocol"] != "mpp" || call["claim_by"] != int64(testTime+900) || call["payment_receipt"] != nil || poll == "" {
		t.Fatalf("call %v", call)
	}
	secret := poll[strings.LastIndex(poll, "/")+1:]
	if v, err := s.OfferingPoll(testContext, "pythia", "oracle", id, secret); err != nil || v["payment_receipt"] != nil {
		t.Fatalf("poll before the claim %v %v", v, err)
	}
	var protocol, ref, nonce string
	if err := s.db.QueryRow("SELECT protocol,payment_ref,auth_nonce FROM offering_calls WHERE id=?", id).Scan(&protocol, &ref, &nonce); err != nil || protocol != "mpp" || ref != c.Params["id"] || nonce != c.Nonce() {
		t.Fatalf("row %s %s %s %v", protocol, ref, nonce, err)
	}
	// The same credential again: the same call and poll.
	if again := callOf(t, run(t, s, buyCmd("pythia/oracle", question, cred))); again["call"] != id || again["poll"] != poll {
		t.Fatalf("repeat %v", again)
	}
	// The provider claims: it settles, on the challenge's nonce.
	claimed := callOf(t, run(t, s, signed(key, Command{Operation: "offering.claim", Target: id})))
	if claimed["state"] != "paid" {
		t.Fatalf("claimed %v", claimed)
	}
	if v, st := f.counts(); v != 1 || st != 1 || f.seen[1] != "/settle "+c.Nonce() || !strings.EqualFold(f.lastPayTo, offeringWallet) {
		t.Fatalf("facilitator %d %d %v", v, st, f.seen)
	}
	// The receipt is on the poll answer.
	v, err := s.OfferingPoll(testContext, "pythia", "oracle", id, secret)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(fmt.Sprint(v["payment_receipt"]))
	var receipt map[string]any
	if json.Unmarshal(raw, &receipt) != nil || receipt["challengeId"] != c.Params["id"] || receipt["reference"] != claimed["tx_hash"] || receipt["status"] != "success" {
		t.Fatalf("poll receipt %v (%s)", v, raw)
	}

	// The discovery probe's MPP challenge is as unpayable as its x402 one.
	probe := offeringQuoteErr(t, s, WithDiscoveryProbe(testContext), buyCmd("pythia/oracle", `{}`, ""))
	pc := mppParse(t, mppChallengeOf(t, probe))
	if mppOpaqueQuote(t, pc) != offeringDiscoveryQuote || x402Accepted(t, probe)["extra"].(map[string]any)["quote"] != offeringDiscoveryQuote {
		t.Fatalf("probe challenge %v", pc.Params)
	}
	paid := mppPay(t, signer, cfg, mppChallengeOf(t, probe), testTime+930)
	if _, e := topupExec(s, mppHeader(paid, "Authorization"), buyCmd("pythia/oracle", question, "")); e == nil || e.Code != "payment_mismatch" {
		t.Fatalf("paid the discovery challenge: %+v", e)
	}
	if v, _ := f.counts(); v != 1 {
		t.Fatal("the discovery challenge reached the facilitator")
	}
}

// With MPP off nothing changes: no challenge, no capability, and an MPP
// credential is an x402 payload that does not parse. The x402 402 is the
// same as with MPP on, but for its fresh quote.
func TestMPPOffUnchanged(t *testing.T) {
	f := &mppFacilitator{}
	cfg := mppConfig(t, f)
	off := mppTopupStore(t, cfg, false)
	on := mppTopupStore(t, cfg, true)
	key := keyFor(205)
	register(t, off, key)
	register(t, on, key)
	if off.MPPEnabled() || !on.MPPEnabled() {
		t.Fatal("MPPEnabled")
	}
	eOff, eOn := mppQuote(t, off, key, 1_000_000), mppQuote(t, on, key, 1_000_000)
	if _, ok := eOff.Details.(map[string]any)["mpp"]; ok || strings.Contains(eOff.Message, "MPP") {
		t.Fatalf("MPP off offers MPP: %+v", eOff)
	}
	if caps := off.TopupCapabilities(); caps["mpp"] != nil || caps["protocols"] != nil {
		t.Fatalf("capabilities %v", caps)
	}
	strip := func(e *Error) string {
		d := e.Details.(map[string]any)
		raw, _ := base64.StdEncoding.DecodeString(d["payment_required"].(string))
		quote := x402Accepted(t, e)["extra"].(map[string]any)["quote"].(string)
		body, _ := json.Marshal(d["x402"])
		return strings.ReplaceAll(string(raw), quote, "Q") + "\n" + strings.ReplaceAll(string(body), quote, "Q")
	}
	if a, b := strip(eOff), strip(eOn); a != b {
		t.Fatalf("x402 402 differs with MPP on:\n off %s\n on  %s", a, b)
	}
	if !strings.HasPrefix(eOn.Message, eOff.Message) {
		t.Fatal("the x402 message changed")
	}
	// An MPP credential is refused as a malformed x402 payment, word for word.
	cred := mppPay(t, mppSigner(t), cfg, mppChallengeOf(t, eOn), testTime+300)
	_, e := topupExec(off, testContext, signed(key, Command{Operation: "credits.topup", Amount: 1_000_000, Data: `{"schema":1,"payment":"` + cred + `"}`}))
	if e == nil || e.Code != "payment_invalid" || !strings.Contains(e.Message, "x402 v2 exact") || e.Details != nil {
		t.Fatalf("off: %+v", e)
	}
	if v, _ := f.counts(); v != 0 {
		t.Fatal("reached the facilitator")
	}
	// The flag parses on and off only.
	for v, want := range map[string]bool{"": false, "off": false, "on": true} {
		if f, err := ParseFeatures(func(k string) string { return map[string]string{"MPP": v}[k] }); err != nil || f.MPP != want {
			t.Fatalf("MPP=%q: %v %v", v, f.MPP, err)
		}
	}
	if _, err := ParseFeatures(func(k string) string { return map[string]string{"MPP": "true"}[k] }); err == nil {
		t.Fatal("MPP=true accepted")
	}
}
