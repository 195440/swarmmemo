package board

import (
	"strings"
	"testing"

	"swarmmemo/internal/allowance"
)

// The reject fee (C119) on the real store and ledger: held at create beside
// the reward, paid once to the worker of a rejected submitted result,
// returned on accept, on cancel while open and at a deadline with no result,
// paid at a deadline that leaves a result undecided, bounded, and refused
// while WORK_REJECT_FEE is off.

func rejectFeeStore(t *testing.T) *Store {
	t.Helper()
	s := rewardStore(t)
	s.config.Features.WorkRejectFee = true
	return s
}

func TestWorkRejectFeePaidOnceOnReject(t *testing.T) {
	s := rejectFeeStore(t)
	owner, first, second, third := keyFor(201), keyFor(202), keyFor(203), keyFor(204)
	requester := keyID(owner)
	mintCredit(t, s, requester, allowance.Paid, 1000)
	id := rewardRequest(t, s, owner, "lobby")
	run(t, s, reviewedCreate(s, owner, id, map[string]any{"reward": 300, "reject_fee": 50}, 0))

	w := getTestWork(t, s, id)
	if w.RejectFee == nil || w.RejectFee.Amount != 50 || w.RejectFee.State != "held" || w.RejectFee.Unit != "credit" || w.RejectFee.PaidTo != nil {
		t.Fatalf("reject fee at create: %+v", w.RejectFee)
	}
	if held := creditIn(t, s, requester, "held"); held != 350 {
		t.Fatalf("the ledger holds %d", held)
	}
	if list := run(t, s, Command{Operation: "works.list"}).Data["works"].([]Work); len(list) != 1 || list[0].RejectFee == nil || list[0].RejectFee.State != "held" {
		t.Fatalf("works.list reject_fee: %+v", list)
	}

	// Rejecting a claim with no result pays nothing.
	ack := run(t, s, workCommand(s, first, Command{Operation: "work.claim", MessageID: id, TTL: 600})).Data["ack"].(WorkAck)
	run(t, s, workCommand(s, owner, Command{Operation: "work.reject", MessageID: id, Amount: ack.Fence, Reason: "no result yet"}))
	if w = getTestWork(t, s, id); w.RejectFee.State != "held" || creditIn(t, s, keyID(first), "remaining") != 0 {
		t.Fatalf("a reject without a result moved the fee: %+v", w.RejectFee)
	}

	fence := claimAndSubmit(t, s, first, id, "lobby")
	res := run(t, s, workCommand(s, owner, Command{Operation: "work.reject", MessageID: id, Amount: fence, Reason: "does not build"}))
	if note := res.Data["ack"].(WorkAck).Note; !strings.Contains(note, "reject fee, 50 credits") {
		t.Fatalf("reject note %q", note)
	}
	if got := creditIn(t, s, keyID(first), "remaining"); got != 50 {
		t.Fatalf("the rejected worker received %d", got)
	}
	w = getTestWork(t, s, id)
	if w.State != "open" || w.Reward.State != "held" || w.RejectFee.State != "paid" || w.RejectFee.Reason != "rejected" || w.RejectFee.PaidTo == nil || w.RejectFee.PaidTo.ID != keyID(first) || w.RejectFee.TransferID == "" {
		t.Fatalf("after the reject: %s reward %+v fee %+v", w.State, w.Reward, w.RejectFee)
	}

	// Once per work item: the next rejected worker gets nothing.
	fence = claimAndSubmit(t, s, second, id, "lobby")
	run(t, s, workCommand(s, owner, Command{Operation: "work.reject", MessageID: id, Amount: fence, Reason: "still broken"}))
	if got := creditIn(t, s, keyID(second), "remaining"); got != 0 {
		t.Fatalf("a second reject paid %d", got)
	}
	fence = claimAndSubmit(t, s, third, id, "lobby")
	run(t, s, workCommand(s, owner, Command{Operation: "work.accept", MessageID: id, Amount: fence}))
	if creditIn(t, s, keyID(third), "remaining") != 300 || creditIn(t, s, keyID(first), "remaining") != 50 || creditIn(t, s, requester, "held") != 0 {
		t.Fatal("accept after a paid reject fee moved the wrong credit")
	}

	h := run(t, s, Command{Operation: "work.history", MessageID: id})
	fee, ok := h.Data["reject_fee"].(*WorkRejectFee)
	if !ok || fee.State != "paid" {
		t.Fatalf("work.history reject_fee: %+v", h.Data["reject_fee"])
	}
	marked := 0
	for _, tr := range h.Data["transitions"].([]WorkTransition) {
		if tr.RejectFee != "" {
			marked++
			if tr.RejectFee != "paid" || tr.Operation != "work.reject" || tr.Fence != 2 {
				t.Fatalf("marked transition: %+v", tr)
			}
		}
	}
	if marked != 1 {
		t.Fatalf("%d transitions marked", marked)
	}
	if err := s.Integrity(testContext); err != nil {
		t.Fatal(err)
	}
}

func TestWorkRejectFeeReturned(t *testing.T) {
	s := rejectFeeStore(t)
	owner, worker := keyFor(205), keyFor(206)
	requester := keyID(owner)
	mintCredit(t, s, requester, allowance.Paid, 1000)

	// Accept: the reward pays and the fee comes back; only its transfer fee stays spent.
	accepted := rewardRequest(t, s, owner, "lobby")
	run(t, s, reviewedCreate(s, owner, accepted, map[string]any{"reward": 300, "reject_fee": 100}, 0))
	before := creditIn(t, s, requester, "remaining")
	fence := claimAndSubmit(t, s, worker, accepted, "lobby")
	run(t, s, workCommand(s, owner, Command{Operation: "work.accept", MessageID: accepted, Amount: fence}))
	w := getTestWork(t, s, accepted)
	if w.RejectFee.State != "released" || w.RejectFee.Reason != "accepted" || w.RejectFee.SettledAt == 0 {
		t.Fatalf("after accept: %+v", w.RejectFee)
	}
	if creditIn(t, s, keyID(worker), "remaining") != 300 || creditIn(t, s, requester, "held") != 0 {
		t.Fatal("accept paid the reject fee")
	}
	if spent := before - creditIn(t, s, requester, "remaining"); spent != 300 {
		t.Fatalf("accept cost the requester %d", spent)
	}
	h := run(t, s, Command{Operation: "work.history", MessageID: accepted}).Data["transitions"].([]WorkTransition)
	if last := h[len(h)-1]; last.Operation != "work.accept" || last.RejectFee != "released" {
		t.Fatalf("accept transition: %+v", last)
	}

	// Cancel while open: returned.
	open := rewardRequest(t, s, owner, "lobby")
	run(t, s, reviewedCreate(s, owner, open, map[string]any{"reward": 10, "reject_fee": 5}, 0))
	run(t, s, workCommand(s, owner, Command{Operation: "work.cancel", MessageID: open, Reason: "not needed"}))
	if w = getTestWork(t, s, open); w.RejectFee.State != "released" || w.RejectFee.Reason != "cancelled" {
		t.Fatalf("after cancel: %+v", w.RejectFee)
	}

	// Cancel while a result waits: the worker is paid, as for a reject.
	waiting := rewardRequest(t, s, owner, "lobby")
	run(t, s, reviewedCreate(s, owner, waiting, map[string]any{"reward": 10, "reject_fee": 5}, 0))
	claimAndSubmit(t, s, worker, waiting, "lobby")
	run(t, s, workCommand(s, owner, Command{Operation: "work.cancel", MessageID: waiting, Reason: "dodging"}))
	if w = getTestWork(t, s, waiting); w.RejectFee.State != "paid" || w.RejectFee.Reason != "cancelled_after_submit" {
		t.Fatalf("after cancel with a result: %+v", w.RejectFee)
	}
	if got := creditIn(t, s, keyID(worker), "remaining"); got != 305 {
		t.Fatalf("worker holds %d", got)
	}
	if held := creditIn(t, s, requester, "held"); held != 0 {
		t.Fatalf("held %d", held)
	}
	if err := s.Integrity(testContext); err != nil {
		t.Fatal(err)
	}
}

func TestWorkRejectFeeAtTheDeadline(t *testing.T) {
	s := rejectFeeStore(t)
	owner, worker, judge := keyFor(207), keyFor(208), keyFor(209)
	requester, reviewer := keyID(owner), keyID(judge)
	register(t, s, judge)
	mintCredit(t, s, requester, allowance.Paid, 1000)
	start := s.now().Unix()

	nobody := rewardRequest(t, s, owner, "lobby")
	run(t, s, reviewedCreate(s, owner, nobody, map[string]any{"reward": 20, "reject_fee": 10}, 3600))
	undecided := rewardRequest(t, s, owner, "lobby")
	run(t, s, reviewedCreate(s, owner, undecided, map[string]any{"reward": 20, "reject_fee": 7}, 3600))
	reviewed := rewardRequest(t, s, owner, "lobby")
	run(t, s, reviewedCreate(s, owner, reviewed, map[string]any{"reward": 20, "reject_fee": 3, "reviewer": reviewer}, 3600))
	claimAndSubmit(t, s, worker, undecided, "lobby")
	claimAndSubmit(t, s, worker, reviewed, "lobby")

	atTime(s, start+3600)
	if _, err := s.SweepAllowance(testContext); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id, state, reason string
	}{{nobody, "released", "expired"}, {undecided, "paid", "requester_lapsed"}, {reviewed, "paid", "review_lapsed"}} {
		if w := getTestWork(t, s, c.id); w.RejectFee.State != c.state || w.RejectFee.Reason != c.reason {
			t.Fatalf("%s at the deadline: %+v", c.reason, w.RejectFee)
		}
	}
	if got := creditIn(t, s, keyID(worker), "remaining"); got != 10 {
		t.Fatalf("lapsed results paid the worker %d", got)
	}
	if held := creditIn(t, s, requester, "held"); held != 0 {
		t.Fatalf("held %d", held)
	}
	// A second sweep moves nothing more.
	if _, err := s.SweepAllowance(testContext); err != nil {
		t.Fatal(err)
	}
	if got := creditIn(t, s, keyID(worker), "remaining"); got != 10 {
		t.Fatalf("a second sweep paid %d", got)
	}
	if err := s.Integrity(testContext); err != nil {
		t.Fatal(err)
	}
}

func TestWorkRejectFeeRefusals(t *testing.T) {
	s := rejectFeeStore(t)
	owner := keyFor(210)
	requester := keyID(owner)
	mintCredit(t, s, requester, allowance.Paid, 100)
	id := rewardRequest(t, s, owner, "lobby")
	for _, extra := range []map[string]any{
		{"reward": 100, "reject_fee": 51},  // over half the reward
		{"reward": 100, "reject_fee": 0},   // under 1
		{"reject_fee": 1},                  // no credit reward
		{"reward": 100, "reject_fee": "5"}, // not an integer
		{"reward": 100, "reject_fee": nil},
	} {
		fails(t, s, reviewedCreate(s, owner, id, extra, 0), "invalid_work_reward")
	}
	sim := run(t, s, signed(owner, Command{Operation: "post", Kind: "simulation", Text: "lab"})).Receipt.ID
	fails(t, s, reviewedCreate(s, owner, sim, map[string]any{"reward": 10, "reject_fee": 1}, 0), "invalid_work_reward")

	// Not more than the requester holds: nothing is created or held.
	fails(t, s, reviewedCreate(s, owner, id, map[string]any{"reward": 90, "reject_fee": 45}, 0), "not_transferable")
	if held := creditIn(t, s, requester, "held"); held != 0 {
		t.Fatalf("a refused create holds %d", held)
	}
	fails(t, s, Command{Operation: "work.get", MessageID: id}, "not_found")
	// The cap itself is accepted; a reward of 1 allows a fee of 1.
	run(t, s, reviewedCreate(s, owner, id, map[string]any{"reward": 2, "reject_fee": 1}, 0))
	if WorkRejectFeeMax(1) != 1 || WorkRejectFeeMax(100) != 50 || WorkRejectFeeMax(3) != 1 {
		t.Fatal("WorkRejectFeeMax")
	}
}

func TestWorkRejectFeeFlagOff(t *testing.T) {
	s := rewardStore(t)
	owner := keyFor(211)
	mintCredit(t, s, keyID(owner), allowance.Paid, 1000)
	id := rewardRequest(t, s, owner, "lobby")
	for _, v := range []any{10, "x", nil} {
		msg := failsWith(t, s, reviewedCreate(s, owner, id, map[string]any{"reward": 100, "reject_fee": v}, 0), "reject_fee_unavailable")
		if !strings.Contains(msg, "reject_fee is off") {
			t.Fatalf("message %q", msg)
		}
	}
	// Only work.create takes it.
	claim := signed(owner, Command{Operation: "work.claim", MessageID: id, TTL: 600, Data: `{"schema":1,"generation":"` + s.generation + `","reject_fee":1}`, Timestamp: s.now().Unix()})
	fails(t, s, claim, "invalid_work_data")
	run(t, s, reviewedCreate(s, owner, id, map[string]any{"reward": 100}, 0))
	if w := getTestWork(t, s, id); w.RejectFee != nil {
		t.Fatalf("reject_fee without one: %+v", w.RejectFee)
	}
}
