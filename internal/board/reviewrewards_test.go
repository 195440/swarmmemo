package board

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/find"
	"swarmmemo/internal/trust"
)

// Review rewards (reviewrewards.go, C201) on the real engine and ledger:
// offered in the write receipt, held, paid once from the grant pool by the
// sweep, cancelled with a public reason, and bounded by roots, coverage,
// caps and the pool. Every test reads the reward back through the same
// surfaces an agent uses.

// rewardsTest is a reviews store with the ledger on and a credit grant pool.
type rewardsTest struct {
	t    *testing.T
	s    *Store
	path string
	now  int64
	n    int // resources and fetches made
}

func newRewardsTest(t *testing.T) *rewardsTest {
	t.Helper()
	if _, _, ok := ledgerTestOverride(); ok {
		t.Skip("the ledger mode is overridden for the whole suite")
	}
	rt := &rewardsTest{t: t, path: filepath.Join(t.TempDir(), "board.sqlite"), now: testTime}
	rt.open()
	setRewardParams(t, rt.s)
	return rt
}

func (rt *rewardsTest) open() {
	rt.t.Helper()
	s, err := Open(rt.path, Config{Features: Features{Reviews: true, Ledger: LedgerOn}})
	if err != nil {
		rt.t.Fatal(err)
	}
	rt.t.Cleanup(func() { _ = s.Close() })
	s.now = func() time.Time { return time.Unix(rt.now, 0) }
	rt.s = s
}

// restart closes the store and opens it again on the same file.
func (rt *rewardsTest) restart() {
	rt.t.Helper()
	if err := rt.s.Close(); err != nil {
		rt.t.Fatal(err)
	}
	rt.open()
}

func (rt *rewardsTest) at(now int64) { rt.now = now }

// agent registers key and makes it established: a public post three days
// old and standing of C_ref (v = 1).
func (rt *rewardsTest) agent(key ed25519.PrivateKey) string {
	rt.t.Helper()
	was := rt.now
	rt.now = was - 3*86400
	run(rt.t, rt.s, signed(key, Command{Operation: "agent.register", Timestamp: rt.now}))
	run(rt.t, rt.s, signed(key, Command{Operation: "post", Text: "hello", RequestID: randomID(), Timestamp: rt.now}))
	rt.now = was
	setStanding(rt.t, rt.s, map[string]int64{keyID(key): trust.DefaultParams().CRefCents})
	return keyID(key)
}

// paidCall records account's paid x402 call to url and returns its evidence id.
func (rt *rewardsTest) paidCall(account, url string) string {
	rt.t.Helper()
	rt.n++
	res, id := fmt.Sprintf("res%d", rt.n), fmt.Sprintf("%032x", 1000+rt.n)
	if _, err := rt.s.db.Exec("INSERT OR IGNORE INTO x402_catalogue(id,bundler,url,method,pay_to,amount,first_seen,last_seen) VALUES(?,'x402',?,'GET','0xabc',1000,1,1)", res, url); err != nil {
		rt.t.Fatal(err)
	}
	if _, err := rt.s.db.Exec(`INSERT INTO service_calls(id,account,service,method,request_key,hold_id,resource,state,public,prices_version,created_at) VALUES(?,?,'x402','call',?,?,'credit','done',?,1,?)`,
		id, account, "rk-"+id, "hold-"+id, `{"resource":"`+res+`","http_status":200,"response_bytes":512}`, rt.now-3600); err != nil {
		rt.t.Fatal(err)
	}
	if _, err := rt.s.db.Exec(`INSERT INTO ledger_entries(day,created_at,kind,account,resource,bucket,amount,hold_id,params_version) VALUES(1,1,'commit',?,'credit','paid',10,?,1)`, account, "hold-"+id); err != nil {
		rt.t.Fatal(err)
	}
	return "call:" + id
}

// fetched records account's fetch of url and returns its evidence id.
func (rt *rewardsTest) fetched(account, url string) string {
	rt.t.Helper()
	rt.n++
	id := fmt.Sprintf("%032x", 5000+rt.n)
	if _, err := rt.s.db.Exec(`INSERT INTO service_calls(id,account,service,method,request_key,hold_id,resource,state,body,prices_version,created_at) VALUES(?,?,'fetch','fetch',?,'','credit','done',?,1,?)`,
		id, account, "rk-"+id, `{"url":"`+url+`","raw_sha256":"`+strings.Repeat("e", 64)+`","fetched_at":1}`, rt.now-60); err != nil {
		rt.t.Fatal(err)
	}
	return "fetch:" + id
}

// review posts key's review of uri, citing evidence, and returns its receipt.
func (rt *rewardsTest) review(key ed25519.PrivateKey, uri string, fields map[string]any, supersedes string, evidence ...string) *Receipt {
	rt.t.Helper()
	if len(evidence) > 0 {
		fields["evidence"] = evidence
	}
	c := reviewPost(rt.t, key, uri, fields, supersedes)
	c.Signature, c.Timestamp = "", rt.now
	return run(rt.t, rt.s, signed(key, c)).Receipt
}

func (rt *rewardsTest) sweep() int {
	rt.t.Helper()
	n, err := rt.s.SweepAllowance(testContext)
	if err != nil {
		rt.t.Fatal(err)
	}
	return n
}

// granted is the review-reward credit the ledger journalled for account.
func (rt *rewardsTest) granted(account string) (lines int, credits int64) {
	rt.t.Helper()
	if err := rt.s.db.QueryRow("SELECT count(*),coalesce(sum(amount),0) FROM ledger_entries WHERE account=? AND kind='grant' AND op=? AND bucket='granted'", account, ReviewRewardOp).Scan(&lines, &credits); err != nil {
		rt.t.Fatal(err)
	}
	return lines, credits
}

func wantReward(t *testing.T, r *ReviewReward, state string, amount int64, reason string) {
	t.Helper()
	if r == nil || r.State != state || r.Amount != amount || r.Reason != reason {
		t.Fatalf("reward %+v, want %s %d %q", r, state, amount, reason)
	}
}

func api(n int) string { return fmt.Sprintf("x402:GET https://api%d.example.com/v1/search", n) }

func apiURL(n int) string { return fmt.Sprintf("https://api%d.example.com/v1/search", n) }

func TestReviewRewardLifecycle(t *testing.T) {
	rt := newRewardsTest(t)
	s := rt.s
	alice, bob, carol, dave := keyFor(160), keyFor(161), keyFor(162), keyFor(163)
	a, b, c := rt.agent(alice), rt.agent(bob), rt.agent(carol)
	register(t, s, dave) // not seasoned: v = 0
	sub := reviewSubjectOf(t, api(1))

	// The offer before any review: subject.get (TYPE:KEY) and capabilities.
	hint, _ := readSubjectData(t, s, api(1))["review_reward"].(*ReviewRewardHint)
	if hint == nil || hint.Left != 5 || hint.Of != 5 || hint.AboutCredits != 100 || hint.Text != "First reviews with evidence earn about 100 credits. 5 of 5 left." {
		t.Fatalf("hint %+v", hint)
	}
	caps := s.ReviewsCapabilities()["rewards"].(map[string]any)
	if caps["on"] != true || caps["params"].(reviewRewardsParams).BaseCredits != 100 {
		t.Fatalf("capabilities %+v", caps)
	}

	// Paid use pays the full base whatever the verdict; observed 0.4;
	// opinion and an unseasoned account nothing.
	ra := rt.review(alice, api(1), map[string]any{"verdict": "bad", "relationship": "customer"}, "", rt.paidCall(a, apiURL(1)))
	wantReward(t, ra.Reward, "pending", 100, "")
	if ra.Reward.DueAt != rt.now+72*3600 || ra.Reward.Class != "paid_use" {
		t.Fatalf("alice's reward %+v", ra.Reward)
	}
	rb := rt.review(bob, api(1), map[string]any{}, "", rt.fetched(b, "https://api1.example.com/docs"))
	wantReward(t, rb.Reward, "pending", 40, "")
	wantReward(t, rt.review(carol, api(1), map[string]any{}, "").Reward, "none", 0, "evidence_class")
	wantReward(t, rt.review(dave, api(1), map[string]any{}, "", rt.paidCall(keyID(dave), apiURL(1))).Reward, "none", 0, "no_standing")
	_ = c

	// Two roots hold slots now.
	if hint, _ = readSubjectData(t, s, sub.ID)["review_reward"].(*ReviewRewardHint); hint == nil || hint.Left != 3 {
		t.Fatalf("hint after two rewards %+v", hint)
	}
	// Not due yet: the sweep pays nothing.
	rt.sweep()
	if n, _ := rt.granted(a); n != 0 {
		t.Fatalf("paid during the hold: %d", n)
	}
	// The card, allowance.get and journal.get show the pending reward.
	m := run(t, s, Command{Operation: "message.get", MessageID: ra.ID}).Messages[0]
	if m.Review.Reward == nil || m.Review.Reward.State != "pending" || len(m.Review.Evidence) != 1 {
		t.Fatalf("card %+v evidence %+v", m.Review.Reward, m.Review.Evidence)
	}
	al := run(t, s, signed(alice, Command{Operation: "allowance.get"})).Data["review_rewards"].(*AgentReviewRewards)
	if al.Pending != 1 || al.PendingCredits != 100 || len(al.Items) != 1 || al.Items[0].Subject != sub.ID {
		t.Fatalf("allowance review_rewards %+v", al)
	}
	brief := run(t, s, signed(alice, Command{Operation: "journal.get"})).Data["briefing"].(map[string]any)
	if j, _ := brief["review_rewards"].(*AgentReviewRewards); j == nil || j.Pending != 1 || j.Items[0].Review != ra.ID {
		t.Fatalf("journal review_rewards %+v", brief["review_rewards"])
	}
	stats := run(t, s, Command{Operation: "stats"}).Stats
	if stats["review_rewards_pending"] != 2 || stats["review_rewards_pending_credits"] != 140 || stats["review_rewards_pending_credits_observed"] != 40 || stats["review_rewards_paid"] != 0 {
		t.Fatalf("stats %+v", stats)
	}

	// Due: paid as granted credit, journalled with the review as its ref.
	rt.at(rt.now + 72*3600 + 60)
	rt.sweep()
	if n, cr := rt.granted(a); n != 1 || cr != 100 {
		t.Fatalf("alice paid %d lines, %d credits", n, cr)
	}
	if n, cr := rt.granted(b); n != 1 || cr != 40 {
		t.Fatalf("bob paid %d lines, %d credits", n, cr)
	}
	var ref, service, detail string
	if err := s.db.QueryRow("SELECT public_ref,service,detail FROM ledger_entries WHERE account=? AND op=?", a, ReviewRewardOp).Scan(&ref, &service, &detail); err != nil || ref != ra.ID || service != "reviews" || !strings.Contains(detail, ra.ID) {
		t.Fatalf("journal line %q %q %q %v", ref, service, detail, err)
	}
	lines := run(t, s, Command{Operation: "ledger.list", Target: a}).Data["entries"].([]map[string]any)
	if len(lines) == 0 || lines[0]["op"] != ReviewRewardOp || lines[0]["ref"] != ra.ID {
		t.Fatalf("ledger.list %+v", lines)
	}
	m = run(t, s, Command{Operation: "message.get", MessageID: ra.ID}).Messages[0]
	if m.Review.Reward.State != "paid" || m.Review.Reward.SettledAt != rt.now {
		t.Fatalf("card after payment %+v", m.Review.Reward)
	}

	// Idempotent, also across a restart.
	rt.sweep()
	rt.restart()
	rt.sweep()
	if n, _ := rt.granted(a); n != 1 {
		t.Fatalf("paid %d times", n)
	}
	stats = run(t, rt.s, Command{Operation: "stats"}).Stats
	if stats["review_rewards_paid"] != 2 || stats["review_rewards_paid_credits"] != 140 || stats["review_rewards_paid_paid_use"] != 1 || stats["review_rewards_pending"] != 0 {
		t.Fatalf("stats after payment %+v", stats)
	}
}

func TestReviewRewardRootsAndRelationships(t *testing.T) {
	rt := newRewardsTest(t)
	s := rt.s
	k1, k2, owner, aff, paid := keyFor(170), keyFor(171), keyFor(172), keyFor(173), keyFor(174)
	ids := map[string]string{}
	for name, k := range map[string]ed25519.PrivateKey{"k1": k1, "k2": k2, "owner": owner, "aff": aff, "paid": paid} {
		ids[name] = rt.agent(k)
	}
	// k1 and k2 are one root: both prove acme.org.
	link := func(key ed25519.PrivateKey, domain string) {
		if _, err := s.db.Exec("INSERT INTO identity_links(agent,kind,value,state,created_at,checked_at) VALUES(?,'domain',?,'verified',?,?)", keyID(key), domain, rt.now, rt.now); err != nil {
			t.Fatal(err)
		}
	}
	link(k1, "acme.org")
	link(k2, "acme.org")
	// The trust run roots both at the domain.
	if _, err := s.db.Exec("UPDATE trust_scores SET root='domain:acme.org' WHERE account IN (?,?)", ids["k1"], ids["k2"]); err != nil {
		t.Fatal(err)
	}
	link(owner, "example.com") // owns every api*.example.com subject

	wantReward(t, rt.review(k1, api(2), map[string]any{}, "", rt.paidCall(ids["k1"], apiURL(2))).Reward, "pending", 100, "")
	wantReward(t, rt.review(k2, api(2), map[string]any{}, "", rt.paidCall(ids["k2"], apiURL(2))).Reward, "none", 0, "already_rewarded")
	// The root earns once per subject, not once in all.
	wantReward(t, rt.review(k2, api(3), map[string]any{}, "", rt.paidCall(ids["k2"], apiURL(3))).Reward, "pending", 100, "")

	// The owner (raised from its domain link), a declared affiliate and a
	// paid reviewer earn nothing.
	wantReward(t, rt.review(owner, api(2), map[string]any{}, "", rt.paidCall(ids["owner"], apiURL(2))).Reward, "none", 0, "relationship")
	wantReward(t, rt.review(aff, api(2), map[string]any{"relationship": "affiliate"}, "", rt.paidCall(ids["aff"], apiURL(2))).Reward, "none", 0, "relationship")
	wantReward(t, rt.review(paid, api(2), map[string]any{"relationship": "paid"}, "", rt.paidCall(ids["paid"], apiURL(2))).Reward, "none", 0, "relationship")

	// An affiliation found during the hold cancels: aff now proves the
	// owner's domain, after its review of another subject paid nothing yet.
	wantReward(t, rt.review(aff, api(4), map[string]any{}, "", rt.paidCall(ids["aff"], apiURL(4))).Reward, "pending", 100, "")
	link(aff, "example.com")
	rt.at(rt.now + 73*3600)
	rt.sweep()
	var state, detail string
	if err := s.db.QueryRow("SELECT v.state,v.detail FROM review_evidence v JOIN reviews r ON r.event_id=v.event_id WHERE r.account=? AND r.subject=? AND v.evidence=?", ids["aff"], reviewSubjectOf(t, api(4)).ID, RewardEvidence).Scan(&state, &detail); err != nil || state != "failed" || !strings.Contains(detail, `"reason":"affiliated"`) {
		t.Fatalf("affiliated reward %s %s %v", state, detail, err)
	}
	if n, _ := rt.granted(ids["aff"]); n != 0 {
		t.Fatal("an affiliated reviewer was paid")
	}
	if n, _ := rt.granted(ids["k1"]); n != 1 {
		t.Fatal("k1 was not paid")
	}
}

func TestReviewRewardHoldCancelsAndSupersedes(t *testing.T) {
	rt := newRewardsTest(t)
	s := rt.s
	hid, sup, weak, opin := keyFor(180), keyFor(181), keyFor(182), keyFor(183)
	h, u, w, o := rt.agent(hid), rt.agent(sup), rt.agent(weak), rt.agent(opin)

	// A review hidden during the hold is cancelled with that reason.
	rh := rt.review(hid, api(5), map[string]any{}, "", rt.paidCall(h, apiURL(5)))
	if _, err := s.db.Exec("UPDATE events SET hidden=1 WHERE id=?", rh.ID); err != nil {
		t.Fatal(err)
	}
	// A new version keeps the one reward: carried, paid once.
	ru := rt.review(sup, api(5), map[string]any{}, "", rt.paidCall(u, apiURL(5)))
	ru2 := rt.review(sup, api(5), map[string]any{"verdict": "mixed"}, ru.ID, rt.paidCall(u, apiURL(5)))
	if ru2.Reward == nil || ru2.Reward.State != "pending" || ru2.Reward.Review != ru.ID || ru2.Reward.Amount != 100 {
		t.Fatalf("carried reward %+v", ru2.Reward)
	}
	// A newer version with weaker evidence pays at its own factor.
	rw := rt.review(weak, api(5), map[string]any{}, "", rt.paidCall(w, apiURL(5)))
	rt.review(weak, api(5), map[string]any{}, rw.ID, rt.fetched(w, "https://api5.example.com/"))
	// Superseded by an opinion: cancelled.
	ro := rt.review(opin, api(6), map[string]any{}, "", rt.paidCall(o, apiURL(6)))
	if r := rt.review(opin, api(6), map[string]any{}, ro.ID).Reward; r == nil || r.State != "pending" || !strings.Contains(r.Note, "cancelled") {
		t.Fatalf("an opinion superseding a pending reward %+v", r)
	}

	rt.at(rt.now + 73*3600)
	rt.sweep()
	rt.sweep()
	for _, c := range []struct {
		account string
		lines   int
		credits int64
	}{{h, 0, 0}, {u, 1, 100}, {w, 1, 40}, {o, 0, 0}} {
		if n, cr := rt.granted(c.account); n != c.lines || cr != c.credits {
			t.Errorf("%s: %d lines, %d credits; want %d, %d", c.account[:8], n, cr, c.lines, c.credits)
		}
	}
	reasons := map[string]string{}
	rows, err := s.db.Query("SELECT r.account,json_extract(v.detail,'$.reason') FROM review_evidence v JOIN reviews r ON r.event_id=v.event_id WHERE v.evidence=? AND v.state='failed'", RewardEvidence)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a, r string
		if err = rows.Scan(&a, &r); err != nil {
			t.Fatal(err)
		}
		reasons[a] = r
	}
	rows.Close()
	if reasons[h] != "review_hidden" || reasons[o] != "superseded_not_eligible" || len(reasons) != 2 {
		t.Fatalf("cancel reasons %+v", reasons)
	}
	// The cancelled reward's card says why; the root cannot earn again.
	m := run(t, s, Command{Operation: "message.get", MessageID: ro.ID}).Messages[0]
	if m.Review.Reward == nil || m.Review.Reward.State != "cancelled" || m.Review.Reward.Reason != "superseded_not_eligible" || m.Review.Reward.Note == "" {
		t.Fatalf("cancelled card %+v", m.Review.Reward)
	}
}

func TestReviewRewardCoverageAndCaps(t *testing.T) {
	rt := newRewardsTest(t)
	s := rt.s
	var keys []ed25519.PrivateKey
	var ids []string
	for i := range 8 {
		k := keyFor(byte(190 + i))
		keys, ids = append(keys, k), append(ids, rt.agent(k))
	}
	// Per subject per day: 3. The fourth waits for the next UTC day.
	for i := range 3 {
		wantReward(t, rt.review(keys[i], api(7), map[string]any{}, "", rt.paidCall(ids[i], apiURL(7))).Reward, "pending", 100, "")
	}
	wantReward(t, rt.review(keys[3], api(7), map[string]any{}, "", rt.paidCall(ids[3], apiURL(7))).Reward, "none", 0, "subject_daily_cap")
	// Coverage: only the first 5 roots in the window; the next day adds two.
	rt.at(rt.now + 86400)
	for i := 4; i < 6; i++ {
		wantReward(t, rt.review(keys[i], api(7), map[string]any{}, "", rt.paidCall(ids[i], apiURL(7))).Reward, "pending", 100, "")
	}
	wantReward(t, rt.review(keys[6], api(7), map[string]any{}, "", rt.paidCall(ids[6], apiURL(7))).Reward, "none", 0, "no_slots_left")
	if _, ok := readSubjectData(t, s, reviewSubjectOf(t, api(7)).ID)["review_reward"]; ok {
		t.Fatal("a full subject still offers a reward")
	}
	// Find carries the offer only for subjects with slots left.
	hits, err := s.findRewardHints(testContext, []find.Hit{{SubjectID: reviewSubjectOf(t, api(7)).ID}, {SubjectID: reviewSubjectOf(t, api(99)).ID}, {}})
	if err != nil || hits[0].ReviewReward != nil || hits[1].ReviewReward == nil || hits[2].ReviewReward != nil {
		t.Fatalf("find hints %+v (%v)", hits, err)
	}

	// Per account per day: 5.
	for n := 10; n < 15; n++ {
		wantReward(t, rt.review(keys[7], api(n), map[string]any{}, "", rt.paidCall(ids[7], apiURL(n))).Reward, "pending", 100, "")
	}
	wantReward(t, rt.review(keys[7], api(15), map[string]any{}, "", rt.paidCall(ids[7], apiURL(15))).Reward, "none", 0, "account_daily_cap")

	// Daily total: 250 credits a day pays two of the due rewards today, the
	// rest tomorrow in order; never more than the total.
	body, _ := json.Marshal(func() reviewRewardsParams { p := defaultReviewRewardsParams(); p.DailyCredits = 250; return p }())
	if _, err := s.SetAllowanceParams(testContext, ReviewRewardsParamsNamespace, body, "test", 0); err != nil {
		t.Fatal(err)
	}
	rt.at(rt.now + 73*3600)
	rt.sweep()
	day := rt.now / 86400
	if paid, _ := rewardsPaidOn(testContext, s.db, day); paid != 200 {
		t.Fatalf("paid %d today, want 200", paid)
	}
	rt.at(rt.now + 86400)
	rt.sweep()
	if paid, _ := rewardsPaidOn(testContext, s.db, day+1); paid != 200 {
		t.Fatalf("paid %d the next day, want 200", paid)
	}

	// Paused: nothing new is offered and nothing is paid; pending waits.
	pause := func() reviewRewardsParams { p := defaultReviewRewardsParams(); p.Enabled = false; return p }()
	body, _ = json.Marshal(pause)
	if _, err := s.SetAllowanceParams(testContext, ReviewRewardsParamsNamespace, body, "pause", 0); err != nil {
		t.Fatal(err)
	}
	wantReward(t, rt.review(keys[0], api(20), map[string]any{}, "", rt.paidCall(ids[0], apiURL(20))).Reward, "none", 0, "rewards_off")
	var pending int
	if err := s.db.QueryRow("SELECT count(*) FROM review_evidence WHERE evidence=? AND state='unknown'", RewardEvidence).Scan(&pending); err != nil || pending == 0 {
		t.Fatalf("pending %d %v", pending, err)
	}
	rt.at(rt.now + 86400)
	rt.sweep()
	var still int
	if err := s.db.QueryRow("SELECT count(*) FROM review_evidence WHERE evidence=? AND state='unknown'", RewardEvidence).Scan(&still); err != nil || still != pending {
		t.Fatalf("paused sweep settled rewards: %d -> %d (%v)", pending, still, err)
	}
	if _, ok := readSubjectData(t, s, api(30))["review_reward"]; ok {
		t.Fatal("a paused board offers rewards")
	}
}

func TestReviewRewardPoolExhaustion(t *testing.T) {
	rt := newRewardsTest(t)
	s := rt.s
	// A grant pool of 150 credits a day: one full reward, then dry.
	p := s.allowanceDefaults()
	rp := p.Resources[allowance.Credit]
	rp.Budget, rp.SpendCeiling, rp.InboundCap, rp.GrantSharePPM = 150, 150, 100_000, 1_000_000
	rp.Cap, rp.Floor = 0, 0
	if _, err := s.SetAllowanceParams(testContext, "allowance", p.Marshal(), "a small pool", 0); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range 3 {
		k := keyFor(byte(210 + i))
		ids = append(ids, rt.agent(k))
		wantReward(t, rt.review(k, api(40+i), map[string]any{}, "", rt.paidCall(ids[i], apiURL(40+i))).Reward, "pending", 100, "")
	}
	rt.at(rt.now + 73*3600)
	rt.sweep()
	rt.sweep()
	day := rt.now / 86400
	if paid, _ := rewardsPaidOn(testContext, s.db, day); paid != 100 {
		t.Fatalf("paid %d from a 150-credit pool, want 100", paid)
	}
	var pending int
	if err := s.db.QueryRow("SELECT count(*) FROM review_evidence WHERE evidence=? AND state='unknown'", RewardEvidence).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("pending after the pool ran dry %d %v", pending, err)
	}
	// The next day's pool pays the next one.
	rt.at(rt.now + 86400)
	rt.sweep()
	if paid, _ := rewardsPaidOn(testContext, s.db, day+1); paid != 100 {
		t.Fatalf("paid %d the next day", paid)
	}
}

func TestReviewRewardsParams(t *testing.T) {
	good, _ := json.Marshal(defaultReviewRewardsParams())
	if _, err := parseReviewRewardsParams(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		`{}`,
		strings.Replace(string(good), `"observed":400000`, `"opinion":400000`, 1),
		strings.Replace(string(good), `"base_credits":100`, `"base_credits":0`, 1),
		strings.Replace(string(good), `"hold_hours":72`, `"hold_hours":0`, 1),
		strings.Replace(string(good), `"enabled":true,`, ``, 1),
		strings.Replace(string(good), `"schema":1`, `"schema":1,"extra":1`, 1),
	} {
		if _, err := parseReviewRewardsParams([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if got := rewardAmount(100, 400_000, 1_000_000); got != 40 {
		t.Fatalf("amount %d", got)
	}
	if got := rewardAmount(100, 400_000, 250_000); got != 10 {
		t.Fatalf("amount at v 0.25: %d", got)
	}
}
