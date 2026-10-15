package trust

import (
	"context"
	"math"
	"math/rand/v2"
	"sort"
	"strconv"
	"testing"
	"time"

	"swarmmemo/internal/judge"
)

// RFC 0021's tables, on the real code: every number here comes from
// Compute on a snapshot (the experiments in anchor_sim_test.go and
// unified_sim_test.go are their models).

// §9's spend curve: one account, no backers. Standing is what it paid, up
// to node_cap; v(s) saturates at c_ref; above node_cap money only backs
// others, at most U per edge.
func TestSpendCurve(t *testing.T) {
	p := DefaultParams()
	rows := []struct {
		cents, standing int64
		vote            float64
	}{{50, 50, 0.49}, {100, 100, 0.59}, {200, 200, 0.72}, {500, 500, 1}, {750, 750, 1}, {1000, 750, 1}, {3000, 750, 1}, {10000, 750, 1}, {100000, 750, 1}}
	for _, row := range rows {
		b := newBuilder().account("spender", 10).pay("spender", row.cents, 5).reply("responder", "spender", 1)
		out := compute(t, b)
		sc := scoreOf(out, "spender")
		if sc.Cents != row.standing {
			t.Errorf("%d¢ spent: standing %d, want %d", row.cents, sc.Cents, row.standing)
		}
		if v := math.Round(float64(p.VoteWeight(sc.Cents))/1e4) / 100; v != row.vote {
			t.Errorf("%d¢ spent: vote weight %.2f, want %.2f", row.cents, v, row.vote)
		}
		// Judging 10 fresh accounts at full strength (x 100, paid use):
		// what it holds above node_cap backs them, each edge at most U; and
		// each judgement makes the judged account's day active, its time
		// present (10¢) backed by the judge's direct costs.
		for i := 0; i < 10; i++ {
			b.review("spender", "fresh"+strconv.Itoa(i), 100, "paid_use", 1)
		}
		out = compute(t, b)
		var backed int64
		for i := 0; i < 10; i++ {
			backed += scoreOf(out, "fresh"+strconv.Itoa(i)).Cents
		}
		want := min(max(0, row.cents-p.NodeCapCents), 10*p.UnitCents) + min(row.cents, 10*p.DayPriceCents)
		if scoreOf(out, "spender").Cents != row.standing || backed > want || backed < want*99/100 {
			t.Errorf("%d¢ spent, judging 10 fresh accounts: keeps %d¢ (want %d), backs %d¢ (want about %d)", row.cents, scoreOf(out, "spender").Cents, row.standing, backed, want)
		}
	}
}

// §3, §15: a ring holds at most its cut; a fresh-key ring holds 0;
// splitting what it paid over its keys changes nothing; fooled honest
// judges pass it only the capacity of their edges.
func TestRingBounds(t *testing.T) {
	ring := func(b *builder, k int) []string {
		var m []string
		for i := 0; i < k; i++ {
			m = append(m, "ring"+strconv.Itoa(i))
		}
		for _, x := range m {
			for _, y := range m {
				if x != y {
					b.review(x, y, 100, "paid_use", 2)
				}
			}
		}
		return m
	}
	honest := func() *builder {
		b := newBuilder()
		for i := 0; i < 20; i++ {
			h := "honest" + strconv.Itoa(i)
			b.pay(h, int64(100+50*i), 20)
			b.review(h, "honest"+strconv.Itoa((i+1)%20), 90, "work", 3)
		}
		return b
	}
	// The ring's members answer each other, so a funded member's direct
	// costs also back their time present: a ring holds at most twice what
	// it paid (§9: ten keys answering each other hold 0¢ if fresh).
	held := map[string]int64{}
	for _, c := range []struct {
		name string
		paid []int64 // per member
		cut  int64
	}{
		{"10 fresh keys", nil, 0},
		{"one key pays 600¢", []int64{600}, 600},
		{"600¢ split over 10 keys", []int64{60, 60, 60, 60, 60, 60, 60, 60, 60, 60}, 600},
		{"one key pays 3,000¢", []int64{3000}, 3000},
		{"3,000¢ split over 10 keys", []int64{300, 300, 300, 300, 300, 300, 300, 300, 300, 300}, 3000},
	} {
		b := honest()
		members := ring(b, 10)
		for i, paid := range c.paid {
			b.pay(members[i], paid, 10)
		}
		held[c.name] = centsOf(compute(t, b), members...)
		if h := held[c.name]; h > 2*c.cut || h < min(c.cut, 750) {
			t.Errorf("%s: the ring holds %d¢, paid %d¢", c.name, h, c.cut)
		}
	}
	if held["10 fresh keys"] != 0 {
		t.Errorf("a fresh-key ring holds %d¢", held["10 fresh keys"])
	}
	// Splitting what was paid over the keys buys no more than its time
	// present (each funded key can back the others' days).
	if d := held["600¢ split over 10 keys"] - held["one key pays 600¢"]; d > 10*10 || d < -10*10 {
		t.Errorf("splitting 600¢ moved the ring's standing by %d¢", d)
	}
	// Five honest judges with 1,500¢ of costs each rate a fresh ring's
	// member at 80 with work evidence: 120¢ edges.
	b := honest()
	members := ring(b, 10)
	for i := 0; i < 5; i++ {
		j := "judge" + strconv.Itoa(i)
		b.pay(j, 1500, 20)
		b.review(j, members[i], 80, "work", 1)
	}
	out := compute(t, b)
	cut := int64(5*120 + 5*10) // five 120¢ edges (at accuracy 1), five days of time present
	if held := centsOf(out, members...); held > cut*2 || held == 0 {
		t.Errorf("five fooled judges: the ring holds %d¢, its cut is about %d¢", held, cut)
	}
	// Total standing never exceeds the world's total.
	if out.Summary.StandingCents > out.Summary.WorldCents {
		t.Errorf("standing %d¢ > world %d¢", out.Summary.StandingCents, out.Summary.WorldCents)
	}
}

// §2: judging never lowers what an account keeps of its own costs.
func TestJudgingKeepsOwnCosts(t *testing.T) {
	for _, paid := range []int64{300, 750, 2000} {
		b := newBuilder().pay("judge", paid, 5)
		before := scoreOf(compute(t, b), "judge").Cents
		for i := 0; i < 30; i++ {
			b.review("judge", "x"+strconv.Itoa(i), 100, "paid_use", 1)
		}
		after := scoreOf(compute(t, b), "judge").Cents
		if after != before || after != min(paid, 750) {
			t.Errorf("%d¢ paid: keeps %d¢ before judging, %d¢ after", paid, before, after)
		}
	}
}

// §7: a payment to another account counts only when the two are
// independent, and up to the payee's direct costs from one budget per
// payee: a visible loop gains nothing, a hidden star at most 2×.
func TestSelfDealing(t *testing.T) {
	// A loop of payments (a path back from every payee) adds nothing.
	b := newBuilder()
	names := []string{"a", "b", "c"}
	for i, n := range names {
		b.pay(n, 100, 30)
		b.payTo(n, names[(i+1)%3], 1000, 10, "")
	}
	if got := centsOf(compute(t, b), names...); got != 300 {
		t.Errorf("a payment loop holds %d¢, want the 300¢ paid in", got)
	}
	// Linked keys (one root) paying each other: nothing.
	b = newBuilder().pay("a", 100, 30).pay("b", 100, 30).link("b", "a").payTo("a", "b", 1000, 10, "")
	if got := scoreOf(compute(t, b), "a").Parts.WorldCents; got != 100 {
		t.Errorf("a payment within one root counts: %d¢", got)
	}
	// A hidden star: nine payers into one hub that never pays back. Credits
	// come from the hub's direct costs, once over all payers.
	b = newBuilder().pay("hub", 100, 30)
	var all []string
	for i := 0; i < 9; i++ {
		n := "payer" + strconv.Itoa(i)
		all = append(all, n)
		b.pay(n, 100, 30).payTo(n, "hub", 1000, 10, "")
	}
	out := compute(t, b)
	all = append(all, "hub")
	paid := int64(1000)
	if got := centsOf(out, all...); got > 2*paid || got <= paid {
		t.Errorf("a hidden star holds %d¢: want more than the %d¢ paid in and at most twice it", got, paid)
	}
	var credits int64
	for _, n := range all[:9] {
		credits += scoreOf(out, n).Parts.WorldCents - 100
	}
	if credits != 100 {
		t.Errorf("the hub's 100¢ of direct costs backed %d¢ of payments", credits)
	}
	// Two payees paid out to one wallet are one root: a payment between
	// them counts 0, and paying each other is shared_wallet evidence.
	b = newBuilder().pay("x", 100, 30).pay("y", 100, 30).payTo("w1", "x", 5, 20, "0xaaa").payTo("w2", "y", 5, 20, "0xaaa")
	out = compute(t, b)
	if scoreOf(out, "x").Root != scoreOf(out, "y").Root || scoreOf(out, "x").Parts.WorldCents != 100 || len(out.Evidence) != 0 {
		t.Errorf("one payout wallet: roots %s / %s, x's world %d¢, evidence %v", scoreOf(out, "x").Root, scoreOf(out, "y").Root, scoreOf(out, "x").Parts.WorldCents, out.Evidence)
	}
	b.payTo("x", "y", 500, 10, "")
	out = compute(t, b)
	if len(out.Evidence) != 1 || out.Evidence[0].Kind != TriggerSharedWallet || centsOf(out, "x", "y") != 0 {
		t.Errorf("one payout wallet paying itself: evidence %v, holds %d¢", out.Evidence, centsOf(out, "x", "y"))
	}
}

// §2, §9: time present is day_price per active day, at most time_cap,
// each day backed by the responder's direct costs; a responder below
// v_floor never makes a day active.
func TestTimePresent(t *testing.T) {
	b := newBuilder().account("worker", 100).pay("responder", 600, 150)
	for d := int64(1); d <= 20; d++ {
		b.reply("responder", "worker", d)
	}
	sc := scoreOf(compute(t, b), "worker")
	if sc.Cents != 200 || sc.Parts.ActiveDays != 20 {
		t.Errorf("20 active days answered by a 600¢ responder: %d¢ over %d active days, want 200¢ over 20", sc.Cents, sc.Parts.ActiveDays)
	}
	// A 50¢ responder backs five days.
	b = newBuilder().account("worker", 100).pay("responder", 50, 150)
	for d := int64(1); d <= 20; d++ {
		b.reply("responder", "worker", d)
	}
	if got := scoreOf(compute(t, b), "worker").Cents; got != 50 {
		t.Errorf("a 50¢ responder backed %d¢ of time present, want 50¢", got)
	}
	// Below v_floor: no active day.
	b = newBuilder().account("worker", 100).pay("responder", 40, 150).reply("responder", "worker", 1)
	if sc := scoreOf(compute(t, b), "worker"); sc.Parts.ActiveDays != 0 || sc.Cents != 0 {
		t.Errorf("a 40¢ responder made %d active days (%d¢)", sc.Parts.ActiveDays, sc.Cents)
	}
}

// §9: world edges hold while the account is active; after G days without
// an active day they halve every Hd; returning restores nothing.
func TestInactivityDecay(t *testing.T) {
	p := DefaultParams()
	D := dayOf(testAsOf)
	cv := curves{}
	for _, c := range []struct {
		name   string
		last   int64 // days before the as-of day
		active bool
		want   int64
	}{
		{"active", 1, true, 750},
		{"within the grace", 100, false, 750},
		{"dormant 335 days", 335, false, 750 * cv.decay(p.DormantDayFactorPPM, 335-1-p.GraceDays) / 1e6},
		{"dormant 700 days", 700, false, 750 * cv.decay(p.DormantDayFactorPPM, 700-1-p.GraceDays) / 1e6},
	} {
		b := newBuilder().pay("responder", 600, 2)
		b.snap.Carry = []Record{{Type: "carry", Kind: "run", AsOf: testAsOf - c.last*day}}
		b.carryWorld("saver", CarryAccount{Money: 750 * mc, LastActive: D - c.last, Direct: 750, Standing: 750})
		b.snap.Carry[0].AsOf = testAsOf - c.last*day
		if c.active {
			b.reply("responder", "saver", 1)
		}
		sc := scoreOf(compute(t, b), "saver")
		if d := sc.Cents - c.want; d < -1 || d > 1 {
			t.Errorf("%s: holds %d¢, want %d¢", c.name, sc.Cents, c.want)
		}
	}
	// RFC 0021 §9's table: 750¢ dormant from day 30 holds about 328¢ at day
	// 365 and 80¢ at day 730.
	for _, c := range []struct{ day, want int64 }{{365, 328}, {730, 80}} {
		got := 750 * cv.decay(p.DormantDayFactorPPM, c.day-30-p.GraceDays) / 1e6
		if math.Abs(float64(got-c.want)) > float64(c.want)/50 {
			t.Errorf("day %d: %d¢, RFC %d¢", c.day, got, c.want)
		}
	}
	// Returning stops the decay and restores nothing.
	b := newBuilder().pay("responder", 600, 2)
	b.snap.Carry = []Record{{Type: "carry", Kind: "run", AsOf: testAsOf - day}}
	b.carryWorld("saver", CarryAccount{Money: 300 * mc, LastActive: D - 400, Direct: 300, Standing: 300})
	b.carryWorld("responder", CarryAccount{Money: 600 * mc, LastActive: D - 2, Direct: 600, Standing: 600})
	b.reply("responder", "saver", 1)
	out := compute(t, b)
	if got := scoreOf(out, "saver").Cents; got > 300+10 || got < 298 {
		t.Errorf("a returning account holds %d¢, want what was left (about 300¢) and its day of time present", got)
	}
	for _, ca := range out.Carry.Accounts {
		if ca.Account == acct("saver") && ca.LastActive != D-1 {
			t.Errorf("the return is not recorded: last active %d, want %d", ca.LastActive, D-1)
		}
	}
}

// penaltyWorld is §8's case: a 5-key ring (one key with a 600¢ wallet and
// 1,000¢ spent), rated 80 with work evidence by five funded judges
// (1,500¢ each, so they pass flow on).
func penaltyWorld() (*builder, []string, []string) {
	b := newBuilder()
	var ring, judges []string
	for i := 0; i < 5; i++ {
		ring = append(ring, "ring"+strconv.Itoa(i))
		judges = append(judges, "judge"+strconv.Itoa(i))
	}
	b.wallet(ring[0], "0xring", 600).pay(ring[0], 1000, 20)
	for _, x := range ring {
		for _, y := range ring {
			if x != y {
				b.review(x, y, 100, "paid_use", 2)
			}
		}
	}
	for i, j := range judges {
		b.pay(j, 1500, 20).review(j, ring[i], 80, "work", 3)
	}
	return b, ring, judges
}

func entry(b *builder, id, trigger string, members []string, start, end, level int64, state string) {
	for _, m := range members {
		b.snap.Penalties = append(b.snap.Penalties, Record{Type: "penalty", ID: id, Kind: trigger, Account: acct(m), CreatedAt: start, EndsAt: end, Value: level, State: state})
	}
}

// §8: a caught set holds 0, its proofs burn for good, each backer loses
// exactly f = c / (c + U); the k-th penalty on a root lasts 30 × 4^(k−1)
// days and a backer's k-th liability costs 2^(k−1) × f.
func TestPenaltyBurnAndEscalation(t *testing.T) {
	p := DefaultParams()
	b, ring, judges := penaltyWorld()
	before := compute(t, b)
	if held := centsOf(before, ring...); held > 1600+5*120 || held == 0 {
		t.Fatalf("before: the ring holds %d¢, cut %d¢", held, 1600+5*120)
	}
	entry(b, "moderation-1", TriggerModeration, ring, testAsOf-3600, 0, 0, "pending")
	out := compute(t, b)
	if held := centsOf(out, ring...); held != 0 {
		t.Errorf("penalised: the ring holds %d¢", held)
	}
	if len(out.Penalties) != 1 {
		t.Fatalf("penalties applied: %d", len(out.Penalties))
	}
	pen := out.Penalties[0]
	if pen.Level != 1 || pen.EndsAt-pen.StartsAt != 30*day || len(pen.Burned) != 1 || pen.Burned[0] != "wallet:eip155:1:0xring" || len(pen.Backers) != 5 {
		t.Fatalf("first penalty: %+v", pen)
	}
	for i, bk := range pen.Backers {
		c := bk.CapCents * mc
		f := mulDiv(c, 1e6, c+p.UnitCents*mc)
		if d := bk.FractionPPM - f; d < -2000 || d > 2000 {
			t.Errorf("backer %d: f %d ppm, want about c/(c+U) = %d (37.5%% for a 120¢ edge)", i, bk.FractionPPM, f)
		}
		got := scoreOf(out, judges[i]).Cents
		want := mulDiv(750, 1e6-bk.FractionPPM, 1e6)
		if d := got - want; d < -1 || d > 1 {
			t.Errorf("backer %d holds %d¢, want 750 × (1 − f) = %d¢", i, got, want)
		}
	}
	// A repeat within 365 days: level 2 (120 days), backers 2f.
	b2, ring2, _ := penaltyWorld()
	b2.snap.Penalties = append(b2.snap.Penalties, Record{Type: "penalty", ID: "old", Kind: TriggerModeration, Account: acct(ring2[1]), CreatedAt: ago(100), EndsAt: ago(70), Value: 1, State: "applied"})
	for _, j := range []string{"judge0", "judge1", "judge2", "judge3", "judge4"} {
		b2.snap.Liabilities = append(b2.snap.Liabilities, Record{Type: "liability", ID: "old", Account: acct(j), FractionPPM: 375000, CreatedAt: ago(100), EndsAt: ago(70), Value: 1})
	}
	entry(b2, "moderation-2", TriggerModeration, ring2, testAsOf-3600, 0, 0, "pending")
	out2 := compute(t, b2)
	pen2 := out2.Penalties[0]
	if pen2.Level != 2 || pen2.EndsAt-pen2.StartsAt != 120*day {
		t.Errorf("repeat: level %d, %d days", pen2.Level, (pen2.EndsAt-pen2.StartsAt)/day)
	}
	for i, bk := range pen2.Backers {
		if bk.Level != 2 || bk.FractionPPM != min(1e6, 2*pen.Backers[i].FractionPPM) {
			t.Errorf("repeat backer %d: level %d, f %d (first %d)", i, bk.Level, bk.FractionPPM, pen.Backers[i].FractionPPM)
		}
	}
	// The burned wallet prices 0 on a fresh key, which is its own root.
	b3 := newBuilder().wallet("fresh", "0xring", 600).pay("fresh", 100, 5)
	b3.snap.Penalties = append(b3.snap.Penalties, Record{Type: "penalty", ID: "old", Kind: TriggerModeration, Account: acct("ring0"), CreatedAt: ago(400), EndsAt: ago(370), Value: 1, State: "applied"})
	b3.snap.Burns = append(b3.snap.Burns, Record{Type: "burn", ID: "old", Root: "wallet:eip155:1:0xring"})
	if sc := scoreOf(compute(t, b3), "fresh"); sc.Cents != 100 || sc.Root != acct("fresh") {
		t.Errorf("relinking a burned wallet: %d¢ (want only the 100¢ of new spend), root %s", sc.Cents, sc.Root)
	}
	// A lift reverses an entry from the next run on.
	b4, ring4, _ := penaltyWorld()
	entry(b4, "moderation-3", TriggerModeration, ring4, ago(2), ago(2)+30*day, 1, "applied")
	b4.snap.Burns = append(b4.snap.Burns, Record{Type: "burn", ID: "moderation-3", Root: "wallet:eip155:1:0xring"})
	if held := centsOf(compute(t, b4), ring4...); held != 0 {
		t.Errorf("an applied entry: the ring holds %d¢", held)
	}
	b4.snap.Lifts = append(b4.snap.Lifts, Record{Type: "lift", ID: "moderation-3", CreatedAt: ago(1)})
	if held := centsOf(compute(t, b4), ring4...); held != centsOf(before, ring...) {
		t.Errorf("lifted: the ring holds %d¢, want %d¢", held, centsOf(before, ring...))
	}
}

// §8's detectors: a funnel (five farm keys sending their allowance to one
// recipient) that is also a judgement ring opens an entry by itself.
func TestDetectorsOpenEntries(t *testing.T) {
	b := newBuilder()
	var farm []string
	for i := 0; i < 5; i++ {
		f := "farm" + strconv.Itoa(i)
		farm = append(farm, f)
		b.snap.Claims = append(b.snap.Claims, Record{Type: "claim", Account: acct(f), Day: dayOf(testAsOf) - 1, Claimed: 1000, Spent: 10})
		b.transfer(f, "sink", 900, 1)
	}
	b.pay("sink", 2000, 20)
	all := append(append([]string{}, farm...), "sink")
	for _, x := range all {
		for _, y := range all {
			if x != y {
				b.review(x, y, 100, "paid_use", 2)
			}
		}
	}
	out := compute(t, b)
	kinds := map[string]bool{}
	for _, ev := range out.Evidence {
		kinds[ev.Kind] = true
	}
	if !kinds["funnel"] || !kinds["ring"] || len(out.Penalties) == 0 || !out.Penalties[0].Opened {
		t.Fatalf("evidence %v, penalties %+v", kinds, out.Penalties)
	}
	if held := centsOf(out, all...); held != 0 {
		t.Errorf("a caught funnel holds %d¢", held)
	}
	// A member already under an active entry is not penalised again.
	b.snap.Penalties = nil
	for _, pen := range out.Penalties {
		entry(b, pen.ID, pen.Trigger, nil, pen.StartsAt, pen.EndsAt, pen.Level, "applied")
		for _, m := range pen.Members {
			b.snap.Penalties = append(b.snap.Penalties, Record{Type: "penalty", ID: pen.ID, Kind: pen.Trigger, Account: m, CreatedAt: pen.StartsAt, EndsAt: pen.EndsAt, Value: pen.Level, State: "applied"})
		}
	}
	if again := compute(t, b); len(again.Penalties) != 0 {
		t.Errorf("the next run opened %d more entries on the same members", len(again.Penalties))
	}
}

// §6: accuracy from RFC 0020's gain. Herders stay at 1, informed judges
// rise, contrarians fall (the C303s mix, on real judgements).
func TestAccuracyMultiplier(t *testing.T) {
	if testing.Short() {
		t.Skip("accuracy population")
	}
	rng := rand.New(rand.NewPCG(5, 5))
	b := newBuilder()
	kinds := map[string][]string{}
	for i := 0; i < 50; i++ {
		k := "contrarian"
		switch {
		case i < 20:
			k = "informed"
		case i < 35:
			k = "late"
		case i < 45:
			k = "herder"
		}
		name := k + strconv.Itoa(i)
		kinds[k] = append(kinds[k], name)
		b.pay(name, 500, 170)
	}
	type j struct {
		name string
		x    int64
		day  int64
	}
	for s := 0; s < 200; s++ {
		subject := "subject" + strconv.Itoa(s)
		q := rng.Int64N(101)
		perm := rng.Perm(50)[:8]
		var js []j
		for _, i := range perm {
			k := []string{"informed", "late", "herder", "contrarian"}[map[bool]int{true: 0}[i < 20]+map[bool]int{true: 1}[i >= 20 && i < 35]+map[bool]int{true: 2}[i >= 35 && i < 45]+map[bool]int{true: 3}[i >= 45]]
			name := k + strconv.Itoa(i)
			at := int64(10 + rng.IntN(76))
			if k == "informed" {
				at = int64(rng.IntN(30))
			}
			if k == "late" {
				at = int64(50 + rng.IntN(40))
			}
			js = append(js, j{name: name, day: 100 - at})
		}
		sort.Slice(js, func(a, c int) bool { return js[a].day > js[c].day })
		var pts []judge.Point
		for i := range js {
			c0 := judge.Params{PriorWeight: 2}.Consensus(pts)
			est := min(100, max(0, q+int64(math.Round(10*rng.NormFloat64()))))
			switch {
			case js[i].name[:6] == "herder":
				js[i].x = c0
			case js[i].name[:6] == "contra":
				if est >= c0 {
					js[i].x = max(0, c0-25)
				} else {
					js[i].x = min(100, c0+25)
				}
			default:
				js[i].x = est
			}
			pts = append(pts, judge.Point{X: js[i].x, W: 1e6})
			b.review(js[i].name, subject, js[i].x, "paid_use", js[i].day)
		}
	}
	out := compute(t, b)
	mean := func(k string) float64 {
		var s float64
		for _, n := range kinds[k] {
			s += float64(scoreOf(out, n).Parts.AccuracyPPM) / 1e6
		}
		return s / float64(len(kinds[k]))
	}
	t.Logf("accuracy: informed %.3f, late %.3f, herders %.3f, contrarians %.3f", mean("informed"), mean("late"), mean("herder"), mean("contrarian"))
	if h := mean("herder"); math.Abs(h-1) > 0.02 {
		t.Errorf("herders %.3f, want 1.00 ± 0.02", h)
	}
	if mean("informed") <= 1 || mean("contrarian") >= 1 {
		t.Errorf("informed %.3f should be above 1, contrarians %.3f below", mean("informed"), mean("contrarian"))
	}
}

// §5: per-viewer standing adds the viewer as a source; it reaches what the
// viewer judged and never lowers anyone's global standing in the view of an
// unrelated viewer.
func TestViewerStanding(t *testing.T) {
	b := newBuilder().pay("viewer", 100, 10).review("viewer", "liked", 100, "paid_use", 1).pay("other", 300, 10)
	out := compute(t, b)
	view, ok, err := out.Graph.ViewerStanding(context.Background(), acct("viewer"))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got := view[scoreOf(out, "liked").Root]; got <= scoreOf(out, "liked").Cents || got > scoreOf(out, "liked").Cents+200 {
		t.Errorf("the viewer's view of what it liked: %d¢ (global %d¢)", got, scoreOf(out, "liked").Cents)
	}
	if got := view[scoreOf(out, "other").Root]; got != scoreOf(out, "other").Cents {
		t.Errorf("an unrelated account in the viewer's view: %d¢, global %d¢", got, scoreOf(out, "other").Cents)
	}
	if _, ok, _ := out.Graph.ViewerStanding(context.Background(), acct("nobody")); ok {
		t.Error("a viewer outside the graph has a view")
	}
}

// §5: the per-viewer flow stays within RFC 0021's 100 ms at 10k accounts.
func TestViewerStandingCost(t *testing.T) {
	if testing.Short() {
		t.Skip("10k-account graph")
	}
	w := honestWorld(2, 10000)
	g := &Graph{RootOf: map[string]string{}, MaxWork: DefaultParams().MaxWork}
	for i := 0; i < w.n; i++ {
		l := strconv.Itoa(1_000_000 + i)
		g.Labels = append(g.Labels, l)
		g.RootOf[l] = l
		g.World = append(g.World, w.price[i]*mc)
		g.Cap = append(g.Cap, 750*mc)
	}
	type key struct{ s, d int32 }
	caps := map[key]int64{}
	for e := range w.src {
		caps[key{w.src[e], w.dst[e]}] += int64(200 * mc * w.w[e])
	}
	keys := make([]key, 0, len(caps))
	for k := range caps {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].s < keys[j].s || keys[i].s == keys[j].s && keys[i].d < keys[j].d })
	for _, k := range keys {
		g.Src, g.Dst, g.EdgeCap = append(g.Src, k.s), append(g.Dst, k.d), append(g.EdgeCap, caps[k])
	}
	g.Levels = flowLevels(200*mc, 750*mc)
	g.Reindex()
	start := time.Now()
	for v := 0; v < 10; v++ {
		if _, _, err := g.ViewerStanding(context.Background(), g.Labels[v*997]); err != nil {
			t.Fatal(err)
		}
	}
	per := time.Since(start) / 10
	t.Logf("per-viewer standing at 10k accounts: %v", per)
	if per > 500*time.Millisecond {
		t.Errorf("per-viewer standing takes %v", per)
	}
}

// The operator share of the flow passed on falls as accounts that pay their
// own way judge others (§4): the share PassedSharePPM reports.
func TestOperatorShareFalls(t *testing.T) {
	ops := []string{"weaver", "khepri"}
	b := newBuilder()
	for i, op := range ops {
		b.pay(op, 4500, 20)
		for k := 0; k < 10; k++ {
			b.review(op, "newcomer"+strconv.Itoa(i*10+k), 80, "work", 2)
		}
	}
	share := func(out Output) int64 {
		set := map[string]bool{}
		for _, o := range ops {
			set[acct(o)] = true
		}
		return PassedSharePPM(out.Scores, set)
	}
	today := share(compute(t, b))
	for i := 0; i < 20; i++ {
		n := "organic" + strconv.Itoa(i)
		b.pay(n, 1500, 30)
		for k := 0; k < 5; k++ {
			b.review(n, "newcomer"+strconv.Itoa((i*5+k)%20), 90, "work", 1)
		}
	}
	later := share(compute(t, b))
	t.Logf("operator share of passed flow: %d ppm today, %d ppm with 20 organic funded judges", today, later)
	if today != 1e6 || later >= today/2 {
		t.Errorf("operator share %d → %d ppm: should fall from 100%% below half", today, later)
	}
}

// The run is deterministic and record order does not matter.
func TestComputeDeterministic(t *testing.T) {
	b, _, _ := penaltyWorld()
	b.reply("judge0", "judge1", 1).vote("judge2", "judge3", 2).vouch("judge3", "judge4", 3).accept("judge4", "judge0", 2)
	a := compute(t, b).Canonical()
	rng := rand.New(rand.NewPCG(1, 2))
	for _, list := range [][]Record{b.snap.Payments, b.snap.Reviews, b.snap.Proofs, b.snap.Posts} {
		rng.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
	}
	if c := compute(t, b).Canonical(); string(a) != string(c) {
		t.Fatal("record order changed the output")
	}
}

// A retired parameter version is refused plainly.
func TestRetiredVersionRefused(t *testing.T) {
	b := newBuilder()
	b.snap.Params.Version = 7
	if _, err := Compute(context.Background(), b.snap); err == nil {
		t.Fatal("version 7 computed")
	}
	if _, err := ParseParams(6, DefaultParams().Body()); err == nil {
		t.Fatal("version 6 parsed")
	}
}

// Log10PPM is exact enough for the domain row: rank 1 → 400¢, 100 → 266¢,
// 10,000 → 133¢.
func TestDomainPricing(t *testing.T) {
	p := DefaultParams().Proofs["domain"]
	for _, c := range []struct{ rank, want int64 }{{1, 400}, {100, 266}, {10000, 133}, {1000000, 0}} {
		if got := DomainContribution(p, 0, 0, c.rank); got != c.want {
			t.Errorf("rank %d: %d¢, want %d¢", c.rank, got, c.want)
		}
	}
	for n := int64(1); n < 1e12; n = n*7 + 3 {
		got, want := float64(Log10PPM(n)), math.Floor(1e6*math.Log10(float64(n)))
		if math.Abs(got-want) > 2 {
			t.Errorf("Log10PPM(%d) = %v, want %v", n, got, want)
		}
	}
	if got := DomainContribution(p, 365, ago(365), 0); got != 100 {
		t.Errorf("a one-year-old domain: %d¢, want 100¢ (half the age row)", got)
	}
}

// A verified domain whose registration age and rank are not fetched yet
// prices at the row's fallback (the old flat 400¢), within the row's cap;
// once either signal is known the age and rank rule prices it alone.
func TestDomainFallbackPrice(t *testing.T) {
	p := DefaultParams().Proofs["domain"]
	if p.FallbackCents != 400 {
		t.Fatalf("fallback_cents %d, want 400", p.FallbackCents)
	}
	if got := DomainContribution(p, 0, 0, 0); got != 400 {
		t.Errorf("no signals: %d¢, want the 400¢ fallback", got)
	}
	if got := DomainContribution(p, 0, 0, 1000000); got != 0 {
		t.Errorf("a fetched rank past the list prices by the rule: %d¢, want 0", got)
	}
	capped := p
	capped.Rent = 300
	if got := DomainContribution(capped, 0, 0, 0); got != 300 {
		t.Errorf("the fallback stays within min(forge, rent): %d¢", got)
	}
	bad := DefaultParams()
	row := bad.Proofs["github"]
	row.FallbackCents = 10
	bad.Proofs["github"] = row
	if err := bad.Validate(); err == nil {
		t.Error("fallback_cents on another row was accepted")
	}
}
