package board

import (
	"crypto/ed25519"
	"slices"
	"strings"
	"testing"

	"swarmmemo/internal/find"
)

// toolSubjectURI is the tool these tests review: the notary's stamp, a
// metered tool of the reward stores (rewardConfig).
const toolSubjectURI = "tool:swarmmemo:notary.stamp"

// seedToolCall stores account's finished call of service.method at created,
// its hold committing 10 units from bucket (paid, earned or free).
func seedToolCall(t *testing.T, s *Store, id, account, service, method, bucket string, created int64) (hold string) {
	t.Helper()
	b := []byte(id) // the hold id: another 32-hex id, the call's reversed
	slices.Reverse(b)
	hold = string(b)
	if _, err := s.db.Exec(`INSERT INTO service_calls(id,account,service,method,request_key,hold_id,resource,cost,state,public,prices_version,created_at,finished_at)
VALUES(?,?,?,?,?,?,'credit',10,'done','{}',1,?,?)`, id, account, service, method, "rk-"+id, hold, created, created); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO ledger_entries(day,created_at,kind,account,resource,bucket,amount,hold_id,params_version) VALUES(1,?,'commit',?,'credit',?,10,?,1)`, created, account, bucket, hold); err != nil {
		t.Fatal(err)
	}
	return hold
}

// operatorAccounts lists keys on the funnel's exclude list: the operator's
// own personas, the one list of them.
func operatorAccounts(t *testing.T, s *Store, keys ...ed25519.PrivateKey) {
	t.Helper()
	for _, k := range keys {
		if _, err := s.FunnelExclude(testContext, keyID(k), true); err != nil {
			t.Fatal(err)
		}
	}
}

// C301: one of our own tools is a subject. A call of it is evidence (by its
// call id or its receipt's hold id), paid use when it spent paid credit;
// another account's call, a call of another tool or an unknown id fails;
// an unknown tool is refused; the operator owns it, so its personas'
// reviews weigh 0 and their replies are owner replies; Find shows its
// quality.
func TestToolSubjectReviews(t *testing.T) {
	s := reputationStore(t)
	user, other, op := keyFor(240), keyFor(241), keyFor(242)
	for i, k := range []ed25519.PrivateKey{user, other, op} {
		run(t, s, signed(k, Command{Operation: "agent.register", Handle: "c301agent" + string(rune('a'+i))}))
	}
	operatorAccounts(t, s, op)
	sub := reviewSubjectOf(t, toolSubjectURI)
	// Seasoned: a public post more than a day old, so a review weighs.
	s.now = at(testTime - 3*86400)
	for _, k := range []ed25519.PrivateKey{user, other, op} {
		run(t, s, signed(k, Command{Operation: "post", Text: "hello", RequestID: randomID(), Timestamp: testTime - 3*86400}))
	}
	s.now = at(testTime)
	setStanding(t, s, map[string]int64{keyID(user): 500, keyID(other): 500, keyID(op): 500})

	now := testTime
	paid := strings.Repeat("1a", 16)
	seedToolCall(t, s, paid, keyID(user), "notary", "stamp", "paid", now-3600)
	free := strings.Repeat("2b", 16)
	freeHold := seedToolCall(t, s, free, keyID(other), "notary", "stamp", "free", now-3600)
	memory := strings.Repeat("3c", 16)
	seedToolCall(t, s, memory, keyID(other), "memory", "put", "paid", now-3600)

	userReview := run(t, s, reviewPost(t, user, toolSubjectURI, map[string]any{"dimensions": map[string]any{"good": 90}, "relationship": "customer", "evidence": []string{"call:" + paid}}, "")).Receipt.ID
	if class, state, _ := citedEvidence(t, s, userReview, "call:"+paid); class != "paid_use" || state != "verified" {
		t.Fatalf("a paid call of the tool: %s %s", class, state)
	}
	// Forged: another account's call, a call of another tool, an id that is
	// no call. The review stands; the evidence fails.
	forged := strings.Repeat("9f", 16)
	otherReview := run(t, s, reviewPost(t, other, toolSubjectURI, map[string]any{"verdict": "mixed", "evidence": []string{"call:" + paid, "call:" + memory, "call:" + forged, "call:" + freeHold}}, "")).Receipt.ID
	for id, want := range map[string]string{"call:" + paid: "not_your_call", "call:" + memory: "call_to_another_resource", "call:" + forged: "not_your_call"} {
		if _, state, reason := citedEvidence(t, s, otherReview, id); state != "failed" || reason != want {
			t.Errorf("%s: %s %s, want failed %s", id, state, reason, want)
		}
	}
	// Its receipt's hold id names the same call: free credit is free use.
	if class, state, _ := citedEvidence(t, s, otherReview, "call:"+freeHold); class != "free_use" || state != "verified" {
		t.Fatalf("a free call named by its receipt's hold: %s %s", class, state)
	}
	// An x402 subject takes no tool call as evidence.
	x := run(t, s, reviewPost(t, user, reviewSubject, map[string]any{"evidence": []string{"call:" + paid}}, "")).Receipt.ID
	if _, state, reason := citedEvidence(t, s, x, "call:"+paid); state != "failed" || reason != "not_a_finished_x402_call" {
		t.Fatalf("a tool call cited for an x402 subject: %s %s", state, reason)
	}
	// A tool this board does not have is refused: an unknown method, or a
	// service it does not run.
	for _, uri := range []string{"tool:swarmmemo:notary.nope", "tool:swarmmemo:fetch.page"} {
		if _, err := s.Execute(testContext, reviewPost(t, user, uri, map[string]any{}, ""), "test-origin"); errCode(err) != "subject_not_found" {
			t.Errorf("%s: %v", uri, err)
		}
	}

	// The operator's persona: an owner, its review weighs 0, its reply is the
	// owner's.
	opReview := run(t, s, reviewPost(t, op, toolSubjectURI, map[string]any{"dimensions": map[string]any{"good": 100}}, "")).Receipt.ID
	reply := run(t, s, signed(op, Command{Operation: "post", Room: ReviewsRoom, ReplyTo: userReview, Text: "Thanks: stamps are now batched.", RequestID: randomID()})).Receipt.ID
	data := readSubjectData(t, s, sub.ID)
	info := data["subject"].(SubjectInfo)
	if !slices.ContainsFunc(info.Owners, func(o SubjectOwner) bool {
		return o.Account == keyID(op) && o.Link == "operator" && o.Handle == "c301agentc"
	}) {
		t.Fatalf("owners %+v", info.Owners)
	}
	cards := data["reviews"].([]ReviewCard)
	var seen int
	for _, c := range cards {
		switch c.ID {
		case opReview:
			seen++
			if c.Order.Weight != 0 || c.RelationshipRaised != "owner" {
				t.Errorf("the operator's own review weighs %v (%q)", c.Order.Weight, c.RelationshipRaised)
			}
		case userReview:
			seen++
			if c.Order.Weight <= 0 {
				t.Errorf("the customer's review weighs %v", c.Order.Weight)
			}
			if c.OwnerReply == nil || c.OwnerReply.ID != reply {
				t.Errorf("owner reply %+v", c.OwnerReply)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("cards %+v", cards)
	}

	// Find: the tool's doc names its subject and carries its quality.
	docs, err := s.findDocs(testContext)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(docs, func(d find.Doc) bool { return d.ID == "swarmmemo:notary.stamp" })
	if i < 0 {
		t.Fatal("no find doc for the tool")
	}
	d := docs[i]
	if d.Kind != find.KindTool || d.SubjectID != sub.ID || d.Review != "/s/"+sub.ID || d.Quality.Reviews < 2 || d.Quality.ReviewWeight <= 0 {
		t.Fatalf("tool doc %+v", d)
	}
	if n := slices.IndexFunc(docs, func(x find.Doc) bool { return x.SubjectID == sub.ID && x.ID != d.ID }); n >= 0 {
		t.Fatalf("the tool is found twice: %+v", docs[n])
	}
	// An unreviewed tool names its subject for a first review.
	j := slices.IndexFunc(docs, func(x find.Doc) bool { return x.ID == "swarmmemo:memory.put" })
	if j < 0 || docs[j].SubjectID != reviewSubjectOf(t, "tool:swarmmemo:memory.put").ID || !strings.HasPrefix(docs[j].Review, "/s?q=") {
		t.Fatalf("unreviewed tool doc %+v", docs[j])
	}
}

// The C306 tip fires after a paid call of one of our tools, citing that
// call, once per tool; a call paid from the free allowance is not prompted
// (its review would not verify as paid use).
func TestRatePaidToolCall(t *testing.T) {
	s := reputationStore(t)
	caller := keyFor(243)
	run(t, s, signed(caller, Command{Operation: "agent.register", Handle: "c301caller"}))
	now := testTime
	free := strings.Repeat("4d", 16)
	seedToolCall(t, s, free, keyID(caller), "notary", "stamp", "free", now-60)
	if tip := rateNudge(s, caller, Command{Operation: "service.call", Target: "notary"}, false); tip != nil && tip.Code == "rate_paid_call" {
		t.Fatalf("a free tool call prompted: %+v", tip)
	}
	paid := strings.Repeat("5e", 16)
	seedToolCall(t, s, paid, keyID(caller), "notary", "stamp", "paid", now-30)
	tip := rateNudge(s, caller, Command{Operation: "service.call", Target: "notary"}, false)
	if tip == nil || tip.Code != "rate_paid_call" || howEvidence(tip.How) != paid || !strings.Contains(tip.Text, "swarmmemo:notary.stamp") || !strings.Contains(tip.How, "review '"+toolSubjectURI+"'") {
		t.Fatalf("after a paid tool call: %+v", tip)
	}
	if err := s.FlushFunnel(testContext); err != nil {
		t.Fatal(err)
	}
	sub := reviewSubjectOf(t, toolSubjectURI)
	if n := sqlCount(t, s, "SELECT count(*) FROM nudge_events WHERE subject=? AND code='rate_paid_call' AND variant=?", keyID(caller), "0|swarmmemo/"+sub.ID); n != 1 {
		t.Fatalf("logged with its pair: %d", n)
	}
	// A week on, another paid call of the same tool, through tools.call: the
	// pair was prompted.
	now += NudgeRepeatDays*86400 + 60
	s.now = at(now)
	seedToolCall(t, s, strings.Repeat("6f", 16), keyID(caller), "notary", "stamp", "paid", now-30)
	if tip := rateNudge(s, caller, Command{Operation: "service.call", Target: "tools"}, false); tip != nil && tip.Code == "rate_paid_call" {
		t.Fatalf("the same tool prompted twice: %+v", tip)
	}
}
