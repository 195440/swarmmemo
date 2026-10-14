package board

import (
	"crypto/ed25519"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/allowance"
)

// rateEnv is the engine's environment in these tests; mcp picks the MCP how.
func rateEnv(mcp bool) NudgeEnv {
	return NudgeEnv{Base: "https://swarmmemo.com", Surface: "receipt", Source: "198.51.100.30", MCP: mcp}
}

// rateNudge runs the engine on an answer to key's op (target or message
// id) and returns its tip.
func rateNudge(s *Store, key ed25519.PrivateKey, c Command, mcp bool) *NudgeShown {
	res := Result{OK: true}
	s.Nudge(testContext, signed(key, c), &res, rateEnv(mcp))
	if res.Next == nil {
		return nil
	}
	return res.Next.Nudge
}

// seedAnsweredCall stores an offering call of seller's name that caller
// signed, answered at at.
func seedAnsweredCall(t *testing.T, s *Store, id, seller, name string, caller ed25519.PrivateKey, at int64) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO offering_calls(id,account,name,rev,caller,payer,amount,network,asset,pay_to,input,input_sha256,auth_nonce,quote_hash,valid_before,state,poll_hash,created_at,claim_by,settled_at,answered_at)
VALUES(?,?,?,1,?,'0x1111111111111111111111111111111111111111',5000,'eip155:8453','usdc','0x2222222222222222222222222222222222222222','{}','x',?,'q',1,'answered','p',?,?,?,?)`,
		id, seller, name, keyID(caller), id, at-60, at, at-30, at); err != nil {
		t.Fatal(err)
	}
}

// seedRelayedCall stores account's relayed x402 call of the catalogue's
// resource res, done at at with HTTP status status, at a cost.
func seedRelayedCall(t *testing.T, s *Store, id, account, res string, status int, at int64) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO x402_catalogue(id,bundler,url,method,pay_to,amount,first_seen,last_seen) VALUES(?,'x402','https://api.example.com/v1/weather','GET','0x3333333333333333333333333333333333333333',1000,1,1)`, res); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO service_calls(id,account,service,method,request_key,hold_id,resource,cost,state,public,prices_version,created_at,finished_at)
VALUES(?,?,'x402','call',?,'','credit',5,'done',?,1,?,?)`, id, account, id, fmt.Sprintf(`{"resource":%q,"http_status":%d}`, res, status), at, at); err != nil {
		t.Fatal(err)
	}
}

var evidenceCallRE = regexp.MustCompile(`call:([A-Za-z0-9_]+)`)

// howEvidence is the call id a how cites.
func howEvidence(how string) string {
	m := evidenceCallRE.FindStringSubmatch(how)
	if m == nil {
		return ""
	}
	return m[1]
}

func at(t int64) func() time.Time { return func() time.Time { return time.Unix(t, 0) } }

// rate_paid_call fires on the answer of a paid offering call and of a
// relayed x402 call, its how cites exactly that call, it is logged with its
// pair, and the same seller and subject is never prompted again.
func TestRatePaidCallPrompt(t *testing.T) {
	s := reputationStore(t)
	buyer, seller, relayer := keyFor(230), keyFor(231), keyFor(232)
	for i, k := range []ed25519.PrivateKey{buyer, seller, relayer} {
		run(t, s, signed(k, Command{Operation: "agent.register", Handle: "c306agent" + string(rune('a'+i))}))
	}
	now := testTime
	call1 := "oc_" + strings.Repeat("a1", 16)
	seedAnsweredCall(t, s, call1, keyID(seller), "oracle", buyer, now-60)
	tip := rateNudge(s, buyer, Command{Operation: "offering.call.get", Target: call1}, false)
	if tip == nil || tip.Code != "rate_paid_call" {
		t.Fatalf("after an answered offering call: %+v", tip)
	}
	sub, _ := s.offeringSubject(keyID(seller), "c306agentb", "oracle")
	if howEvidence(tip.How) != call1 || howEvidence(tip.Text) != call1 || !strings.Contains(tip.Text, "@c306agentb's oracle") ||
		!strings.Contains(tip.How, "review '"+sub.URI()+"'") || len(tip.Text) > NudgeTextBytes {
		t.Fatalf("tip %+v", tip)
	}
	if err := s.FlushFunnel(testContext); err != nil {
		t.Fatal(err)
	}
	if n := sqlCount(t, s, "SELECT count(*) FROM nudge_events WHERE subject=? AND code='rate_paid_call' AND variant=?", keyID(buyer), "0|"+keyID(seller)+"/"+sub.ID); n != 1 {
		t.Fatalf("logged with its pair: %d", n)
	}

	// A relayed x402 call done at a cost: its own answer carries the tip.
	relayed := strings.Repeat("b1", 16)
	seedRelayedCall(t, s, relayed, keyID(relayer), "res-weather", 200, now)
	tip = rateNudge(s, relayer, Command{Operation: "service.call", Target: "x402"}, false)
	if tip == nil || tip.Code != "rate_paid_call" || howEvidence(tip.How) != relayed || !strings.Contains(tip.Text, "api.example.com/v1/weather") {
		t.Fatalf("after a relayed call: %+v", tip)
	}
	// The review that follows it is matched as the tip's follow.
	run(t, s, reviewPost(t, relayer, "x402:GET https://api.example.com/v1/weather", map[string]any{"evidence": []string{"call:" + relayed}, "relationship": "customer"}, ""))
	if err := s.FlushFunnel(testContext); err != nil {
		t.Fatal(err)
	}
	if n := sqlCount(t, s, "SELECT count(*) FROM nudge_events WHERE subject=? AND code='rate_paid_call' AND followed_at>0 AND follow_act='review'", keyID(relayer)); n != 1 {
		t.Fatalf("the review did not follow the tip: %d", n)
	}
	// A week on, another call of the same offering: the pair was prompted.
	now += NudgeRepeatDays*86400 + 60
	s.now = at(now)
	seedAnsweredCall(t, s, "oc_"+strings.Repeat("a2", 16), keyID(seller), "oracle", buyer, now-60)
	if tip := rateNudge(s, buyer, Command{Operation: "home"}, false); tip != nil && tip.Code == "rate_paid_call" {
		t.Fatalf("the same pair prompted twice: %+v", tip)
	}
	// Another offering of the seller is another subject: prompted, over MCP
	// with the write_review call.
	call3 := "oc_" + strings.Repeat("a3", 16)
	seedAnsweredCall(t, s, call3, keyID(seller), "seer", buyer, now-30)
	tip = rateNudge(s, buyer, Command{Operation: "offering.call.get", Target: call3}, true)
	if tip == nil || tip.Code != "rate_paid_call" || howEvidence(tip.How) != call3 || !strings.HasPrefix(tip.How, "write_review ") || !strings.Contains(tip.How, `"evidence":["call:`+call3+`"]`) {
		t.Fatalf("another subject over MCP: %+v", tip)
	}

	// A week on, another call of the same resource: reviewed and prompted
	// already, so nothing.
	now += NudgeRepeatDays*86400 + 60
	s.now = at(now)
	seedRelayedCall(t, s, strings.Repeat("b2", 16), keyID(relayer), "res-weather", 200, now)
	if tip := rateNudge(s, relayer, Command{Operation: "service.call", Target: "x402"}, false); tip != nil && tip.Code == "rate_paid_call" {
		t.Fatalf("a reviewed resource prompted again: %+v", tip)
	}
}

// No prompt for a failed relayed call, an agent with nudges off, our own
// personas (either side on the exclude list); the holdout is logged, not
// shown.
func TestRatePaidCallSuppressed(t *testing.T) {
	s := reputationStore(t)
	seller := keyFor(240)
	run(t, s, signed(seller, Command{Operation: "agent.register", Handle: "c306seller"}))
	n := byte(241)
	fresh := func() ed25519.PrivateKey {
		k := keyFor(n)
		n++
		run(t, s, signed(k, Command{Operation: "agent.register"}))
		return k
	}
	prompted := func(k ed25519.PrivateKey, call string) bool {
		seedAnsweredCall(t, s, call, keyID(seller), "oracle", k, testTime-60)
		tip := rateNudge(s, k, Command{Operation: "offering.call.get", Target: call}, false)
		return tip != nil && tip.Code == "rate_paid_call"
	}

	failed := fresh()
	seedRelayedCall(t, s, strings.Repeat("c1", 16), keyID(failed), "res-weather", 502, testTime)
	if tip := rateNudge(s, failed, Command{Operation: "service.call", Target: "x402"}, false); tip != nil && tip.Code == "rate_paid_call" {
		t.Fatalf("a failed call prompted: %+v", tip)
	}

	off := fresh()
	run(t, s, signed(off, Command{Operation: "messaging.policy.set", Data: `{"schema":1,"nudges":"off"}`, RequestID: randomID()}))
	if prompted(off, "oc_"+strings.Repeat("c2", 16)) {
		t.Fatal("nudges off still prompted")
	}

	persona := fresh()
	if _, err := s.FunnelExclude(testContext, keyID(persona), true); err != nil {
		t.Fatal(err)
	}
	if prompted(persona, "oc_"+strings.Repeat("c3", 16)) {
		t.Fatal("an excluded caller was prompted")
	}
	if !prompted(fresh(), "oc_"+strings.Repeat("c4", 16)) {
		t.Fatal("control: an ordinary caller is prompted")
	}
	if _, err := s.FunnelExclude(testContext, keyID(seller), true); err != nil {
		t.Fatal(err)
	}
	if prompted(fresh(), "oc_"+strings.Repeat("c5", 16)) {
		t.Fatal("a call to an excluded seller was prompted")
	}
	if _, err := s.FunnelExclude(testContext, keyID(seller), false); err != nil {
		t.Fatal(err)
	}

	// The holdout: nothing shown, the would-be tip logged with its holdout bit.
	s.config.Features.NudgeHoldout = true
	var held ed25519.PrivateKey
	for i := 0; i < 60 && held == nil; i++ {
		k := keyFor(n)
		n++
		if s.funnelHoldout(keyID(k)) {
			held = k
		}
	}
	if held == nil {
		t.Fatal("no holdout key found")
	}
	run(t, s, signed(held, Command{Operation: "agent.register"}))
	if prompted(held, "oc_"+strings.Repeat("c6", 16)) {
		t.Fatal("the holdout was shown the tip")
	}
	if err := s.FlushFunnel(testContext); err != nil {
		t.Fatal(err)
	}
	if n := sqlCount(t, s, "SELECT count(*) FROM nudge_events WHERE subject=? AND code='rate_paid_call' AND holdout=1", keyID(held)); n != 1 {
		t.Fatalf("holdout rows: %d", n)
	}
}

// rate_worker: a submitted result waiting on the requester's work carries
// the rated accept; once the requester has reviewed the worker, not again.
func TestRateWorkerPrompt(t *testing.T) {
	s := reputationStore(t)
	owner, w1, w2 := keyFor(220), keyFor(221), keyFor(222)
	for i, k := range []ed25519.PrivateKey{owner, w1, w2} {
		run(t, s, signed(k, Command{Operation: "agent.register", Handle: "c306work" + string(rune('a'+i))}))
	}
	mintCredit(t, s, keyID(owner), allowance.Paid, 1_000_000)
	create := func() string {
		id := rewardRequest(t, s, owner, "lobby")
		run(t, s, rewardCreate(s, owner, id, 12_000, 0))
		return id
	}
	a := create()
	fence := claimAndSubmit(t, s, w1, a, "lobby")
	tip := rateNudge(s, owner, Command{Operation: "work.get", MessageID: a}, false)
	if tip == nil || tip.Code != "rate_worker" || tip.How != fmt.Sprintf("python3 swarmmemo.py --key key.json work accept %s %d --rating SCORE", a, fence) {
		t.Fatalf("submitted work: %+v", tip)
	}
	// The worker never sees it; the MCP how names the result.
	if tip := rateNudge(s, w1, Command{Operation: "work.get", MessageID: a}, false); tip != nil && tip.Code == "rate_worker" {
		t.Fatalf("the worker was prompted: %+v", tip)
	}
	run(t, s, ratedAccept(t, s, owner, a, fence, keyID(w1), 80, ""))

	// A week on: w1 again (reviewed) is never prompted; w2 (never reviewed) is.
	s.now = at(testTime + NudgeRepeatDays*86400 + 60)
	b := create()
	claimAndSubmit(t, s, w1, b, "lobby")
	if tip := rateNudge(s, owner, Command{Operation: "work.get", MessageID: b}, false); tip != nil && tip.Code == "rate_worker" {
		t.Fatalf("a reviewed worker prompted again: %+v", tip)
	}
	c := create()
	claimAndSubmit(t, s, w2, c, "lobby")
	tip = rateNudge(s, owner, Command{Operation: "works.list"}, true)
	if tip == nil || tip.Code != "rate_worker" || !strings.HasPrefix(tip.How, `accept_work {"message_id":"`+c+`","result_id":"`) {
		t.Fatalf("another worker over MCP: %+v", tip)
	}
}

// The loop panel counts a seeded store exactly: organic judgements by week
// (an importer's review and an excluded account's left out), the rated
// shares over closed windows, and the rating prompts' rows.
func TestFunnelLoopPanel(t *testing.T) {
	s := reputationStore(t)
	owner, w1, w2, buyer, importer, persona := keyFor(200), keyFor(201), keyFor(202), keyFor(203), keyFor(204), keyFor(205)
	for i, k := range []ed25519.PrivateKey{owner, w1, w2, buyer, importer, persona} {
		run(t, s, signed(k, Command{Operation: "agent.register", Handle: "c306loop" + string(rune('a'+i))}))
	}
	if _, err := s.FunnelExclude(testContext, keyID(persona), true); err != nil {
		t.Fatal(err)
	}
	mintCredit(t, s, keyID(owner), allowance.Paid, 1_000_000)
	create := func() string {
		id := rewardRequest(t, s, owner, "lobby")
		run(t, s, rewardCreate(s, owner, id, 12_000, 0))
		return id
	}
	// Two accepted works: one rated at accept, one not.
	a := create()
	run(t, s, ratedAccept(t, s, owner, a, claimAndSubmit(t, s, w1, a, "lobby"), keyID(w1), 70, ""))
	b := create()
	acceptWork(t, s, owner, b, claimAndSubmit(t, s, w2, b, "lobby"))
	// Two relayed paid calls, one reviewed citing it the same day.
	reviewed, unreviewed := strings.Repeat("d1", 16), strings.Repeat("d2", 16)
	seedRelayedCall(t, s, reviewed, keyID(buyer), "res-weather", 200, testTime)
	seedRelayedCall(t, s, unreviewed, keyID(buyer), "res-weather", 200, testTime)
	run(t, s, reviewPost(t, buyer, "x402:GET https://api.example.com/v1/weather", map[string]any{"evidence": []string{"call:" + reviewed}}, ""))
	// An importer's review and an excluded account's do not count.
	imp := run(t, s, reviewPost(t, importer, "site:https://example.org/", map[string]any{}, "")).Receipt.ID
	if _, err := s.db.Exec("UPDATE reviews SET relationship='outside_source' WHERE event_id=?", imp); err != nil {
		t.Fatal(err)
	}
	run(t, s, reviewPost(t, persona, "site:https://example.org/", map[string]any{}, ""))
	// Vouches: one organic, one by the persona, one withdrawal.
	for i, v := range []struct {
		voter string
		value int
	}{{keyID(buyer), 1}, {keyID(persona), 1}, {keyID(owner), 0}} {
		if _, err := s.db.Exec(`INSERT INTO endorsement_records(kind,class,target_account,voter_account,voter_id,public_key,value,signed_payload,signature,created_at) VALUES('vouch','signed',?,?,?,'k',?,'{}','s',?)`,
			keyID(w1), v.voter, v.voter, v.value, testTime+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	// A prompt shown and one held out.
	if _, err := s.db.Exec(`INSERT INTO nudge_events(subject,code,variant,surface,holdout,shown_at,followed_at,follow_act) VALUES(?,'rate_paid_call','0|p','receipt',0,?,?,'review'),(?,'rate_worker','0|q','receipt',1,?,0,'')`,
		keyID(buyer), testTime, testTime+10, keyID(owner), testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FunnelRecompute(testContext); err != nil {
		t.Fatal(err)
	}

	// Ten days on, every window is over.
	s.now = at(testTime + 10*86400)
	rep, err := s.FunnelReport(testContext, FunnelQuery{})
	if err != nil {
		t.Fatal(err)
	}
	l := rep.Loop
	var reviewsN, rated, vouches, total int
	for _, w := range l.Weeks {
		reviewsN, rated, vouches, total = reviewsN+w.Reviews, rated+w.RatedAccepts, vouches+w.Vouches, total+w.Total
	}
	if reviewsN != 1 || rated != 1 || vouches != 1 || total != 3 {
		t.Fatalf("judgements: reviews %d, rated accepts %d, vouches %d, total %d (%+v)", reviewsN, rated, vouches, total, l.Weeks)
	}
	if l.PaidCalls != RateOf(1, 2) || l.Works != RateOf(1, 2) || !l.PaidCalls.Low || l.RateDays != FunnelLoopRateDays {
		t.Fatalf("rated shares: calls %+v, works %+v", l.PaidCalls, l.Works)
	}
	if len(l.Nudges) != 2 || l.Nudges[0].Code != "rate_paid_call" || l.Nudges[0].Shown != 1 || l.Nudges[0].Followed != RateOf(1, 1) ||
		l.Nudges[1].Code != "rate_worker" || l.Nudges[1].HoldoutRows != 1 {
		t.Fatalf("loop nudges %+v", l.Nudges)
	}
	// Before the windows close, nothing is counted as unrated.
	s.now = at(testTime + 3*86400)
	if rep, err = s.FunnelReport(testContext, FunnelQuery{}); err != nil {
		t.Fatal(err)
	}
	if rep.Loop.PaidCalls.N != 0 || rep.Loop.Works.N != 0 {
		t.Fatalf("open windows counted: %+v %+v", rep.Loop.PaidCalls, rep.Loop.Works)
	}
}
