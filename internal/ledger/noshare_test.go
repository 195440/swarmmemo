package ledger

import (
	"database/sql"
	"errors"
	"testing"

	"swarmmemo/internal/allowance"
)

// A band the parameters give no share of a resource (the cap 0, or
// anonymous_share_ppm 0 for anonymous callers) is refused no_share, naming
// the resource and the band, with no retry_after: waiting for 00:00 UTC never
// helps. A real dry slice stays global_quota_exhausted, and a lever's zero
// stays as it was.

func (h *harness) spendMemory(s allowance.Subject, units int64) error {
	return h.do(func(q *sql.Tx) error {
		_, err := h.l.Spend(ctx, q, s, allowance.MemoryBytes, units, Ref{Service: "memory", Op: "service.call", Method: "put"}, h.now)
		return err
	})
}

func (h *harness) reserveMemory(s allowance.Subject, units int64, key string) error {
	return h.do(func(q *sql.Tx) error {
		_, err := h.l.Reserve(ctx, q, s, allowance.MemoryBytes, units, key, Ref{Service: "memory", Op: "service.call", Method: "put"}, 60, h.now)
		return err
	})
}

func hostedSubject(id string) allowance.Subject {
	return allowance.Subject{ID: id, KeyID: "key-" + id, Signed: true, Hosted: true}
}

func wantNoShare(t *testing.T, err error, r allowance.Resource, band allowance.Band) {
	t.Helper()
	var e *allowance.Err
	if !errors.As(err, &e) || e.Code != "no_share" || e.Resource != r || e.Band != band || e.RetryAfter != 0 {
		t.Fatalf("want no_share for %s, band %s, no retry_after; got %#v", r, band.Name(), err)
	}
}

// Memory with anonymous_share_ppm 0 gives anonymous callers (and hosted
// keys, which take their rules) none. The anonymous-shrink lever only
// lowers a share, so pulling it to 100% gives none either.
func TestNoShareAnonymous(t *testing.T) {
	p := DefaultAllowanceParams()
	p.Resources[allowance.MemoryBytes].AnonymousSharePPM = 0
	h := newHarness(t, p)
	wantNoShare(t, h.spendMemory(hostedSubject("hosted-1"), 300), allowance.MemoryBytes, allowance.BandAnonymous)
	wantNoShare(t, h.reserveMemory(hostedSubject("hosted-1"), 300, "put-1"), allowance.MemoryBytes, allowance.BandAnonymous)
	wantNoShare(t, h.spendMemory(anonSubject("net-a"), 300), allowance.MemoryBytes, allowance.BandAnonymous)
	// Signed accounts have their share; posting is untouched for anonymous
	// callers.
	if err := h.spendMemory(signedSubject("alice"), 300); err != nil {
		t.Fatalf("signed memory: %v", err)
	}
	if err := h.spend(hostedSubject("hosted-1"), 300); err != nil {
		t.Fatalf("hosted posting: %v", err)
	}
	h.levers.lv.AnonymousSharePPM = ppm
	wantNoShare(t, h.spendMemory(hostedSubject("hosted-2"), 300), allowance.MemoryBytes, allowance.BandAnonymous)
	checkInvariants(t, h.db, h.p)
}

// Credit with a cap of 0: no band has a share. A lever's zero is a lever,
// not the parameters: posting stays as before (a share issued at claim,
// then no water).
func TestNoShareByCap(t *testing.T) {
	p := DefaultAllowanceParams()
	p.Resources[allowance.Credit].Cap, p.Resources[allowance.Credit].Floor = 0, 0
	h := newHarness(t, p)
	wantNoShare(t, h.do(func(q *sql.Tx) error {
		_, err := h.l.Spend(ctx, q, signedSubject("alice"), allowance.Credit, 1, Ref{Service: "notary", Op: "service.call"}, h.now)
		return err
	}), allowance.Credit, allowance.BandSigned)
	h.levers.lv.AnonymousSharePPM = 0
	if got := code(h.spend(anonSubject("net-b"), 1)); got != "global_quota_exhausted" {
		t.Fatalf("a lever's zero share: %s", got)
	}
}
