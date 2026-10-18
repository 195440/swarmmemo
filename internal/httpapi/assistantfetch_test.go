package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
	"swarmmemo/internal/web"
)

const (
	claudeUserUA  = "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Claude-User/1.0; +Claude-User@anthropic.com)"
	chatgptUserUA = "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko); compatible; ChatGPT-User/1.0; +https://openai.com/bot"
)

// Only a GET/HEAD of a landing page by a user-initiated assistant fetcher
// adds a fetch key, folded to the page's shape; crawlers, browsers, writes
// and other paths add none.
func TestAssistantFetchKeyClassifiesAndFolds(t *testing.T) {
	for _, c := range []struct{ method, path, ua, want string }{
		{"GET", "/llms.txt", claudeUserUA, "fetch:Claude-User:/llms.txt"},
		{"HEAD", "/e/01HZX3ABCDEF?ref=x&q=secret", chatgptUserUA, "fetch:ChatGPT-User:/e/{id}"},
		{"GET", "/agent/9f86d081884c7d65/trust", "Mozilla/5.0 (compatible; Perplexity-User/1.0)", "fetch:Perplexity-User:/agent/{id}/trust"},
		{"GET", "/r/lobby", "meta-externalfetcher/1.1", "fetch:meta-externalfetcher:/r/{room}"},
		{"GET", "/for/chatgpt", "MistralAI-User/1.0", "fetch:MistralAI-User:/for/chatgpt"},
		{"GET", "/llms.txt", "Mozilla/5.0 (compatible; ClaudeBot/1.0)", ""},
		{"GET", "/llms.txt", "Mozilla/5.0 (compatible; GPTBot/1.0)", ""},
		{"GET", "/llms.txt", "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0", ""},
		{"GET", "/api/messages", claudeUserUA, ""},
		{"POST", "/llms.txt", claudeUserUA, ""},
	} {
		r := httptestRequest(c.method, c.path, "")
		r.Header.Set("User-Agent", c.ua)
		got := assistantFetchKey(r, userAgentFamily(c.ua))
		if got != c.want {
			t.Errorf("%s %s by %q: %q, want %q", c.method, c.path, c.ua, got, c.want)
		}
	}
	for _, family := range board.AssistantFetchFamilies {
		if !strings.Contains(strings.Join(board.UserAgentFamilies, " "), family) {
			t.Errorf("%s is not a counted User-Agent family", family)
		}
	}
}

// A flood of distinct fetched page shapes cannot grow memory: past the bound, a
// family's new paths count as its fetch-other until the next write.
func TestAssistantFetchMemoryIsBounded(t *testing.T) {
	s := New(&fakeService{}, nil, Config{})
	for i := 0; i < 2*referrerPendingFetch; i++ {
		word := string(rune('a'+i%26)) + string(rune('a'+i/26%26)) // a fixed-word segment, kept by LandingKey
		r := httptestRequest("GET", "/e/1/"+word, "")
		r.Header.Set("User-Agent", claudeUserUA)
		s.countReferrer(r)
	}
	r := httptestRequest("GET", "/llms.txt", "")
	r.Header.Set("User-Agent", chatgptUserUA)
	s.countReferrer(r)
	s.referrers.mu.Lock()
	defer s.referrers.mu.Unlock()
	for _, d := range s.referrers.pending {
		if d.fetches["Claude-User"] != referrerPendingFetch || d.counts["fetch-other:Claude-User"] != int64(referrerPendingFetch) || d.counts["fetch:ChatGPT-User:/llms.txt"] != 1 {
			t.Fatalf("fetches %v, other %d", d.fetches, d.counts["fetch-other:Claude-User"])
		}
	}
}

// End to end on the real engine: assistant fetches are stored as family,
// day, folded path and count only, the operator reads them all, and
// /api/stats/daily and /stats publish the paths from the minimum, page and
// API agreeing.
func TestAssistantFetchesStoredFoldedAndPublished(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fetches.sqlite")
	store, err := board.Open(dbPath, board.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := New(store, web.Handler(store), Config{PublicURL: "https://swarmmemo.com"})
	fetch := func(ua, path string, n int) {
		for i := 0; i < n; i++ {
			r := httptestRequest("GET", path, "https://chatgpt.com/c/private-chat-id")
			r.Header.Set("User-Agent", ua)
			serve(s, r)
		}
	}
	fetch(claudeUserUA, "/llms.txt?utm_source=secret-query", 4)
	fetch(claudeUserUA, "/e/01HZXSECRETID01", 2)
	fetch(claudeUserUA, "/e/01HZXSECRETID02", 1)
	fetch(claudeUserUA, "/api/messages", 2) // not a landing page
	fetch(chatgptUserUA, "/for-agents", 1)
	fetch("Mozilla/5.0 (compatible; ClaudeBot/1.0)", "/llms.txt", 5)
	s.FlushReaderCounts()

	days, err := store.ReadReferrerStats(context.Background(), time.Now(), 1)
	if err != nil {
		t.Fatal(err)
	}
	got := days[0].Assistants
	if len(got) != 2 || got[0].Family != "ChatGPT-User" || got[1].Family != "Claude-User" {
		t.Fatalf("families: %+v", got)
	}
	if claude := got[1]; len(claude.Paths) != 2 || claude.Paths[0] != (board.ReferrerCount{Name: "/llms.txt", Count: 4}) || claude.Paths[1] != (board.ReferrerCount{Name: "/e/{id}", Count: 3}) || claude.Other != 0 {
		t.Fatalf("Claude-User: %+v", claude)
	}
	// Nothing but the folded paths reached storage.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	rows, err := raw.Query("SELECT scope FROM counters WHERE scope LIKE 'referrer:%fetch%'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var scope string
		if err = rows.Scan(&scope); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(scope, "SECRET") || strings.Contains(scope, "secret") || strings.Contains(scope, "?") || strings.Contains(scope, "Mozilla") || strings.Contains(scope, "api") {
			t.Fatalf("stored more than a folded path: %q", scope)
		}
	}

	// The API: written counts, each path from the minimum, the rest in other.
	var body struct {
		Daily []struct {
			Day              string `json:"day"`
			AssistantFetches map[string]struct {
				Paths map[string]int64 `json:"paths"`
				Other int64            `json:"other"`
			} `json:"assistant_fetches"`
		} `json:"daily"`
	}
	w := makeRequest(s, "GET", "/api/stats/daily?days="+strconv.Itoa(web.ClientStatsDays), "", "")
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 {
		t.Fatalf("%d %v", w.Code, err)
	}
	today := body.Daily[len(body.Daily)-1]
	if c := today.AssistantFetches["Claude-User"]; len(c.Paths) != 2 || c.Paths["/llms.txt"] != 4 || c.Paths["/e/{id}"] != 3 || c.Other != 0 {
		t.Fatalf("API Claude-User: %+v", c)
	}
	if c := today.AssistantFetches["ChatGPT-User"]; len(c.Paths) != 0 || c.Other != 1 {
		t.Fatalf("API ChatGPT-User below the minimum: %+v", c)
	}
	if _, crawler := today.AssistantFetches["ClaudeBot"]; crawler || strings.Contains(w.Body.String(), "chatgpt.com") || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatal("API exposes more than assistant fetch paths")
	}

	// The page: the API summed over its days.
	page := getHTML(t, s, "/stats")
	if !strings.Contains(page, "Pages assistants fetch for their users") || strings.Contains(page, "SECRET") {
		t.Fatal("/stats does not show the assistant fetch table")
	}
	want := map[string]int64{}
	for _, d := range body.Daily {
		for family, f := range d.AssistantFetches {
			for path, n := range f.Paths {
				want[family+"."+path] += n
			}
			if f.Other > 0 {
				want[family+".other"] += f.Other
			}
		}
	}
	cells := map[string]int64{}
	for _, c := range pageCells(t, page) {
		family, _, _ := strings.Cut(c[0], ".")
		if board.IsAssistantFetchFamily(family) {
			n, _ := strconv.ParseInt(c[1], 10, 64)
			cells[c[0]] = n
		}
	}
	if len(cells) != len(want) || len(want) != 3 {
		t.Fatalf("page %v, API %v", cells, want)
	}
	for key, n := range want {
		if cells[key] != n {
			t.Fatalf("%s: page %d, API %d", key, cells[key], n)
		}
	}
}
