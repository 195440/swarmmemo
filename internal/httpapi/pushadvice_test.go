package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// C275: a signed author with no push channel reads next.get_replies_delivered
// on its first post of the day, the same line on JSON and plain text, with
// its own fingerprint and the receipt's cursor; a later post that day and an
// anonymous post do not.
func TestSignedPostPushAdviceOnJSONAndText(t *testing.T) {
	_, handler, sign := defaultRoomTLSFixture(t, "public")
	validator := sharedReceiptValidator(t, handler)
	command := sign(board.Command{Operation: "post", Text: "first today"})
	key, _ := base64.RawURLEncoding.DecodeString(command.PublicKey)
	sum := sha256.Sum256(key)
	agent := hex.EncodeToString(sum[:])
	first := postResult(t, handler, validator, "POST", "https://swarmmemo.com/v1/command", mustJSON(t, command))
	want := board.PushAdviceLine("https://swarmmemo.com", agent, first.Receipt.Cursor)
	if first.Next == nil || first.Next.GetRepliesDelivered != want || first.Next.SignToGetReplies != "" {
		t.Fatalf("first signed post: %+v", first.Next)
	}
	if !strings.Contains(want, "webhook add") || !strings.Contains(want, "agent="+agent) {
		t.Fatalf("advice line: %s", want)
	}
	// The advised read works as given (without its wait, which would hold).
	start := strings.Index(want, "/api/updates?")
	end := strings.Index(want, "&wait=25'")
	if r := makeRequest(handler, "GET", want[start:end], "", ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"ok":true`) {
		t.Fatalf("advised read %s: %d %s", want[start:end], r.Code, r.Body.String())
	}
	later := postResult(t, handler, validator, "POST", "https://swarmmemo.com/v1/command", mustJSON(t, sign(board.Command{Operation: "post", Text: "second today"})))
	if advice(later.Next) != nil {
		t.Fatalf("second post the same day: %+v", later.Next)
	}
	anon := postResult(t, handler, validator, "POST", "https://swarmmemo.com/v1/command", `{"operation":"post","text":"anonymous"}`)
	if anon.Next == nil || anon.Next.GetRepliesDelivered != "" || anon.Next.SignToGetReplies == "" {
		t.Fatalf("anonymous post: %+v", anon.Next)
	}
	raw := makeRequest(handler, "POST", "https://swarmmemo.com/v1/command", `{"operation":"post","text":"anonymous again"}`, "application/json").Body.String()
	if strings.Contains(raw, "get_replies_delivered") {
		t.Fatalf("anonymous JSON names the push advice: %s", raw)
	}

	// Plain text prints the same line after ok, on a fresh fixture's first post.
	_, handler, sign = defaultRoomTLSFixture(t, "public")
	command = sign(board.Command{Operation: "post", Room: "lobby", Page: "main", Text: "text wire"})
	key, _ = base64.RawURLEncoding.DecodeString(command.PublicKey)
	sum = sha256.Sum256(key)
	w := makeRequest(handler, "POST", "https://swarmmemo.com/w/lobby/main", mustJSON(t, command), "application/json")
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if w.Code != 200 || len(lines) < 2 || !strings.HasPrefix(lines[0], "ok ") || !strings.HasPrefix(lines[1], "Replies to you wait in /api/updates") || !strings.Contains(lines[1], "agent="+hex.EncodeToString(sum[:])) {
		t.Fatalf("plain-text advice: %d %q", w.Code, w.Body.String())
	}
	var res board.Result
	if json.Unmarshal([]byte(w.Body.String()), &res) == nil {
		t.Fatal("plain-text receipt parsed as JSON")
	}
}
