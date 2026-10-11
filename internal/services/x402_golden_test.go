package services

// Golden equivalence of the x402 server wire (C202a). The file
// testdata/x402_golden.json was written by this test BEFORE the x402
// challenge, parse and check code moved behind internal/pay's x402Wire; it
// must keep matching byte for byte after the move. Regenerate only for a
// deliberate wire change: go test ./internal/services -run TestX402Golden -update-x402-golden

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var updateX402Golden = flag.Bool("update-x402-golden", false, "rewrite testdata/x402_golden.json")

const x402GoldenPath = "testdata/x402_golden.json"

type x402GoldenFile struct {
	Challenges  []x402GoldenChallenge `json:"challenges"`
	Parse       []x402GoldenParse     `json:"parse"`
	Check       []x402GoldenCheck     `json:"check"`
	Facilitator []x402GoldenBody      `json:"facilitator"`
}

type x402GoldenChallenge struct {
	Name   string `json:"name"`
	Body   string `json:"body"`   // json.Marshal of the returned body object
	Header string `json:"header"` // the PAYMENT-REQUIRED value
}

type x402GoldenParse struct {
	Name   string `json:"name"`
	Input  string `json:"input"`
	Result string `json:"result"` // the parsed fields, or the error
}

type x402GoldenCheck struct {
	Name   string `json:"name"`
	Result string `json:"result"`
}

type x402GoldenBody struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

func x402GoldenErr(err error) string {
	if err == nil {
		return "ok"
	}
	var te *TopupError
	if errors.As(err, &te) {
		return "TopupError code=" + te.Code + " reason=" + te.Reason + " definite=" + strconv.FormatBool(te.Definite) + " text=" + err.Error()
	}
	return "error " + err.Error()
}

func x402GoldenPayment(p TopupPayment) string {
	return strings.Join([]string{
		"scheme=" + p.Scheme, "network=" + p.Network, "asset=" + p.Asset, "payTo=" + p.PayTo, "amount=" + p.Amount,
		"name=" + p.Name, "version=" + p.Version, "quote=" + p.Quote,
		"from=" + p.From.String(), "to=" + p.To.String(), "value=" + strconv.FormatInt(p.Value, 10),
		"validAfter=" + strconv.FormatInt(p.ValidAfter, 10), "validBefore=" + strconv.FormatInt(p.ValidBefore, 10),
		"nonce=" + p.Nonce, "signature=" + p.Signature,
	}, " ")
}

// x402GoldenCompute runs today's x402 server wire over a fixed corpus.
func x402GoldenCompute(t *testing.T) x402GoldenFile {
	t.Helper()
	cfg, err := ParseTopupConfig([]byte(topupTestConfig), nil)
	if err != nil {
		t.Fatal(err)
	}
	var g x402GoldenFile
	provider, _ := ParseEVMAddress("0x3333333333333333333333333333333333333333")
	r := TopupRequirement{Amount: 250_000, Quote: "4000.salt.mac", Expires: 4000}
	offer := X402Requirement{PayTo: provider, Amount: 1_500_000, Quote: "1700000300.c2FsdA.bWFj", Expires: 1_700_000_300, MaxTimeout: 900}

	// Challenges.
	addChallenge := func(name string, body map[string]any, header string) {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		g.Challenges = append(g.Challenges, x402GoldenChallenge{Name: name, Body: string(raw), Header: header})
	}
	b, h := cfg.PaymentRequired(r, "https://swarmmemo.example/v1/command", "Top up 250000 SwarmMemo credits (0.25 USDC)")
	addChallenge("topup", b, h)
	b, h = cfg.PaymentRequiredFor(offer, "https://swarmmemo.example/@ada/summarize", "Summarize <b>&</b> \"quote\" \u2028 é (1.5 USDC, /@ada/summarize)")
	addChallenge("offering, no extensions, escaping", b, h)
	b, h = cfg.PaymentRequiredWith(offer, "https://swarmmemo.example/@ada/summarize", "Summarize (1.5 USDC, /@ada/summarize)", map[string]any{
		"bazaar": map[string]any{"discoverable": true, "info": map[string]any{"input": map[string]any{"type": "http", "method": "POST", "bodyType": "json"},
			"output": map[string]any{"type": "object"}}, "schema": map[string]any{"z": 1, "a": []any{"x", 2.5, nil, true}}}})
	addChallenge("offering, bazaar", b, h)
	b, h = cfg.PaymentRequiredWith(X402Requirement{PayTo: cfg.PayTo, Amount: 1, Quote: "discovery-only", Expires: 1, MaxTimeout: 60}, "", "", map[string]any{})
	addChallenge("discovery probe, empty extensions", b, h)

	// Parse.
	auth := func(m map[string]any) map[string]any {
		return m["payload"].(map[string]any)["authorization"].(map[string]any)
	}
	good := topupTestPayment(cfg, r, nil)
	goodRaw, _ := base64.StdEncoding.DecodeString(good)
	inputs := []struct {
		name, input string
	}{
		{"valid", good},
		{"valid, padded with spaces", "  " + good + "\n"},
		{"valid, raw url base64", base64.RawURLEncoding.EncodeToString(goodRaw)},
		{"valid, url base64", base64.URLEncoding.EncodeToString(goodRaw)},
		{"valid, raw std base64", base64.RawStdEncoding.EncodeToString(goodRaw)},
		{"empty", ""},
		{"not base64", "!!!"},
		{"oversized", strings.Repeat("A", TopupPaymentBytes+4)},
		{"plain JSON", string(goodRaw)},
		{"version 1", topupTestPayment(cfg, r, func(m map[string]any) { m["x402Version"] = 1 })},
		{"version string", topupTestPayment(cfg, r, func(m map[string]any) { m["x402Version"] = "2" })},
		{"unknown envelope field", topupTestPayment(cfg, r, func(m map[string]any) { m["x"] = 1 })},
		{"resource object", topupTestPayment(cfg, r, func(m map[string]any) {
			m["resource"] = map[string]any{"url": "https://swarmmemo.example/v1/command", "z": "<&>"}
		})},
		{"resource array", topupTestPayment(cfg, r, func(m map[string]any) { m["resource"] = []any{1} })},
		{"resource null", topupTestPayment(cfg, r, func(m map[string]any) { m["resource"] = nil })},
		{"extensions object", topupTestPayment(cfg, r, func(m map[string]any) { m["extensions"] = map[string]any{"a": 1} })},
		{"extensions too big", topupTestPayment(cfg, r, func(m map[string]any) {
			m["extensions"] = map[string]any{"a": strings.Repeat("x", x402RawFieldBytes)}
		})},
		{"accepted not object", topupTestPayment(cfg, r, func(m map[string]any) { m["accepted"] = "x" })},
		{"accepted unknown field", topupTestPayment(cfg, r, func(m map[string]any) { m["accepted"].(map[string]any)["x"] = 1 })},
		{"extra missing", topupTestPayment(cfg, r, func(m map[string]any) { delete(m["accepted"].(map[string]any), "extra") })},
		{"quote bad chars", topupTestPayment(cfg, r, func(m map[string]any) {
			m["accepted"].(map[string]any)["extra"].(map[string]any)["quote"] = "a b"
		})},
		{"quote 129", topupTestPayment(cfg, r, func(m map[string]any) {
			m["accepted"].(map[string]any)["extra"].(map[string]any)["quote"] = strings.Repeat("q", 129)
		})},
		{"assetTransferMethod eip3009", topupTestPayment(cfg, r, func(m map[string]any) {
			m["accepted"].(map[string]any)["extra"].(map[string]any)["assetTransferMethod"] = "eip3009"
		})},
		{"assetTransferMethod permit2", topupTestPayment(cfg, r, func(m map[string]any) {
			m["accepted"].(map[string]any)["extra"].(map[string]any)["assetTransferMethod"] = "permit2"
		})},
		{"other scheme and network echoed", topupTestPayment(cfg, r, func(m map[string]any) {
			m["accepted"].(map[string]any)["scheme"] = "upto"
			m["accepted"].(map[string]any)["network"] = "eip155:1"
		})},
		{"from zero", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["from"] = "0x0000000000000000000000000000000000000000" })},
		{"from bad checksum", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["from"] = "0xAbCdEf0000000000000000000000000000000000" })},
		{"from upper", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["from"] = "0XABCDEF0000000000000000000000000000000000" })},
		{"to checksummed", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["to"] = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913" })},
		{"value zero", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["value"] = "0" })},
		{"value leading zero", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["value"] = "0250000" })},
		{"value number", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["value"] = 250000 })},
		{"value 19 digits", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["value"] = "1000000000000000000" })},
		{"validBefore 13 digits", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["validBefore"] = "1000000000000" })},
		{"validAfter zero", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["validAfter"] = "0" })},
		{"nonce upper", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["nonce"] = "0x" + strings.Repeat("AB", 32) })},
		{"nonce short", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["nonce"] = "0x" + strings.Repeat("ab", 31) })},
		{"signature upper", topupTestPayment(cfg, r, func(m map[string]any) {
			m["payload"].(map[string]any)["signature"] = "0x" + strings.Repeat("1F", 65)
		})},
		{"signature 64 bytes", topupTestPayment(cfg, r, func(m map[string]any) {
			m["payload"].(map[string]any)["signature"] = "0x" + strings.Repeat("1f", 64)
		})},
		{"payload unknown field", topupTestPayment(cfg, r, func(m map[string]any) { m["payload"].(map[string]any)["x"] = 1 })},
		{"authorization unknown field", topupTestPayment(cfg, r, func(m map[string]any) { auth(m)["x"] = 1 })},
		{"duplicate key", base64.StdEncoding.EncodeToString([]byte(strings.Replace(string(goodRaw), `"x402Version":2`, `"x402Version":2,"X402VERSION":2`, 1)))},
		{"trailing data", base64.StdEncoding.EncodeToString(append(append([]byte{}, goodRaw...), []byte(" {}")...))},
		{"invalid utf8", base64.StdEncoding.EncodeToString([]byte(strings.Replace(string(goodRaw), `"USD Coin"`, "\"USD\xffCoin\"", 1)))},
	}
	parsed := map[string]TopupPayment{}
	for _, in := range inputs {
		p, err := ParseTopupPayment(in.input)
		res := x402GoldenErr(err)
		if err == nil {
			res = x402GoldenPayment(p)
			parsed[in.name] = p
		}
		g.Parse = append(g.Parse, x402GoldenParse{Name: in.name, Input: in.input, Result: res})
	}

	// Check.
	p := parsed["valid"]
	checks := []struct {
		name string
		p    TopupPayment
		r    X402Requirement
		now  int64
	}{
		{"valid topup", p, cfg.topup(r), 2000},
		{"quote expired", p, cfg.topup(r), 4001},
		{"at expiry", p, cfg.topup(r), 4000},
		{"not yet valid", p, cfg.topup(r), 999},
		{"validBefore margin edge", p, cfg.topup(TopupRequirement{Amount: r.Amount, Quote: r.Quote, Expires: 99999}), 5000 - topupValidBeforeMargin},
		{"validBefore inside margin", p, cfg.topup(TopupRequirement{Amount: r.Amount, Quote: r.Quote, Expires: 99999}), 5000 - topupValidBeforeMargin + 1},
		{"other amount", p, cfg.topup(TopupRequirement{Amount: 250_001, Quote: r.Quote, Expires: r.Expires}), 2000},
		{"other quote", p, cfg.topup(TopupRequirement{Amount: r.Amount, Quote: "4000.salt.mad", Expires: r.Expires}), 2000},
		{"other payTo", p, X402Requirement{PayTo: provider, Amount: r.Amount, Quote: r.Quote, Expires: r.Expires, MaxTimeout: 300}, 2000},
		{"zero payTo", p, X402Requirement{Amount: r.Amount, Quote: r.Quote, Expires: r.Expires, MaxTimeout: 300}, 2000},
	}
	mut := func(f func(q *TopupPayment)) TopupPayment { q := p; f(&q); return q }
	checks = append(checks, []struct {
		name string
		p    TopupPayment
		r    X402Requirement
		now  int64
	}{
		{"scheme upto", mut(func(q *TopupPayment) { q.Scheme = "upto" }), cfg.topup(r), 2000},
		{"network other", mut(func(q *TopupPayment) { q.Network = "eip155:1" }), cfg.topup(r), 2000},
		{"asset lower", mut(func(q *TopupPayment) { q.Asset = strings.ToLower(q.Asset) }), cfg.topup(r), 2000},
		{"asset other", mut(func(q *TopupPayment) { q.Asset = "0x1111111111111111111111111111111111111111" }), cfg.topup(r), 2000},
		{"asset garbage", mut(func(q *TopupPayment) { q.Asset = "usdc" }), cfg.topup(r), 2000},
		{"accepted payTo lower", mut(func(q *TopupPayment) { q.PayTo = strings.ToLower(q.PayTo) }), cfg.topup(r), 2000},
		{"authorization to other", mut(func(q *TopupPayment) { q.To = provider }), cfg.topup(r), 2000},
		{"name empty", mut(func(q *TopupPayment) { q.Name = "" }), cfg.topup(r), 2000},
		{"name other", mut(func(q *TopupPayment) { q.Name = "USDC" }), cfg.topup(r), 2000},
		{"version empty", mut(func(q *TopupPayment) { q.Version = "" }), cfg.topup(r), 2000},
		{"version other", mut(func(q *TopupPayment) { q.Version = "1" }), cfg.topup(r), 2000},
		{"accepted amount other", mut(func(q *TopupPayment) { q.Amount = "250001" }), cfg.topup(r), 2000},
		{"accepted amount padded", mut(func(q *TopupPayment) { q.Amount = "0250000" }), cfg.topup(r), 2000},
		{"value other", mut(func(q *TopupPayment) { q.Value = 249_999 }), cfg.topup(r), 2000},
		{"validAfter equals validBefore", mut(func(q *TopupPayment) { q.ValidAfter = q.ValidBefore }), cfg.topup(r), 2000},
		{"validAfter future", mut(func(q *TopupPayment) { q.ValidAfter = 2001 }), cfg.topup(r), 2000},
	}...)
	for _, c := range checks {
		g.Check = append(g.Check, x402GoldenCheck{Name: c.name, Result: x402GoldenErr(cfg.CheckRequirement(c.p, c.r, c.now))})
	}
	g.Check = append(g.Check, x402GoldenCheck{Name: "Check wrapper", Result: x402GoldenErr(cfg.Check(p, r, 2000))})

	// Facilitator bodies: rebuilt from what we parsed and our own requirement.
	for _, name := range []string{"valid", "resource object", "nonce upper", "signature upper", "to checksummed", "valid, raw url base64"} {
		q, ok := parsed[name]
		if !ok {
			t.Fatalf("golden corpus: %s did not parse", name)
		}
		for _, req := range []struct {
			label string
			r     X402Requirement
		}{{"topup", cfg.topup(r)}, {"offering", offer}} {
			body, err := cfg.facilitatorBody(q, req.r)
			if err != nil {
				t.Fatal(err)
			}
			g.Facilitator = append(g.Facilitator, x402GoldenBody{Name: name + " / " + req.label, Body: string(body)})
		}
	}
	return g
}

func TestX402Golden(t *testing.T) {
	got := x402GoldenCompute(t)
	if *updateX402Golden {
		raw, err := json.MarshalIndent(got, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(x402GoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(x402GoldenPath, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(x402GoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var want x402GoldenFile
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	compare := func(kind string, n int, name func(i int) string, eq func(i int) (string, string)) {
		t.Helper()
		for i := 0; i < n; i++ {
			g, w := eq(i)
			if g != w {
				t.Errorf("%s %q changed:\n got  %s\n want %s", kind, name(i), g, w)
			}
		}
	}
	if len(got.Challenges) != len(want.Challenges) || len(got.Parse) != len(want.Parse) || len(got.Check) != len(want.Check) || len(got.Facilitator) != len(want.Facilitator) {
		t.Fatalf("golden corpus size changed")
	}
	compare("challenge", len(got.Challenges), func(i int) string { return want.Challenges[i].Name }, func(i int) (string, string) {
		return got.Challenges[i].Name + "\n" + got.Challenges[i].Body + "\n" + got.Challenges[i].Header,
			want.Challenges[i].Name + "\n" + want.Challenges[i].Body + "\n" + want.Challenges[i].Header
	})
	compare("parse", len(got.Parse), func(i int) string { return want.Parse[i].Name }, func(i int) (string, string) {
		return got.Parse[i].Name + "\n" + got.Parse[i].Input + "\n" + got.Parse[i].Result,
			want.Parse[i].Name + "\n" + want.Parse[i].Input + "\n" + want.Parse[i].Result
	})
	compare("check", len(got.Check), func(i int) string { return want.Check[i].Name }, func(i int) (string, string) {
		return got.Check[i].Name + " " + got.Check[i].Result, want.Check[i].Name + " " + want.Check[i].Result
	})
	compare("facilitator body", len(got.Facilitator), func(i int) string { return want.Facilitator[i].Name }, func(i int) (string, string) {
		return got.Facilitator[i].Name + "\n" + got.Facilitator[i].Body, want.Facilitator[i].Name + "\n" + want.Facilitator[i].Body
	})
}
