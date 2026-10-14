package web

import (
	"html"
	"net/http/httptest"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// threadPage renders /e/ID for one root post.
func threadPage(t *testing.T, root board.Message) string {
	t.Helper()
	s := &testService{execute: func(c board.Command) (board.Result, error) {
		if c.Operation == "room.get" {
			return board.Result{OK: true, Room: &board.Room{Name: "lobby", Visibility: "public"}}, nil
		}
		return board.Result{OK: true, Messages: []board.Message{root}, Data: map[string]any{"root_id": root.ID}}, nil
	}}
	w := httptest.NewRecorder()
	Handler(s).ServeHTTP(w, httptest.NewRequest("GET", "/e/"+root.ID, nil))
	if w.Code != 200 {
		t.Fatalf("GET /e/%s: %d", root.ID, w.Code)
	}
	return w.Body.String()
}

// C324: a post sent without a format whose text is Markdown that renders
// without loss shows as Markdown, with a view raw toggle holding the text
// exactly as sent; text that would lose a character stays plain.
func TestAutoMarkdownPostsShowWithViewRaw(t *testing.T) {
	audit := "## FlapJax audit\n\n| Check | Result |\n|---|---|\n| **Auth** | pass, `ed25519` |\n| Limits | fail |\n\n- fix the limiter\n- re-run"
	key := strings.Repeat("ab", 16)
	post := func(text, format, display string, signed bool) board.Message {
		m := board.Message{ID: "0296f1f0bbbbbbbbbbbbbbbbbbbbbbbb", Room: "lobby", Page: "main", Kind: "note", Author: "anonymous", Visibility: "public", Type: "message", CreatedAt: 1, Text: text, Format: format, DisplayFormat: display}
		if signed {
			m.Author, m.PublicKey = key, key
		}
		return m
	}
	raw := `<details class="memo-raw"><summary>view raw</summary><pre>`
	for _, tc := range []struct {
		name     string
		m        board.Message
		markdown bool
	}{
		{"auto, set on read", post(audit, "", board.PostFormatMarkdown, true), true},
		{"auto, decided by the page", post(audit, "", "", true), true},
		{"auto, unsigned", post(audit, "", "", false), true},
		{"signed format", post("plain words", board.PostFormatMarkdown, "", true), true},
		{"lossy", post("## Math\n\n2*3*4 = 24", "", "", true), false},
		{"read says plain", post(audit, "", board.DisplayPlain, true), false},
		{"unsigned labelled link", post("- [docs](https://example.com/d)\n- two", "", "", false), false},
	} {
		body := threadPage(t, tc.m)
		if got := strings.Contains(body, raw); got != tc.markdown {
			t.Errorf("%s: view raw %v, want %v", tc.name, got, tc.markdown)
		}
		if got := strings.Contains(body, `<div class="memo-text md`); got != tc.markdown {
			t.Errorf("%s: rendered as Markdown %v, want %v", tc.name, got, tc.markdown)
		}
		if !tc.markdown {
			continue
		}
		if !strings.Contains(body, raw+html.EscapeString(tc.m.Text)+"</pre></details>") {
			t.Errorf("%s: view raw does not hold the text exactly as sent", tc.name)
		}
		if tc.m.Text == audit && (!strings.Contains(body, "<table>") || !strings.Contains(body, "<strong>Auth</strong>") || !strings.Contains(body, "<code>ed25519</code>")) {
			t.Errorf("%s: the audit's table, bold and code are not rendered", tc.name)
		}
	}
}

// A storefront or subject review shown as Markdown renders it; a plain one
// stays text.
func TestReviewBodies(t *testing.T) {
	if got := string(reviewBody("**good** and *fast*", board.PostFormatMarkdown)); !strings.Contains(got, "<strong>good</strong>") {
		t.Errorf("markdown review body %q", got)
	}
	if got := string(reviewBody("**good** and *fast*", board.DisplayPlain)); strings.Contains(got, "<strong>") {
		t.Errorf("plain review body %q", got)
	}
	if got := string(reviewExcerpt("**good** <b>", board.PostFormatMarkdown)); !strings.Contains(got, "<strong>good</strong>") || strings.Contains(got, "<b>") {
		t.Errorf("markdown review excerpt %q", got)
	}
	if got := string(reviewExcerpt("**good** <b>", board.DisplayPlain)); got != "**good** &lt;b&gt;" {
		t.Errorf("plain review excerpt %q", got)
	}
}
