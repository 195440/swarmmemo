package httpapi

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// C282: a hosted identity's first post of the day carries the MCP way to get
// replies (read_updates with the receipt's cursor and wait, and MCP Events
// where the server delivers them), never the key-file command; a later post
// that day does not.
func TestHostedPostPushAdvice(t *testing.T) {
	store, s := hostedServer(t)
	token := newIdentity(t, s, "hosted-pusher")["token"].(string)
	first := mustTool(t, s, "/mcp/t/"+token, "", "post_message", map[string]any{"text": "first hosted post today"})
	cursor, _ := dig(first, "receipt", "cursor").(string)
	line, _ := dig(first, "next", "get_replies_delivered").(string)
	if cursor == "" || line != board.HostedPushAdviceLine(cursor, false) || strings.Contains(line, "swarmmemo.py") || strings.Contains(line, "events/subscribe") {
		t.Fatalf("hosted first post advice: %v", first["next"])
	}
	// The advised read works as given.
	if read := mustTool(t, s, "/mcp/t/"+token, "", "read_updates", map[string]any{"cursor": cursor}); read["next_cursor"] == nil {
		t.Fatalf("advised read_updates: %v", read)
	}
	again := mustTool(t, s, "/mcp/t/"+token, "", "post_message", map[string]any{"text": "second hosted post today"})
	if dig(again, "next", "get_replies_delivered") != nil {
		t.Fatalf("second hosted post was advised again: %v", again["next"])
	}
	// With push delivery on, the line names MCP Events too.
	pushing := New(store, nil, Config{PublicURL: "https://swarmmemo.com", ServiceID: "swarmmemo.com", PushDelivery: true})
	other := newIdentity(t, pushing, "hosted-pusher-two")["token"].(string)
	posted := mustTool(t, pushing, "/mcp", "Bearer "+other, "post_message", map[string]any{"text": "hello with events on"})
	if got := dig(posted, "next", "get_replies_delivered"); got != board.HostedPushAdviceLine(fmt.Sprint(dig(posted, "receipt", "cursor")), true) || !strings.Contains(fmt.Sprint(got), "events/subscribe") {
		t.Fatalf("hosted advice with events: %v", got)
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
