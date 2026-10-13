package httpapi

import (
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
