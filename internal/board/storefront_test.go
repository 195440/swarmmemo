package board

import (
	"crypto/ed25519"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/judge"
)

// The storefront (C305) on the real store: a seeded agent's storefront has
// exactly the expected rows (its two offerings and its one live listing,
// never its stale listing or another agent's; its work; its backers; its
// latest reviews), agent.get carries exactly what the one function builds,
// and an agent with nothing has only its header.

// storefrontProtocolSection is PROTOCOL.md's generated storefront section:
// the rule and the fields from their one source (storefront.go).
func storefrontProtocolSection() string {
	return "- **Storefront.** " + StorefrontRule + "\n" +
		"- **Fields.** `handle`, `agent`, `line` (the reputation's); `sells` [{`kind` (offering or x402), `title`, `method`, `url`, `price`, `version`, `paid_calls`, `proof`, `subject`, `subject_id`, `page`, `score` {`" + judge.ScoreField + "`, `judgements`, `thin`}, `call`, `review`}], each action {`url`, `command`, `note`}; `works` {`record`: the `work_record`, `open` [{`id`, `title`, `url`, `reward`, `reward_usdc`, `reward_note`, `deadline`}], `hire`}; `backed_by`: the reputation's; `reviews` [{`id`, `subject`, `page`, `author`, `verdict`, `evidence_class`, `badge`, `text`, `created_at`, `thread`}].\n"
}

// storefrontStore runs reviews, offerings, the x402 relay with its open
// catalogue, and paid work on the ledger.
func storefrontStore(t *testing.T) *Store {
	t.Helper()
	if _, _, ok := ledgerTestOverride(); ok {
		t.Skip("the ledger mode is overridden for the whole suite")
	}
	c := rewardConfig()
	c.ServiceID = "swarmmemo.com"
	c.Features.Services = append(c.Features.Services, "x402")
	c.Features.Reviews = true
	c.X402 = x402ListingConfig(t)
	c.Offerings = topupConfig(t, &fakeFacilitator{}, "")
	s := openTest(t, c)
	t.Cleanup(s.stopServices)
	t.Cleanup(s.stopOfferings)
	setRewardParams(t, s)
	return s
}

// readStorefront is the one function's storefront of account, in its own
// read transaction.
func readStorefront(t *testing.T, s *Store, account, agent string) *Storefront {
	t.Helper()
	tx, err := s.db.BeginTx(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	now := s.now().Unix()
	rep, err := s.reputation(testContext, tx, account, now)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.storefront(testContext, tx, account, agent, rep, now)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestStorefrontRows(t *testing.T) {
	s := storefrontStore(t)
	seller, other, r1, r2 := keyFor(170), keyFor(171), keyFor(172), keyFor(173)
	provider(t, s, seller, "oracle")
	run(t, s, signed(other, Command{Operation: "agent.register", Handle: "neighbour"}))
	register(t, s, r1)
	register(t, s, r2)
	seasoned(t, s, r1, r2)
	account := keyID(seller)
	for key, domain := range map[string]string{account: "example.com", keyID(other): "other.example.com"} {
		if _, err := s.db.Exec("INSERT INTO identity_links(agent,kind,value,state,created_at,checked_at) VALUES(?,'domain',?,'verified',?,?)", key, domain, testTime, testTime); err != nil {
			t.Fatal(err)
		}
	}

	// Sells: two offerings, one paid call on the first.
	publish(t, s, seller, listing("forecast", nil))
	publish(t, s, seller, listing("stars", map[string]any{"title": "Read the stars", "price": "1.50"}))
	seedAnsweredCall(t, s, randomID(), account, "forecast", r1, s.now().Unix())
	// Its two listings, one gone stale; the neighbour's listing, submitted
	// after (its load drops the stale one).
	submit := func(key ed25519.PrivateKey, target string) {
		run(t, s, signed(key, Command{Operation: "x402.submit", Target: target, RequestID: randomID()}))
	}
	submit(seller, "GET https://example.com/api")
	submit(seller, "GET https://example.com/api2")
	if _, err := s.db.Exec("UPDATE x402_catalogue SET last_seen=1 WHERE url='https://example.com/api2'"); err != nil {
		t.Fatal(err)
	}
	submit(other, "GET https://other.example.com/api3")

	// Works: r1's paid task, done by the seller and accepted with a rating
	// (a review with work evidence); the seller's own open task.
	mintCredit(t, s, keyID(r1), allowance.Paid, 1_000_000)
	mintCredit(t, s, account, allowance.Paid, 1_000_000)
	done := rewardRequest(t, s, r1, "lobby")
	run(t, s, rewardCreate(s, r1, done, 12_000, 0))
	fence := claimAndSubmit(t, s, seller, done, "lobby")
	rated := run(t, s, ratedAccept(t, s, r1, done, fence, account, 90, "Fast and correct.")).Data["review"].(*Receipt).ID
	open := rewardRequest(t, s, seller, "lobby")
	run(t, s, rewardCreate(s, seller, open, 20_000, 0))

	// Reviews: r2 of the agent and of the first offering; r2 of the
	// neighbour's listing (not the seller's).
	agentReview := run(t, s, reviewPost(t, r2, "agent:"+account, map[string]any{"verdict": "good"}, "")).Receipt.ID
	offeringReview := run(t, s, reviewPost(t, r2, "x402:POST https://swarmmemo.com/@oracle/forecast", map[string]any{"verdict": "good"}, "")).Receipt.ID
	run(t, s, reviewPost(t, r2, "x402:GET https://other.example.com/api3", map[string]any{"verdict": "good"}, ""))

	f := run(t, s, Command{Operation: "agent.get", Target: account}).Agent.Storefront
	sub := func(uri string) string { return reviewSubjectOf(t, uri).ID }
	want := []string{
		"sells:" + sub("x402:POST https://swarmmemo.com/@oracle/forecast"), "sells:" + sub("x402:POST https://swarmmemo.com/@oracle/stars"), "sells:" + sub("x402:GET https://example.com/api"),
		"works:record", "works:" + open,
		"backed_by:" + keyID(r1), "backed_by:" + keyID(r2),
		"reviews:" + rated, "reviews:" + agentReview, "reviews:" + offeringReview,
	}
	got := f.Rows()
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("storefront rows\n got %q\nwant %q", got, want)
	}
	// The rows themselves: offerings first, by name, then the listing.
	forecast, stars, api := f.Sells[0], f.Sells[1], f.Sells[2]
	price := func(name string) any {
		return run(t, s, Command{Operation: "offering.get", Target: "oracle/" + name}).Data["offering"].(map[string]any)["price"]
	}
	if forecast.Kind != "offering" || forecast.Title != "Consult the oracle" || forecast.Price != price("forecast") || forecast.Version != 1 || forecast.PaidCalls == nil || *forecast.PaidCalls != 1 ||
		forecast.Call == nil || forecast.Call.URL != "/@oracle/forecast" || forecast.Page != "/s/"+forecast.SubjectID || forecast.Score == nil || forecast.Score.Judgements != 1 || !forecast.Score.Thin {
		t.Fatalf("forecast %+v paid %v score %+v call %+v", forecast, forecast.PaidCalls, forecast.Score, forecast.Call)
	}
	if stars.Title != "Read the stars" || stars.Price != price("stars") || *stars.PaidCalls != 0 || stars.Score == nil || stars.Score.Judgements != 0 {
		t.Fatalf("stars %+v", stars)
	}
	if api.Kind != "x402" || api.Method != "GET" || api.URL != "https://example.com/api" || api.Proof != "domain" || api.Price != "0.001" || api.Call != nil ||
		api.Review.URL != api.Page || api.Score == nil {
		t.Fatalf("listing %+v score %+v", api, api.Score)
	}
	if w := f.Works; w.Record == nil || w.Record.AsWorker.Accepted != 1 || len(w.Open) != 1 || w.Open[0].Reward == nil || w.Open[0].Reward.Amount != 20_000 || w.Hire.URL != "/work" {
		t.Fatalf("works %+v", w)
	}
	badges := map[string]string{}
	for _, r := range f.Reviews {
		badges[r.ID] = r.Badge
	}
	if badges[rated] != "work ✓" || badges[agentReview] != "opinion" || badges[offeringReview] != "opinion" {
		t.Fatalf("badges %v", badges)
	}
	if f.Handle != "oracle" || f.Line == "" || f.Line != run(t, s, Command{Operation: "agent.get", Target: account}).Agent.Reputation.Line {
		t.Fatalf("header %+v", f)
	}

	// One function: agent.get carries exactly what storefront builds.
	agent := run(t, s, Command{Operation: "agent.get", Target: account}).Agent
	if a, b := storefrontJSON(t, agent.Storefront), storefrontJSON(t, readStorefront(t, s, account, agent.ID)); a != b {
		t.Fatalf("agent.get storefront drifted from storefront:\n%s\n%s", a, b)
	}
	// The neighbour's storefront has its listing and nothing of the seller's.
	n := run(t, s, Command{Operation: "agent.get", Target: keyID(other)}).Agent.Storefront
	if len(n.Sells) != 1 || n.Sells[0].URL != "https://other.example.com/api3" || len(n.Reviews) != 1 {
		t.Fatalf("neighbour %+v", n)
	}
}

func storefrontJSON(t *testing.T, f *Storefront) string {
	t.Helper()
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// An agent with nothing: only its header.
func TestStorefrontEmpty(t *testing.T) {
	s := storefrontStore(t)
	key := keyFor(174)
	run(t, s, signed(key, Command{Operation: "agent.register", Handle: "quiet"}))
	f := run(t, s, Command{Operation: "agent.get", Target: keyID(key)}).Agent.Storefront
	if f == nil || len(f.Rows()) != 0 || f.Sells != nil || f.Works != nil || f.BackedBy != nil || f.Reviews != nil || f.Handle != "quiet" || f.Agent != keyID(key) || f.Line == "" {
		t.Fatalf("empty storefront %+v", f)
	}
	var keys map[string]any
	if err := json.Unmarshal([]byte(storefrontJSON(t, f)), &keys); err != nil || len(keys) != 3 {
		t.Fatalf("empty storefront JSON %v %v", keys, err)
	}
}
