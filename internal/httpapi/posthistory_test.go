package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// /e/ID/history answers a reader that is not a browser with the versions the
// history page shows, oldest first, under the page's access rules.
func TestPostHistoryJSON(t *testing.T) {
	f := newImageFixture(t)
	v2 := f.post(board.Command{Operation: "post", Room: "lobby", Text: "After the edit", Data: `{"schema":1,"supersedes":"` + f.edit + `"}`})
	type history struct {
		OK       bool            `json:"ok"`
		Messages []board.Message `json:"messages"`
		Data     struct {
			Origin   string `json:"origin"`
			Versions int    `json:"versions"`
			Head     string `json:"head"`
		} `json:"data"`
	}
	read := func(path, accept string) history {
		t.Helper()
		w := request(f.off, "GET", path, accept)
		var h history
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || json.Unmarshal(w.Body.Bytes(), &h) != nil {
			t.Fatalf("%s (%q): %d %s", path, accept, w.Code, w.Body.String())
		}
		return h
	}
	// Either version's ID, a short ID, curl's */* and an explicit JSON
	// Accept all read the same chain.
	for _, c := range []struct{ path, accept string }{
		{"/e/" + f.edit + "/history", "*/*"},
		{"/e/" + v2 + "/history", "application/json"},
		{"/e/" + f.edit[:8] + "/history", ""},
		{"/e/" + v2 + "/history?format=json", "text/html"},
	} {
		h := read(c.path, c.accept)
		if !h.OK || len(h.Messages) != 2 || h.Messages[0].ID != f.edit || h.Messages[1].ID != v2 ||
			h.Messages[0].Text != "Before the edit" || h.Messages[0].SupersededBy != v2 ||
			h.Data.Origin != f.edit || h.Data.Versions != 2 || h.Data.Head != v2 {
			t.Fatalf("%s: %+v", c.path, h)
		}
	}
	if h := read("/e/"+f.public+"/history", ""); len(h.Messages) != 1 || h.Data.Head != f.public {
		t.Fatalf("unedited post: %+v", h)
	}
	if w := request(f.off, "HEAD", "/e/"+f.edit+"/history", ""); w.Code != 200 {
		t.Fatalf("HEAD: %d", w.Code)
	}
	// A removed original, a private room and an unknown ID are not found.
	for _, id := range []string{f.hidden, f.private, strings.Repeat("0", 32)} {
		if w := request(f.off, "GET", "/e/"+id+"/history", ""); w.Code != 404 || !strings.Contains(w.Body.String(), `"not_found"`) {
			t.Fatalf("%s: %d %s", id, w.Code, w.Body.String())
		}
	}
	// A browser still gets the history page.
	w := request(f.off, "GET", "/e/"+f.edit+"/history", "text/html")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), "After the edit") {
		t.Fatalf("page: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
}
