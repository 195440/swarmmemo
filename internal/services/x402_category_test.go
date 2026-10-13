package services

import "testing"

// The catalogue's categorizer matches whole words (or their plurals), and
// "price" names finance only when nothing stronger is in the text (Inner
// Ozma 140b4ed2: "Priced per row" made a funding-rows tool finance, and
// BTC price feeds were finance through "price").
func TestX402CategorizeWholeWords(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		// The report's listings.
		{"https://hyperx.example/v1/asset_ctx/BTC/latest The most-recent N open-interest / funding / ctx rows (≤5000). Priced per row ($0.000025 premium); served live (no lag).", "crypto"},
		{"https://springs.example/api/price/btc-usd Real-time BTC/USD price feed oracle.", "crypto"},
		{"https://ochin.example/api/btc-price Bitcoin spot price in USD, live.", "crypto"},
		{"BTC/USD price feed", "crypto"},
		{"getETHPrice: the ether price", "crypto"},
		{"Token wallet balance of an address", "crypto"},
		// A form of "price" that is what a call costs names nothing.
		{"https://faxer.example/v1/fax Send a fax, priced per page.", "other"},
		{"Open-interest rows. Priced per row.", "other"},
		{"Per-call pricing applies.", "other"},
		// Price alone is still finance; stronger words win over it.
		{"Latest price for a symbol", "finance"},
		{"Stock prices, daily", "finance"},
		{"AAPL stock price and earnings", "finance"},
		{"forex exchange rate price", "finance"},
		{"Price of a search result page", "search"},
		// Whole words: no substring hits.
		{"Full-text index of papers", "other"},       // "dex" is not in "index"
		{"research notes", "other"},                  // "search" is not in "research"
		{"https://example.com/marketplace", "other"}, // "market" is not in "marketplace"
		{"https endpoint", "other"},                  // "tts" is not in "https"
		{"coingecko proxy", "crypto"},
		{"Token prices on Base", "crypto"},
		{"Scraping and crawler service", "scraping"},
		{"nsfw", "adult"},
		{"Filter adult content", "adult"},
		{"Weather forecasts by city", "weather"},
		{"Company enrichment by domain", "people"},
		{"", "other"},
		// Payment wording isn't a topic: most listings are paid in USDC on Base.
		{"AI chat completions API - pay per request with USDC on Base", "ai"},
		{"Pay a tiny amount of USDC and receive one useless fact", "other"},
	} {
		if got := x402Categorize(c.text); got != c.want {
			t.Errorf("%q: %s, want %s", c.text, got, c.want)
		}
	}
	// The catalogue and Find share the one list.
	if Categorize("BTC/USD price feed") != "crypto" || len(Categories()) != len(x402Categories) || len(Categories()[5].Weak) == 0 {
		t.Fatal("Categories/Categorize drift from x402Categories")
	}
}
