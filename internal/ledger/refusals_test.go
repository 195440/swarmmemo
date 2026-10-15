package ledger

import (
	"database/sql"
	"testing"

	"swarmmemo/internal/allowance"
)

// Every refused spend and reserve is counted by day, resource and code; a
// spend that succeeds or fails otherwise is not; a take clears the counts and
// a put-back restores them.
func TestRefusalsCounted(t *testing.T) {
	h := newHarness(t, smallParams()) // floor 200, cap 400
	alice := signedSubject("alice")
	if err := h.spend(alice, 150); err != nil {
		t.Fatal(err)
	}
	if code(h.spend(alice, 100)) != "quota_exhausted" {
		t.Fatal("expected quota_exhausted")
	}
	if code(h.spend(alice, 100)) != "quota_exhausted" {
		t.Fatal("expected quota_exhausted")
	}
	if err := h.do(func(q *sql.Tx) error {
		_, err := h.l.Reserve(ctx, q, alice, allowance.PostBytes, 100, "k1", Ref{Service: "echo"}, 60, h.now)
		return err
	}); code(err) != "quota_exhausted" {
		t.Fatalf("reserve: %v", err)
	}
	if code(h.spend(alice, -1)) != "invalid_amount" {
		t.Fatal("expected invalid_amount")
	}
	got := h.l.TakeRefusals()
	if len(got) != 1 || got[0] != (Refusal{Day: day0, Resource: allowance.PostBytes, Code: "quota_exhausted", N: 3}) {
		t.Fatalf("refusals %+v", got)
	}
	if again := h.l.TakeRefusals(); len(again) != 0 {
		t.Fatalf("a take does not clear: %+v", again)
	}
	h.l.PutBackRefusals(got)
	if back := h.l.TakeRefusals(); len(back) != 1 || back[0].N != 3 {
		t.Fatalf("put back %+v", back)
	}
}
