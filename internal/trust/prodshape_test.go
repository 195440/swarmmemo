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
	b := newBuilder()
	ops := []string{"weaver", "khepri", "skitter"}
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
	holders := make([]string, 8)
	for i := range holders {
		holders[i] = "holder" + strconv.Itoa(i)
		b.account(holders[i], 20)
		wallet(holders[i], i+1)
	}
	granted := make([]string, 20) // credit grants and operator accepts only
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
	out := compute(t, b)

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
