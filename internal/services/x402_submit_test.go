package services

// Self-serve listing (x402.submit): a submitted endpoint's 402 challenge and
// the same endpoint as a Bazaar item normalise to the same catalogue row;
// the guardrails, the caps, Find, the relay's callable rule, withdrawing
// and the refresh's re-probe all treat it as the crawl's. Every upstream is
// a fake behind the harness's dialer: no test reaches the network.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

// fakeChallenge answers an unpaid request with a 402 challenge (v2 in the
// PAYMENT-REQUIRED header, v1 in the body), or with status when set.
type fakeChallenge struct {
	mu      sync.Mutex
	status  int
	version int
	payTo   string
	network string
	amount  int64
	desc    string
	hits    int
	paid    int // requests that carried a payment header
}

func (c *fakeChallenge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hits++
	if r.Header.Get("PAYMENT-SIGNATURE") != "" || r.Header.Get("X-PAYMENT") != "" {
		c.paid++
	}
	if c.status != 0 {
		w.WriteHeader(c.status)
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}
	option := map[string]any{"scheme": "exact", "network": c.network, "amount": strconv.FormatInt(c.amount, 10), "asset": testUSDC, "payTo": c.payTo,
		"maxTimeoutSeconds": 60, "extra": map[string]any{"name": "USD Coin", "version": "2"}}
	input := map[string]any{"type": "http", "method": r.Method, "queryParams": map[string]any{"q": map[string]any{"type": "string"}}}
	var raw []byte
	if c.version == 1 {
		delete(option, "amount")
		option["maxAmountRequired"], option["network"], option["description"] = strconv.FormatInt(c.amount, 10), "base", c.desc
		option["outputSchema"] = map[string]any{"input": input}
		raw, _ = json.Marshal(map[string]any{"x402Version": 1, "error": "payment required", "accepts": []any{option}})
	} else {
		raw, _ = json.Marshal(map[string]any{"x402Version": 2, "resource": map[string]any{"url": "https://elsewhere.example/ignored", "description": c.desc},
			"accepts": []any{option}, "extensions": map[string]any{"bazaar": map[string]any{"info": map[string]any{"input": input}}}})
		w.Header().Set("PAYMENT-REQUIRED", base64.StdEncoding.EncodeToString(raw))
		raw = []byte("{}")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_, _ = w.Write(raw)
}

func newChallenge(desc string, amount int64) *fakeChallenge {
	return &fakeChallenge{version: 2, payTo: testPayTo, network: "eip155:8453", amount: amount, desc: desc}
}

const submitter = "acct-seller"

// submitHarness is the catalogue harness with fake endpoints at paths of
// example.com.
func submitHarness(t *testing.T, endpoints map[string]http.Handler) (*x402Harness, *x402) {
	t.Helper()
	fake := &fakeX402{price: 1500}
	h, x := newCatalogueHarness(t, fake, &fakeBazaar{}, catalogueConfig(t, `{}`, testCatalogue))
	for path, handler := range endpoints {
		fake.extra[path] = handler
	}
	return h, x
}

// submit submits s as submitter, by default owning the domain.
func (h *x402Harness) submit(s X402Submission) (map[string]any, error) {
	if s.Account == "" {
		s.Account = submitter
	}
	if s.Wallets == nil {
		s.Domain = true
	}
	if s.Method == "" {
		s.Method = "GET"
	}
	return h.engine.Registry().SubmitX402(context.Background(), s, h.now)
}

func storedRow(t *testing.T, h *x402Harness, id string) map[string]any {
	t.Helper()
	var bundler, u, method, payTo, query, category, summary, source string
	var amount, body int64
	err := h.db.QueryRow("SELECT bundler,url,method,pay_to,amount,query,body,category,summary,source FROM x402_catalogue WHERE id=?", id).
		Scan(&bundler, &u, &method, &payTo, &amount, &query, &body, &category, &summary, &source)
	if err != nil {
		t.Fatalf("row %s: %v", id, err)
	}
	return map[string]any{"id": id, "bundler": bundler, "url": u, "method": method, "pay_to": payTo, "amount": amount, "query": query, "body": body,
		"category": category, "summary": summary, "source": source}
}

// One normaliser: the same endpoint read from its 402 challenge (v2 and
// v1) and from a Bazaar item becomes the same catalogue row, but for where
// it came from.
func TestSubmittedAndCrawledNormaliseAlike(t *testing.T) {
	const desc = "Web search API, fast"
	for _, version := range []int{2, 1} {
		ch := newChallenge(desc, 1500)
		ch.version = version
		h, x := submitHarness(t, map[string]http.Handler{"/open/search": ch})
		id := openIDFor("/open/search", "GET")
		item := bazaarItemJSON("https://example.com/open/search", "GET", 1500, testPayTo, "eip155:8453", desc, "")
		crawled, _, ok := bazaarResource(json.RawMessage(item), x.cfg, min(x.cfg.Catalogue.MaxPrice, x.cfg.PerCall))
		if !ok {
			t.Fatal("the Bazaar item does not normalise")
		}
		crawled.ID = openID(crawled.ID, crawled.Method, crawled.URL)
		probed, _, err := x.probe(context.Background(), "GET", "https://example.com/open/search")
		if err != nil {
			t.Fatalf("v%d probe: %v", version, err)
		}
		if !reflect.DeepEqual(crawled, probed) {
			t.Fatalf("v%d: crawled %+v\nprobed %+v", version, crawled, probed)
		}
		// Through the import and the submission, into the table.
		h.fake.extra[discoveryPath] = &fakeBazaar{items: []string{item}}
		if _, err = x.importCatalogue(context.Background(), h.now); err != nil {
			t.Fatal(err)
		}
		fromBazaar := storedRow(t, h, id)
		if _, err = h.db.Exec("DELETE FROM x402_catalogue"); err != nil {
			t.Fatal(err)
		}
		if _, err = h.submit(X402Submission{URL: "https://example.com/open/search"}); err != nil {
			t.Fatalf("v%d submit: %v", version, err)
		}
		fromSubmit := storedRow(t, h, id)
		if fromBazaar["source"] != "bazaar example.com" || fromSubmit["source"] != submittedSource(ProofDomain, submitter) {
			t.Fatalf("sources: %v, %v", fromBazaar["source"], fromSubmit["source"])
		}
		delete(fromBazaar, "source")
		delete(fromSubmit, "source")
		if !reflect.DeepEqual(fromBazaar, fromSubmit) {
			t.Fatalf("v%d rows differ:\n bazaar %v\n submit %v", version, fromBazaar, fromSubmit)
		}
		if ch.paid != 0 {
			t.Fatal("a probe sent a payment")
		}
	}
}

// The listing: a candidate like any open resource (listed, in Find with its
// submitter, its summary pending the screen, not callable), callable once
// vetted, the same review subject; the submitter's summary and category
// replace the challenge's; a resubmit updates it; a crawl never overwrites
// it; a withdraw removes it.
func TestSubmittedListing(t *testing.T) {
	ch := newChallenge("weather", 1500)
	h, x := submitHarness(t, map[string]http.Handler{"/w": ch})
	out, err := h.submit(X402Submission{URL: "https://example.com/w", Summary: "Forecast for any city", Category: "weather"})
	if err != nil {
		t.Fatal(err)
	}
	l := out["listing"].(map[string]any)
	id := openIDFor("/w", "GET")
	if l["id"] != id || l["source"] != "submitted" || l["callable"] != false || l["vetted"] != false || l["max_price"] != "0.0015" ||
		l["category"] != "weather" || l["summary_status"] != summaryPending || l["subject"] != "x402:GET https://example.com/w" {
		t.Fatalf("listing: %v", l)
	}
	var found *X402Listing
	for _, li := range h.engine.Registry().X402Listings() {
		if li.ID == id {
			found = &li
		}
	}
	if found == nil || found.ListedBy != submitter || found.Callable || found.Summary != "" || found.Subject == "" {
		t.Fatalf("Find's listing: %+v", found)
	}
	if _, err = h.call(testSubject, `{"resource":"`+id+`"}`, creditsFor(1500)); errCode(err) != "x402_unvetted" {
		t.Fatalf("an unvetted submission is callable: %v", err)
	}
	page := h.resources(`{"query":"forecast"}`)
	if got := page["resources"].([]any); len(got) != 1 || got[0].(map[string]any)["source"] != "submitted" {
		t.Fatalf("resources read: %v", page["resources"])
	}
	// Vetted, it is callable through the relay under the open rules.
	vetOpen(t, h, x, id)
	if !x.callable(x.cat.snap.Load().byID[id]) {
		t.Fatal("a vetted submission is not callable")
	}
	// A real paid call: the fake resource server (any other path) takes
	// the payment once its listing is vetted.
	if _, err = h.submit(X402Submission{URL: "https://example.com/relay"}); err != nil {
		t.Fatal(err)
	}
	relay := openIDFor("/relay", "GET")
	vetOpen(t, h, x, relay)
	if _, err = h.call(testSubject, `{"resource":"`+relay+`"}`, creditsFor(1500)); err != nil || h.fake.paid != 1 {
		t.Fatalf("a vetted submission through the relay: %v, paid %d", err, h.fake.paid)
	}
	// A resubmit updates the entry in place.
	if _, err = h.submit(X402Submission{URL: "https://example.com/w", Summary: "Hourly forecast"}); err != nil {
		t.Fatal(err)
	}
	if r := x.cat.snap.Load().byID[id]; r == nil || r.Summary != "Hourly forecast" || !r.Vetted {
		t.Fatalf("after a resubmit: %+v", r)
	}
	// The crawl lists the same endpoint: the submitted row stands.
	h.fake.extra[discoveryPath] = &fakeBazaar{items: []string{bazaarItemJSON("https://example.com/w", "GET", 1500, testPayTo, "eip155:8453", "crawled text", "")}}
	if _, err = x.importCatalogue(context.Background(), h.now+60); err != nil {
		t.Fatal(err)
	}
	if row := storedRow(t, h, id); row["source"] != submittedSource(ProofDomain, submitter) || row["summary"] != "Hourly forecast" {
		t.Fatalf("the crawl overwrote a submitted row: %v", row)
	}
	// Withdraw: only the submitter (or a domain owner) removes it.
	reg := h.engine.Registry()
	if _, err = reg.WithdrawX402(context.Background(), "acct-other", "GET", "https://example.com/w", false, h.now); errCode(err) != "x402_listing_not_found" {
		t.Fatalf("another account withdrew it: %v", err)
	}
	if _, err = reg.WithdrawX402(context.Background(), submitter, "GET", "https://example.com/w", false, h.now); err != nil {
		t.Fatal(err)
	}
	if _, ok := x.cat.snap.Load().byID[id]; ok {
		t.Fatal("withdrawn, still listed")
	}
	if _, err = reg.WithdrawX402(context.Background(), submitter, "GET", "https://example.com/w", false, h.now); errCode(err) != "x402_listing_not_found" {
		t.Fatalf("withdrawn twice: %v", err)
	}
}

// Every refusal: the probe's (no answer, no challenge, an unpayable or dear
// one), the guardrails' (denied, pinned) and the caps'.
func TestSubmitRefusals(t *testing.T) {
	solana := newChallenge("x", 1000)
	solana.network = "solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp"
	denied := newChallenge("x", 1000)
	denied.payTo = deniedPayTo
	endpoints := map[string]http.Handler{
		"/free":    &fakeChallenge{status: 200},
		"/missing": &fakeChallenge{status: 404},
		"/garbage": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(402); _, _ = w.Write([]byte("pay me")) }),
		"/solana":  solana,
		"/dear":    newChallenge("x", 30000),
		"/denied":  denied,
		"/adult":   newChallenge("nsfw pictures", 1000),
	}
	for i := range 4 {
		endpoints["/ok/"+strconv.Itoa(i)] = newChallenge("data", 1000)
	}
	h, _ := submitHarness(t, endpoints)
	for _, c := range []struct{ url, method, code string }{
		{"https://other.example.net/x", "GET", "x402_listing_unreachable"},
		{"https://example.com/free", "GET", "x402_listing_no_challenge"},
		{"https://example.com/missing", "GET", "x402_listing_no_challenge"},
		{"https://example.com/garbage", "GET", "x402_listing_no_challenge"},
		{"https://example.com/solana", "GET", "x402_listing_unsupported"},
		{"https://example.com/dear", "GET", "x402_listing_price"},
		{"https://example.com/denied", "GET", "x402_listing_denied"},
		{"https://example.com/adult", "GET", "x402_listing_denied"},
		{"https://example.com/paid", "GET", "x402_listing_pinned"},
	} {
		if _, err := h.submit(X402Submission{URL: c.url, Method: c.method}); errCode(err) != c.code {
			t.Errorf("%s %s: %v, want %s", c.method, c.url, err, c.code)
		}
	}
	if err := CheckX402Submission(X402Submission{Summary: "two\nlines"}); errCode(err) != "x402_listing_summary" {
		t.Errorf("summary: %v", err)
	}
	if err := CheckX402Submission(X402Submission{Category: "nonsense"}); errCode(err) != "x402_listing_category" {
		t.Errorf("category: %v", err)
	}
	for _, target := range []string{"GET http://example.com/x", "PUT https://example.com/x", "https://example.com/x", "GET https://10.0.0.1/x", "GET https://example.com:8443/x"} {
		if _, _, err := ParseX402Target(target); errCode(err) != "x402_listing_target" {
			t.Errorf("target %q: %v", target, err)
		}
	}
	// Three per recipient and per domain: the fourth does not fit; one of
	// the three is still updated.
	for i := range 3 {
		if _, err := h.submit(X402Submission{URL: "https://example.com/ok/" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.submit(X402Submission{URL: "https://example.com/ok/3"}); errCode(err) != "x402_listing_caps" {
		t.Fatalf("a fourth listing: %v", err)
	}
	if _, err := h.submit(X402Submission{URL: "https://example.com/ok/1", Summary: "updated"}); err != nil {
		t.Fatalf("resubmitting a listed one: %v", err)
	}
}

// The refresh re-probes submitted entries: a changed recipient is upserted
// and unbinds the vetting (re-flagged); a challenge that went away leaves
// the row unseen, stale after three refreshes.
func TestSubmittedReprobe(t *testing.T) {
	ch := newChallenge("search", 1000)
	h, x := submitHarness(t, map[string]http.Handler{"/s": ch})
	if _, err := h.submit(X402Submission{URL: "https://example.com/s"}); err != nil {
		t.Fatal(err)
	}
	id := openIDFor("/s", "GET")
	vetOpen(t, h, x, id)
	const newPayTo = "0x2222222222222222222222222222222222222222"
	ch.payTo = newPayTo
	h.now += 3600
	if n, err := x.reprobeSubmitted(context.Background(), h.now); err != nil || n != 1 {
		t.Fatalf("reprobe: %d, %v", n, err)
	}
	r := x.cat.snap.Load().byID[id]
	if r == nil || r.PayTo.String() != newPayTo || r.Vetted || x.callable(r) {
		t.Fatalf("after the recipient changed: %+v", r)
	}
	ch.status = 200 // the challenge is gone
	h.now += 3600
	if n, _ := x.reprobeSubmitted(context.Background(), h.now); n != 0 {
		t.Fatalf("a gone challenge was refreshed: %d", n)
	}
	h.now += 3 * 6 * 3600
	if err := x.loadCatalogue(context.Background(), h.now); err != nil {
		t.Fatal(err)
	}
	if _, ok := x.cat.snap.Load().byID[id]; ok {
		t.Fatal("unseen for three refreshes, still listed")
	}
	if ch.paid != 0 {
		t.Fatal("a probe sent a payment")
	}
}

// The wallet proof, for sellers on shared hosting whose DNS they cannot
// write: every option of the challenge pays a wallet the submitter
// verified. A mismatch is unowned; a re-probe that sees another recipient
// removes the listing, its only proof gone; a domain-proven one stays,
// re-flagged.
func TestSubmitWalletProof(t *testing.T) {
	ch, other := newChallenge("search", 1000), newChallenge("weather", 1000)
	h, x := submitHarness(t, map[string]http.Handler{"/s": ch, "/w": other})
	mine, _ := ParseEVMAddress(testPayTo)
	stranger, _ := ParseEVMAddress(deniedPayTo)
	if _, err := h.submit(X402Submission{URL: "https://example.com/s", Wallets: map[EVMAddress]bool{stranger: true}}); errCode(err) != "x402_listing_unowned" {
		t.Fatalf("another wallet: %v", err)
	}
	out, err := h.submit(X402Submission{URL: "https://example.com/s", Wallets: map[EVMAddress]bool{mine: true}})
	if err != nil {
		t.Fatal(err)
	}
	id := openIDFor("/s", "GET")
	if l := out["listing"].(map[string]any); l["proof"] != ProofWallet || storedRow(t, h, id)["source"] != submittedSource(ProofWallet, submitter) {
		t.Fatalf("wallet listing: %v", l)
	}
	proofs := map[any]any{}
	for _, r := range h.resources(`{"query":"search"}`)["resources"].([]any) {
		proofs[r.(map[string]any)["id"]] = r.(map[string]any)["proof"]
	}
	if proofs[id] != ProofWallet || proofs["search"] != nil {
		t.Fatalf("resources read proofs: %v", proofs)
	}
	// A domain-proven one beside it.
	if _, err = h.submit(X402Submission{URL: "https://example.com/w"}); err != nil {
		t.Fatal(err)
	}
	// Both challenges now pay someone else.
	const newPayTo = "0x2222222222222222222222222222222222222222"
	ch.payTo, other.payTo = newPayTo, newPayTo
	h.now += 3600
	if _, err = x.reprobeSubmitted(context.Background(), h.now); err != nil {
		t.Fatal(err)
	}
	snap := x.cat.snap.Load()
	if _, ok := snap.byID[id]; ok {
		t.Fatal("a wallet-proven listing kept its place after its recipient changed")
	}
	var lastSeen int64
	if err := h.db.QueryRow("SELECT last_seen FROM x402_catalogue WHERE id=?", id).Scan(&lastSeen); err != nil || lastSeen != 0 {
		t.Fatalf("the wallet-proven row is kept and marked stale, not deleted: last_seen %d, %v", lastSeen, err)
	}
	if r := snap.byID[openIDFor("/w", "GET")]; r == nil || r.PayTo.String() != newPayTo || r.Proof() != ProofDomain {
		t.Fatalf("the domain-proven listing: %+v", r)
	}
}

// An owner-proven submission takes precedence over crawled listings under a
// full cap (C308): per recipient and per registrable domain, the
// lowest-ranked crawled entry gives way, its row kept; a later crawl keeps
// the submitted entry and does not bring the displaced one back; a cap full
// of vetted and submitted entries still refuses.
func TestSubmittedDisplacesCrawled(t *testing.T) {
	const otherPayTo = "0x3333333333333333333333333333333333333333"
	for _, c := range []struct {
		name  string
		items func(i int) (url, payTo string)
	}{
		// Three crawled listings paying the submitter's wallet, on other domains.
		{"per_pay_to", func(i int) (string, string) {
			return "https://api.seller" + strconv.Itoa(i) + ".net/q", testPayTo
		}},
		// Three crawled listings on the submitter's domain, paying someone else.
		{"per_domain", func(i int) (string, string) {
			return "https://example.com/crawled/" + strconv.Itoa(i), otherPayTo
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			endpoints := map[string]http.Handler{"/mine": newChallenge("search", 1000), "/vetfull": newChallenge("search", 1000)}
			h, x := submitHarness(t, endpoints)
			var items, ids []string
			for i := range 3 {
				u, payTo := c.items(i)
				// Fewer payers ranks lower: the third is the lowest.
				items = append(items, bazaarItemJSON(u, "GET", 1000, payTo, "eip155:8453", "data api", quality(30-10*i, 100)))
			}
			h.fake.extra[discoveryPath] = &fakeBazaar{items: items}
			if n, err := x.importCatalogue(context.Background(), h.now); err != nil || n != 3 {
				t.Fatalf("import: %d, %v", n, err)
			}
			for _, r := range x.cat.snap.Load().open {
				ids = append(ids, r.ID)
			}
			if len(ids) != 3 {
				t.Fatalf("crawled: %v", ids)
			}
			lowest := ids[2]
			// The wallet proof (the challenge pays testPayTo) for the
			// recipient cap, the domain proof for the domain cap.
			s := X402Submission{URL: "https://example.com/mine"}
			if c.name == "per_pay_to" {
				mine, _ := ParseEVMAddress(testPayTo)
				s.Wallets = map[EVMAddress]bool{mine: true}
			}
			if _, err := h.submit(s); err != nil {
				t.Fatalf("an owner-proven submission under a crawled cap: %v", err)
			}
			mine := openIDFor("/mine", "GET")
			check := func(when string) {
				t.Helper()
				snap := x.cat.snap.Load()
				if snap.byID[mine] == nil || snap.byID[mine].Submitter() != submitter {
					t.Fatalf("%s: the submitted listing is not listed", when)
				}
				if _, ok := snap.byID[lowest]; ok {
					t.Fatalf("%s: the lowest crawled listing was not displaced", when)
				}
				for _, id := range ids[:2] {
					if _, ok := snap.byID[id]; !ok {
						t.Fatalf("%s: crawled %s displaced too", when, id)
					}
				}
				n := 0
				for _, r := range snap.open {
					if (c.name == "per_pay_to" && r.PayTo == snap.byID[mine].PayTo) || (c.name == "per_domain" && r.domain == snap.byID[mine].domain) {
						n++
					}
				}
				if n != 3 {
					t.Fatalf("%s: %d listings under the cap, want 3", when, n)
				}
				var rows int
				if err := h.db.QueryRow("SELECT count(*) FROM x402_catalogue WHERE id=?", lowest).Scan(&rows); err != nil || rows != 1 {
					t.Fatalf("%s: the displaced row was deleted: %d, %v", when, rows, err)
				}
			}
			check("after the submission")
			// A later crawl lists the same three again.
			h.now += 3600
			if _, err := x.importCatalogue(context.Background(), h.now); err != nil {
				t.Fatal(err)
			}
			if src := storedRow(t, h, mine)["source"].(string); len(src) < len(x402SubmittedPrefix) || src[:len(x402SubmittedPrefix)] != x402SubmittedPrefix {
				t.Fatalf("the crawl overwrote the submitted row: %s", src)
			}
			check("after a crawl")
			// The cap full of vetted and submitted entries: a further
			// submission is refused.
			vetOpen(t, h, x, ids[0], ids[1])
			if _, err := h.submit(X402Submission{URL: "https://example.com/vetfull", Wallets: s.Wallets}); errCode(err) != "x402_listing_caps" {
				t.Fatalf("a cap full of vetted and submitted entries: %v", err)
			}
		})
	}
}

// A cap full of vetted crawled entries refuses a submission.
func TestSubmitVettedCapRefuses(t *testing.T) {
	h, x := submitHarness(t, map[string]http.Handler{"/mine": newChallenge("search", 1000)})
	var items []string
	for i := range 3 {
		items = append(items, bazaarItemJSON("https://example.com/crawled/"+strconv.Itoa(i), "GET", 1000, testPayTo, "eip155:8453", "data api", ""))
	}
	h.fake.extra[discoveryPath] = &fakeBazaar{items: items}
	if _, err := x.importCatalogue(context.Background(), h.now); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range x.cat.snap.Load().open {
		ids = append(ids, r.ID)
	}
	vetOpen(t, h, x, ids...)
	if _, err := h.submit(X402Submission{URL: "https://example.com/mine"}); errCode(err) != "x402_listing_caps" {
		t.Fatalf("a cap full of vetted entries: %v", err)
	}
	if _, ok := x.cat.snap.Load().byID[openIDFor("/mine", "GET")]; ok {
		t.Fatal("refused, yet listed")
	}
}

// A path template (C314): the relay calls a listing's URL as listed and
// fills query parameters only, so /ares/{ico} is never callable. A
// submission is refused before any probe, literal or escaped; withdrawing
// still parses; a crawled item with one is not admitted; and a row stored
// before the refusal is marked stale at the refresh (last_seen 0, kept),
// never probed and never listed.
func TestSubmitPathTemplate(t *testing.T) {
	tmpl, concrete := newChallenge("company registry", 1000), newChallenge("company registry", 1000)
	h, x := submitHarness(t, map[string]http.Handler{"/ares/{ico}": tmpl, "/ares/27074358": concrete})
	for _, u := range []string{"https://example.com/ares/{ico}", "https://example.com/ares/%7Bico%7D", "https://example.com/ares/%7bico%7d", "https://example.com/ares/x}"} {
		if _, err := h.submit(X402Submission{URL: u}); errCode(err) != "x402_listing_path_template" {
			t.Errorf("%s: %v, want x402_listing_path_template", u, err)
		}
	}
	if tmpl.hits != 0 {
		t.Fatalf("a templated path was probed %d times", tmpl.hits)
	}
	if _, _, err := ParseX402Target("GET https://example.com/ares/{ico}"); err != nil {
		t.Fatalf("withdrawing a templated listing must still parse: %v", err)
	}
	if !x402PathTemplate("https://example.com/a/%7Bb%7D") || x402PathTemplate("https://example.com/ares/27074358?q={x}") || x402PathTemplate("https://example.com/") {
		t.Fatal("x402PathTemplate reads the path only, escaped or not")
	}
	item := bazaarItemJSON("https://example.com/ares/{ico}", "GET", 1000, testPayTo, "eip155:8453", "company registry", "")
	if _, _, ok := bazaarResource(json.RawMessage(item), x.cfg, min(x.cfg.Catalogue.MaxPrice, x.cfg.PerCall)); ok {
		t.Fatal("a crawled item with a path template was admitted")
	}

	// A row stored before the refusal, as the live one was.
	if _, err := h.submit(X402Submission{URL: "https://example.com/ares/27074358"}); err != nil {
		t.Fatal(err)
	}
	id := openIDFor("/ares/27074358", "GET")
	if _, err := h.db.Exec("UPDATE x402_catalogue SET url='https://example.com/ares/%7Bico%7D' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	concrete.hits = 0
	h.now += 3600
	if _, err := x.reprobeSubmitted(context.Background(), h.now); err != nil {
		t.Fatal(err)
	}
	var lastSeen int64
	if err := h.db.QueryRow("SELECT last_seen FROM x402_catalogue WHERE id=?", id).Scan(&lastSeen); err != nil || lastSeen != 0 {
		t.Fatalf("the templated row is kept and marked stale: last_seen %d, %v", lastSeen, err)
	}
	if _, ok := x.cat.snap.Load().byID[id]; ok {
		t.Fatal("a templated row is still listed")
	}
	if tmpl.hits != 0 || concrete.hits != 0 {
		t.Fatalf("the refresh probed a templated row: %d %d", tmpl.hits, concrete.hits)
	}
}

// An ephemeral tunnel host (C316): trycloudflare.com and the rest change
// their hostname on every restart, so a listing on one dies within hours. A
// submission is refused before any probe, suffix matching is label-exact, a
// crawled item on one is not admitted, and a row stored before the refusal
// is marked stale at the refresh (last_seen 0, kept), never probed and
// never listed, exactly as a path template is (TestSubmitPathTemplate).
func TestSubmitEphemeralHost(t *testing.T) {
	concrete := newChallenge("company registry", 1000)
	h, x := submitHarness(t, map[string]http.Handler{"/ares/27074358": concrete})
	for _, u := range []string{
		"https://abc123.trycloudflare.com/ares/27074358", "https://a.b.trycloudflare.com/x", "https://foo.ngrok-free.app/x",
		"https://foo.ngrok.app/x", "https://foo.ngrok.io/x", "https://foo.loca.lt/x", "https://foo.localtunnel.me/x",
		"https://foo.serveo.net/x", "https://foo.localhost.run/x", "https://foo.lhr.life/x", "https://foo.pinggy.link/x", "https://foo.bore.pub/x",
	} {
		if _, err := h.submit(X402Submission{URL: u}); errCode(err) != "x402_listing_ephemeral_host" {
			t.Errorf("%s: %v, want x402_listing_ephemeral_host", u, err)
		}
	}
	if concrete.hits != 0 {
		t.Fatalf("an ephemeral host was probed %d times", concrete.hits)
	}
	if _, _, err := ParseX402Target("GET https://abc123.trycloudflare.com/x"); err != nil {
		t.Fatalf("withdrawing an ephemeral-host listing must still parse: %v", err)
	}
	// Label-exact: a suffix only matches on a label boundary.
	if x402EphemeralHost("https://xtrycloudflare.com/x") {
		t.Fatal("xtrycloudflare.com was matched as trycloudflare.com")
	}
	if !x402EphemeralHost("https://a.b.trycloudflare.com/x") || !x402EphemeralHost("https://trycloudflare.com/x") {
		t.Fatal("a.b.trycloudflare.com or the bare suffix was not matched")
	}

	item := bazaarItemJSON("https://abc123.trycloudflare.com/ares/27074358", "GET", 1000, testPayTo, "eip155:8453", "company registry", "")
	if _, _, ok := bazaarResource(json.RawMessage(item), x.cfg, min(x.cfg.Catalogue.MaxPrice, x.cfg.PerCall)); ok {
		t.Fatal("a crawled item on an ephemeral tunnel host was admitted")
	}

	// A row stored before the refusal, as the live one was.
	if _, err := h.submit(X402Submission{URL: "https://example.com/ares/27074358"}); err != nil {
		t.Fatal(err)
	}
	id := openIDFor("/ares/27074358", "GET")
	if _, err := h.db.Exec("UPDATE x402_catalogue SET url='https://abc123.trycloudflare.com/ares/27074358' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	concrete.hits = 0
	h.now += 3600
	if _, err := x.reprobeSubmitted(context.Background(), h.now); err != nil {
		t.Fatal(err)
	}
	var lastSeen int64
	if err := h.db.QueryRow("SELECT last_seen FROM x402_catalogue WHERE id=?", id).Scan(&lastSeen); err != nil || lastSeen != 0 {
		t.Fatalf("the ephemeral-host row is kept and marked stale: last_seen %d, %v", lastSeen, err)
	}
	if _, ok := x.cat.snap.Load().byID[id]; ok {
		t.Fatal("an ephemeral-host row is still listed")
	}
	if concrete.hits != 0 {
		t.Fatalf("the refresh probed an ephemeral-host row: %d", concrete.hits)
	}
}
