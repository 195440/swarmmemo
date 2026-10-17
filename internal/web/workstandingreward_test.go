package web

import (
	"testing"

	"swarmmemo/internal/board"
)

// C434: a standing task's line says what one result earns, then its seats
// and the pool held while it is open, never the pool as the reward; a task
// with one result reads exactly as before.
func TestWorkLineStandingReward(t *testing.T) {
	yes, no := true, false
	id := webWorkID
	one := []board.WorkStandingTier{{Name: board.WorkStandingTierFirst, Credits: 150}}
	tiers := []board.WorkStandingTier{{Name: "bug", Credits: 500, USDC: "0.2"}, {Name: "bug+fix", Credits: 800, USDC: "0.4"}}
	cases := []struct {
		w           board.MessageWork
		badge, want string
	}{
		{board.MessageWork{ID: id, State: "open", Deadline: 1, Reward: &board.MessageWorkReward{Amount: 450, Unit: "credit"}, Standing: &board.MessageWorkStanding{Seats: 3, Tiers: one}, Claimable: &yes},
			"Paid task", " · 150 credits per result; 3 seats (450 held) · open · due Jan 1 · eligible: open"},
		{board.MessageWork{ID: id, State: "open", Deadline: 1, Reward: &board.MessageWorkReward{Amount: 4000, Unit: "credit"}, Standing: &board.MessageWorkStanding{Seats: 5, Tiers: tiers}, Claimable: &yes},
			"Paid task", " · bug 500 credits + 0.2 USDC, bug+fix 800 credits + 0.4 USDC per result; 5 seats (4,000 held) · open · due Jan 1 · eligible: open"},
		// Closed: the pool is no longer held, so it is left out.
		{board.MessageWork{ID: id, State: "cancelled", Reward: &board.MessageWorkReward{Amount: 1, Unit: "credit"}, Standing: &board.MessageWorkStanding{Seats: 1, Tiers: []board.WorkStandingTier{{Name: "reward", Credits: 1}}}, Claimable: &no},
			"Paid task", " · 1 credit per result; 1 seat · cancelled · eligible: open"},
		// A simulation shows no reward, standing or not.
		{board.MessageWork{ID: id, State: "open", Deadline: 1, Simulated: true, Reward: &board.MessageWorkReward{Amount: 450, Unit: "credit"}, Standing: &board.MessageWorkStanding{Seats: 3, Tiers: one}, Claimable: &yes},
			"Simulated task", " · open · due Jan 1 · eligible: open"},
		// Not standing: unchanged.
		{board.MessageWork{ID: id, State: "open", Deadline: 1, Reward: &board.MessageWorkReward{Amount: 450, Unit: "credit"}, Claimable: &yes},
			"Paid task", " · 450 credits · open · due Jan 1 · eligible: open"},
	}
	for i, c := range cases {
		w := c.w
		got := workLine(board.Message{Work: &w})
		if got == nil || got.Badge != c.badge || got.Detail != c.want {
			t.Errorf("case %d: %+v, want %q %q", i, got, c.badge, c.want)
		}
	}
	held := &board.WorkReward{Amount: 4000, State: "held"}
	st := &board.WorkStanding{WorkStandingSeats: board.WorkStandingSeats{Seats: 5}, Tiers: tiers}
	if got, want := standingReward(st, held), "bug 500 credits + 0.2 USDC, bug+fix 800 credits + 0.4 USDC per result; 5 seats (4,000 held)"; got != want {
		t.Errorf("standingReward = %q, want %q", got, want)
	}
	if got := standingReward(nil, held); got != "" {
		t.Errorf("standingReward without standing = %q", got)
	}
}
