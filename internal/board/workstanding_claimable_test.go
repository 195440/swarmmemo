package board

import (
	"testing"

	"swarmmemo/internal/allowance"
)

// C384: every work read says whether it can be claimed now (claimable); a
// standing task with no seat open says when one opens by itself
// (standing.next_open_at) and what one result is paid (standing.per_result),
// so a reader does not take the pool for an open, per-claim reward.
func TestWorkClaimableAndStandingNextOpen(t *testing.T) {
	s := rewardStore(t)
	owner := keyFor(190)
	mintCredit(t, s, keyID(owner), allowance.Paid, 5000)
	ttl := int64(10 * 86400)

	// A standing task with a seat open: claimable, no next_open_at.
	daily := rewardRequest(t, s, owner, "lobby")
	run(t, s, standingCreate(s, owner, daily, map[string]any{"seats": 3, "reward": 150, "day_max": 2}, ttl))
	w := getTestWork(t, s, daily)
	if !w.Claimable || w.Standing.Open != 3 || w.Standing.NextOpenAt != 0 || w.Standing.PerResult != 150 || w.Reward.Amount != 450 {
		t.Fatalf("open standing task: claimable %v %+v reward %+v", w.Claimable, w.Standing.WorkStandingSeats, w.Reward)
	}
	// day_max reached with a seat still free: the next seat opens at the
	// next 00:00 UTC, the day count's reset.
	for _, k := range []int{191, 192} {
		_, c := standingClaim(t, s, keyFor(byte(k)), daily, nil)
		run(t, s, c)
	}
	w = getTestWork(t, s, daily)
	midnight := testTime - testTime%86400 + 86400
	if w.Claimable || w.State != "open" || w.Standing.Open != 0 || w.Standing.NextOpenAt != midnight {
		t.Fatalf("day_max reached: claimable %v %+v", w.Claimable, w.Standing.WorkStandingSeats)
	}

	// Every seat holds a waiting result: the first verdict deadline frees one.
	full := rewardRequest(t, s, owner, "lobby")
	run(t, s, standingCreate(s, owner, full, map[string]any{"seats": 2, "reward": 150, "day_max": 5}, ttl))
	for _, k := range []int{193, 194} {
		_, c := standingClaim(t, s, keyFor(byte(k)), full, nil)
		run(t, s, c)
	}
	w = getTestWork(t, s, full)
	if w.Claimable || w.Standing.Open != 0 || w.Standing.NextOpenAt != testTime+WorkStandingVerdictTTL {
		t.Fatalf("seats held: claimable %v %+v", w.Claimable, w.Standing.WorkStandingSeats)
	}

	// Results capped and reached: none reopens by itself.
	capped := rewardRequest(t, s, owner, "lobby")
	run(t, s, standingCreate(s, owner, capped, map[string]any{"seats": 1, "reward": 150, "results_max": 1}, ttl))
	_, c := standingClaim(t, s, keyFor(195), capped, nil)
	run(t, s, c)
	if w = getTestWork(t, s, capped); w.Claimable || !w.Standing.Full || w.Standing.NextOpenAt != 0 {
		t.Fatalf("full standing task: claimable %v %+v", w.Claimable, w.Standing.WorkStandingSeats)
	}

	// Ordinary work: claimable while open, not once claimed.
	plain := rewardRequest(t, s, owner, "lobby")
	run(t, s, rewardCreate(s, owner, plain, 100, 86400))
	if w = getTestWork(t, s, plain); !w.Claimable || w.Standing != nil {
		t.Fatalf("open work: %+v", w)
	}
	run(t, s, workCommand(s, keyFor(196), Command{Operation: "work.claim", MessageID: plain, TTL: 600}))
	if w = getTestWork(t, s, plain); w.Claimable || w.State != "claimed" {
		t.Fatalf("claimed work: claimable %v state %s", w.Claimable, w.State)
	}
}
