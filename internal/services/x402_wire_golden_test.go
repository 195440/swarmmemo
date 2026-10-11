package services

// x402Wire (internal/pay) against the golden file recorded before the
// move: the Wire interface writes the same 402 bodies and PAYMENT-REQUIRED
// headers and parses the same payments as the code it replaced.

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"swarmmemo/internal/pay"
)

func TestX402WireMatchesGolden(t *testing.T) {
	raw, err := os.ReadFile(x402GoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var g x402GoldenFile
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	cfg, err := ParseTopupConfig([]byte(topupTestConfig), nil)
	if err != nil {
		t.Fatal(err)
	}
	w := pay.NewX402(cfg.Rail())
	provider, _ := ParseEVMAddress("0x3333333333333333333333333333333333333333")
	offer := pay.Ask{PayTo: provider, Amount: 1_500_000, Quote: "1700000300.c2FsdA.bWFj", Expires: 1_700_000_300, MaxTimeout: 900}
	asks := map[string]pay.Ask{
		"topup": {PayTo: cfg.PayTo, Amount: 250_000, Quote: "4000.salt.mac", Expires: 4000, MaxTimeout: TopupQuoteSeconds,
			Resource: "https://swarmmemo.example/v1/command", Description: "Top up 250000 SwarmMemo credits (0.25 USDC)"},
		"offering, no extensions, escaping": func() pay.Ask {
			a := offer
			a.Resource, a.Description = "https://swarmmemo.example/@ada/summarize", "Summarize <b>&</b> \"quote\" \u2028 é (1.5 USDC, /@ada/summarize)"
			return a
		}(),
		"offering, bazaar": func() pay.Ask {
			a := offer
			a.Resource, a.Description = "https://swarmmemo.example/@ada/summarize", "Summarize (1.5 USDC, /@ada/summarize)"
			a.Extensions = map[string]any{
				"bazaar": map[string]any{"discoverable": true, "info": map[string]any{"input": map[string]any{"type": "http", "method": "POST", "bodyType": "json"},
					"output": map[string]any{"type": "object"}}, "schema": map[string]any{"z": 1, "a": []any{"x", 2.5, nil, true}}}}
			return a
		}(),
		"discovery probe, empty extensions": {PayTo: cfg.PayTo, Amount: 1, Quote: "discovery-only", Expires: 1, MaxTimeout: 60, Extensions: map[string]any{}},
	}
	if len(asks) != len(g.Challenges) {
		t.Fatal("golden challenges changed")
	}
	for _, c := range g.Challenges {
		h := http.Header{}
		body, err := w.Challenge(asks[c.Name], h)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := json.Marshal(body)
		if string(got) != c.Body || h.Get("PAYMENT-REQUIRED") != c.Header {
			t.Errorf("%s: x402Wire differs from the golden 402:\n got  %s\n want %s", c.Name, got, c.Body)
		}
	}
	for _, c := range g.Parse {
		x, err := w.Parse(c.Input, "Payment-Signature")
		res := x402GoldenErr(topupFromPay(err))
		if err == nil {
			res = x402GoldenPayment(x.Payment())
		}
		if res != c.Result {
			t.Errorf("parse %s:\n got  %s\n want %s", c.Name, res, c.Result)
		}
	}
	// Bound is CheckRequirement: the golden check results the Ask can express.
	x, err := w.Parse(g.Parse[0].Input, "Payment-Signature")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, c := range g.Check {
		want[c.Name] = c.Result
	}
	topup := asks["topup"]
	for name, b := range map[string]struct {
		a   pay.Ask
		now int64
	}{
		"valid topup":   {topup, 2000},
		"quote expired": {topup, 4001},
		"at expiry":     {topup, 4000},
		"not yet valid": {topup, 999},
		"other amount":  {func() pay.Ask { a := topup; a.Amount = 250_001; return a }(), 2000},
		"other quote":   {func() pay.Ask { a := topup; a.Quote = "4000.salt.mad"; return a }(), 2000},
		"other payTo":   {func() pay.Ask { a := topup; a.PayTo = provider; return a }(), 2000},
		"zero payTo":    {func() pay.Ask { a := topup; a.PayTo = EVMAddress{}; return a }(), 2000},
	} {
		if got := x402GoldenErr(topupFromPay(w.Bound(x, b.a, b.now))); got != want[name] {
			t.Errorf("bound %s: %s, golden %s", name, got, want[name])
		}
	}
}
