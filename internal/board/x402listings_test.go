package board

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"swarmmemo/internal/find"
	"swarmmemo/internal/services"
)

// x402ListingStore is a board with the x402 relay and its open catalogue
// on, every upstream an httptest server standing in for example.com: one
// endpoint answering 402 with an x402 challenge (/api), others not.
func x402ListingStore(t *testing.T) *Store {
	t.Helper()
	s := openTest(t, Config{Features: Features{Services: []string{"x402"}}, X402: x402ListingConfig(t)})
	t.Cleanup(s.stopServices)
	return s
}

// x402ListingConfig is that relay's configuration: example.com and
// other.example.com both reach the httptest server, where every path under /api
// answers 402 with an x402 challenge and any other path is not found.
func x402ListingConfig(t *testing.T) *services.X402Config {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	config := write("x402.json", `{"schema":1,"enabled":true,"network":"eip155:8453","asset":"0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913","asset_name":"USD Coin","asset_version":"2","decimals":6,
"caps":{"global_daily":"1","agent_daily":"0.5","per_call":"0.05"},
"wallet_key_file":"`+write("wallet.key", "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80\n")+`",
"allowlist_file":"`+write("allowlist.json", `{"schema":1,"version":2,"resources":[]}`)+`",
"catalogue":{"discovery_urls":["https://example.com/discovery"],"mpp_directory_url":"off","max_price":"0.002","open_daily":"0.004","recipient_daily":"0.002"}}`)
	cfg, err := services.LoadX402Config(config)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api") {
			http.NotFound(w, r)
			return
		}
		raw, _ := json.Marshal(map[string]any{"x402Version": 2, "resource": map[string]any{"description": "Web search"},
			"accepts": []any{map[string]any{"scheme": "exact", "network": "eip155:8453", "amount": "1000", "asset": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
				"payTo": "0x209693Bc6afc0C5328bA36FaF03C514EF312287C", "maxTimeoutSeconds": 60, "extra": map[string]any{"name": "USD Coin", "version": "2"}}}})
		w.Header().Set("PAYMENT-REQUIRED", base64.StdEncoding.EncodeToString(raw))
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	t.Cleanup(srv.Close)
	target := srv.Listener.Addr().String()
	cfg.UseTestUpstream(srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "example.com:443" && addr != "other.example.com:443" {
			return nil, fmt.Errorf("unexpected address %s", addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	})
	return cfg
}

// x402.submit end to end on the board: ownership through a verified domain
// link (subjectOwners), the daily limit, the listing in Find with its
// submitter's handle, the funnel's seller act, and x402.withdraw.
func TestX402SubmitOnTheBoard(t *testing.T) {
	s := x402ListingStore(t)
	seller, stranger := keyFor(91), keyFor(92)
	run(t, s, signed(seller, Command{Operation: "agent.register", Handle: "tuan"}))
	register(t, s, stranger)
	cmd := func(op, target, data string, key int) Command {
		return signed(keyFor(byte(key)), Command{Operation: op, Target: target, Data: data, RequestID: randomID()})
	}
	const target = "GET https://example.com/api"

	// Unsigned, a bad target, no domain link: refused before anything is
	// fetched.
	fails(t, s, Command{Operation: "x402.submit", Target: target, RequestID: randomID()}, "signature_required")
	fails(t, s, cmd("x402.submit", "GET http://example.com/api", "", 91), "x402_listing_target")
	fails(t, s, cmd("x402.submit", target, "", 91), "x402_listing_unowned")
	fails(t, s, cmd("x402.submit", target, `{"schema":1,"extra":1}`, 91), "x402_listing_data")
	// A claimed (unverified) link is not ownership; a verified one on the
	// registrable domain is.
	if _, err := s.db.Exec("INSERT INTO identity_links(agent,kind,value,state,created_at) VALUES(?,'domain','example.com','claimed',?)", keyID(stranger), testTime); err != nil {
		t.Fatal(err)
	}
	fails(t, s, cmd("x402.submit", target, "", 92), "x402_listing_unowned")
	if _, err := s.db.Exec("INSERT INTO identity_links(agent,kind,value,state,created_at,checked_at) VALUES(?,'domain','example.com','verified',?,?)", keyID(seller), testTime, testTime); err != nil {
		t.Fatal(err)
	}
	out := run(t, s, cmd("x402.submit", target, `{"schema":1,"summary":"Web search, ten results","category":"search"}`, 91))
	l, _ := out.Data["listing"].(map[string]any)
	if l == nil || l["source"] != "submitted" || l["callable"] != false || l["category"] != "search" || l["url"] != "https://example.com/api" {
		t.Fatalf("listing: %v", out.Data)
	}
	// Probe failures are refusals with their own codes.
	fails(t, s, cmd("x402.submit", "GET https://example.com/nothing", "", 91), "x402_listing_no_challenge")

	// Find lists it, named by its submitter's handle.
	var hit *find.Hit
	for _, h := range run(t, s, Command{Operation: "find", Query: "web search"}).Data["results"].([]find.Hit) {
		if h.Kind == find.KindX402 {
			hit = &h
		}
	}
	if hit == nil || hit.ListedBy != "@tuan" || hit.SubjectID == "" {
		t.Fatalf("Find: %+v", hit)
	}
	// The seller act.
	var listed int
	_ = s.db.QueryRow("SELECT count(*) FROM audit WHERE operation=? AND target=?", auditX402Listed, accountOf(t, s, seller)).Scan(&listed)
	if listed != 1 {
		t.Fatalf("listed acts recorded: %d", listed)
	}

	// The daily limit counts every submission that reached the probe.
	for range services.X402SubmitsPerDay - 2 {
		run(t, s, cmd("x402.submit", target, "", 91))
	}
	fails(t, s, cmd("x402.submit", target, "", 91), "x402_listing_limit")

	// Withdraw: another account without the domain cannot; the submitter can.
	fails(t, s, cmd("x402.withdraw", target, "", 92), "x402_listing_not_found")
	if got := run(t, s, cmd("x402.withdraw", target, "", 91)); got.Data["withdrawn"] == nil {
		t.Fatalf("withdraw: %v", got.Data)
	}
	for _, h := range run(t, s, Command{Operation: "find", Query: "web search"}).Data["results"].([]find.Hit) {
		if h.Kind == find.KindX402 {
			t.Fatalf("withdrawn, still found: %+v", h)
		}
	}

	// The wallet proof, without the domain: a verified wallet link the
	// challenge pays is enough (accountWallets, as for offerings); a wallet
	// the challenge does not pay is not.
	host, other := keyFor(93), keyFor(94)
	run(t, s, signed(host, Command{Operation: "agent.register", Handle: "hosted-seller"}))
	register(t, s, other)
	for key, wallet := range map[int]string{93: "0x209693bc6afc0c5328ba36faf03c514ef312287c", 94: "0x1111111111111111111111111111111111111111"} {
		if _, err := s.db.Exec("INSERT INTO identity_links(agent,kind,value,state,created_at,checked_at) VALUES(?,'wallet',?,'verified',?,?)", keyID(keyFor(byte(key))), wallet, testTime, testTime); err != nil {
			t.Fatal(err)
		}
	}
	fails(t, s, cmd("x402.submit", target, "", 94), "x402_listing_unowned")
	out = run(t, s, cmd("x402.submit", target, "", 93))
	if l, _ := out.Data["listing"].(map[string]any); l == nil || l["proof"] != services.ProofWallet {
		t.Fatalf("wallet listing: %v", out.Data)
	}
}

func accountOf(t *testing.T, s *Store, key ed25519.PrivateKey) string {
	t.Helper()
	var account string
	if err := s.db.QueryRow("SELECT account FROM identities WHERE id=?", keyID(key)).Scan(&account); err != nil {
		t.Fatal(err)
	}
	return account
}

// C312: a listing proven by wallet is its submitter's (the API pays its
// verified wallet). The submitter is among subjectOwners, so it gets the
// owner pings, and its own review of its listing weighs 0 on every
// surface: subject.get's score_shadow, the reputation's sells and Find.
func TestX402WalletSubmitterOwnsItsListing(t *testing.T) {
	s := x402ListingStore(t)
	s.config.Features.Reviews, s.config.Features.InboxEntries = true, InboxRead
	seller, r1, r2, r3 := keyFor(95), keyFor(96), keyFor(97), keyFor(98)
	run(t, s, signed(seller, Command{Operation: "agent.register", Handle: "walletseller"}))
	for _, k := range []ed25519.PrivateKey{r1, r2, r3} {
		register(t, s, k)
	}
	seasoned(t, s, seller, r1, r2, r3)
	account := accountOf(t, s, seller)
	if _, err := s.db.Exec("INSERT INTO identity_links(agent,kind,value,state,created_at,checked_at) VALUES(?,'wallet','0x209693bc6afc0c5328ba36faf03c514ef312287c','verified',?,?)", keyID(seller), testTime, testTime); err != nil {
		t.Fatal(err)
	}
	const uri = "x402:GET https://example.com/api"
	out := run(t, s, signed(seller, Command{Operation: "x402.submit", Target: "GET https://example.com/api", RequestID: randomID()}))
	if l, _ := out.Data["listing"].(map[string]any); l == nil || l["proof"] != services.ProofWallet {
		t.Fatalf("wallet listing: %v", out.Data)
	}
	sub := reviewSubjectOf(t, uri)

	run(t, s, reviewPost(t, r1, uri, map[string]any{"verdict": "good", "dimensions": map[string]int{"good": 70}}, ""))
	run(t, s, reviewPost(t, r2, uri, map[string]any{"verdict": "mixed"}, ""))
	run(t, s, reviewPost(t, r3, uri, map[string]any{"verdict": "good", "dimensions": map[string]int{"good": 75}}, ""))
	run(t, s, reviewPost(t, seller, uri, map[string]any{"verdict": "good", "dimensions": map[string]int{"good": 100}}, ""))

	// An owner: listed with its proof, and pinged for each other review.
	data := readSubjectData(t, s, uri)
	info := data["subject"].(SubjectInfo)
	if len(info.Owners) != 1 || info.Owners[0].Account != account || info.Owners[0].Link != "submitted:"+services.ProofWallet {
		t.Fatalf("owners %+v", info.Owners)
	}
	if got := inboxKinds(t, s, account, inboxReview); len(got) != 3 {
		t.Fatalf("owner pings %v", got)
	}
	// Its own review weighs 0 everywhere.
	for _, c := range data["reviews"].([]ReviewCard) {
		if (c.Author.Account == account) != (c.Order != nil && c.Order.Weight == 0) {
			t.Fatalf("review by %s weighs %+v", c.Author.Account, c.Order)
		}
	}
	agg := data["aggregate"].(ReviewAggregate)
	tx, err := s.db.BeginTx(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	points, n, err := s.subjectJudgements(testContext, tx, sub, s.now().Unix())
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	want := scoreOf(points, n)
	if want.Judgements != 3 || agg.ScoreShadow != want.Score || agg.ScoreThin != want.Thin {
		t.Fatalf("aggregate %d %v, filtered %+v", agg.ScoreShadow, agg.ScoreThin, want)
	}
	rep := readReputation(t, s, account)
	if len(rep.Reviewed.Owned) == 0 || rep.Reviewed.Owned[0].ID != sub.ID || rep.Reviewed.Owned[0].ReputationScore != want || rep.Reviewed.Sells == nil || *rep.Reviewed.Sells != want {
		t.Fatalf("reputation %+v, want %+v", rep.Reviewed, want)
	}
	q, err := s.findQuality(testContext, s.now().Unix())
	if err != nil {
		t.Fatal(err)
	}
	if got := q[sub.ID]; got.Reviews != agg.Reviews || got.ReviewMean != agg.VerdictMean || got.ReviewWeight != agg.WeightTotal {
		t.Fatalf("find quality %+v, aggregate %+v", got.Quality, agg)
	}
}
