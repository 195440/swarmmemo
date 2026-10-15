package ledger

import (
	"database/sql"
	"testing"

	"swarmmemo/internal/allowance"
)

// The transition floor (RFC 0021 §14, C320c: standing only adds at the
// cutover): a signed account's share under the share rule is never lower
// than what the tier waterfall gave it on a waterfall day within
// TransitionFloorDays, within the cap. The fixture accounts are prod's
// shapes: an operator (tier 1, the 16 MiB cap), a proven account (8 MiB), a
// signed account (512 KiB, now below the floor), one with standing.

// legacyClaim records subject's share of post_bytes on a waterfall day
// (allowance_pools with its tier-4 row), or on a share-rule day when
// waterfall is false.
func (h *harness) legacyClaim(subject string, day, entitlement int64, waterfall bool) {
	h.t.Helper()
	if err := h.do(func(q *sql.Tx) error {
		if waterfall {
			for tier := 0; tier <= 4; tier++ {
				if _, err := q.Exec(`INSERT OR IGNORE INTO allowance_pools(resource,day,tier,size,want,expected_units,expected_weight) VALUES('post_bytes',?,?,0,0,0,0)`, day, tier); err != nil {
					return err
				}
			}
		}
		_, err := q.Exec(`INSERT INTO allowance_claims(resource,day,subject,tier,weight,root,entitlement,granted,source,lot_id,created_at) VALUES('post_bytes',?,?,3,1000000,?,?,0,'tier',0,?)`,
			day, subject, subject, entitlement, day*86400)
		return err
	}); err != nil {
		h.t.Fatal(err)
	}
}

func TestTransitionFloorNeverLowerThanTheWaterfall(t *testing.T) {
	const mib, kib = int64(1) << 20, int64(1) << 10
	h := newHarness(t, DefaultAllowanceParams())
	rp := h.p.Resources[allowance.PostBytes]
	h.now = (day0 + 3) * 86400
	fixture := []struct {
		subject string
		legacy  int64
	}{
		{"operator", 16 * mib},
		{"proven", 8 * mib},
		{"signed", 512 * kib},
		{"c300-standing", 6 * mib},
	}
	for _, f := range fixture {
		h.legacyClaim(f.subject, day0+1, f.legacy/2, true)
		h.legacyClaim(f.subject, day0+2, f.legacy, true) // the largest counts
	}
	h.legacyClaim("stale", day0+3-TransitionFloorDays-1, 16*mib, true)
	h.legacyClaim("sharerule", day0, 12*mib, false)
	h.legacyClaim("x-new", day0+2, 8*mib, true)

	for _, f := range fixture {
		s := signedSubject(f.subject)
		pre := h.balance(s) // the prospective share agrees with the claim
		if err := h.spend(s, 0); err != nil {
			t.Fatal(err)
		}
		b := h.balance(s)
		want := min(f.legacy, rp.Cap)
		if b.Entitlement < want || pre.Entitlement != b.Entitlement {
			t.Errorf("%s: share %d (prospective %d), below its waterfall share %d", f.subject, b.Entitlement, pre.Entitlement, want)
		}
		if f.legacy <= rp.Floor && b.FloorPart != rp.Floor {
			t.Errorf("%s: a waterfall share below the floor keeps the floor: %d", f.subject, b.FloorPart)
		}
		t.Logf("%s: waterfall %d KiB → share rule %d KiB (floor part %d, standing part %d)", f.subject, f.legacy>>10, b.Entitlement>>10, b.FloorPart>>10, b.StandingPart>>10)
	}
	// Older than TransitionFloorDays, a share-rule day, a new key under
	// pause-new-keys and an anonymous subject: the share rule alone.
	h.levers.lv.PauseNewKeys = true
	for _, s := range []allowance.Subject{signedSubject("stale"), signedSubject("sharerule"), signedSubject("x-new"), anonSubject("net")} {
		if err := h.spend(s, 0); err != nil {
			t.Fatal(err)
		}
		if b := h.balance(s); b.Entitlement > rp.Floor {
			t.Errorf("%s: %d, above the floor %d", s.ID, b.Entitlement, rp.Floor)
		}
	}
	// What the compare tool reads.
	var got int64
	if err := h.do(func(q *sql.Tx) (err error) {
		got, err = LegacyShare(ctx, q, allowance.PostBytes, day0+3, "operator")
		return err
	}); err != nil || got != 16*mib {
		t.Fatalf("LegacyShare %d %v", got, err)
	}
	checkInvariants(t, h.db, h.p)
}
