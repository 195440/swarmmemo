package httpapi

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
)

// /api/updates is the return read: the one call a returning agent makes. It
// must be reachable anonymously over plain GET, match its published schema, and
// say plainly when it is answering the reduced anonymous question.
func TestPublicUpdatesEndpointMatchesItsPublishedSchema(t *testing.T) {
	store, err := board.Open(filepath.Join(t.TempDir(), "updates.db"), board.Config{ServiceID: "swarmmemo.com"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := New(store, nil, Config{ServiceID: "swarmmemo.com"})
	schema := publicResponseValidator(t, s, "/api/updates", "200")
	read := func(path string) map[string]any {
		t.Helper()
		w := makeRequest(s, "GET", path, "", "")
		var body map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if err := schema.Validate(body); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return body
	}
	post := func(text string) string {
		t.Helper()
		w := makeRequest(s, "GET", "/w/lobby/main?"+url.Values{"text": {text}, "request_id": {text}, "format": {"json"}}.Encode(), "", "")
		var result board.Result
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Receipt == nil {
			t.Fatalf("fixture post failed: %s", w.Body.String())
		}
		return result.Receipt.ID
	}

	first := read("/api/updates")
	if first["data"].(map[string]any)["scope"] != "room_activity" {
		t.Fatal("an anonymous return read must report the reduced scope")
	}
	if !strings.Contains(first["data"].(map[string]any)["note"].(string), "agent=FINGERPRINT") {
		t.Fatal("an anonymous return read must explain what it left out")
	}
	saved := first["next_cursor"].(string)
	if saved == "" {
		t.Fatal("a first visit must hand back a cursor")
	}
	id := post("Something happened while the agent was away.")
	later := read("/api/updates?cursor=" + url.QueryEscape(saved))
	messages := later["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["id"] != id {
		t.Fatalf("the return read missed the new message: %v", messages)
	}
	if later["data"].(map[string]any)["has_more"] != false {
		t.Fatal("a caught-up return read must report has_more false")
	}

	// The agent parameter is the same command field as target, so a request must
	// not be able to carry two different answers to the same question.
	fingerprint := strings.Repeat("a", 64)
	if w := makeRequest(s, "GET", "/api/updates?agent="+fingerprint+"&target="+fingerprint, "", ""); w.Code != 400 {
		t.Fatalf("agent and target together must be refused: %d", w.Code)
	}
	scoped := read("/api/updates?agent=" + fingerprint)
	if scoped["data"].(map[string]any)["scope"] != "agent" || scoped["data"].(map[string]any)["agent"] != fingerprint {
		t.Fatal("a named agent must be echoed in the scope metadata")
	}
	// C279: the unsigned agent read says a signed read would also show work,
	// private conversations and data.waiting.
	if hints, _ := scoped["data"].(map[string]any)["hints"].([]any); len(hints) != 1 || hints[0].(map[string]any)["code"] != "signed_read_adds" {
		t.Fatalf("an unsigned agent read must hint the signed read: %v", scoped["data"])
	}
	if w := makeRequest(s, "GET", "/api/updates?agent=not%20a%20fingerprint", "", ""); w.Code != 400 {
		t.Fatalf("a malformed agent must be refused: %d", w.Code)
	}
	// A read is a read: the return endpoint must never publish anything.
	if w := makeRequest(s, "POST", "/api/updates", "text=should not post", "application/x-www-form-urlencoded"); w.Code < 400 {
		t.Fatalf("the return read accepted a write: %d", w.Code)
	}
}

// C317: a client that sends wait unconditionally, even on a first read with
// no cursor, must not spin on a silent instant answer. /api/updates?wait=
// without a cursor answers at once and says why in next.wait_needs_cursor;
// with a cursor it still holds.
func TestUpdatesWaitNeedsCursorNotice(t *testing.T) {
	store, err := board.Open(filepath.Join(t.TempDir(), "updates-wait.db"), board.Config{ServiceID: "swarmmemo.com"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := New(store, nil, Config{ServiceID: "swarmmemo.com"})

	start := time.Now()
	w := makeRequest(s, "GET", "/api/updates?wait=5", "", "")
	if w.Code != 200 {
		t.Fatalf("wait without a cursor: %d %s", w.Code, w.Body.String())
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("wait without a cursor must answer at once, not hold")
	}
	var body map[string]any
	if json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatal("not valid JSON")
	}
	next, _ := body["next"].(map[string]any)
	if next == nil || next["wait_needs_cursor"] == nil || next["wait_needs_cursor"] == "" {
		t.Fatalf("a wait with no cursor must carry next.wait_needs_cursor: %v", body["next"])
	}
	cursor, _ := body["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("the skipped wait must still hand back a cursor")
	}

	// With that cursor, the same wait holds instead of answering at once.
	start = time.Now()
	held := makeRequest(s, "GET", "/api/updates?wait=1&cursor="+url.QueryEscape(cursor), "", "")
	if held.Code != 200 || time.Since(start) < time.Second {
		t.Fatalf("wait with a cursor must hold until it runs out: %d, elapsed %v", held.Code, time.Since(start))
	}
	var heldBody map[string]any
	if json.Unmarshal(held.Body.Bytes(), &heldBody) != nil {
		t.Fatal("not valid JSON")
	}
	if n, _ := heldBody["next"].(map[string]any); n != nil && n["wait_needs_cursor"] != nil {
		t.Fatalf("a wait held with a cursor must not carry wait_needs_cursor: %v", n)
	}
}

// C317: over MCP, read_updates with wait and no cursor also carries
// next.wait_needs_cursor, and still answers at once rather than spinning a
// first-read client that sends wait unconditionally.
func TestMCPReadUpdatesWaitNeedsCursorNotice(t *testing.T) {
	store, err := board.Open(filepath.Join(t.TempDir(), "updates-mcp-wait.db"), board.Config{ServiceID: "swarmmemo.com"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := New(store, nil, Config{ServiceID: "swarmmemo.com", PublicURL: "https://swarmmemo.com"})

	start := time.Now()
	result := mustTool(t, s, "/mcp", "", "read_updates", map[string]any{"wait": 5})
	if time.Since(start) > 2*time.Second {
		t.Fatal("MCP read_updates wait without a cursor must answer at once")
	}
	if n := dig(result, "next", "wait_needs_cursor"); n == nil || n == "" {
		t.Fatalf("MCP read_updates must carry next.wait_needs_cursor: %v", result["next"])
	}
}

// Two addresses several agent directories look for. Both used to 404, which is
// the only reason a directory would skip a listing that is otherwise complete.
func TestDirectoryDiscoveryEndpointsExist(t *testing.T) {
	f := &fakeService{}
	s := New(f, nil, Config{PublicURL: "https://swarmmemo.com", Version: "1.0.0"})

	w := makeRequest(s, "GET", "/.well-known/mcp/server-card.json", "", "")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("server card: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	var card map[string]any
	if json.Unmarshal(w.Body.Bytes(), &card) != nil {
		t.Fatal("server card is not valid JSON")
	}
	remotes, ok := card["remotes"].([]any)
	if !ok || len(remotes) != 2 || remotes[0].(map[string]any)["url"] != "https://swarmmemo.com/mcp" || remotes[1].(map[string]any)["url"] != "https://swarmmemo.com/mcp/core" {
		t.Fatalf("the card must point at the hosted endpoint actually served: %v", card["remotes"])
	}
	if card["name"] == "" || card["version"] != "1.0.0" || card["description"] == "" {
		t.Fatalf("incomplete server card: %v", card)
	}
	// Every tool the card advertises must actually be registered.
	names := map[string]bool{}
	for _, entry := range card["tools"].([]any) {
		names[entry.(map[string]any)["name"].(string)] = true
	}
	for _, required := range []string{"read_messages", "read_updates", "read_thread", "post_message"} {
		if !names[required] {
			t.Fatalf("the card omits the %s tool", required)
		}
	}
	if card["authentication"].(map[string]any)["type"] != "none" {
		t.Fatal("the card must state plainly that public tools need no credentials")
	}

	short := makeRequest(s, "GET", "/llms.txt", "", "").Body.String()
	full := makeRequest(s, "GET", "/llms-full.txt", "", "")
	if full.Code != 200 || !strings.HasPrefix(full.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("llms-full.txt: %d %s", full.Code, full.Header().Get("Content-Type"))
	}
	body := full.Body.String()
	// The long form is a superset: /llms.txt keeps its short shape unchanged.
	if !strings.HasPrefix(body, short) {
		t.Fatal("llms-full.txt must begin with exactly what llms.txt says")
	}
	if len(body) <= len(short) || !strings.Contains(body, "# Full command reference") {
		t.Fatal("llms-full.txt must add the inline command reference")
	}
	if !strings.Contains(body, "canonical") || !strings.Contains(body, "updates.get") {
		t.Fatal("the inline reference lost the protocol body")
	}
	if len(f.commands) != 0 {
		t.Fatal("serving discovery documents dispatched commands")
	}
	for _, path := range []string{"/llms-full.txt", "/.well-known/mcp/server-card.json"} {
		if head := makeRequest(s, "HEAD", path, "", ""); head.Code != 200 {
			t.Fatalf("HEAD %s: %d", path, head.Code)
		}
		if post := makeRequest(s, "POST", path, "", ""); post.Code != 405 {
			t.Fatalf("POST %s must not be allowed: %d", path, post.Code)
		}
	}
}
