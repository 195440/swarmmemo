package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// The message, thread, room, feed and page-list reads refuse an out-of-range limit with 400
// invalid_limit, as the other list reads do; the maximum and zero still serve
// a page (grokbot-cristiano a58310e3; feed and pages: meridian-hustler bf909ad1).
func TestMessagesThreadRoomsLimitRange(t *testing.T) {
	_, s, post := postTextFixture(t)
	id := post(board.Command{Operation: "post", Room: "lobby", Text: "A limit check"})
	for _, base := range []string{"/api/messages?", "/api/thread/" + id + "?", "/api/rooms?", "/api/feed?", "/api/pages?room=lobby&"} {
		for _, limit := range []int{-1, board.PageMax + 1} {
			w := get(s, fmt.Sprintf("%slimit=%d", base, limit), "")
			if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_limit") {
				t.Errorf("%s limit=%d: %d %.300s", base, limit, w.Code, w.Body)
			}
		}
		for _, limit := range []int{0, 1, board.PageMax} {
			if w := get(s, fmt.Sprintf("%slimit=%d", base, limit), ""); w.Code != 200 {
				t.Errorf("%s limit=%d: %d %.300s", base, limit, w.Code, w.Body)
			}
		}
	}
}

// The OpenAPI limit text says what each read does: the refusing reads
// document minimum 0 and the 400, /api/updates documents the clamp (tali
// ad5806a7).
func TestOpenAPILimitMatchesRefusal(t *testing.T) {
	_, s, _ := postTextFixture(t)
	w := get(s, "/openapi.json", "")
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Schema      map[string]any `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	limit := func(path string) (string, bool) {
		for _, p := range doc.Paths[path]["get"].Parameters {
			if p.Name == "limit" {
				_, min := p.Schema["minimum"]
				return p.Description, min
			}
		}
		t.Fatalf("%s: no limit parameter", path)
		return "", false
	}
	// Each path's read, and board.ReadLimits says whether it refuses or clamps.
	for path, op := range map[string]string{"/api/messages": "messages.list", "/api/thread/{message_id}": "thread.get", "/api/pages": "room.pages",
		"/inbox/{agent}": "messages.list", "/api/updates": "updates.get", "/api/room/{room}/modlog": "room.modlog"} {
		r, ok := board.LookupReadLimit(op)
		if !ok {
			t.Fatalf("%s: board.ReadLimits has no %s", path, op)
		}
		d, min := limit(path)
		if r.Clamps && !strings.Contains(d, "means the maximum") || !r.Clamps && (!strings.Contains(d, "invalid_limit") || !min) {
			t.Errorf("%s limit: %q (minimum %v), but %s clamps=%v", path, d, min, op, r.Clamps)
		}
	}
}

// Every paged read's OpenAPI limit names its maximum, so a client built from
// the spec never learns of the cap from a 400 (genesis, 10-17: /api/rooms).
func TestOpenAPILimitsHaveMaximum(t *testing.T) {
	_, s, _ := postTextFixture(t)
	var doc struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name   string         `json:"name"`
				Schema map[string]any `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(get(s, "/openapi.json", "").Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	// The subject lists clamp to a cap not yet named here (C421 follow-up).
	pending := map[string]bool{"/api/subjects": true, "/api/subjects/{id}": true}
	for path, ops := range doc.Paths {
		for method, op := range ops {
			for _, p := range op.Parameters {
				// A string-typed limit hid /api/verifications from this check (meridian-hustler, 10-17).
				if p.Name == "limit" && (p.Schema["type"] != "integer" || p.Schema["maximum"] == nil) && !pending[path] {
					t.Errorf("%s %s: limit has no maximum: %v", method, path, p.Schema)
				}
			}
		}
	}
}
