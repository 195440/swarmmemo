package trust

import (
	"math/rand/v2"
	"strconv"
	"testing"
)

// C336, RFC 0021 §2's bound under version 10's pass share: no money is
// created. Every cent of standing is a cent of some root's world edge (its
// paid costs): a root keeps min(world, cap) less its pass share, and what
// the flow delivers over judgement edges left another root's offer (its
// surplus above cap plus its pass share). So standing received over
// judgements equals standing passed, total standing never exceeds the
// world's, and a set of roots holds at most its own world plus what its
// outside backers offered over edges into it.

// checkConserved checks the bound on a run whose roots are single accounts.
func checkConserved(t *testing.T, name string, out Output) {
	t.Helper()
	p := DefaultParams()
	var received, passed int64
	for _, sc := range out.Scores {
		received += sc.Parts.ReceivedCents
		passed += sc.Parts.PassedCents
		keep := min(sc.Parts.RootWorldCents, p.NodeCapCents)
		share := max(0, keep*p.PassSharePPM/1e6-(sc.Parts.RootWorldCents-keep))
		if sc.Parts.Penalty == nil && sc.Parts.OwnCents < keep-share-1 {
			t.Errorf("%s: %s keeps %d¢ of its own, below %d¢ − its pass share %d¢", name, sc.Account[:8], sc.Parts.OwnCents, keep, share)
		}
		if sc.Cents > p.NodeCapCents {
			t.Errorf("%s: %s holds %d¢ above node_cap", name, sc.Account[:8], sc.Cents)
		}
	}
	n := int64(len(out.Scores))
	if d := received - passed; d > n || d < -n {
		t.Errorf("%s: received %d¢ over judgements, passed %d¢: money created or lost", name, received, passed)
	}
	if out.Summary.StandingCents > out.Summary.WorldCents {
		t.Errorf("%s: standing %d¢ > world %d¢", name, out.Summary.StandingCents, out.Summary.WorldCents)
	}
}

// Rings: fresh keys hold 0 however they rate each other; a ring that paid
// holds what it paid (plus the time present its own costs back, §9's 2×),
// split or not, whatever its members' pass shares; the pass share only
// moves standing between members.
func TestPassBoundSybilRing(t *testing.T) {
	ring := func(b *builder, k int) []string {
		var m []string
		for i := 0; i < k; i++ {
			m = append(m, "ring"+strconv.Itoa(i))
		}
		for _, x := range m {
			for _, y := range m {
				if x != y {
					b.review(x, y, 100, "paid_use", 2).accept(x, y, 1)
				}
			}
		}
		return m
	}
	for _, c := range []struct {
		name string
		paid []int64
	}{
		{"20 fresh keys", nil},
		{"one key pays 600¢", []int64{600}},
		{"600¢ over 10 keys", []int64{60, 60, 60, 60, 60, 60, 60, 60, 60, 60}},
		{"3,000¢ over 20 keys", []int64{150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150, 150}},
	} {
		var held [2]int64
		for v, params := range []Params{ProofsParams(), DefaultParams()} {
			b := newBuilder()
			b.snap.Params = params
			members := ring(b, 20)
			var paid int64
			for i, x := range c.paid {
				b.pay(members[i], x, 10)
				paid += x
			}
			out := compute(t, b)
			held[v] = centsOf(out, members...)
			if held[v] > 2*paid {
				t.Errorf("%s (version %d): the ring holds %d¢, paid %d¢", c.name, params.Version, held[v], paid)
			}
			if v == 1 {
				checkConserved(t, c.name, out)
			}
		}
		if c.paid == nil && held != [2]int64{} {
			t.Errorf("%s: a fresh-key ring holds %v", c.name, held)
		}
		// The pass share moves standing inside the ring; it never adds.
		if held[1] > held[0]+int64(len(c.paid)) {
			t.Errorf("%s: the ring holds %d¢ under version 10, %d¢ under 9", c.name, held[1], held[0])
		}
	}
}

// Star vouching: one root vouching for (and accepting the work of) 100
// fresh accounts gives them at most its offer, its surplus above node_cap
// topped up to pass_share_ppm of what it keeps, however many it backs.
func TestPassBoundStar(t *testing.T) {
	p := DefaultParams()
	for _, paid := range []int64{70, 300, 750, 900, 5000} {
		b := newBuilder().pay("hub", paid, 20)
		for i := 0; i < 100; i++ {
			s := "spoke" + strconv.Itoa(i)
			b.vouch("hub", s, 3).accept("hub", s, 2).review("hub", s, 90, "work", 1)
		}
		b.snap.Payments = b.snap.Payments[:1]
		out := compute(t, b)
		keep := min(paid, p.NodeCapCents)
		offer := max(paid-keep, keep*p.PassSharePPM/1e6)
		var got int64
		for i := 0; i < 100; i++ {
			got += scoreOf(out, "spoke"+strconv.Itoa(i)).Parts.ReceivedCents
		}
		if got > offer || got < offer-100 {
			t.Errorf("%d¢ hub: 100 spokes received %d¢, its offer is %d¢", paid, got, offer)
		}
		if hub := scoreOf(out, "hub"); hub.Cents < keep-(offer-(paid-keep))-1 {
			t.Errorf("%d¢ hub keeps %d¢", paid, hub.Cents)
		}
		checkConserved(t, "star "+strconv.FormatInt(paid, 10), out)
	}
}

// Self-dealing: an actor's second root, fresh, rated by its first gains
// only what the first gives up (and §7's time present, backed by the
// first's direct costs); keys linked into one root make no edge at all.
func TestPassBoundSelfDealing(t *testing.T) {
	p := DefaultParams()
	b := newBuilder().pay("main", 700, 20).review("main", "alt", 100, "paid_use", 1).accept("main", "alt", 1).vouch("main", "alt", 1).
		review("alt", "main", 100, "paid_use", 1)
	out := compute(t, b)
	main, alt := scoreOf(out, "main"), scoreOf(out, "alt")
	if main.Cents+alt.Cents > 700+p.TimeCapCents || alt.Parts.ReceivedCents > 700*p.PassSharePPM/1e6 || main.Cents+alt.Parts.ReceivedCents > 700 {
		t.Errorf("two roots of one actor: main %d¢, alt %d¢ (received %d¢)", main.Cents, alt.Cents, alt.Parts.ReceivedCents)
	}
	checkConserved(t, "two roots", out)

	b = newBuilder().pay("main", 700, 20).link("alt", "main").review("main", "alt", 100, "paid_use", 1).accept("main", "alt", 1)
	out = compute(t, b)
	if s := scoreOf(out, "main"); s.Cents != 700 || s.Parts.PassedCents != 0 || len(s.Parts.Backs) != 0 {
		t.Errorf("one root rating its own key: %d¢, passed %d¢, backs %+v", s.Cents, s.Parts.PassedCents, s.Parts.Backs)
	}
}

// Random graphs: the bound holds whatever the shape.
func TestPassBoundRandom(t *testing.T) {
	belowCap := 0
	for seed := uint64(1); seed <= 25; seed++ {
		rng := rand.New(rand.NewPCG(seed, 336))
		b := newBuilder()
		n := 20 + rng.IntN(60)
		name := func(i int) string { return "n" + strconv.Itoa(i) }
		for i := 0; i < n; i++ {
			b.account(name(i), 40)
			if rng.IntN(3) == 0 {
				b.pay(name(i), int64(10+rng.IntN(1500)), int64(1+rng.IntN(30)))
			}
		}
		for k := 0; k < n*3; k++ {
			x, y := rng.IntN(n), rng.IntN(n)
			if x == y {
				continue
			}
			switch rng.IntN(4) {
			case 0:
				b.review(name(x), name(y), int64(51+rng.IntN(50)), "work", int64(1+rng.IntN(20)))
			case 1:
				b.accept(name(x), name(y), int64(1+rng.IntN(20)))
			case 2:
				b.vote(name(x), name(y), int64(1+rng.IntN(20)))
			default:
				b.reply(name(x), name(y), int64(1+rng.IntN(20)))
			}
		}
		out := compute(t, b)
		checkConserved(t, "seed "+strconv.FormatUint(seed, 10), out)
		for _, sc := range out.Scores {
			if sc.Parts.RootWorldCents <= DefaultParams().NodeCapCents && sc.Parts.PassedCents > 0 {
				belowCap++
			}
		}
	}
	if belowCap == 0 {
		t.Error("no root at or below node_cap passed anything: the fixtures miss the pass share")
	}
	t.Logf("roots at or below node_cap passing standing: %d", belowCap)
}
