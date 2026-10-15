package trust

import (
	"strconv"
	"testing"
)

// TestProdShapedFirstRun is a first run (no carry) shaped like the
// production copy of 2026-10-14 (C320d): nine verified wallets assessed 0
// and, as the board exports a link rechecked after the as-of midnight,
// checked at as_of − 1; a claimed (unverified) domain; operator personas
// under breakers granting credit by transfer and accepting work; no money
// paid at all. Each wallet holds the wallet fallback, wallet holders' votes
// and replies give the accounts they answer time present, and credit grants
// and the operators' judgements give nothing: the operators hold only what
// their own wallet and time present give.
func TestProdShapedFirstRun(t *testing.T) {
	b, ops, holders, granted := prodShaped()
	out := compute(t, b)
	checkProdShaped(t, out, ops, holders, granted)
}

// prodShaped is TestProdShapedFirstRun's snapshot.
func prodShaped() (b *builder, ops, holders, granted []string) {
	b = newBuilder()
	ops = []string{"weaver", "khepri", "skitter"}
	for _, o := range ops {
		b.account(o, 60)
		b.snap.Breakers = append(b.snap.Breakers, Record{Type: "breaker", Account: acct(o), StartedAt: ago(10), TrustUntil: testAsOf + 30*day})
	}
	b.snap.Proofs = append(b.snap.Proofs, Record{Type: "proof", Account: acct("weaver"), Kind: "domain", LinkValue: "operator.example", State: "claimed", CreatedAt: ago(40)})
	wallet := func(name string, i int) {
		addr := "0x" + strconv.Itoa(1000+i)
		b.snap.Proofs = append(b.snap.Proofs, Record{Type: "proof", Account: acct(name), Kind: "wallet", LinkValue: addr, State: "verified",
			CreatedAt: ago(int64(1 + i%5)), CheckedAt: testAsOf - 1, Root: "wallet:eip155:1:" + addr})
	}
	wallet("skitter", 0)
	holders = make([]string, 8)
	for i := range holders {
		holders[i] = "holder" + strconv.Itoa(i)
		b.account(holders[i], 20)
		wallet(holders[i], i+1)
	}
	granted = make([]string, 20) // credit grants and operator accepts only
	for i := range granted {
		granted[i] = "granted" + strconv.Itoa(i)
		b.account(granted[i], 15)
		b.transfer("weaver", granted[i], 250+int64(i)*50, 1+int64(i%7))
		b.accept("khepri", granted[i], 1+int64(i%9))
		b.accept("skitter", granted[i], 2+int64(i%9))
	}
	for i, h := range holders {
		b.transfer("weaver", h, 3000, 2+int64(i%5))
		b.accept("khepri", h, 1+int64(i%6))
		b.vote(holders[(i+1)%len(holders)], h, 1+int64(i%4))
		for d := int64(1); d <= 3; d++ {
			b.reply(h, "answered"+strconv.Itoa(i), d)
		}
		b.vote(h, "weaver", 1+int64(i%3))
	}
	return b, ops, holders, granted
}

func checkProdShaped(t *testing.T, out Output, ops, holders, granted []string) {
	t.Helper()
	for _, h := range holders {
		if got := scoreOf(out, h).Cents; got < WalletFallbackCents {
			t.Errorf("%s: a verified wallet holds %d¢, want at least %d¢", h, got, WalletFallbackCents)
		}
	}
	answered := 0
	for i := range holders {
		if s := scoreOf(out, "answered"+strconv.Itoa(i)); s.Cents > 0 && s.Parts.ActiveDays > 0 {
			answered++
		}
	}
	if answered == 0 {
		t.Error("no account answered by a wallet holder earns time present")
	}
	for _, g := range granted {
		if s := scoreOf(out, g); s.Cents != 0 {
			t.Errorf("%s holds %d¢ from credit grants and operators' accepts alone: %+v", g, s.Cents, s.Parts.World)
		}
	}
	var best int64
	for _, h := range holders {
		best = max(best, scoreOf(out, h).Cents)
	}
	for _, o := range ops {
		s := scoreOf(out, o)
		for _, it := range s.Parts.World {
			if it.Kind == "proof" && it.Source == "domain" && it.Cents != 0 {
				t.Errorf("%s: a claimed domain prices %d¢", o, it.Cents)
			}
			if it.Kind == "money" || it.Kind == "payments" {
				t.Errorf("%s: credit grants count as %s: %+v", o, it.Kind, it)
			}
		}
		if s.Cents > best {
			t.Errorf("%s holds %d¢, above every outside wallet holder (%d¢)", o, s.Cents, best)
		}
	}
	if out.Summary.AboveVoteFloor < int64(len(holders)) {
		t.Errorf("above the vote floor: %d, want at least the %d wallet holders", out.Summary.AboveVoteFloor, len(holders))
	}
	t.Logf("holder0 %d¢, answered0 %d¢, weaver %d¢, skitter %d¢, above vote floor %d",
		scoreOf(out, "holder0").Cents, scoreOf(out, "answered0").Cents, scoreOf(out, "weaver").Cents, scoreOf(out, "skitter").Cents, out.Summary.AboveVoteFloor)
}

// C336: the prod shape plus judgements. holder0 (a 50¢ wallet and 20¢
// paid, 70¢) rates five workers' accepted work at 85; skitter, under its
// breaker, rates them too. Under version 9 the workers received 0 over
// those edges; under version 10 they share holder0's pass share (a quarter
// of its 80¢: wallet, money and time present), holder0 keeps the rest,
// skitter's breaker still makes no edge, and the rest of the prod shape
// holds. The holders' votes for weaver now pass it standing too (it was
// 0¢ under version 9): conserved, and never above a holder.
func TestProdShapedJudgements(t *testing.T) {
	p := DefaultParams()
	var received [2]int64
	for v, params := range []Params{ProofsParams(), DefaultParams()} {
		b, ops, holders, granted := prodShaped()
		b.snap.Params = params
		b.pay("holder0", 20, 3)
		workers := []string{"w0", "w1", "w2", "w3", "w4"}
		for i, w := range workers {
			b.account(w, 10)
			for _, by := range []string{"holder0", "skitter"} {
				b.accept(by, w, 1+int64(i%3)).review(by, w, 85, "work", 1+int64(i%3))
			}
		}
		out := compute(t, b)
		checkProdShaped(t, out, ops, holders, granted)
		for _, w := range workers {
			sc := scoreOf(out, w)
			received[v] += sc.Parts.ReceivedCents
			for _, f := range sc.Parts.Backers {
				if f.Root == scoreOf(out, "skitter").Root && f.CapCents > 0 {
					t.Errorf("version %d: skitter, under a breaker, backs %s: %+v", params.Version, w, f)
				}
			}
		}
		if v == 1 {
			checkConserved(t, "prod shape", out)
			h := scoreOf(out, "holder0")
			share := min(h.Parts.RootWorldCents, p.NodeCapCents) * p.PassSharePPM / 1e6
			// The workers get holder0's pass share, and what holder0's own
			// backers (the holders' votes) pass through it.
			if received[1] < share-5 || h.Parts.PassedCents > share || h.Cents < h.Parts.RootWorldCents-share-1 {
				t.Errorf("holder0 (%d¢ world) keeps %d¢ and passed %d¢; five workers received %d¢, its pass share is %d¢", h.Parts.RootWorldCents, h.Cents, h.Parts.PassedCents, received[1], share)
			}
			// Operators judge nothing under their breakers and receive only
			// what the holders' votes pass them, like any voted account:
			// never more than the holders offered, nor above a holder.
			var offered int64
			for _, x := range holders {
				offered += scoreOf(out, x).Parts.PassedCents
			}
			for _, o := range ops {
				if s := scoreOf(out, o); s.Parts.PassedCents > 0 || s.Parts.ReceivedCents > offered {
					t.Errorf("operator %s passed %d¢, received %d¢ (holders offered %d¢)", o, s.Parts.PassedCents, s.Parts.ReceivedCents, offered)
				}
			}
			t.Logf("operators received: weaver %d¢, khepri %d¢, skitter %d¢", scoreOf(out, "weaver").Parts.ReceivedCents, scoreOf(out, "khepri").Parts.ReceivedCents, scoreOf(out, "skitter").Parts.ReceivedCents)
			t.Logf("holder0 %d¢ (world %d¢, passed %d¢); workers received %d¢; w0 %d¢; summary passed %d¢ of world %d¢",
				h.Cents, h.Parts.RootWorldCents, h.Parts.PassedCents, received[1], scoreOf(out, "w0").Cents, out.Summary.PassedCents, out.Summary.WorldCents)
		}
	}
	if received[0] != 0 || received[1] == 0 {
		t.Errorf("workers received %d¢ under version 9, %d¢ under version 10", received[0], received[1])
	}
}
