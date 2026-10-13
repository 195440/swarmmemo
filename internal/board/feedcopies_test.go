package board

// Copies fold into one entry on ranked views across rooms (C263).

import (
	"fmt"
	"testing"
	"time"
)

func TestCopyKeys(t *testing.T) {
	a, at := copyKeys("Join the AGI blueprint: https://Example.com/plan/ and https://docs.example.org/x#intro.")
	b, bt := copyKeys("加入我们 https://example.com/plan https://DOCS.example.org/x")
	if a == 0 || a != b {
		t.Fatalf("same URL set, other language: %x %x", a, b)
	}
	if at == bt {
		t.Fatal("different texts share a text key")
	}
	if one, _ := copyKeys("only https://example.com/plan here"); one != 0 {
		t.Fatal("a single URL is a URL set")
	}
	if two, _ := copyKeys("https://example.com/plan https://example.com/plan/"); two != 0 {
		t.Fatal("one URL twice is two distinct URLs")
	}
	_, x := copyKeys("  Hello   World ")
	_, y := copyKeys("hello world")
	if x == 0 || x != y {
		t.Fatal("normalized text differs")
	}
}

// feedEntries is feed.get's first page by id, with each entry's copies.
func feedEntries(t *testing.T, s *Store) map[string]*MessageCopies {
	t.Helper()
	s.dropRankings()
	out := map[string]*MessageCopies{}
	for _, m := range feedRead(t, s, `{}`, 100, "").Messages {
		out[m.ID] = m.Copies
	}
	return out
}

func TestFeedFoldsCopies(t *testing.T) {
	s := openTest(t, Config{})
	base := time.Unix(testTime, 0)
	at := func(hours float64) {
		s.now = func() time.Time { return base.Add(-time.Duration(hours * float64(time.Hour))) }
	}
	anon := func(name string, hours float64, room, text, ip string) string {
		t.Helper()
		at(hours)
		r, err := s.Execute(testContext, Command{Operation: "post", Room: room, Text: text, RequestID: "copies-" + name}, ip)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return r.Receipt.ID
	}
	// One anonymous author, six copies in four languages across two rooms
	// and rotating networks, all carrying the same two links.
	links := " https://agi.example/blueprint https://agi.example/join"
	texts := []string{"Join the AGI blueprint", "加入 AGI 蓝图", "AGI設計図に参加", "Присоединяйтесь к плану AGI", "Join the AGI blueprint today", "AGI blueprint: last call"}
	rooms := []string{"lobby", "research", "lobby", "research", "lobby", "research"}
	var copies []string
	for i, text := range texts {
		copies = append(copies, anon(fmt.Sprint("agi-", i), 10-float64(i), rooms[i], text+links, fmt.Sprintf("198.51.100.%d", i+1)))
	}
	// Two signed agents share one pair of links: a discussion, not copies.
	alice, bob := keyFor(91), keyFor(92)
	at(5)
	aliceID := postAs(t, s, alice, Command{Room: "lobby", Text: "Read these https://paper.example/a https://paper.example/b", RequestID: "alice"})
	bobID := postAs(t, s, bob, Command{Room: "research", Text: "Disagree with https://paper.example/b and https://paper.example/a", RequestID: "bob"})
	// The same text by one author three days apart: both stay.
	at(80)
	oldID := postAs(t, s, alice, Command{Room: "lobby", Text: "Weekly office hours are open", RequestID: "weekly-1"})
	at(2)
	newID := postAs(t, s, alice, Command{Room: "lobby", Text: "Weekly office hours are open", RequestID: "weekly-2"})
	at(0)

	got := feedEntries(t, s)
	for _, id := range copies[1:] {
		if _, ok := got[id]; ok {
			t.Fatalf("copy %s ranked on its own", id)
		}
	}
	c, ok := got[copies[0]]
	if !ok || c == nil || c.Count != 5 || len(c.IDs) != 5 || c.IDs[0] != copies[1] {
		t.Fatalf("the earliest copy's entry: listed %t, copies %+v", ok, c)
	}
	for _, id := range []string{aliceID, bobID, oldID, newID} {
		if c, ok := got[id]; !ok || c != nil {
			t.Fatalf("%s: listed %t, copies %+v", id, ok, c)
		}
	}
	// The front page's hot read is the same ranking.
	hot := run(t, s, Command{Operation: "messages.list", Data: `{"sort":"hot"}`, Limit: 100})
	seen := 0
	for _, m := range hot.Messages {
		for i, id := range copies {
			if m.ID == id {
				seen++
				if i != 0 || m.Copies == nil || m.Copies.Count != 5 {
					t.Fatalf("hot front page: copy %d, copies %+v", i, m.Copies)
				}
			}
		}
	}
	if seen != 1 {
		t.Fatalf("hot front page lists %d of the copies", seen)
	}
	// Every copy stays in its room, newest first and in the room's own
	// ranked view.
	for _, room := range []string{"lobby", "research"} {
		for _, data := range []string{`{"sort":"new"}`, `{"sort":"hot"}`} {
			listed := map[string]bool{}
			for _, m := range run(t, s, Command{Operation: "messages.list", Room: room, Data: data, Limit: 100}).Messages {
				listed[m.ID] = true
				if m.Copies != nil {
					t.Fatalf("%s %s: copies on a room read", room, data)
				}
			}
			for i, id := range copies {
				if rooms[i] == room && !listed[id] {
					t.Fatalf("%s %s: copy %d missing", room, data, i)
				}
			}
		}
	}

	// A copy posted after the ranking was made folds into it too.
	s.dropRankings()
	feedRead(t, s, `{}`, 100, "")
	late := anon("agi-late", 0, "lobby", "AGI blueprint again"+links, "198.51.100.9")
	found := false
	for _, m := range feedRead(t, s, `{}`, 100, "").Messages {
		if m.ID == late {
			t.Fatal("a fresh copy ranked on its own")
		}
		if m.ID == copies[0] {
			found = true
			if m.Copies == nil || m.Copies.Count != 6 || m.Copies.IDs[5] != late {
				t.Fatalf("after a fresh copy: %+v", m.Copies)
			}
		}
	}
	if !found {
		t.Fatal("the folded entry left the feed")
	}
}
