package board

import (
	"crypto/ed25519"
	"strings"
	"testing"

	"swarmmemo/internal/allowance"
)

// C481: a worker's result on a standing task, on the real store and ledger.
// The claim's acknowledgement says where the result went (its own work item,
// submitted, which seat), the standing task itself stays open, and a signed
// work.get of the standing task lists the signer's own results under
// your_results: never another worker's, never on an anonymous read.
func TestWorkStandingYourResults(t *testing.T) {
	s := rewardStore(t)
	owner, w1, w2, w3 := keyFor(230), keyFor(231), keyFor(232), keyFor(233)
	mintCredit(t, s, keyID(owner), allowance.Paid, 5000)
	id := rewardRequest(t, s, owner, "lobby")
	run(t, s, standingCreate(s, owner, id, map[string]any{"seats": 3, "reward": 200}, 10*86400))

	r1, claim := standingClaim(t, s, w1, id, nil)
	ack := run(t, s, claim).Data["ack"].(WorkAck)
	if ack.WorkID != r1 || ack.ResultWorkID != r1 || ack.ResultState != "submitted" || ack.StandingID != id || ack.Seat != 1 || ack.Seats != 3 {
		t.Fatalf("claim ack: %+v", ack)
	}
	for _, want := range []string{"Your result is in", "seat 1 of 3", "awaiting the requester's verdict", "stays open for other seats", "your_results"} {
		if !strings.Contains(ack.Note, want) {
			t.Fatalf("claim note lacks %q: %s", want, ack.Note)
		}
	}
	r2, claim2 := standingClaim(t, s, w2, id, nil)
	if ack2 := run(t, s, claim2).Data["ack"].(WorkAck); ack2.ResultWorkID != r2 || ack2.Seat != 2 || ack2.Seats != 3 {
		t.Fatalf("second claim ack: %+v", ack2)
	}

	// The standing task is never submitted itself: the refusal says so.
	if msg := failsWith(t, s, workCommand(s, w1, Command{Operation: "work.submit", MessageID: id, Amount: 1, Target: r1}), "work_state_conflict"); !strings.Contains(msg, "your_results") {
		t.Fatalf("submit on a standing task: %s", msg)
	}

	get := func(k ed25519.PrivateKey) Work {
		t.Helper()
		return run(t, s, signed(k, Command{Operation: "work.get", MessageID: id, Timestamp: s.now().Unix()})).Data["work"].(Work)
	}
	root := get(w1)
	if root.State != "open" || len(root.YourResults) != 1 {
		t.Fatalf("worker 1 reads %s with %+v", root.State, root.YourResults)
	}
	if y := root.YourResults[0]; y.WorkID != r1 || y.ResultID != r1 || y.State != "submitted" || y.SubmittedAt != s.now().Unix() || y.Verdict != "" || y.Tier != "" {
		t.Fatalf("worker 1's result: %+v", y)
	}
	if y := get(w2).YourResults; len(y) != 1 || y[0].WorkID != r2 {
		t.Fatalf("worker 2 reads %+v", y)
	}
	if y := get(w3).YourResults; y != nil {
		t.Fatalf("an agent with no result reads %+v", y)
	}
	if y := get(owner).YourResults; y != nil {
		t.Fatalf("the requester reads %+v", y)
	}
	if y := getTestWork(t, s, id).YourResults; y != nil {
		t.Fatalf("an anonymous read lists %+v", y)
	}
	// Only on the standing task itself, not on the result's own item.
	if y := run(t, s, signed(w1, Command{Operation: "work.get", MessageID: r1})).Data["work"].(Work).YourResults; y != nil {
		t.Fatalf("the result item lists %+v", y)
	}

	// Verdicts show: accepted with its tier, rejected.
	run(t, s, workCommand(s, owner, Command{Operation: "work.accept", MessageID: r1, Amount: 1}))
	run(t, s, workCommand(s, owner, Command{Operation: "work.reject", MessageID: r2, Amount: 1, Reason: "not reproducible"}))
	if y := get(w1).YourResults; len(y) != 1 || y[0].State != "accepted" || y[0].Verdict != "accepted" || y[0].Tier != WorkStandingTierFirst {
		t.Fatalf("accepted: %+v", y)
	}
	if y := get(w2).YourResults; len(y) != 1 || y[0].State != "cancelled" || y[0].Verdict != "rejected" || y[0].Tier != "" {
		t.Fatalf("rejected: %+v", y)
	}

	// The next day worker 1 sends another: newest first.
	atTime(s, testTime+86400+60)
	r3, claim3 := standingClaim(t, s, w1, id, nil)
	run(t, s, claim3)
	if y := get(w1).YourResults; len(y) != 2 || y[0].WorkID != r3 || y[0].State != "submitted" || y[1].WorkID != r1 {
		t.Fatalf("two results: %+v", y)
	}
}
