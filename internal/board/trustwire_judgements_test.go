package board

import (
	"encoding/json"
	"testing"
	"time"

	"swarmmemo/internal/judge"
)

// RFC 0020 in shadow (C303a): a rule 3 run reads the window's community
// reviews (current and superseded) and their subjects' owners into its
// snapshot, records the judgements_shadow section with its inputs, and
// changes nothing else; subject.get's aggregate carries score_shadow.
func TestTrustJudgementsShadow(t *testing.T) {
	s := openTest(t, Config{Features: Features{Trust: TrustShadow, Reviews: true}})
	a, b, owner := keyFor(71), keyFor(72), keyFor(73)
	register(t, s, a)
	register(t, s, b)
	register(t, s, owner)
	if _, err := s.db.Exec("INSERT INTO identity_links(agent,kind,value,state,created_at,checked_at) VALUES(?,'domain','example.com','verified',?,?)",
		keyID(owner), s.now().Unix()-86400, s.now().Unix()); err != nil {
		t.Fatal(err)
	}
	first := run(t, s, reviewPost(t, a, reviewSubject, map[string]any{"verdict": "mixed"}, "")).Receipt.ID
	run(t, s, reviewPost(t, a, reviewSubject, map[string]any{"verdict": "good", "dimensions": map[string]any{"good": 91}}, first))
	run(t, s, reviewPost(t, b, reviewSubject, map[string]any{"verdict": "bad"}, ""))
	run(t, s, reviewPost(t, owner, reviewSubject, map[string]any{"verdict": "unverified"}, ""))
	if agg := readSubjectData(t, s, reviewSubject)["aggregate"].(ReviewAggregate); agg.ScoreShadow < judge.Min || agg.ScoreShadow > judge.Max {
		t.Fatalf("score_shadow %d", agg.ScoreShadow)
	}
	posted := s.now()
	s.now = func() time.Time { return posted.Add(25 * time.Hour) }

	snap, err := s.TrustInputs(testContext, 0)
	if err != nil {
		t.Fatal(err)
	}
	sub := judge.ReviewSubject("x402", reviewSubjectOf(t, reviewSubject).Key, reviewSubjectOf(t, reviewSubject).ID)
	xs := map[int64]int{}
	for _, r := range snap.Reviews {
		if r.Target != sub {
			t.Fatalf("review record %+v", r)
		}
		xs[r.Value]++
	}
	// mixed (50), then good with good=91 (91), bad (20); unverified is none.
	if len(snap.Reviews) != 3 || xs[50] != 1 || xs[91] != 1 || xs[20] != 1 {
		t.Fatalf("reviews in the snapshot: %+v", snap.Reviews)
	}
	if len(snap.Owners) != 1 || snap.Owners[0].Account != keyID(owner) || snap.Owners[0].Target != sub {
		t.Fatalf("owners in the snapshot: %+v", snap.Owners)
	}

	sum, err := s.RunTrust(testContext)
	if err != nil || sum.State != "done" {
		t.Fatalf("run: %+v %v", sum, err)
	}
	var raw string
	if err = s.db.QueryRow("SELECT inputs FROM trust_runs WHERE id=?", sum.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var inputs struct {
		Shadow struct {
			Version int64            `json:"version"`
			SHA256  string           `json:"sha256"`
			ByAct   map[string]int64 `json:"by_act"`
			Scores  []struct {
				Subject    string `json:"subject"`
				Judgements int64  `json:"judgements"`
			} `json:"scores"`
		} `json:"judgements_shadow"`
	}
	if err = json.Unmarshal([]byte(raw), &inputs); err != nil {
		t.Fatal(err)
	}
	sh := inputs.Shadow
	if sh.Version != judge.ShadowVersion || len(sh.SHA256) != 64 || sh.ByAct[judge.ActReview] != 3 || len(sh.Scores) == 0 || sh.Scores[0].Subject != sub || sh.Scores[0].Judgements != 2 {
		t.Fatalf("judgements_shadow %s", raw)
	}
	// trust.get still reads the run's summary.
	if run := trustGet(t, s, keyID(a))["run"]; run == nil {
		t.Fatal("trust.get lost the run")
	}
}

func TestReviewScoreShadow(t *testing.T) {
	// Two weight groups: good with good=90 at 3.5, bad at 1.0, plus the
	// prior (2 at 50): the median is 90.
	if got := reviewScoreShadow(`[["good",-1,90,3.5],["bad",-1,-1,1.0],["unverified",-1,-1,9.0]]`); got != 90 {
		t.Fatalf("score_shadow %d", got)
	}
	if got := reviewScoreShadow(`[]`); got != judge.Neutral {
		t.Fatalf("empty score_shadow %d", got)
	}
}
