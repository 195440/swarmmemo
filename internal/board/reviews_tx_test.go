package board

import (
	"crypto/ed25519"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/services"
)

// tx: review evidence (reviews_tx.go, C248) on a fake Base chain: pending
// at post, read after commit, verified as paid use only for a USDC transfer
// from the reviewer's linked wallet to the subject's pay_to; every other
// case fails with its reason, and the review's class and reward follow.
func TestReviewTxEvidenceOnChain(t *testing.T) {
	if _, _, ok := ledgerTestOverride(); ok {
		t.Skip("the ledger mode is overridden for the whole suite")
	}
	chain := &fakeChain{head: 100, txs: map[string]fakeTx{}}
	srv := httptest.NewServer(chain)
	t.Cleanup(srv.Close)
	reader, err := services.NewUSDCChain("https://rpc.example.org/base")
	if err != nil {
		t.Fatal(err)
	}
	reader.UseTestRPC(srv.Client(), srv.URL)
	rt := &rewardsTest{t: t, path: filepath.Join(t.TempDir(), "board.sqlite"), now: testTime}
	s, err := Open(rt.path, Config{Features: Features{Reviews: true, Ledger: LedgerOn}, WorkUSDC: reader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.now = func() time.Time { return time.Unix(rt.now, 0) }
	rt.s = s
	setRewardParams(t, s)
	asset := s.config.WorkUSDC.Asset.String()

	const payTo = "0x00000000000000000000000000000000000000a1"
	wallet := func(n int) string { return fmt.Sprintf("0x%040x", 0xb00+n) }
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO x402_catalogue(id,bundler,url,method,pay_to,amount,first_seen,last_seen) VALUES('res-tx','x402','https://api.example.com/v1/search','GET',?,1000,1,1)", payTo)
	link := func(key ed25519.PrivateKey, kind, value string) {
		exec("INSERT INTO identity_links(agent,kind,value,state,created_at,checked_at) VALUES(?,?,?,'verified',1,1)", keyID(key), kind, value)
	}
	keys := make([]ed25519.PrivateKey, 10)
	for i := range keys {
		keys[i] = keyFor(byte(200 + i))
		rt.agent(keys[i])
		link(keys[i], "wallet", wallet(i))
	}
	owner := keys[7]
	link(owner, "domain", "example.com")
	link(keys[9], "wallet", wallet(1)) // keys[9] shares keys[1]'s wallet
	// The provider's own wallet pays the subject from a reviewer's account too.
	link(keys[8], "wallet", wallet(7))

	block := int64(80)
	add := func(n int, status string, when int64, logs ...map[string]any) string {
		block++
		chain.add(txHash(n), fakeTx{status: status, block: block, blockTime: when, logs: logs})
		return "tx:eip155:8453:" + txHash(n)
	}
	recent := testTime - 3600
	valid := "tx:eip155:8453:" + txHash(901) // mined only after the first read
	cases := []struct {
		key      ed25519.PrivateKey
		evidence string
		state    string
		reason   string
	}{
		{keys[1], valid, "verified", ""},
		{keys[2], add(902, "0x1", recent, transferLog(asset, wallet(99), payTo, 1000)), "failed", "tx_not_from_your_wallet"},
		{keys[3], add(903, "0x1", recent, transferLog(asset, wallet(3), wallet(98), 1000)), "failed", "tx_not_to_the_subjects_pay_to"},
		{keys[4], add(904, "0x1", recent, transferLog(asset, wallet(4), payTo, 0)), "failed", "tx_amount_zero"},
		{keys[5], add(905, "0x1", testTime-40*86400, transferLog(asset, wallet(5), payTo, 1000)), "failed", "older_than_30_days"},
		{keys[6], add(906, "0x0", recent, transferLog(asset, wallet(6), payTo, 1000)), "failed", "tx_failed"},
		{keys[0], add(907, "0x1", recent, transferLog(usdcToken, wallet(0), payTo, 1000)), "failed", "tx_not_to_the_subjects_pay_to"},
		{owner, add(908, "0x1", recent, transferLog(asset, wallet(7), payTo, 1000)), "failed", "paid_from_the_providers_wallet"},
		{keys[8], add(909, "0x1", recent, transferLog(asset, wallet(7), payTo, 1000)), "failed", "paid_from_the_providers_wallet"},
	}
	receipts := map[string]*Receipt{}
	for _, c := range cases {
		receipts[c.evidence] = rt.review(c.key, reviewSubject, map[string]any{}, "", c.evidence)
	}
	// Until the chain is read the reward is pending the evidence check, not
	// none (C254), on the receipt and on the review's card alike.
	for _, c := range cases {
		r := receipts[c.evidence].Reward
		if keyID(c.key) == keyID(owner) { // the owner earns nothing whatever the evidence
			if r == nil || r.State != "none" || r.Reason != "relationship" {
				t.Fatalf("owner's receipt reward %+v", r)
			}
			continue
		}
		if r == nil || r.State != RewardPendingEvidence || r.Evidence != "pending" || len(r.Pending) != 1 || r.Pending[0] != c.evidence || r.Reason != "" || !strings.Contains(r.Note, "Pending evidence check") {
			t.Fatalf("%s: receipt reward %+v", c.evidence, r)
		}
	}
	card := func(evidence string) *MessageReview {
		t.Helper()
		return run(t, s, Command{Operation: "message.get", MessageID: receipts[evidence].ID}).Messages[0].Review
	}
	if r := card(valid).Reward; r == nil || r.State != RewardPendingEvidence || r.Evidence != "pending" {
		t.Fatalf("card before the read: %+v", r)
	}
	// Another chain fails at post; a subject no record names a pay_to fails too.
	failedAtPost := rt.review(keys[2], "x402:GET https://nopay.example.org/x", map[string]any{}, "", "tx:eip155:8453:"+txHash(902))
	if r := failedAtPost.Reward; r == nil || r.State != "none" || r.Reason != "evidence_class" || len(failedAtPost.EvidenceFailed) != 1 || failedAtPost.EvidenceFailed[0].Reason != "subject_pay_to_unknown" {
		t.Fatalf("failed at post: %+v %+v", r, failedAtPost.EvidenceFailed)
	}
	rt.review(keys[3], "x402:GET https://nopay.example.org/x", map[string]any{}, "", "tx:eip155:1:"+txHash(903))

	row := func(key ed25519.PrivateKey, evidence string) (state, reason, class string) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT v.state,coalesce(json_extract(v.detail,'$.reason'),''),r.evidence_class FROM review_evidence v JOIN reviews r ON r.event_id=v.event_id
 WHERE v.evidence=? AND r.account=? AND r.subject=? AND r.current=1`, evidence, keyID(key), reviewSubjectOf(t, reviewSubject).ID).Scan(&state, &reason, &class); err != nil {
			t.Fatal(evidence, err)
		}
		return
	}
	var unknownPayTo, otherChain string
	_ = s.db.QueryRow(`SELECT json_extract(v.detail,'$.reason') FROM review_evidence v JOIN reviews r ON r.event_id=v.event_id WHERE r.account=? AND r.subject<>? AND v.evidence LIKE 'tx:%'`, keyID(keys[2]), reviewSubjectOf(t, reviewSubject).ID).Scan(&unknownPayTo)
	_ = s.db.QueryRow(`SELECT json_extract(v.detail,'$.reason') FROM review_evidence v JOIN reviews r ON r.event_id=v.event_id WHERE r.account=? AND r.subject<>? AND v.evidence LIKE 'tx:%'`, keyID(keys[3]), reviewSubjectOf(t, reviewSubject).ID).Scan(&otherChain)
	if unknownPayTo != "subject_pay_to_unknown" || otherChain != "tx_not_on_base" {
		t.Fatalf("post-time failures: %q %q", unknownPayTo, otherChain)
	}

	verify := func() {
		t.Helper()
		for i := 0; i < 4; i++ {
			if _, err := s.verifyReviewTxs(testContext); err != nil {
				t.Fatal(err)
			}
		}
	}
	verify()
	// The valid hash is not mined yet: still pending, read again later.
	if st, reason, class := row(keys[1], valid); st != "unknown" || reason != TxPending || class != "opinion" {
		t.Fatalf("not yet mined: %s %s %s", st, reason, class)
	}
	add(901, "0x1", recent, transferLog(asset, wallet(1), payTo, 250_000))
	rt.now += 2 * reviewTxRetry
	verify()
	for _, c := range cases {
		st, reason, class := row(c.key, c.evidence)
		if st != c.state || reason != c.reason {
			t.Fatalf("%s: %s %q, want %s %q", c.evidence, st, reason, c.state, c.reason)
		}
		if want := map[bool]string{true: "paid_use", false: "opinion"}[c.state == "verified"]; class != want {
			t.Fatalf("%s: class %s", c.evidence, class)
		}
	}
	var amount, rewards string
	if err := s.db.QueryRow(`SELECT json_extract(v.detail,'$.amount') FROM review_evidence v JOIN reviews r ON r.event_id=v.event_id WHERE v.evidence=? AND r.account=?`, valid, keyID(keys[1])).Scan(&amount); err != nil || amount != "0.25" {
		t.Fatalf("amount %q %v", amount, err)
	}
	// The verified payment raised the class after commit and offered a reward.
	if err := s.db.QueryRow(`SELECT json_extract(v.detail,'$.class') FROM review_evidence v JOIN reviews r ON r.event_id=v.event_id WHERE v.evidence=? AND r.account=?`, RewardEvidence, keyID(keys[1])).Scan(&rewards); err != nil || rewards != "paid_use" {
		t.Fatalf("reward %q %v", rewards, err)
	}
	// Verified: the card's reward is the offered one; failed: none pending,
	// and the evidence names its failure reason.
	if r := card(valid).Reward; r == nil || r.State != "pending" || r.Amount <= 0 || r.Class != "paid_use" || r.Evidence != "" {
		t.Fatalf("card after verification: %+v", r)
	}
	for _, c := range cases[1:] {
		m := card(c.evidence)
		if m.Reward != nil || len(m.Evidence) != 1 || m.Evidence[0].State != "failed" || m.Evidence[0].Detail["reason"] != c.reason || m.Evidence[0].Why == "" {
			t.Fatalf("%s: card after failure %+v %+v", c.evidence, m.Reward, m.Evidence)
		}
	}
	// One payment backs one account's review of the subject.
	rt.now++
	rt.review(keys[9], reviewSubject, map[string]any{}, "", valid)
	verify()
	if st, reason, _ := row(keys[9], valid); st != "failed" || reason != "tx_cited_by_another_review" {
		t.Fatalf("cited twice: %s %s", st, reason)
	}
	// A hash never mined fails a day after the review.
	late := keyFor(215)
	rt.agent(late)
	rt.review(late, reviewSubject, map[string]any{}, "", "tx:eip155:8453:"+txHash(950))
	verify()
	rt.now += reviewTxNotFoundAfter + 2*reviewTxRetry
	verify()
	if st, reason, _ := row(late, "tx:eip155:8453:"+txHash(950)); st != "failed" || reason != "tx_not_found" {
		t.Fatalf("never mined: %s %s", st, reason)
	}
	for _, code := range []string{"tx_not_from_your_wallet", "tx_not_to_the_subjects_pay_to", "tx_amount_zero", "tx_failed", "tx_not_found", "tx_after_the_review", "tx_cited_by_another_review", "subject_pay_to_unknown", "tx_not_on_base", "not_an_x402_or_mpp_subject", TxPending} {
		if ReviewEvidenceReasons[code] == "" {
			t.Fatalf("no sentence for %s", code)
		}
	}
}

// Without a chain reader tx: stays unknown and unchecked, as before.
func TestReviewTxEvidenceUnchecked(t *testing.T) {
	s := reviewsStore(t)
	key := keyFor(216)
	register(t, s, key)
	run(t, s, reviewPost(t, key, reviewSubject, map[string]any{"evidence": []string{"tx:eip155:8453:" + txHash(1)}}, ""))
	var state, reason string
	if err := s.db.QueryRow("SELECT state,json_extract(detail,'$.reason') FROM review_evidence WHERE evidence=?", "tx:eip155:8453:"+txHash(1)).Scan(&state, &reason); err != nil || state != "unknown" || reason != txUnchecked {
		t.Fatalf("%s %s %v", state, reason, err)
	}
	if n, err := s.verifyReviewTxs(testContext); n != 0 || err != nil {
		t.Fatalf("read %d %v", n, err)
	}
}
