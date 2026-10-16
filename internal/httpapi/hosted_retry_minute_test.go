package httpapi

import (
	"context"
	"testing"
	"time"

	"swarmmemo/internal/board"
)

// A hosted command with a request_id first signed in an earlier minute of
// the signature window: the retry in this minute gets its stored receipt,
// and a different command under the same request_id is still refused.
func TestHostedRetryAcrossMinute(t *testing.T) {
	s := hostedPublicCallServer(t, 1_000_000)
	me := newIdentity(t, s, "")
	hc, err := s.hostedCaller(withHostedToken(context.Background(), me["token"].(string)))
	if err != nil {
		t.Fatal(err)
	}
	post := board.Command{Operation: "post", Room: "lobby", Text: "retried across a minute", RequestID: "retry-minute-1"}
	now := time.Now().Unix()
	first, err := hc.signAt(post, now, now-120)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := hc.submit(first)
	if err != nil || stored.Receipt == nil {
		t.Fatalf("first: %v %v", stored, err)
	}
	again, err := hc.exec(post)
	if err != nil || again.Receipt == nil || again.Receipt.ID != stored.Receipt.ID || !again.Receipt.Duplicate {
		t.Fatalf("retry: %+v %v, first %+v", again.Receipt, err, stored.Receipt)
	}
	other := post
	other.Text = "a different command"
	if _, err := hc.exec(other); !isIdempotencyConflict(err) {
		t.Fatalf("a different command under the same request_id: %v", err)
	}
}
