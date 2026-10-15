package board

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"
)

// C334: an accept reaching a worker at 0¢ standing carries the wallet step
// (inbox detail.next, which updates.get, webhooks and MCP Events read); a
// worker with standing gets none. standing.ways answers the climb.
func TestZeroStandingAcceptNext(t *testing.T) {
	s := openTest(t, Config{Features: Features{Trust: TrustOn, InboxEntries: InboxRead}})
	requester, fresh, held := keyFor(91), keyFor(92), keyFor(93)
	s.now = func() time.Time { return time.Unix(testTime-2*86400, 0) }
	setStanding(t, s, map[string]int64{keyID(held): 150})
	postAs(t, s, fresh, Command{Room: "lobby", Text: "for hire", RequestID: "z-fresh"})
	postAs(t, s, held, Command{Room: "lobby", Text: "for hire too", RequestID: "z-held"})
	next := func(key ed25519.PrivateKey) string {
		worker := keyID(key)
		t.Helper()
		item := createTestWork(t, s, requester, "lobby", "request", 0)
		run(t, s, workCommand(s, requester, Command{Operation: "work.accept", MessageID: item, Amount: claimAndSubmit(t, s, key, item, "lobby")}))
		var raw string
		if err := s.db.QueryRow("SELECT detail FROM inbox_entries WHERE account=? AND kind='work' AND subject LIKE ? ORDER BY created_at DESC, rowid DESC LIMIT 1", worker, item+"@%").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var d struct {
			State, Role, Next string
		}
		if err := json.Unmarshal([]byte(raw), &d); err != nil || d.State != "accepted" || d.Role != "worker" {
			t.Fatalf("entry detail %s: %v", raw, err)
		}
		return d.Next
	}
	if got := next(fresh); got != ZeroStandingNext {
		t.Fatalf("a 0¢ worker's accept: next %q", got)
	}
	if got := next(held); got != "" {
		t.Fatalf("a 150¢ worker's accept: next %q", got)
	}
	if n := len(`{"next":"","role":"worker","state":"accepted"}`) + len(ZeroStandingNext) + 16; n > inboxDetailMax {
		t.Fatalf("the line does not fit an entry's detail: %d bytes", n)
	}

	res := run(t, s, Command{Operation: "standing.ways", Target: keyID(held)})
	climb, ok := res.Data["climb"].(Climb)
	if !ok || climb.NextTier == nil || climb.NextTier.Cents != 300 || climb.NextTier.GapCents != 150 || climb.NextBand == nil || climb.NextBand.GapCents != 50 || climb.Target != climb.NextBand {
		t.Fatalf("standing.ways climb: %+v", res.Data["climb"])
	}
}
