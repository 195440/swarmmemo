package ledger

import (
	"database/sql"
	"strings"
	"testing"

	"swarmmemo/internal/allowance"
)

// Escrow splits and joins (C278, C281) on the real ledger and SQLite: a
// join carries the credential spend of the units it moves, so a release of
// the joined escrow gives it back to the credential and day that spent it;
// and a join that would leave an escrow with more parts than a release
// reads is refused.

func (h *harness) escrowSplit(id string, amount int64, key string, until int64) (Hold, error) {
	var hold Hold
	err := h.do(func(q *sql.Tx) error {
		var err error
		hold, err = h.l.EscrowSplit(ctx, q, id, amount, key, until, h.now)
		return err
	})
	return hold, err
}

func (h *harness) escrowJoin(id, into string) error {
	return h.do(func(q *sql.Tx) error { return h.l.EscrowJoin(ctx, q, id, into, h.now) })
}

func TestEscrowJoinCarriesCredentialSpend(t *testing.T) {
	h := newHarness(t, escrowParams())
	pool, refill := limited("owner", "key:pool"), limited("owner", "key:refill")
	h.setLimit("owner", "key:refill", ptr(110), nil, 0)
	h.mintCredit("owner", allowance.Paid, 2000)
	until := h.now + 30*86400
	p, err := h.escrow(pool, 300, "pool", until)
	if err != nil {
		t.Fatal(err)
	}
	// A result's reward leaves the pool and is paid out.
	item, err := h.escrowSplit(p.ID, 100, "item", until)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.escrowPay(item.ID, "worker"); err != nil {
		t.Fatal(err)
	}
	// The refill, through another credential, joins the pool: its spend
	// stays counted while its units are held.
	r, err := h.escrow(refill, 100, "refill", until)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.escrowJoin(r.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if st := h.limitStatus("owner", "key:refill"); st.SpentToday != 103 {
		t.Fatalf("after the join the refill counts %d, want 103", st.SpentToday)
	}
	// Releasing the pool gives the refill's units back to the refill's
	// credential first, the rest to the pool's: what was paid stays spent.
	if err = h.escrowRelease(p.ID); err != nil {
		t.Fatal(err)
	}
	if st := h.limitStatus("owner", "key:refill"); st.SpentToday != 3 {
		t.Fatalf("after the pool's release the refill counts %d, want its fee 3", st.SpentToday)
	}
	if st := h.limitStatus("owner", "key:pool"); st.SpentToday != 103 {
		t.Fatalf("after the pool's release the pool's key counts %d, want the 100 paid and its fee", st.SpentToday)
	}
	if _, err = h.escrow(refill, 100, "again", until); err != nil {
		t.Fatalf("the refill credential's day was not given back: %v", err)
	}
	checkInvariants(t, h.db, h.p)
}

// A join between escrows of the same credential and day records nothing
// extra: the pool's own release gives it all back once.
func TestEscrowJoinSameCredential(t *testing.T) {
	h := newHarness(t, escrowParams())
	key := limited("owner", "key:one")
	h.mintCredit("owner", allowance.Paid, 1000)
	until := h.now + 30*86400
	p, err := h.escrow(key, 200, "pool", until)
	if err != nil {
		t.Fatal(err)
	}
	item, err := h.escrowSplit(p.ID, 50, "item", until)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.escrowJoin(item.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if n := h.count("SELECT count(*) FROM spend_limit_hold_shares WHERE units>0"); n != 0 {
		t.Fatalf("%d shares for a join within one credential and day", n)
	}
	if err = h.escrowRelease(p.ID); err != nil {
		t.Fatal(err)
	}
	if st := h.limitStatus("owner", "key:one"); st.SpentToday != 3 {
		t.Fatalf("after the release %d counted, want the fee 3", st.SpentToday)
	}
	checkInvariants(t, h.db, h.p)
}

// A join that would give an escrow more lots than a release reads
// (liveLotsMax) is refused and changes nothing; the source can still be
// released in full, and a hold past the limit is an error, never a silent
// partial release.
func TestEscrowJoinPartsLimit(t *testing.T) {
	h := newHarness(t, escrowParams())
	h.mintCredit("owner", allowance.Paid, 13) // the pool's 10 and its fee
	s := signedSubject("owner")
	until := h.now + 30*86400
	p, err := h.escrow(s, 10, "pool", until)
	if err != nil {
		t.Fatal(err)
	}
	// The pool gathers liveLotsMax-1 more one-unit lots (as joins over many
	// days would), each held by it; they sort last, so a new escrow still
	// sees the owner's other lots.
	if err = h.do(func(q *sql.Tx) error {
		for range liveLotsMax - 1 {
			res, err := q.ExecContext(ctx, "INSERT INTO ledger_lots(account,resource,bucket,origin_tier,origin_account,hops,issued_day,expires_at,half_life_days,decayed_day,initial,remaining,held,state,created_at) VALUES('owner','credit','paid',0,'owner',0,?,0,0,?,1,1,1,'live',?)", day0+5, day0+5, h.now)
			if err != nil {
				return err
			}
			lot, _ := res.LastInsertId()
			if _, err = q.ExecContext(ctx, "INSERT INTO ledger_hold_parts(hold_id,lot_id,units) VALUES(?,?,1)", p.ID, lot); err != nil {
				return err
			}
		}
		_, err := q.ExecContext(ctx, "UPDATE ledger_holds SET max_units=max_units+? WHERE id=?", liveLotsMax-1, p.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := h.count("SELECT count(*) FROM ledger_hold_parts WHERE hold_id=?", p.ID); n != liveLotsMax {
		t.Fatalf("the pool has %d parts", n)
	}
	// A refill from a new lot, one the pool lacks.
	h.now += 86400
	if err = h.do(func(q *sql.Tx) error {
		_, err := h.l.TopUp(ctx, q, "owner", allowance.Credit, 60, "test", Ref{Service: "x402", Op: "topup", PublicRef: "t1"}, h.now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r, err := h.escrow(s, 20, "refill", until)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.escrowJoin(r.ID, p.ID); code(err) != "too_many_parts" {
		t.Fatalf("a join past the parts limit: %v", err)
	}
	if n := h.count("SELECT max_units FROM ledger_holds WHERE id=?", r.ID); n != 20 {
		t.Fatalf("the refused join moved units: %d left", n)
	}
	if err = h.escrowRelease(r.ID); err != nil {
		t.Fatal(err)
	}
	// A pool at the limit releases every part.
	if err = h.escrowRelease(p.ID); err != nil {
		t.Fatal(err)
	}
	if held := h.count("SELECT coalesce(sum(held),0) FROM ledger_lots WHERE account='owner'"); held != 0 {
		t.Fatalf("%d units still held after both releases", held)
	}
	// A hold with more parts than a release reads is refused loudly.
	q, err := h.escrow(s, 5, "over", until)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.do(func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO ledger_hold_parts(hold_id,lot_id,units) SELECT ?,id,0 FROM ledger_lots WHERE account='owner' AND id NOT IN (SELECT lot_id FROM ledger_hold_parts WHERE hold_id=?) LIMIT ?", q.ID, q.ID, liveLotsMax)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = h.escrowRelease(q.ID); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("a release past the parts limit: %v", err)
	}
}
