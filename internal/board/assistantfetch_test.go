package board

import (
	"strconv"
	"testing"
	"time"
)

// Assistant fetch rows hold a known assistant family and a folded path
// only: no query, no raw ID shape, no crawler, no User-Agent.
func TestAssistantFetchKeysAcceptOnlyFoldedPaths(t *testing.T) {
	s := openTest(t, Config{})
	day := time.Unix(testTime, 0).UTC().Format("2006-01-02")
	for _, key := range []string{
		"fetch:Claude-User:/e/01HZX?x=1", "fetch:Claude-User:/E/ABC", "fetch:Claude-User:e/{id}", "fetch:Claude-User:",
		"fetch:ClaudeBot:/llms.txt", "fetch:Googlebot:/", "fetch:Mozilla/5.0:/", "fetch:Claude-User:/a b", "fetch:Claude-User:/a//b",
		"fetch:Claude-User:/{ID}", "fetch:Claude-User:/x:y", "fetch:Claude-User:/\n", "fetch-other:ClaudeBot",
		"fetch-other:", "fetch:", "fetch:Claude-User:/" + longSegment(AssistantPathBytes),
	} {
		if err := s.AddReferrerCounts(testContext, day, map[string]int64{key: 1}); err == nil {
			t.Errorf("accepted key %q", key)
		}
	}
	for _, key := range []string{"fetch:Claude-User:/", "fetch:ChatGPT-User:/e/{id}/proof", "fetch:Perplexity-User:/r/{room}", "fetch-other:Claude-User", "fetch:meta-externalfetcher:/llms-full.txt"} {
		if err := s.AddReferrerCounts(testContext, day, map[string]int64{key: 1}); err != nil {
			t.Errorf("refused key %q: %v", key, err)
		}
	}
}

func longSegment(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// Each family keeps its AssistantPathsPerDay largest paths per day, the rest
// folded into its own fetch-other; one family's flood never folds another's,
// and the referrer domains are untouched. The public read applies the
// ClientCountMinimum per path.
func TestAssistantFetchPathsAreCappedPerFamilyPerDay(t *testing.T) {
	s := openTest(t, Config{})
	now := time.Unix(testTime, 0)
	day := now.UTC().Format("2006-01-02")
	counts := map[string]int64{"fetch:ChatGPT-User:/llms.txt": 2, "fetch:ChatGPT-User:/for-agents": 4, "host:example.com": 1, "agent:Claude-User": 999}
	var total int64
	for i := 1; i <= AssistantPathsPerDay+20; i++ {
		counts["fetch:Claude-User:/guides/p"+strconv.Itoa(i)] = int64(i)
		total += int64(i)
	}
	if err := s.AddReferrerCounts(testContext, day, counts); err != nil {
		t.Fatal(err)
	}
	if err := s.AddReferrerCounts(testContext, day, map[string]int64{"fetch:Claude-User:/guides/newcomer": 1, "fetch-other:Claude-User": 3}); err != nil {
		t.Fatal(err)
	}
	total += 4
	days, err := s.ReadReferrerStats(testContext, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	d := days[0]
	if len(d.Assistants) != 2 || d.Assistants[0].Family != "ChatGPT-User" || d.Assistants[1].Family != "Claude-User" {
		t.Fatalf("families: %+v", d.Assistants)
	}
	chat, claude := d.Assistants[0], d.Assistants[1]
	if len(chat.Paths) != 2 || chat.Paths[0] != (ReferrerCount{"/for-agents", 4}) || chat.Other != 0 {
		t.Fatalf("ChatGPT-User: %+v", chat)
	}
	if len(claude.Paths) != AssistantPathsPerDay || claude.Paths[0] != (ReferrerCount{"/guides/p70", 70}) || claude.Paths[AssistantPathsPerDay-1].Name != "/guides/p21" {
		t.Fatalf("Claude-User kept %d paths: %v ... %v", len(claude.Paths), claude.Paths[0], claude.Paths[len(claude.Paths)-1])
	}
	if claude.Total() != total {
		t.Fatalf("folding lost fetches: %d != %d", claude.Total(), total)
	}
	if len(d.Hosts) != 1 || d.Other != 0 || len(d.Agents) != 1 {
		t.Fatalf("referrer rows disturbed: %+v", d)
	}
	pub, err := s.ReadAssistantFetches(testContext, now, 2)
	if err != nil || len(pub) != 2 || pub[0].Day != day || len(pub[1].Families) != 0 {
		t.Fatalf("%+v %v", pub, err)
	}
	pchat := pub[0].Families[0]
	if len(pchat.Paths) != 1 || pchat.Paths[0] != (ReferrerCount{"/for-agents", 4}) || pchat.Other != 2 {
		t.Fatalf("public ChatGPT-User: %+v", pchat)
	}
	if pclaude := pub[0].Families[1]; pclaude.Total() != total || len(pclaude.Paths) != AssistantPathsPerDay {
		t.Fatalf("public Claude-User: %d paths, total %d", len(pclaude.Paths), pclaude.Total())
	}
}
