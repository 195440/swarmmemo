package trust

import (
	"bytes"
	"context"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"swarmmemo/internal/judge"
)

// Tests of rule 4 (parameter version 7, C303b: RFC 0020 is the rule).

// rule4 turns a builder's snapshot into a version 7 one, keeping its seeds.
func rule4(b *builder) *builder {
	p := JudgementsParams()
	p.Seeds, p.ServiceAccounts = b.snap.Params.Seeds, b.snap.Params.ServiceAccounts
	b.snap.Params = p
	return b
}

func (b *builder) review(id, from, subject string, x int64, class string, at int64) *builder {
	b.snap.Reviews = append(b.snap.Reviews, Record{Type: "review", ID: id, Account: acct(from), Target: subject, Value: x, Kind: class,
		LinkValue: "customer", State: "good", CreatedAt: at})
	return b
}

func (b *builder) owns(subject, owner string) *builder {
	b.snap.Owners = append(b.snap.Owners, Record{Type: "owner", Target: subject, Account: acct(owner)})
	return b
}

// runAt computes the snapshot as of asOf with the previous run's carry.
func runAt(t *testing.T, snap Snapshot, asOf int64, carry *JudgementCarry) Output {
	t.Helper()
	snap.Meta.AsOf, snap.Carry = asOf, carry.Records()
	out, err := Compute(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	if out.Judgements == nil || out.JudgementsShadow != nil {
		t.Fatal("a rule 4 run has no judgements section, or a shadow")
	}
	checkRule4Conserves(t, out)
	return out
}

// checkRule4Conserves: nothing is minted in the run, and the carry it hands
// on keeps Σ balances + escrow ≤ 0.
func checkRule4Conserves(t *testing.T, out Output) {
	t.Helper()
	j := out.Judgements
	if j.Settlement.Paid > j.Settlement.Forfeited || j.StandingMass > j.SeedMass {
		t.Fatalf("minted: paid %d of %d forfeited, standing %d of seed %d", j.Settlement.Paid, j.Settlement.Forfeited, j.StandingMass, j.SeedMass)
	}
	total := j.Carry.Escrow
	for _, b := range j.Carry.Balances {
		total += b.Amount
	}
	if total > 0 || j.Carry.Escrow < 0 {
		t.Fatalf("the carry would mint: balances + escrow = %d", total)
	}
	var cents int64
	for _, s := range out.Scores {
		if s.Parts.Standing != nil {
			cents += s.Parts.Standing.Cents
		}
	}
	if cents*massPerCent > j.SeedMass {
		t.Fatalf("Σ standing %d¢ above the seed mass %d", cents, j.SeedMass)
	}
}

// The version: off by default, valid, and rule 4 only with its section.
func TestRule4Params(t *testing.T) {
	if DefaultVersion == JudgementsVersion || DefaultParams().Standing.Rule != 3 || DefaultParams().Standing.Judgements != nil {
		t.Fatal("rule 4 must not be the compiled-in default")
	}
	p := JudgementsParams()
	if p.Version != JudgementsVersion || p.Standing.Rule != 4 || *p.Standing.Judgements != judge.Default {
		t.Fatalf("version 7: %+v", p.Standing)
	}
	back, err := ParseParams(JudgementsVersion, p.Body())
	if err != nil || !bytes.Equal(back.Body(), p.Body()) {
		t.Fatalf("version 7 does not round-trip: %v", err)
	}
	if !strings.Contains(string(p.Body()), `"judgements":{"day_factor_ppm":996157,"half_life_days":180,"k_min":2,"prior_weight":2,"review_load":10,"thin_below":3}`) {
		t.Fatalf("version 7's judgements section: %s", p.Body())
	}
	bad := JudgementsParams()
	bad.Standing.Judgements = nil
	if bad.Validate() == nil {
		t.Fatal("rule 4 without standing.judgements validated")
	}
	bad = DefaultParams()
	jp := judge.Default
	bad.Standing.Judgements = &jp
	if bad.Validate() == nil {
		t.Fatal("standing.judgements under rule 3 validated")
	}
	bad = JudgementsParams()
	bad.Standing.Judgements.DayFactorPPM++
	bad.Standing.Judgements.DayFactorPPM++
	if bad.Validate() == nil {
		t.Fatal("a wrong judgements day factor validated")
	}
}

// Without judgements, rule 4 moves nothing: every account holds its seed.
// Posts, replies and transfers are not judgements.
func TestRule4NoJudgementsNoMovement(t *testing.T) {
	b := rule4(stakesWorld())
	b.snap.Transfers = append(b.snap.Transfers, Record{Type: "transfer", From: acct("s1"), To: acct("f1"), Amount: 5000, CreatedAt: testAsOf - day})
	out := runAt(t, b.snap, testAsOf, nil)
	for _, s := range out.Scores {
		if st := s.Parts.Standing; st != nil && st.Cents != st.SeedCents {
			t.Errorf("%s: %d¢ with seed %d¢", s.Account[:8], st.Cents, st.SeedCents)
		}
	}
	if j := out.Judgements; j.Edges.Endorse+j.Edges.Oppose != 0 || len(j.Final) != 0 || j.Judgements != 0 {
		t.Fatalf("section %+v", j)
	}
}

// Judgement edges: an endorsing judgement moves |x − 50| / 50 of its stake
// to the owner; an opposing one costs the author and the owner that share,
// which returns to the seeds. Rule 3's stakes are not read.
func TestRule4EdgesMoveStanding(t *testing.T) {
	subject := "subject:offering"
	b := rule4(stakesWorld())
	b.account("owner", 100, 3, 0).owns(subject, "owner")
	b.review("r1", "s1", subject, 100, "paid_use", testAsOf-2*day)
	out := runAt(t, b.snap, testAsOf, nil)
	s1, owner := standingOf(out, "s1"), standingOf(out, "owner")
	if owner.Cents <= 0 || s1.Cents >= s1.SeedCents || s1.SeedCents-s1.Cents < owner.Cents || out.Judgements.Edges.Endorse != 1 {
		t.Fatalf("endorse: s1 %d¢ of %d¢, owner %d¢, edges %+v", s1.Cents, s1.SeedCents, owner.Cents, out.Judgements.Edges)
	}
	// σ = 500¢ × 0.25% × 10 (a review) = 12.5¢, all of it at x = 100.
	if got := s1.SeedCents - s1.Cents; got < 12 || got > 13 {
		t.Fatalf("an endorsing review at 100 moved %d¢, want 12.5", got)
	}
	b.review("r2", "s2", subject, 25, "paid_use", testAsOf-day)
	out2 := runAt(t, b.snap, testAsOf, nil)
	s2, owner2 := standingOf(out2, "s2"), standingOf(out2, "owner")
	if s2.Cents >= s2.SeedCents || owner2.Cents >= owner.Cents || out2.Judgements.Edges.Oppose != 1 || out2.Judgements.Edges.OpposeMass == 0 {
		t.Fatalf("oppose: s2 %d¢ of %d¢, owner %d¢ → %d¢", s2.Cents, s2.SeedCents, owner.Cents, owner2.Cents)
	}
	// Self-owned: the owner's own review is no judgement and no edge.
	b.review("r3", "owner", subject, 100, "paid_use", testAsOf-day)
	if out3 := runAt(t, b.snap, testAsOf, nil); out3.Judgements.Edges.Endorse != 1 || out3.Judgements.Judgements != 2 {
		t.Fatalf("own review counted: %+v", out3.Judgements.Edges)
	}
}

// finalityWorld: two early judges right, then l1, a contrarian wrong and
// two later independent judges, all between 89 and 75.5 days before
// testAsOf. l1 enters at the consensus of the two early ones (68).
func finalityWorld() (*builder, string) {
	subject := "subject:finality"
	b := rule4(stakesWorld())
	b.account("owner", 200, 3, 0).owns(subject, "owner")
	b.review("early", "s1", subject, 90, "paid_use", testAsOf-89*day)
	b.review("early2", "s6", subject, 86, "paid_use", testAsOf-88*day)
	b.review("l1", "s3", subject, 88, "paid_use", testAsOf-80*day)
	b.review("wrong", "s2", subject, 10, "paid_use", testAsOf-77*day)
	b.review("l3", "s5", subject, 90, "paid_use", testAsOf-76*day)
	b.review("l2", "s4", subject, 92, "paid_use", testAsOf-75*day-day/2)
	return b, subject
}

// Finality: in the window a mark is provisional (never paid); when the
// judgement leaves the window its last provisional mark settles, and only
// then does standing move by it, through the author's judgement balance.
func TestRule4FinalMarksSettleAtExit(t *testing.T) {
	b, _ := finalityWorld()
	run1 := runAt(t, b.snap, testAsOf, nil)
	prov := map[string]JudgementMark{}
	for _, m := range run1.Judgements.Marks {
		if m.Mark != 0 {
			t.Fatalf("a provisional mark was paid: %+v", m)
		}
		prov[m.ID] = m
	}
	early, wrong := prov["review:early"], prov["review:wrong"]
	if early.Raw <= 0 || wrong.Raw >= 0 || len(run1.Judgements.Final) != 0 || len(run1.Judgements.Carry.Judgements) != 6 {
		t.Fatalf("run 1: early %+v wrong %+v final %d carry %d", early, wrong, len(run1.Judgements.Final), len(run1.Judgements.Carry.Judgements))
	}
	// Run 2, 15 days later: every judgement has left the window.
	asOf2 := int64(testAsOf + 15*day)
	run2 := runAt(t, b.snap, asOf2, run1.Judgements.Carry)
	j := run2.Judgements
	if len(j.Marks) != 0 || len(j.Final) < 2 || j.Settlement.Paid == 0 || j.CarryIn != sha256Hex(Canonical(run1.Judgements.Carry)) {
		t.Fatalf("run 2: marks %d final %+v settlement %+v", len(j.Marks), j.Final, j.Settlement)
	}
	settled := map[string]int64{}
	for _, m := range j.Final {
		settled[m.Author] += m.Mark
	}
	if settled[acct("s1")] <= 0 || settled[acct("s2")] != wrong.Raw || settled[acct("s1")] > early.Raw {
		t.Fatalf("settled %v (raw early %d, wrong %d)", settled, early.Raw, wrong.Raw)
	}
	// Standing moves only by the balance: no judgement is in run 2's
	// window, so an account's standing is its seed plus its balance.
	bal := map[string]int64{}
	for _, x := range j.Carry.Balances {
		bal[x.Account] = x.Amount
	}
	without := runAt(t, b.snap, asOf2, nil)
	for _, name := range []string{"s1", "s2", "s3", "owner", "author"} {
		with, base := standingOf(run2, name), standingOf(without, name)
		if want := (base.Cents*massPerCent + bal[acct(name)]) / massPerCent; with.Cents != want && with.Cents != want+1 && with.Cents != want-1 {
			t.Errorf("%s: %d¢ with the carry, %d¢ without, balance %d", name, with.Cents, base.Cents, bal[acct(name)])
		}
	}
	if judgement(standingOf(run2, "s1")) <= 0 && settled[acct("s1")] >= massPerCent {
		t.Error("the breakdown does not show the judgement balance")
	}
	// Run 3, a week on: the balances decay with standing's half-life, and
	// nothing settles twice.
	run3 := runAt(t, b.snap, asOf2+7*day, j.Carry)
	if len(run3.Judgements.Final) != len(j.Carry.Owed) {
		t.Fatalf("run 3 settled again: %+v", run3.Judgements.Final)
	}
	for _, x := range run3.Judgements.Carry.Balances {
		if x.Account == acct("s1") && (x.Amount >= bal[acct("s1")] || x.Amount <= 0) {
			t.Fatalf("s1's balance %d → %d does not decay", bal[acct("s1")], x.Amount)
		}
	}
}

// c₀ is the consensus at entry: the first run that sees a judgement
// computes it, and later runs read it from the carry, even when their own
// window no longer holds the judgements before it.
func TestRule4C0StoredAtEntry(t *testing.T) {
	b, _ := finalityWorld()
	run1 := runAt(t, b.snap, testAsOf, nil)
	c0 := map[string]int64{}
	for _, x := range run1.Judgements.Carry.Judgements {
		c0[x.ID] = x.C0
	}
	// Ten days on, "early" and "wrong" have left the window: recomputed,
	// l1's c0 would see no judgement before it (50).
	asOf := int64(testAsOf + 10*day)
	with, without := runAt(t, b.snap, asOf, run1.Judgements.Carry), runAt(t, b.snap, asOf, nil)
	got := map[string]int64{}
	for _, x := range with.Judgements.Carry.Judgements {
		got[x.ID] = x.C0
	}
	var fresh int64 = -1
	for _, x := range without.Judgements.Carry.Judgements {
		if x.ID == "review:l1" {
			fresh = x.C0
		}
	}
	if got["review:l1"] != c0["review:l1"] || c0["review:l1"] == fresh || fresh != judge.Neutral {
		t.Fatalf("l1's c0: entry %d, carried %d, recomputed %d", c0["review:l1"], got["review:l1"], fresh)
	}
	// A changed carry changes the mark: the run reads c0, never recomputes it.
	tampered := *run1.Judgements.Carry
	tampered.Judgements = append([]CarriedJudgement(nil), tampered.Judgements...)
	for i := range tampered.Judgements {
		if tampered.Judgements[i].ID == "review:l1" {
			tampered.Judgements[i].C0 = 20
		}
	}
	for _, m := range runAt(t, b.snap, asOf, &tampered).Judgements.Marks {
		if m.ID == "review:l1" && m.C0 != 20 {
			t.Fatalf("the carried c0 was not read: %+v", m)
		}
	}
}

// The carry is published input: it round-trips through the snapshot's
// JSONL, and a carry that would mint, comes from the future or meets an
// earlier rule is refused.
func TestRule4CarryIsCheckedInput(t *testing.T) {
	b, _ := finalityWorld()
	run1 := runAt(t, b.snap, testAsOf, nil)
	snap := b.snap
	snap.Meta.AsOf, snap.Carry = testAsOf+15*day, run1.Judgements.Carry.Records()
	var buf bytes.Buffer
	if err := snap.WriteJSONL(&buf); err != nil {
		t.Fatal(err)
	}
	again, err := ReadJSONL(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := Compute(context.Background(), snap)
	c, err := Compute(context.Background(), again)
	if err != nil || !bytes.Equal(a.Canonical(), c.Canonical()) {
		t.Fatalf("the carry does not round-trip: %v", err)
	}
	refuse := func(name string, s Snapshot) {
		t.Helper()
		if _, err := Compute(context.Background(), s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	minting := *run1.Judgements.Carry
	minting.Balances = append([]CarriedBalance{{Account: acct("f1"), Amount: 1}}, minting.Balances...)
	minting.Escrow += 5
	s := snap
	s.Carry = minting.Records()
	refuse("balances + escrow > 0", s)
	s = snap
	s.Meta.AsOf = testAsOf - day
	refuse("a carry after the run", s)
	s = snap
	s.Params = DefaultParams()
	refuse("a carry under rule 3", s)
	s = snap
	s.Carry = append(append([]Record(nil), snap.Carry...), Record{Type: "carry", Kind: carryRun, AsOf: testAsOf})
	refuse("two run records", s)
}

// Conservation as a property: chains of rule 4 runs over random worlds
// never mint, run by run and over the chain.
func TestRule4ConservationProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(303))
	for world := 0; world < 12; world++ {
		b := rule4(stakesWorld())
		subjects := []string{"subject:a", "subject:b", "subject:c"}
		owners := []string{"author", "f1", "f2"}
		for i, s := range subjects {
			b.owns(s, owners[i])
		}
		judges := []string{"s1", "s2", "s3", "s4", "s5", "s6", "f3", "f4"}
		for k := 0; k < 30; k++ {
			class := []string{"paid_use", "free_use", "opinion"}[rng.Intn(3)]
			b.review("w"+strconv.Itoa(k), judges[rng.Intn(len(judges))], subjects[rng.Intn(3)], rng.Int63n(101), class, testAsOf-rng.Int63n(120)*day-rng.Int63n(day))
		}
		for k := 0; k < 10; k++ {
			b.voteOn(judges[rng.Intn(len(judges))], "author", "p"+strconv.Itoa(1+rng.Intn(5)), rng.Int63n(100*24*60))
		}
		var carry *JudgementCarry
		var paid, forfeited int64
		for r := int64(0); r < 8; r++ {
			out := runAt(t, b.snap, testAsOf+r*9*day, carry)
			for _, m := range out.Judgements.Final {
				if m.Mark > 0 {
					paid += m.Mark
				} else {
					forfeited -= m.Mark
				}
			}
			carry = out.Judgements.Carry
		}
		if paid > forfeited {
			t.Fatalf("world %d: paid %d of %d forfeited over the chain", world, paid, forfeited)
		}
	}
}

// The replay reads a rule 4 snapshot without minting.
func TestRule4Replay(t *testing.T) {
	r, err := Replay(context.Background(), goldenJudgementsSnapshot(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Conservation.Minted || r.Judgements == 0 {
		t.Fatalf("replay %+v", r.Conservation)
	}
}
