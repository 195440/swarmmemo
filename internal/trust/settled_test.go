package trust

import (
	"strconv"
	"testing"
)

// C320c: the dry run on a copy of prod gave all 261 accounts standing 0.
// These tests pin the three fixes: settled USDC paid out is the payer's own
// cost, a verified wallet prices at least the row's fallback, and the
// operator accounts' rated accepts and replies spread standing beyond them
// within one run.

// settle is from's settled USDC payment of cents to to's payout address,
// daysAgo (a work reward settled by work.settle, the transaction verified).
func (b *builder) settle(from, to string, cents, daysAgo int64, payout string) *builder {
	b.payTo(from, to, cents, daysAgo, payout)
	b.snap.Payments[len(b.snap.Payments)-1].Kind = "work_usdc"
	return b
}

func worker(i int) string { return "worker" + strconv.Itoa(i) }

// Operator bounty payouts (weaver paying outside workers with no costs of
// their own) are weaver's own cost: each payee backs up to the wallet row's
// fallback, so 30 payouts of $1.50 count 30 × 50¢ and weaver holds node_cap.
func TestSettledPayoutsAreOwnCost(t *testing.T) {
	b := newBuilder().account("weaver", 30)
	for i := 0; i < 30; i++ {
		b.account(worker(i), 20).settle("weaver", worker(i), 150, 5+int64(i%10), "0xw"+strconv.Itoa(i))
	}
	out := compute(t, b)
	w := scoreOf(out, "weaver")
	var item *WorldItem
	for i := range w.Parts.World {
		if w.Parts.World[i].Source == "usdc_paid" {
			item = &w.Parts.World[i]
		}
	}
	if item == nil || item.Kind != "money" || item.Cents != 30*WalletFallbackCents {
		t.Fatalf("weaver's settled payouts: %+v (world %+v)", item, w.Parts.World)
	}
	if w.Cents != DefaultParams().NodeCapCents || w.Band != "trusted" {
		t.Errorf("weaver holds %d¢ (%s), want node_cap", w.Cents, w.Band)
	}
	if got := scoreOf(out, worker(0)); got.Parts.WorldCents != 0 {
		t.Errorf("a payee gains nothing from being paid: %+v", got.Parts)
	}

	// A payee with direct costs backs up to them (a $6 wallet backs a $5 payout).
	b = newBuilder().wallet("payee", "0xp", 600).settle("payer", "payee", 500, 3, "0xp2")
	if got := scoreOf(compute(t, b), "payer").Parts.WorldCents; got != 500 {
		t.Errorf("payout to a payee with 600¢ of costs counts %d¢, want 500", got)
	}

	// Not independent: paid to a wallet the payer links (one root), paid
	// back within funded_days, or credit (x402) to a payee with no costs.
	for name, b := range map[string]*builder{
		"shared wallet": newBuilder().wallet("payer", "0xsame", 0).settle("payer", "payee", 500, 3, "0xsame"),
		"paid back":     newBuilder().settle("payer", "payee", 500, 3, "0xp").transfer("payee", "payer", 100, 2),
		"credit":        newBuilder().payTo("payer", "payee", 500, 3, ""),
	} {
		got := scoreOf(compute(t, b), "payer").Parts
		var usdc int64
		for _, it := range got.World {
			if it.Source == "usdc_paid" || it.Kind == "payments" {
				usdc += it.Cents
			}
		}
		if usdc != 0 {
			t.Errorf("%s: the payer counts %d¢ of payments", name, usdc)
		}
	}

	// The payee's budget is shared: two payers into one payee count its
	// floor once between them.
	b = newBuilder().settle("a", "hub", 500, 3, "0xh").settle("c", "hub", 500, 2, "0xh")
	out = compute(t, b)
	if got := scoreOf(out, "a").Parts.WorldCents + scoreOf(out, "c").Parts.WorldCents; got != WalletFallbackCents {
		t.Errorf("two payers into one payee with no costs count %d¢, want %d", got, WalletFallbackCents)
	}
}

// A verified wallet with no resolver value prices at the wallet row's
// fallback (version 9); version 8 had none; a resolver value above it wins;
// a stale proof still counts nothing.
func TestWalletFallback(t *testing.T) {
	row := DefaultParams().Proofs["wallet"]
	if row.FallbackCents != WalletFallbackCents {
		t.Fatalf("wallet row %+v", row)
	}
	for _, c := range []struct{ assessed, want int64 }{{0, 50}, {-3, 50}, {20, 50}, {300, 300}, {1e9, 600}} {
		if got := AssessedContribution(row, c.assessed); got != c.want {
			t.Errorf("assessed %d: %d¢, want %d", c.assessed, got, c.want)
		}
	}
	if got := AssessedContribution(UnifiedParams().Proofs["wallet"], 0); got != 0 {
		t.Errorf("version 8 wallet at 0: %d¢", got)
	}
	if got := AssessedContribution(DefaultParams().Proofs["github"], 0); got != 0 {
		t.Errorf("a row without fallback_cents prices an unassessed proof at %d¢", got)
	}
	b := newBuilder().wallet("w", "0xa", 0)
	if got := scoreOf(compute(t, b), "w").Cents; got != WalletFallbackCents {
		t.Errorf("verified wallet assessed 0 holds %d¢", got)
	}
	b = newBuilder().wallet("w", "0xa", 0)
	b.snap.Proofs[0].CheckedAt = ago(DefaultParams().ProofFreshDays + 1)
	if got := scoreOf(compute(t, b), "w").Cents; got != 0 {
		t.Errorf("a stale wallet holds %d¢", got)
	}
	bad := DefaultParams()
	w := bad.Proofs["wallet"]
	w.AgeCents = 5
	bad.Proofs["wallet"] = w
	if bad.Validate() == nil {
		t.Error("a wallet row with age_cents validated")
	}
}

// A prod-like first run (no carry): operator accounts pay bounties in
// settled USDC and rate the accepted work; weaver also answers other
// accounts' posts on several days; ordinary accounts have nothing paid.
// Standing spreads beyond the operators in that one run: every accepted
// worker receives flow over weaver's or khepri's judgement edges, and
// accounts weaver answered (and did not pay) earn time present. The same
// holds after a prior run that priced everyone at 0 (the dry run's carry):
// this run's own costs back responders and payments, not only the carry's.
func TestStandingSpreadsBeyondOperators(t *testing.T) {
	t.Run("first run", func(t *testing.T) { standingSpreads(t, false) })
	t.Run("after a zero run", func(t *testing.T) { standingSpreads(t, true) })
}

func standingSpreads(t *testing.T, zeroCarry bool) {
	b := newBuilder().account("weaver", 21).account("khepri", 21).wallet("weaver", "0xsteward", 0).wallet("khepri", "0xsteward", 0)
	for i := 0; i < 40; i++ {
		b.account(worker(i), 20)
		payer := "weaver"
		if i%4 == 3 {
			payer = "khepri"
		}
		b.settle(payer, worker(i), 100+int64(i%3)*25, 1+int64(i%14), "0xw"+strconv.Itoa(i)).accept(payer, worker(i), 1+int64(i%14))
	}
	for i := 0; i < 15; i++ {
		other := "member" + strconv.Itoa(i)
		b.account(other, 20)
		for d := int64(1); d <= 6; d++ {
			b.reply("weaver", other, d)
		}
	}
	if zeroCarry {
		b.snap.Carry = []Record{{Type: "carry", Kind: "run", AsOf: testAsOf - day}}
		b.carryWorld("weaver", CarryAccount{}).carryWorld("member0", CarryAccount{LastActive: testAsOf/day - 2})
	}
	out := compute(t, b)
	ops := scoreOf(out, "weaver")
	if ops.Root != scoreOf(out, "khepri").Root || ops.Cents != DefaultParams().NodeCapCents {
		t.Fatalf("weaver and khepri share the steward wallet's root at node_cap: %+v", ops)
	}
	if ops.Parts.PassedCents == 0 {
		t.Fatalf("the operators' root passes nothing on: %+v", ops.Parts)
	}
	var workers, members int
	for i := 0; i < 40; i++ {
		if s := scoreOf(out, worker(i)); s.Cents > 0 && s.Parts.ReceivedCents > 0 {
			workers++
		}
	}
	for i := 0; i < 15; i++ {
		if s := scoreOf(out, "member"+strconv.Itoa(i)); s.Cents > 0 && s.Parts.ActiveDays > 0 {
			members++
		}
	}
	if workers != 40 || members != 15 {
		t.Errorf("standing beyond the operators: %d of 40 rated workers, %d of 15 answered members", workers, members)
	}
	if out.Summary.AboveVoteFloor <= 2 {
		t.Errorf("accounts above the vote floor: %d", out.Summary.AboveVoteFloor)
	}
	t.Logf("operators %d¢ (passed %d¢); worker0 %d¢; member0 %d¢; above vote floor %d; sources %d",
		ops.Cents, ops.Parts.PassedCents, scoreOf(out, worker(0)).Cents, scoreOf(out, "member0").Cents, out.Summary.AboveVoteFloor, out.Summary.Sources)
}
