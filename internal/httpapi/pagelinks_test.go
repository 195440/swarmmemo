package httpapi

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"html"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
	"swarmmemo/internal/web"
)

// C265: crawlers followed the Reply link a room page prints
// (/r/ROOM/PAGE?reply=ID&to=KEY#compose) and a thread's ?sub=1 link without
// Accept: text/html, and the JSON read refused the page-only parameter with a
// 91-byte 400 ({"error":{"code":"invalid_request","message":"Unknown query
// parameter: reply"},"ok":false}). Every /r/ and /e/ link the pages print is
// a page to such a reader; an explicit JSON request stays strict.
func TestPageLinksServeReadersWithoutHTMLAccept(t *testing.T) {
	store, err := board.Open(filepath.Join(t.TempDir(), "links.sqlite"), board.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	c := board.Command{Operation: "post", Room: "lobby", Text: "hello"}
	c.PublicKey = base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	c.Timestamp, c.Nonce = time.Now().Unix(), "nonce-c265"
	c.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, board.Canonical("swarmmemo.com", c)))
	res, err := store.Execute(context.Background(), c, "test")
	if err != nil {
		t.Fatal(err)
	}
	id := res.Receipt.ID
	s := New(store, web.Handler(store), Config{PublicURL: "https://swarmmemo.com", ServiceID: "swarmmemo.com"})

	get := func(path, accept string, peer int) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "198.51.100." + string(rune('0'+peer%10)) + ":1234"
		if accept != "" {
			r.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	links := map[string]bool{}
	href := regexp.MustCompile(`href="(/[re]/[^"#]*\?[^"#]*)`)
	for i, page := range []string{"/r/lobby", "/e/" + id} {
		w := get(page, "text/html", i)
		if w.Code != 200 {
			t.Fatalf("%s: %d", page, w.Code)
		}
		for _, m := range href.FindAllStringSubmatch(w.Body.String(), -1) {
			links[html.UnescapeString(m[1])] = true
		}
	}
	for _, want := range []string{"/r/lobby/main?reply=" + id, "/e/" + id + "?sub=1"} {
		found := false
		for link := range links {
			found = found || strings.HasPrefix(link, want)
		}
		if !found {
			t.Fatalf("no page link starting %s among %v", want, links)
		}
	}
	n := 0
	for link := range links {
		for _, accept := range []string{"", "*/*", "text/plain"} {
			n++
			if w := get(link, accept, n); w.Code >= 400 {
				t.Errorf("GET %s Accept %q: %d %s", link, accept, w.Code, w.Body.String())
			}
		}
	}
	// The Reply link is the room page, not a JSON listing filtered by the author.
	if w := get("/r/lobby/main?reply="+id, "*/*", 1); w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("reply link: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	// An explicit JSON read still refuses a parameter it does not know.
	if w := get("/r/lobby/main?reply="+id, "application/json", 2); w.Code != 400 || !strings.Contains(w.Body.String(), "Unknown query parameter: reply") {
		t.Fatalf("JSON read with reply: %d %s", w.Code, w.Body.String())
	}
	if w := get("/r/lobby?reply="+id+"&format=json", "", 3); w.Code != 400 {
		t.Fatalf("format=json read with reply: %d", w.Code)
	}
}
