package board

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/trust"
)

// setStanding records a finished trust run (parameter version 8) in which
// each named account holds the given standing, as Run writes it: what every
// consumer reads.
func setStanding(t *testing.T, s *Store, cents map[string]int64) int64 {
	t.Helper()
	p := trust.DefaultParams()
	now := s.now().Unix()
	asOf := now - now%86400
	res, err := s.db.Exec("INSERT INTO trust_runs(as_of,params_version,state,inputs,started_at,finished_at) VALUES(?,?,'done','{}',?,?)", asOf, trust.DefaultVersion, now, now)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := res.LastInsertId()
	for account, c := range cents {
		parts := string(trust.Canonical(trust.Parts{World: []trust.WorldItem{}, Backers: []trust.FlowPart{}, Backs: []trust.FlowPart{}, AccuracyPPM: 1e6,
			Burn: trust.BurnPreview{ProofRoots: []string{}, Backers: []trust.BackerAtRisk{}}}))
		if _, err = s.db.Exec("INSERT INTO trust_scores(run_id,account,root,proof_collateral,flow_a,flow_b,flow,collateral,tier,weight_ppm,parts) VALUES(?,?,?,0,0,0,?,?,?,?,?)",
			run, account, account, c, c, int64(p.Band(c)), p.VoteWeight(c), parts); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec("INSERT INTO trust_current(account,run_id) VALUES(?,?) ON CONFLICT(account) DO UPDATE SET run_id=excluded.run_id", account, run); err != nil {
			t.Fatal(err)
		}
	}
	// Every account a run scores is in it: the earlier run's other rows carry
	// over, as a nightly run would score them again.
	if _, err = s.db.Exec(`INSERT OR IGNORE INTO trust_scores(run_id,account,root,proof_collateral,flow_a,flow_b,flow,collateral,tier,weight_ppm,parts)
 SELECT ?,account,root,proof_collateral,flow_a,flow_b,flow,collateral,tier,weight_ppm,parts FROM trust_scores WHERE run_id=(SELECT max(id) FROM trust_runs WHERE id<? AND state='done')`, run, run); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE trust_current SET run_id=? WHERE account IN (SELECT account FROM trust_scores WHERE run_id=?)", run, run); err != nil {
		t.Fatal(err)
	}
	s.trust.forgetParams()
	return run
}

// RFC 0021 §12: every consumer reads the one standing. An account with 300¢
// (proven) and one with none are read alike by the allowance classifier,
// v(s) (votes, reviews), the names gate, the inbox gate, the reputation's
// Accountable part, Find and the standing breakdown.
func TestEveryConsumerReadsOneStanding(t *testing.T) {
	s := openTest(t, Config{Features: Features{Trust: TrustOn, NameGate: true}})
	proven, fresh := keyFor(81), keyFor(82)
	s.now = func() time.Time { return time.Unix(testTime-2*86400, 0) }
	setStanding(t, s, map[string]int64{keyID(proven): 300})
	postAs(t, s, proven, Command{Room: "lobby", Text: "hello", RequestID: "c-hello"})
	post := postAs(t, s, fresh, Command{Room: "lobby", Text: "a post to vote on", RequestID: "c-post"})
	s.now = func() time.Time { return time.Unix(testTime+3600, 0) }
	p := trust.DefaultParams()
	now := s.now().Unix()

	// The classifier: band, cents and share.
	tx, err := s.db.BeginTx(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	stP, _ := s.classifier().Classify(testContext, tx, allowance.Subject{ID: keyID(proven), Signed: true}, now)
	stF, _ := s.classifier().Classify(testContext, tx, allowance.Subject{ID: keyID(fresh), Signed: true}, now)
	stA, _ := s.classifier().Classify(testContext, tx, allowance.Subject{ID: "anon:x"}, now)
	stH, _ := s.classifier().Classify(testContext, tx, allowance.Subject{ID: keyID(proven), Signed: true, Hosted: true}, now)
	vP, _ := s.voteWeightPPM(testContext, tx, keyID(proven), false, now)
	vF, _ := s.voteWeightPPM(testContext, tx, keyID(fresh), false, now)
	cents, _ := accountStanding(testContext, tx, keyID(proven))
	tx.Rollback()
	if stP.Band != allowance.BandProven || stP.Cents != 300 || stP.ShareCents != 300 || stF.Band != allowance.BandSigned || stF.Cents != 0 ||
		stA.Band != allowance.BandAnonymous || stA.ShareCents != 0 || stH.Band != allowance.BandAnonymous || stH.ShareCents != 0 || cents != 300 {
		t.Fatalf("classifier: %+v %+v %+v %+v", stP, stF, stA, stH)
	}
	if vP != p.VoteWeight(300) || vP == 0 || vF != 0 {
		t.Fatalf("v(s): %d %d", vP, vF)
	}

	// Votes: a seasoned voter's vote weighs a whole vote (the floor, RFC
	// 0021 §14), whatever its standing; any other vote weighs v(s).
	tx, err = s.db.BeginTx(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	vFS, _ := s.voteWeightPPM(testContext, tx, keyID(fresh), true, now)
	tx.Rollback()
	if vFS != 1e6 {
		t.Fatalf("a seasoned account without standing weighs %d", vFS)
	}
	run(t, s, signed(proven, Command{Operation: "vote", MessageID: post, Data: `{"value":1}`, Timestamp: now}))
	newcomer := keyFor(83)
	setStanding(t, s, map[string]int64{keyID(newcomer): 300})
	postAs(t, s, newcomer, Command{Room: "lobby", Text: "new here", RequestID: "c-new"})
	run(t, s, signed(newcomer, Command{Operation: "vote", MessageID: post, Data: `{"value":1}`, Timestamp: now}))
	weights, err := s.standingVoteWeights(testContext, s.db, now)
	if err != nil || weights[post] != p.VoteWeight(300) {
		t.Fatalf("vote weights: %v %v", weights, err)
	}
	var seasonedVotes int64
	if err = s.db.QueryRow("SELECT score FROM event_scores WHERE event_id=?", post).Scan(&seasonedVotes); err != nil || seasonedVotes != 1 {
		t.Fatalf("seasoned votes %d %v", seasonedVotes, err)
	}
	if w := rankWeight(seasonedVotes, weights[post]); w != 1+float64(p.VoteWeight(300))/1e6 {
		t.Fatalf("ranking weight %v", w)
	}

	// The names gate: below the proven band, no new names.
	if _, err := s.Execute(testContext, signed(fresh, Command{Operation: "room.create", Room: "freshroom", Visibility: "public", Timestamp: now}), "o"); errCode(err) != "standing_required" {
		t.Fatalf("a signed key without standing creates a room: %v", err)
	}
	if _, err := s.Execute(testContext, signed(proven, Command{Operation: "room.create", Room: "provenroom", Visibility: "public", Timestamp: now}), "o"); err != nil {
		t.Fatalf("a proven key: %v", err)
	}

	// The inbox gate: trust_at_least proven.
	ev := &policyEval{q: s.db, in: inboundCase{Sender: keyID(proven), Recipient: keyID(fresh), Now: now, Trust: true}}
	ok, err := ev.holds(testContext, PolicyCondition{TrustAtLeast: &TrustBar{Band: "proven"}})
	if err != nil || !ok {
		t.Fatalf("trust_at_least proven for a proven sender: %v %v", ok, err)
	}
	ev.in.Sender = keyID(fresh)
	if ok, err = ev.holds(testContext, PolicyCondition{TrustAtLeast: &TrustBar{Cents: 1}}); err != nil || ok {
		t.Fatalf("trust_at_least 1¢ for a sender without standing: %v %v", ok, err)
	}

	// The reputation's Accountable part and Find.
	rep, err := s.Reputation(testContext, keyID(proven))
	if err != nil || rep.Accountable.StandingCents != 300 || rep.Accountable.Band != "proven" || !strings.Contains(rep.Line, "proven") {
		t.Fatalf("reputation: %+v %v", rep.Accountable, err)
	}
	if f := s.findStanding(testContext)[keyID(proven)]; f.band != "proven" || f.v != float64(p.VoteWeight(300))/1e6 {
		t.Fatalf("find: %+v", f)
	}

	// The breakdown says the same.
	out, err := s.TrustStanding(testContext, keyID(proven), "")
	if err != nil || out["standing"].(*trust.StandingView).StandingCents != 300 {
		t.Fatalf("breakdown: %+v %v", out, err)
	}
}

// The one standing has one reader in the board: trust_scores is queried
// only where the standing is read (standing.go) or listed whole (the trust
// API, the public graph, Find, the work record's root), and no file computes
// v(s), a band or a tier of its own. A new reader must go through these.
func TestStandingHasOneReader(t *testing.T) {
	readers := map[string]bool{"standing.go": true, "trustapi.go": true, "trustgraph.go": true, "trustlayer.go": true, "find.go": true, "workreputation.go": true, "trustcompare.go": true}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// trust.Params.FlooredVoteWeight is the one floored weight (C320c); the
	// retired per-tier FlooredVoteWeightPPM stays out.
	ownWeight := regexp.MustCompile(`VoteWeightPPM\(|isqrt|ThetaProven|TierTrusted|TierProven|TierSigned|allowance\.Tier\b|FlooredVoteWeightPPM|standing\.mode`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if strings.Contains(src, "trust_scores") && !readers[f] {
			t.Errorf("%s reads trust_scores: read standing through standing.go (accountStanding, voteWeightPPM, the classifier)", f)
		}
		if m := ownWeight.FindString(src); m != "" {
			t.Errorf("%s computes standing its own way (%s): use trust.Params (VoteWeight, Band, BandFloorCents)", f, m)
		}
	}
}
