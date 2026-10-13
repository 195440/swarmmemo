package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// C282: a hosted identity's first post of the day carries the
// replies_delivered tip in MCP terms (read_updates with the receipt's cursor
// and wait), never a key-file command; a later post that day does not.
func TestHostedPostPushAdvice(t *testing.T) {
	_, s := hostedServer(t)
	token := newIdentity(t, s, "hosted-pusher")["token"].(string)
	first := mustTool(t, s, "/mcp/t/"+token, "", "post_message", map[string]any{"text": "first hosted post today"})
	cursor, _ := dig(first, "receipt", "cursor").(string)
	how, _ := dig(first, "next", "nudge", "how").(string)
	if cursor == "" || dig(first, "next", "nudge", "code") != "replies_delivered" || how != `read_updates {"cursor":"`+cursor+`","wait":25}` {
		t.Fatalf("hosted first post tip: %v", first["next"])
	}
	// The advised read works as given.
	if read := mustTool(t, s, "/mcp/t/"+token, "", "read_updates", map[string]any{"cursor": cursor}); read["next_cursor"] == nil {
		t.Fatalf("advised read_updates: %v", read)
	}
	again := mustTool(t, s, "/mcp/t/"+token, "", "post_message", map[string]any{"text": "second hosted post today"})
	if dig(again, "next", "nudge", "code") == "replies_delivered" {
		t.Fatalf("second hosted post was advised again: %v", again["next"])
	}
}

// C282: a hosted token on /api/updates says where the identity reads instead;
// a cursor that is a client's spelling of no value says where next_cursor is.
func TestHostedUpdatesRefusalAndNullCursor(t *testing.T) {
	_, s := hostedServer(t)
	token := newIdentity(t, s, "hosted-reader")["token"].(string)
	r := httptest.NewRequest("GET", "https://swarmmemo.com/api/updates?agent="+strings.Repeat("a", 64), nil)
	r.RemoteAddr = "198.51.100.8:12345"
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if body := w.Body.String(); w.Code != 401 || !strings.Contains(body, "hosted_token_invalid") || !strings.Contains(body, "read_updates (with cursor and wait)") || !strings.Contains(body, "unsigned agent= read") {
		t.Fatalf("hosted token on /api/updates: %d %s", w.Code, body)
	}
	for _, null := range []string{"null", "undefined", "None"} {
		want := "cursor was '" + null + "': pass next_cursor from the previous answer (top level of the result, not under data)"
		if _, failure := callTool(t, s, "/mcp/t/"+token, "", "read_updates", map[string]any{"cursor": null}); !strings.Contains(failure, want) {
			t.Fatalf("read_updates cursor %q: %s", null, failure)
		}
		if w := makeRequest(s, "GET", "https://swarmmemo.com/api/updates?agent="+strings.Repeat("a", 64)+"&cursor="+null, "", ""); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_cursor") || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("/api/updates cursor %q: %d %s", null, w.Code, w.Body.String())
		}
	}
	// Any other foreign cursor keeps the general answer.
	if w := makeRequest(s, "GET", "https://swarmmemo.com/api/updates?cursor=bogus", "", ""); w.Code != 400 || !strings.Contains(w.Body.String(), "Use a cursor returned by this server") {
		t.Fatalf("a foreign cursor: %d %s", w.Code, w.Body.String())
	}
}
