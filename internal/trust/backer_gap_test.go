package trust

import (
	"strconv"
	"testing"
)

// C334, the reported gap, and C336's fix. A worker with accepted, rated work
// (3 accepts, 2 rated accepts at 85, work evidence) from a 70¢ requester got
// nothing over judgement edges under version 9: a root kept min(world,
// node_cap) and only what it held above node_cap (750¢) flowed on, so a 70¢
// backer passed 0 however it rated. Version 10 (pass_share_ppm, RFC 0021 §2) has a
// root offer its edges at least a quarter of what it keeps: the 70¢ backer
// passes up to 17¢, shared max-min fairly with every account it rated,
// and keeps the rest. Standing is conserved: what the worker gains, the
// backer gave.
func TestLowStandingBackerPasses(t *testing.T) {
	p := DefaultParams()
	rated := func(b *builder, backer string) *builder {
		for d := int64(1); d <= 3; d++ {
			b.accept(backer, "worker", d)
		}
		return b.review(backer, "worker", 85, "work", 2).review(backer, "worker", 85, "work", 1)
	}
	share := 70 * p.PassSharePPM / 1e6 // 17¢

	// Version 9: an edge carrying 0, only time present (3 active days, 30¢).
	b := rated(newBuilder().account("worker", 30).pay("skitter", 70, 60), "skitter")
	b.snap.Params = ProofsParams()
	sc := scoreOf(compute(t, b), "worker")
	if sc.Parts.ReceivedCents != 0 || len(sc.Parts.Backers) != 1 || sc.Parts.Backers[0].CapCents <= 0 || sc.Parts.Backers[0].Cents != 0 {
		t.Fatalf("version 9: a 70¢ backer passed %d¢ over %+v, want an edge carrying 0", sc.Parts.ReceivedCents, sc.Parts.Backers)
	}
	if sc.Cents != 3*p.DayPriceCents || sc.Parts.ActiveDays != 3 {
		t.Fatalf("version 9: the worker holds %d¢ over %d active days, want %d¢ of time present", sc.Cents, sc.Parts.ActiveDays, 3*p.DayPriceCents)
	}

	// Version 10: the same edge carries the backer's pass share.
	out := compute(t, rated(newBuilder().account("worker", 30).pay("skitter", 70, 60), "skitter"))
	sc, backer := scoreOf(out, "worker"), scoreOf(out, "skitter")
	if sc.Parts.ReceivedCents < share-1 || sc.Parts.ReceivedCents > share || sc.Cents != 3*p.DayPriceCents+sc.Parts.ReceivedCents {
		t.Fatalf("a 70¢ backer passed %d¢ (worker %d¢), want its %d¢ pass share", sc.Parts.ReceivedCents, sc.Cents, share)
	}
	if backer.Cents+sc.Parts.ReceivedCents < 69 || backer.Cents > 70-share+1 || backer.Parts.PassedCents != sc.Parts.ReceivedCents {
		t.Fatalf("the backer keeps %d¢ and passed %d¢: standing is not conserved", backer.Cents, backer.Parts.PassedCents)
	}

	// The same backer rating seven other workers too: the pass share goes
	// to the eight, never more in total.
	b = newBuilder().account("worker", 30).pay("skitter", 70, 60)
	for i := 0; i < 7; i++ {
		b.accept("skitter", "other"+strconv.Itoa(i), 5)
	}
	out = compute(t, rated(b, "skitter"))
	var total int64
	for _, w := range []string{"worker", "other0", "other1", "other2", "other3", "other4", "other5", "other6"} {
		total += scoreOf(out, w).Parts.ReceivedCents
	}
	// (Max-min fairness is by level, U/64 = 3¢ the first: each of the eight
	// gets a few cents, each rounded down.)
	if got := scoreOf(out, "worker"); got.Parts.ReceivedCents <= 0 || total < share-8 || total > share || got.Parts.ActiveDays != 3 {
		t.Fatalf("a 70¢ backer of eight: worker received %d¢, all eight %d¢ (share %d¢)", got.Parts.ReceivedCents, total, share)
	}

	// A backer above node_cap passes its surplus over the same edges, and
	// keeps node_cap once the surplus is at least its pass share.
	out = compute(t, rated(newBuilder().account("worker", 30).pay("whale", 2000, 60), "whale"))
	if sc = scoreOf(out, "worker"); sc.Parts.ReceivedCents <= 0 || scoreOf(out, "whale").Cents != p.NodeCapCents {
		t.Fatalf("a 2,000¢ backer passed %d¢ and keeps %d¢", sc.Parts.ReceivedCents, scoreOf(out, "whale").Cents)
	}
}
