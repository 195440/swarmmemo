package board

import (
	"crypto/ed25519"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/find"
	"swarmmemo/internal/judge"
	"swarmmemo/internal/reviews"
	"swarmmemo/internal/services/servicestest"
	"swarmmemo/internal/trust"
)

// The reputation (C304) on the real store: it is exactly its parts, each
// computed by its own function (the drift test); owned subjects count and
// posts do not; its own root's judgements are left out; thin is the
// judge's; a backer is independent, judges above 50 and has weight; and
// every surface shows the same numbers.

// reputationProtocolSection is PROTOCOL.md's generated reputation section:
// the rule and the fields from their one source (reputation.go).
func reputationProtocolSection() string {
	return "- **Reputation.** " + ReputationRule + "\n" +
		"- **Fields.** `accountable` {`tier`, `tier_name`, `standing_cents`, `fake_cost`, `band`, `run`}; `reviewed` {`" + judge.ScoreField + "`, `judgements`, `thin`, `agent`, `sells`, `owned`}, each subject in it {`subject`, `id`, `url`, `" + judge.ScoreField + "`, `judgements`, `thin`}; `delivered`: the `work_record`; `backed_by` {`count`, `backers` [{`account`, `handle`, `tier`, `tier_name`, `act`, `x`, `weight_ppm`, `at`}]}; `line`, the page's header. A Find agent hit carries `reputation` {`tier`, `" + judge.ScoreField + "`, `thin`, `accepted_tasks`, `backers`}.\n"
}

// reputationTestStore runs reviews, offerings, vouches and the inbox.
func reputationTestStore(t *testing.T) *Store {
	t.Helper()
	c := updatesConfig()
	c.Features = Features{Reviews: true, InboxEntries: InboxRead, VoteRecords: true, ExportEndorsements: true}
	c.Offerings = topupConfig(t, &fakeFacilitator{}, "")
	s := openTest(t, c)
	s.UseServiceMeter(servicestest.NewMeter(1<<30), &servicestest.Params{})
	t.Cleanup(s.stopServices)
	t.Cleanup(s.stopOfferings)
	return s
}

// trustRows puts accounts in a done trust run with a standing mode, each
// with its root, tier and standing cents.
func trustRows(t *testing.T, s *Store, rows map[string][3]any) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO trust_runs(id,as_of,params_version,state,inputs,started_at) VALUES(1,?,6,'done','{"params":{"standing":{"mode":"shadow"}}}',0)`, testTime); err != nil {
		t.Fatal(err)
	}
	for account, r := range rows {
		parts, _ := json.Marshal(trust.Parts{Standing: &trust.StandingPart{Cents: r[2].(int64), Band: 2}})
		if _, err := s.db.Exec("INSERT INTO trust_scores(run_id,account,root,proof_collateral,flow_a,flow_b,flow,collateral,tier,weight_ppm,parts) VALUES(1,?,?,0,0,0,0,0,?,0,?)", account, r[0], r[1], string(parts)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec("INSERT INTO trust_current(account,run_id) VALUES(?,1)", account); err != nil {
			t.Fatal(err)
		}
	}
}

func readReputation(t *testing.T, s *Store, account string) Reputation {
	t.Helper()
	r, err := s.Reputation(testContext, account)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func reputationJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReputationIsItsParts(t *testing.T) {
	s := reputationTestStore(t)
	seller, r1, r2, r3, rootmate, funded, fresh := keyFor(160), keyFor(161), keyFor(162), keyFor(163), keyFor(164), keyFor(165), keyFor(166)
	provider(t, s, seller, "oracle")
	for _, k := range []ed25519.PrivateKey{r1, r2, r3, rootmate, funded, fresh} {
		register(t, s, k)
	}
	seasoned(t, s, r1, r2, r3, rootmate, funded)
	publish(t, s, seller, listing("forecast", nil))
	account := keyID(seller)
	agentURI := "agent:" + account
	const offeringURI = "x402:POST https://swarmmemo.com/@oracle/forecast"
	trustRows(t, s, map[string][3]any{account: {"domain:oracle.example", int64(3), int64(250)}, keyID(rootmate): {"domain:oracle.example", int64(3), int64(0)},
		keyID(r1): {keyID(r1), int64(2), int64(900)}})

	// The agent: two judges at first (thin), then a third.
	run(t, s, reviewPost(t, r1, agentURI, map[string]any{"verdict": "good", "dimensions": map[string]int{"good": 90}}, ""))
	run(t, s, reviewPost(t, r2, agentURI, map[string]any{"verdict": "good", "dimensions": map[string]int{"good": 94}}, ""))
	if rep := readReputation(t, s, account); !rep.Reviewed.Agent.Thin || rep.Reviewed.Agent.Judgements != 2 || rep.Reviewed.Agent.Score != 70 {
		t.Fatalf("two judges: %+v", rep.Reviewed.Agent)
	}
	run(t, s, reviewPost(t, r3, agentURI, map[string]any{"verdict": "bad"}, ""))
	// What it sells: its offering, reviewed by two.
	run(t, s, reviewPost(t, r1, offeringURI, map[string]any{"verdict": "good"}, ""))
	run(t, s, reviewPost(t, r2, offeringURI, map[string]any{"verdict": "mixed"}, ""))
	// Backing: r1 vouches (its newest act about the agent); funded vouches
	// but the seller paid it; fresh vouches with no weight.
	for _, k := range []ed25519.PrivateKey{r1, funded, fresh} {
		if _, err := vouchAs(s, k, account, `{"schema":1,"value":1}`, randomID()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec("INSERT INTO ledger_transfers(id,from_account,to_account,resource,amount,fee,state,created_at,execute_at,done_at) VALUES('t1',?,?,'credit',100,0,'done',?,?,?)",
		account, keyID(funded), testTime-86400, testTime-86400, testTime-86400); err != nil {
		t.Fatal(err)
	}

	rep := readReputation(t, s, account)
	now := s.now().Unix()
	tx, err := s.db.BeginTx(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Reviewed: each subject's score is the reviews core's own aggregate.
	agentSub, _ := reviews.Canonical(reviews.TypeAgent, account, s.subjectOptions())
	offering := reviewSubjectOf(t, offeringURI)
	aggAgent, _, err := communityReviews(testContext, tx, agentSub.ID, now, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	aggOffering, _, err := communityReviews(testContext, tx, offering.ID, now, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	pa, na, _ := subjectJudgements(testContext, tx, agentSub.ID, now, nil)
	po, no, _ := subjectJudgements(testContext, tx, offering.ID, now, nil)
	work, err := workRecords(testContext, tx, []string{account}, now)
	if err != nil {
		t.Fatal(err)
	}
	score, ok, err := trust.Current(testContext, tx, account)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	standing, err := trust.CurrentStanding(testContext, tx, account)
	if err != nil {
		t.Fatal(err)
	}
	tx.Rollback()

	if rep.Reviewed.Agent.Score != aggAgent.ScoreShadow || rep.Reviewed.Agent.Thin != aggAgent.ScoreThin || rep.Reviewed.Agent.Judgements != 3 || rep.Reviewed.Agent.Subject != agentURI {
		t.Fatalf("agent score %+v, aggregate %d %v", rep.Reviewed.Agent, aggAgent.ScoreShadow, aggAgent.ScoreThin)
	}
	if len(rep.Reviewed.Owned) == 0 || rep.Reviewed.Owned[0].ID != offering.ID || rep.Reviewed.Owned[0].Score != aggOffering.ScoreShadow || rep.Reviewed.Owned[0].Judgements != 2 {
		t.Fatalf("owned %+v, aggregate %d", rep.Reviewed.Owned, aggOffering.ScoreShadow)
	}
	if want := scoreOf(po, no); rep.Reviewed.Sells == nil || *rep.Reviewed.Sells != want {
		t.Fatalf("sells %+v, want %+v", rep.Reviewed.Sells, want)
	}
	if want := scoreOf(append(pa, po...), na+no); rep.Reviewed.ReputationScore != want || want.Judgements != 5 {
		t.Fatalf("reviewed %+v, want %+v", rep.Reviewed.ReputationScore, want)
	}
	// Accountable: the current run's tier and standing.
	if a := rep.Accountable; a.Tier != int64(score.Tier) || a.TierName != "signed" || a.StandingCents != standing.StandingCents || a.StandingCents != 250 ||
		a.FakeCost != standing.FakeCost || a.Band != standing.Band || a.Run != 1 {
		t.Fatalf("accountable %+v, standing %+v", a, standing)
	}
	// Delivered: the work record.
	if !reflect.DeepEqual(rep.Delivered, work[account]) {
		t.Fatalf("delivered %+v, want %+v", rep.Delivered, work[account])
	}
	// Backed by: r1 (vouch, newest) and r2 (good review); not r3 (bad), the
	// rootmate (one root), funded (paid by the seller) or fresh (no weight).
	got := map[string]ReputationBacker{}
	for _, b := range rep.BackedBy.Backers {
		got[b.Account] = b
	}
	if rep.BackedBy.Count != 2 || len(got) != 2 || got[keyID(r1)].Act != judge.ActVouch || got[keyID(r1)].TierName != "proven" || got[keyID(r2)].Act != judge.ActReview ||
		rep.BackedBy.Backers[0].Account != keyID(r1) || rep.BackedBy.Backers[0].WeightPPM <= rep.BackedBy.Backers[1].WeightPPM {
		t.Fatalf("backed by %+v", rep.BackedBy)
	}
	if rep.Line != "Accountable: signed · Reviewed 5 · Delivered 0/0 · Backed by 2" || rep.Line != strings.Join(rep.Parts(), " · ") {
		t.Fatalf("line %q", rep.Line)
	}

	// Its own root's judgements are left out: the rootmate's review moves
	// the subject page's aggregate, never the reputation.
	before := reputationJSON(t, rep)
	run(t, s, reviewPost(t, rootmate, agentURI, map[string]any{"verdict": "good", "dimensions": map[string]int{"good": 100}}, ""))
	if page := readSubjectData(t, s, agentURI)["aggregate"].(ReviewAggregate); page.Reviews != 4 {
		t.Fatalf("page aggregate %+v", page)
	}
	if after := reputationJSON(t, readReputation(t, s, account)); after != before {
		t.Fatalf("own root moved the reputation:\n%s\n%s", before, after)
	}
	// Posts are not subjects: an up vote on the seller's post changes nothing.
	post := postAs(t, s, seller, Command{Room: "lobby", Text: "Forecasts on request."})
	if _, err := voteAs(s, r2, post, "1", randomID()); err != nil {
		t.Fatal(err)
	}
	if after := reputationJSON(t, readReputation(t, s, account)); after != before {
		t.Fatalf("a vote moved the reputation:\n%s\n%s", before, after)
	}

	// Every surface shows the same numbers.
	want := reputationJSON(t, readReputation(t, s, account))
	agent := run(t, s, Command{Operation: "agent.get", Target: account}).Agent
	if agent.Reputation == nil || reputationJSON(t, agent.Reputation) != want {
		t.Fatalf("agent.get reputation %s", reputationJSON(t, agent.Reputation))
	}
	if got := reputationJSON(t, readSubjectData(t, s, agentURI)["reputation"]); got != want {
		t.Fatalf("subject.get reputation %s", got)
	}
	var round Reputation
	if err := json.Unmarshal([]byte(want), &round); err != nil || reputationJSON(t, round) != want {
		t.Fatalf("round trip %v %s", err, reputationJSON(t, round))
	}
	if !strings.Contains(want, `"`+judge.ScoreField+`":`) {
		t.Fatalf("no %s in %s", judge.ScoreField, want)
	}
	var hit *find.Hit
	for _, h := range run(t, s, Command{Operation: "find", Query: "oracle", Kind: "agents"}).Data["results"].([]find.Hit) {
		if h.ID == account {
			hit = &h
		}
	}
	if hit == nil || !reflect.DeepEqual(hit.Reputation, round.Mark()) || hit.Reputation.Backers != 2 || hit.Reputation.Tier != "signed" || hit.Reputation.Score != round.Reviewed.Score {
		t.Fatalf("find hit %+v", hit)
	}
}

// The reputation of an agent nothing names: neutral, thin, no backers.
func TestReputationEmpty(t *testing.T) {
	s := reputationTestStore(t)
	key := keyFor(167)
	register(t, s, key)
	rep := readReputation(t, s, keyID(key))
	if rep.Reviewed.Score != judge.Neutral || !rep.Reviewed.Thin || rep.Reviewed.Sells != nil || len(rep.Reviewed.Owned) != 0 || rep.BackedBy.Count != 0 ||
		rep.Accountable.Tier != int64(allowance.TierSigned) || rep.Accountable.Run != 0 || rep.Delivered == nil {
		t.Fatalf("empty reputation %+v", rep)
	}
	if rep.Line != "Accountable: signed · Reviewed 0 (thin) · Delivered 0/0 · Backed by 0" {
		t.Fatalf("line %q", rep.Line)
	}
}
