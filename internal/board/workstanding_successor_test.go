package board

import (
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"

	"swarmmemo/internal/allowance"
)

// successorSet is the requester's signed work.successor.set on id naming next.
func successorSet(s *Store, key ed25519.PrivateKey, id, next string) Command {
	b, _ := json.Marshal(map[string]any{"schema": 1, "generation": s.generation, "successor": next})
	return signed(key, Command{Operation: WorkSuccessorSet, MessageID: id, Data: string(b), Timestamp: s.now().Unix()})
}

// C391: the requester of a standing task names the next round. Refused for
// another requester's task (either end), a task that is not standing, the
// task itself, one not open and a cycle; work.get shows standing.successor,
// and while the round is not claimable standing.next_round, the first
// claimable round along the chain.
func TestWorkStandingSuccessor(t *testing.T) {
	s := rewardStore(t)
	owner, other := keyFor(200), keyFor(201)
	mintCredit(t, s, keyID(owner), allowance.Paid, 10000)
	mintCredit(t, s, keyID(other), allowance.Paid, 5000)
	ttl := int64(10 * 86400)
	standing := func(key ed25519.PrivateKey, extra map[string]any) string {
		id := rewardRequest(t, s, key, "lobby")
		data := map[string]any{"seats": 1, "reward": 100}
		for k, v := range extra {
			data[k] = v
		}
		run(t, s, standingCreate(s, key, id, data, ttl))
		return id
	}
	round1 := standing(owner, map[string]any{"results_max": 1})
	round2 := standing(owner, map[string]any{"results_max": 1})
	round3 := standing(owner, nil)
	foreign := standing(other, nil)
	plain := rewardRequest(t, s, owner, "lobby")
	run(t, s, rewardCreate(s, owner, plain, 100, 86400))

	fails(t, s, successorSet(s, other, round1, round2), "work_forbidden")
	fails(t, s, successorSet(s, owner, round1, foreign), "invalid_work_data")
	fails(t, s, successorSet(s, owner, round1, plain), "invalid_work_data")
	fails(t, s, successorSet(s, owner, plain, round1), "work_state_conflict")
	fails(t, s, successorSet(s, owner, round1, round1), "invalid_work_data")
	fails(t, s, successorSet(s, owner, round1, strings.Repeat("9", 32)), "invalid_work_data")
	b, _ := json.Marshal(map[string]any{"schema": 1, "generation": s.generation})
	fails(t, s, signed(owner, Command{Operation: WorkSuccessorSet, MessageID: round1, Data: string(b), Timestamp: s.now().Unix()}), "invalid_work_data")

	// round1 → round2 → round3; round3 → round1 would be a cycle.
	run(t, s, successorSet(s, owner, round1, round2))
	run(t, s, successorSet(s, owner, round2, round3))
	fails(t, s, successorSet(s, owner, round3, round1), "invalid_work_data")

	// round1 is claimable: it names its successor, no next_round.
	w := getTestWork(t, s, round1)
	if !w.Claimable || w.Standing.Successor != round2 || w.Standing.NextRound != "" {
		t.Fatalf("open round: claimable %v successor %q next %q", w.Claimable, w.Standing.Successor, w.Standing.NextRound)
	}
	// round1 fills: next_round is round2.
	_, c := standingClaim(t, s, keyFor(202), round1, nil)
	run(t, s, c)
	if w = getTestWork(t, s, round1); w.Claimable || w.Standing.NextRound != round2 {
		t.Fatalf("full round1: claimable %v next %q", w.Claimable, w.Standing.NextRound)
	}
	// round2 fills too: round1's next_round resolves through it to round3.
	_, c = standingClaim(t, s, keyFor(203), round2, nil)
	run(t, s, c)
	if w = getTestWork(t, s, round1); w.Standing.NextRound != round3 {
		t.Fatalf("round1 next %q, want round3 %s", w.Standing.NextRound, round3)
	}
	// A shorter cycle is refused too; a successor can be named on a closed
	// round (not named as one), and the history holds each signed change.
	fails(t, s, successorSet(s, owner, round3, round2), "invalid_work_data")
	run(t, s, workCommand(s, owner, Command{Operation: "work.cancel", MessageID: round2, Reason: "next round"}))
	run(t, s, successorSet(s, owner, round2, round3))
	fails(t, s, successorSet(s, owner, round3, round2), "invalid_work_data")
	if w = getTestWork(t, s, round2); w.State != "cancelled" || w.Standing.NextRound != round3 {
		t.Fatalf("cancelled round2: %s next %q", w.State, w.Standing.NextRound)
	}
	found := 0
	for _, tr := range run(t, s, Command{Operation: "work.history", MessageID: round2}).Data["transitions"].([]WorkTransition) {
		if tr.Operation == WorkSuccessorSet {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("round2 history holds %d successor changes", found)
	}
	// round3 is cancelled too: nothing claimable along the chain.
	run(t, s, workCommand(s, owner, Command{Operation: "work.cancel", MessageID: round3, Reason: "done"}))
	if w = getTestWork(t, s, round1); w.Standing.Successor != round2 || w.Standing.NextRound != "" {
		t.Fatalf("no open round: successor %q next %q", w.Standing.Successor, w.Standing.NextRound)
	}
}
