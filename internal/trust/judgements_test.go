package trust

import (
	"bytes"
	"context"
	"slices"
	"strconv"
	"testing"

	"swarmmemo/internal/judge"
)

// Tests of RFC 0020 in shadow (C303a, judgements.go).

func shadowOf(t *testing.T, b *builder) *JudgementsShadow {
	t.Helper()
	out := compute(t, b)
	if out.JudgementsShadow == nil {
		t.Fatal("a rule 3 run has no judgements_shadow")
	}
	checkShadowConserves(t, out.JudgementsShadow)
	return out.JudgementsShadow
}

func checkShadowConserves(t *testing.T, s *JudgementsShadow) {
	t.Helper()
	var paid, forfeited int64
	for _, m := range s.Marks {
		if m.Mark > 0 {
			paid += m.Mark
		} else {
			forfeited -= m.Mark
		}
		if m.Mark > m.Raw && m.Raw >= 0 || m.Raw < 0 && m.Mark != m.Raw {
			t.Fatalf("mark %d from raw %d", m.Mark, m.Raw)
		}
	}
	if paid != s.Settlement.Paid || forfeited != s.Settlement.Forfeited || paid > forfeited || s.StandingMassShadow > s.StandingMass {
		t.Fatalf("minted: paid %d forfeited %d, standing %d → %d", paid, forfeited, s.StandingMass, s.StandingMassShadow)
	}
}

func marksBy(s *JudgementsShadow, name string) []JudgementMark {
	var out []JudgementMark
	for _, m := range s.Marks {
		if m.Author == acct(name) {
			out = append(out, m)
		}
	}
	return out
}

// An early judge the later independent consensus confirms earns, one that
// voted against it pays, a judgement with one later judgement stays open,
// and the winners are paid only from the losers (run-wide). Herding's zero
// is judge's unit test.
func TestShadowEarlyRightEarns(t *testing.T) {
	b := stakesWorld()
	b.voteOn("s1", "author", "p61", 3000)
	b.endorse("vote", "s5", "author", -1, 0)
	last := &b.snap.Endorsements[len(b.snap.Endorsements)-1]
	last.MessageID, last.CreatedAt = "p61", testAsOf-2500*60
	b.voteOn("s2", "author", "p61", 2000).voteOn("s3", "author", "p61", 1900).voteOn("s4", "author", "p61", 1800).voteOn("s6", "author", "p61", 1700)
	s := shadowOf(t, b)
	early, against, herd := marksBy(s, "s1"), marksBy(s, "s5"), marksBy(s, "s3")
	if len(early) != 1 || early[0].C0 != 50 || early[0].CT != 100 || early[0].GainPPM != 500_000 || early[0].Mark <= 0 {
		t.Fatalf("early judge: %+v", early)
	}
	if len(against) != 1 || against[0].GainPPM >= 0 || against[0].Mark >= 0 {
		t.Fatalf("judge against: %+v", against)
	}
	if len(herd) != 1 {
		t.Fatalf("s3 has two later judgements: %+v", herd)
	}
	if late := marksBy(s, "s4"); len(late) != 0 {
		t.Fatalf("s4 has one later judgement, fewer than k_min: %+v", late)
	}
	if s.Settlement.Paid == 0 || s.Settlement.ScalePPM >= 1e6 && s.Settlement.Owed > s.Settlement.Forfeited {
		t.Fatalf("settlement %+v", s.Settlement)
	}
	var score SubjectScore
	for _, sc := range s.Scores {
		if sc.Subject == judge.PostSubject("p61") {
			score = sc
		}
	}
	if score.Judgements != 6 || score.Roots != 6 || score.Score != 100 {
		t.Fatalf("post score %+v", score)
	}
}

// A ring that judges itself gets no marks: its members are not independent
// of each other (transfers within funded_days), so none of their judgements
// has an independent later one. Fresh keys without standing weigh nothing,
// so they neither earn nor force an honest judge's settlement.
func TestShadowRingGetsNoMarks(t *testing.T) {
	b := stakesWorld("r1", "r2", "r3")
	ring := []string{"r1", "r2", "r3"}
	for i, a := range ring {
		for j, c := range ring {
			if i != j {
				b.snap.Transfers = append(b.snap.Transfers, Record{Type: "transfer", From: acct(a), To: acct(c), Amount: 1000, CreatedAt: testAsOf - 2*day})
			}
		}
	}
	// r1, r2 and r3 each have ten posts: p61..p90 (author's follow).
	for i, a := range ring {
		for j, c := range ring {
			if i != j {
				b.voteOn(a, c, "p"+strconv.Itoa(61+10*j), int64(3000-100*i))
				b.vouch(a, c)
			}
		}
	}
	// Fresh keys pile onto an honest judge's subject after it.
	b.voteOn("s1", "author", "p91", 3000)
	for i := 0; i < 6; i++ {
		b.endorse("vote", "f"+strconv.Itoa(i), "author", -1, 0)
		last := &b.snap.Endorsements[len(b.snap.Endorsements)-1]
		last.MessageID, last.CreatedAt = "p91", testAsOf-int64(2000-10*i)*60
		b.voteOn("f"+strconv.Itoa(i), "f"+strconv.Itoa((i+1)%6), "p"+strconv.Itoa(100+i), 1000)
	}
	s := shadowOf(t, b)
	for _, a := range append(slices.Clone(ring), "f0", "f1", "f2", "f3", "f4", "f5", "s1") {
		if m := marksBy(s, a); len(m) != 0 {
			t.Errorf("%s has marks: %+v", a, m)
		}
	}
	if s.Judgements < 12 {
		t.Fatalf("the ring's judgements were not read: %d", s.Judgements)
	}
}

// A penalised owner forces c_T = 0: its vouchers lose, settled at once.
func TestShadowPenalisedOwner(t *testing.T) {
	b := stakesWorld()
	b.account("bad", 30, 3, 0)
	b.vouch("s1", "bad")
	b.snap.Penalties = append(b.snap.Penalties, Record{Type: "penalty", Account: acct("bad"), Evidence: "ev1", FractionPPM: 1e6, EndsAt: testAsOf + 10*day})
	s := shadowOf(t, b)
	m := marksBy(s, "s1")
	if len(m) != 1 || !m[0].Penalised || m[0].CT != 0 || m[0].GainPPM != -500_000 || m[0].Mark >= 0 {
		t.Fatalf("voucher of a penalised agent: %+v", m)
	}
}

// Reviews are judgements of their subject; the owner's own review is none;
// the score is the weighted median with the prior.
func TestShadowReviews(t *testing.T) {
	b := stakesWorld()
	b.account("owner", 100, 3, 0)
	subject := judge.ReviewSubject("x402", "GET https://api.example/x", "abc")
	review := func(id, from string, x, minutesAgo int64) {
		class := "paid_use"
		if from == "s6" {
			class = "observed" // 0.4: no exact half, so no midpoint
		}
		b.snap.Reviews = append(b.snap.Reviews, Record{Type: "review", ID: id, Account: acct(from), Target: subject, Value: x, Kind: class,
			LinkValue: "customer", State: "good", CreatedAt: testAsOf - minutesAgo*60})
	}
	b.snap.Owners = append(b.snap.Owners, Record{Type: "owner", Target: subject, Account: acct("owner")})
	review("ev1", "s1", 90, 3000)
	review("ev2", "s2", 85, 2000)
	review("ev3", "s3", 80, 1900)
	review("ev4", "s4", 95, 1800)
	review("ev5", "s5", 88, 1750)
	review("ev6", "s6", 92, 1720)
	review("ev7", "owner", 100, 1700)
	s := shadowOf(t, b)
	var score SubjectScore
	for _, sc := range s.Scores {
		if sc.Subject == subject {
			score = sc
		}
	}
	if score.Judgements != 6 || score.Score != 85 {
		t.Fatalf("review subject score %+v", score)
	}
	m := marksBy(s, "s1")
	// c_T is the later judges' median without the prior (judge.Outcome, C303s):
	// 80, 85, 88, 92 (0.4) and 95 weigh 4.4, so 88; the score keeps the prior.
	if len(m) != 1 || m[0].C0 != 50 || m[0].CT != 88 || m[0].GainPPM != 360_000 || m[0].Coarse {
		t.Fatalf("first reviewer: %+v", m)
	}
	if s.ByAct[judge.ActReview] != 6 {
		t.Fatalf("by act %v", s.ByAct)
	}
}

// Determinism: the same snapshot, in any record order and through its JSONL
// form, gives the same canonical bytes; rule 3's output does not read the
// judgement inputs (reviews, owners), and recompute.py accepts them.
func TestShadowDeterministic(t *testing.T) {
	snap := goldenStandingSnapshot()
	base, err := Compute(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	subject := judge.ReviewSubject("domain", "example.com", "xyz")
	for i, s := range snap.Params.Seeds[:4] {
		snap.Reviews = append(snap.Reviews, Record{Type: "review", ID: "r" + strconv.Itoa(i), Account: s, Target: subject, Value: int64(60 + 10*i), Kind: "observed",
			State: "good", CreatedAt: testAsOf - int64(10-i)*day})
	}
	snap.Owners = append(snap.Owners, Record{Type: "owner", Target: subject, Account: acct("alice")})
	a, err := Compute(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Canonical(), base.Canonical()) {
		t.Fatal("review inputs changed rule 3's output")
	}
	if bytes.Equal(a.JudgementsShadow.Canonical(), base.JudgementsShadow.Canonical()) {
		t.Fatal("review inputs did not reach the shadow")
	}
	shuffled := snap
	for _, list := range []*[]Record{&shuffled.Endorsements, &shuffled.Acts, &shuffled.Reviews, &shuffled.Owners, &shuffled.Posts, &shuffled.Transfers} {
		*list = slices.Clone(*list)
		slices.Reverse(*list)
	}
	var buf bytes.Buffer
	if err = shuffled.WriteJSONL(&buf); err != nil {
		t.Fatal(err)
	}
	again, err := ReadJSONL(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []Snapshot{shuffled, again} {
		b, err := Compute(context.Background(), s)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b.JudgementsShadow.Canonical(), a.JudgementsShadow.Canonical()) || !bytes.Equal(b.Canonical(), a.Canonical()) {
			t.Fatal("the same snapshot gave different bytes")
		}
	}
	testRecomputePy(t, buf.Bytes(), a.Canonical())
	// Before rule 3 there is no shadow.
	for _, build := range []func() Snapshot{goldenSnapshot, goldenStandingV5Snapshot} {
		out, err := Compute(context.Background(), build())
		if err != nil {
			t.Fatal(err)
		}
		if out.JudgementsShadow != nil {
			t.Fatal("a run before rule 3 has a judgements_shadow")
		}
	}
	// The published form is capped, with the full section's sha256.
	pub := a.JudgementsShadow.Published(1)
	if pub["sha256"] != sha256Hex(a.JudgementsShadow.Canonical()) || len(pub["marks"].([]JudgementMark)) > 1 {
		t.Fatalf("published %v", pub["sha256"])
	}
}

// The replay reports on the golden snapshot without minting.
func TestReplay(t *testing.T) {
	r, err := Replay(context.Background(), goldenStandingSnapshot(), 90)
	if err != nil {
		t.Fatal(err)
	}
	if r.Conservation.Minted || r.Judgements == 0 || len(r.MostJudged) == 0 || len(r.Detected) == 0 || r.WindowDays != 90 {
		t.Fatalf("replay %+v", r)
	}
	if md := r.Markdown(); !bytes.Contains([]byte(md), []byte("## Conservation")) || !bytes.Contains([]byte(md), []byte("## Ring and funnel members")) {
		t.Fatal(md)
	}
	if _, err = Replay(context.Background(), goldenStandingV5Snapshot(), 0); err == nil {
		t.Fatal("a version 5 snapshot replayed")
	}
}
