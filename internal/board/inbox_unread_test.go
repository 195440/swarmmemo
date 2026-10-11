package board

import (
	"crypto/ed25519"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// C199: read state. Under INBOX_ENTRIES=read the board keeps which replies,
// public DMs and mentions an account has read, so every device agrees; under
// off and shadow nothing changes.

func markReadCmd(key ed25519.PrivateKey, ids ...string) Command {
	raw, _ := json.Marshal(map[string]any{"schema": 1, "ids": ids})
	return signed(key, Command{Operation: "updates.mark_read", Data: string(raw)})
}

// unreadOf is data.unread.updates of key's own counts read, from cursor (a
// second device holds a different cursor, or none).
func unreadOf(t *testing.T, s *Store, key ed25519.PrivateKey, cursor string) InboxUnreadUpdates {
	t.Helper()
	r := runBounded(t, s, signed(key, Command{Operation: "updates.get", Target: keyID(key), Cursor: cursor, Limit: 1, Data: `{"schema":1,"counts":true}`}))
	u, ok := r.Data["unread"].(InboxUnread)
	if !ok || u.Updates == nil {
		t.Fatalf("no data.unread.updates: %+v", r.Data)
	}
	return *u.Updates
}

func unreadIDs(u InboxUnreadUpdates) []string {
	return sortedIDs(slices.Concat(u.Replies, u.Addressed, u.Mentions)...)
}

func TestInboxReadState(t *testing.T) {
	s := openInboxTest(t, InboxRead)
	alice, bob, carol, dave := keyFor(251), keyFor(252), keyFor(253), keyFor(254)
	for i, k := range []ed25519.PrivateKey{alice, bob, carol, dave} {
		runBounded(t, s, signed(k, Command{Operation: "agent.register", Handle: []string{"ralice", "rbob", "rcarol", "rdave"}[i]}))
	}
	post := func(key ed25519.PrivateKey, c Command) string { return runBounded(t, s, signed(key, c)).Receipt.ID }

	root := post(alice, Command{Operation: "post", Room: "lobby", Text: "root by alice"})
	reply := post(bob, Command{Operation: "post", Room: "lobby", ReplyTo: root, Text: "a reply, and @ralice too"})
	dm := post(carol, Command{Operation: "post", Room: "lobby", To: keyID(alice), Text: "alice, a public DM"})
	dmEdited := post(carol, Command{Operation: "post", Room: "lobby", To: keyID(alice), Text: "alice, a better public DM", Data: dataJSON(`"supersedes":"` + dm + `"`)})
	mention := post(dave, Command{Operation: "post", Room: "lobby", Text: "what does @ralice think"})
	bobRoot := post(bob, Command{Operation: "post", Room: "lobby", Text: "root by bob"})
	toBob := post(dave, Command{Operation: "post", Room: "lobby", ReplyTo: bobRoot, Text: "a reply to bob"})

	// Two devices: one that has polled (its own cursor), one that never has.
	laptop := runBounded(t, s, signed(alice, Command{Operation: "updates.get", Target: keyID(alice), Data: `{"schema":1,"counts":true}`})).NextCursor
	u := unreadOf(t, s, alice, laptop)
	// A reply that also mentions is a reply; each message once, as its
	// newest version.
	if !slices.Equal(u.Replies, []string{reply}) || !slices.Equal(u.Addressed, []string{dmEdited}) || !slices.Equal(u.Mentions, []string{mention}) || u.Total != 3 || u.Capped {
		t.Fatalf("alice's unread: %+v", u)
	}
	phone := unreadOf(t, s, alice, "")
	if !slices.Equal(unreadIDs(phone), unreadIDs(u)) {
		t.Fatalf("devices disagree: %+v vs %+v", phone, u)
	}

	// Read on the phone: the laptop's next poll agrees.
	r := runBounded(t, s, markReadCmd(alice, reply))
	if r.Data["changed"].(int64) != 1 || len(r.Data["entries"].([]string)) != 1 {
		t.Fatalf("mark_read: %+v", r.Data)
	}
	if got := r.Data["unread"].(map[string]any)["updates"].(InboxUnreadUpdates); got.Total != 2 || len(got.Replies) != 0 {
		t.Fatalf("mark_read answers the unread left: %+v", got)
	}
	if got := unreadOf(t, s, alice, laptop); got.Total != 2 || slices.Contains(unreadIDs(got), reply) {
		t.Fatalf("laptop after the phone read the reply: %+v", got)
	}
	// Idempotent: again changes nothing; an id that names no entry is no error.
	if again := runBounded(t, s, markReadCmd(alice, reply, "not-an-entry")); again.Data["changed"].(int64) != 0 {
		t.Fatalf("a repeat: %+v", again.Data)
	}
	// Any version of a message names the entries of all of them.
	if r := runBounded(t, s, markReadCmd(alice, dm)); r.Data["changed"].(int64) != 2 {
		t.Fatalf("by the edited version: %+v", r.Data)
	}

	// Private to the account: bob's inbox is his own, another reader sees no
	// read state, and nobody else can mark alice's.
	if got := unreadOf(t, s, bob, ""); !slices.Equal(unreadIDs(got), []string{toBob}) {
		t.Fatalf("bob's unread: %+v", got)
	}
	other := runBounded(t, s, signed(bob, Command{Operation: "updates.get", Target: keyID(alice), Data: `{"schema":1,"counts":true}`}))
	if _, ok := other.Data["unread"]; ok {
		t.Fatalf("another reader sees data.unread: %+v", other.Data)
	}
	if code := disposeCode(t, s, signed(bob, Command{Operation: "updates.mark_read", Target: keyID(alice), Data: `{"schema":1,"all":true}`})); code != "own_inbox_only" {
		t.Fatalf("bob marking alice's inbox: %s", code)
	}
	if r := runBounded(t, s, markReadCmd(bob, mention)); r.Data["changed"].(int64) != 0 {
		t.Fatalf("bob's mark of alice's mention: %+v", r.Data)
	}
	if got := unreadOf(t, s, alice, laptop); !slices.Equal(unreadIDs(got), []string{mention}) {
		t.Fatalf("alice's unread after the others: %+v", got)
	}

	// Answering is reading: a reply to a mention clears it.
	again := post(dave, Command{Operation: "post", Room: "lobby", Text: "@ralice again"})
	post(alice, Command{Operation: "post", Room: "lobby", ReplyTo: again, Text: "here"})
	if got := unreadOf(t, s, alice, ""); slices.Contains(unreadIDs(got), again) {
		t.Fatalf("a mention alice replied to is unread: %+v", got)
	}

	// Mark all read.
	post(bob, Command{Operation: "post", Room: "lobby", ReplyTo: root, Text: "another reply"})
	all := runBounded(t, s, signed(alice, Command{Operation: "updates.mark_read", Data: `{"schema":1,"all":true}`}))
	if all.Data["changed"].(int64) != 2 || unreadOf(t, s, alice, laptop).Total != 0 || unreadOf(t, s, alice, "").Total != 0 {
		t.Fatalf("mark all read: %+v", all.Data)
	}

	for _, bad := range []string{`{"schema":1}`, `{"schema":1,"ids":[]}`, `{"schema":1,"all":true,"ids":["x"]}`, `{"schema":2,"all":true}`, `{"schema":1,"all":false}`,
		`{"schema":1,"ids":["` + strings.Repeat(`x","`, InboxMarkReadMax) + `x"]}`, `{"schema":1,"ids":[""]}`} {
		if code := disposeCode(t, s, signed(alice, Command{Operation: "updates.mark_read", Data: bad})); code != "invalid_request" {
			t.Fatalf("%s: %s", bad, code)
		}
	}
	if code := disposeCode(t, s, Command{Operation: "updates.mark_read", Data: `{"schema":1,"all":true}`}); code != "signature_required" {
		t.Fatalf("unsigned: %s", code)
	}

	// The unread read is one range of the inbox_unread index.
	rows, err := s.db.Query("EXPLAIN QUERY PLAN SELECT ie.kind,ie.subject FROM inbox_entries ie WHERE "+inboxUnreadFilter+" ORDER BY ie.created_at DESC,ie.seq DESC LIMIT 101", "a", 0, "a")
	if err != nil {
		t.Fatal(err)
	}
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	rows.Close()
	if !strings.Contains(strings.Join(plan, "\n"), "USING INDEX inbox_unread") || strings.Contains(strings.Join(plan, "\n"), "TEMP B-TREE") {
		t.Fatalf("unread plan:\n%s", strings.Join(plan, "\n"))
	}
}

// Under off and shadow read state stays the browser's: updates.mark_read is
// unavailable and data.unread has no updates.
func TestInboxReadStateOffAndShadow(t *testing.T) {
	s := openInboxTest(t, InboxShadow)
	alice, bob := keyFor(249), keyFor(250)
	root := runBounded(t, s, signed(alice, Command{Operation: "post", Room: "lobby", Text: "root"})).Receipt.ID
	runBounded(t, s, signed(bob, Command{Operation: "post", Room: "lobby", ReplyTo: root, Text: "reply"}))
	for _, mode := range []InboxMode{InboxOff, InboxShadow} {
		setInboxMode(s, mode)
		if code := disposeCode(t, s, markReadCmd(alice, root)); code != "service_unavailable" {
			t.Fatalf("%v: updates.mark_read: %s", mode, code)
		}
		r := runBounded(t, s, signed(alice, Command{Operation: "updates.get", Target: keyID(alice), Data: `{"schema":1,"counts":true}`}))
		if u := r.Data["unread"].(InboxUnread); u.Updates != nil {
			t.Fatalf("%v: data.unread.updates: %+v", mode, u)
		}
		raw, _ := json.Marshal(r.Data["unread"])
		if strings.Contains(string(raw), "updates") {
			t.Fatalf("%v: data.unread: %s", mode, raw)
		}
	}
	// Entries written in shadow are unread once the board reads the log.
	setInboxMode(s, InboxRead)
	if u := unreadOf(t, s, alice, ""); u.Total != 1 {
		t.Fatalf("after the flip: %+v", u)
	}
}

// A mention, once read, never comes back as unread: not on a later poll of
// the same device, not on another device, not after more traffic, and not
// when its author edits it (the edit does not mention anew, so the entry
// stays on the version that delivered it; the list names the newest
// version, what its page shows, and marking that marks the entry).
// pliplop's report (post 0a89299a, message 1c1fd79b) was this class.
func TestInboxReadMentionStaysRead(t *testing.T) {
	s := openInboxTest(t, InboxRead)
	alice, weaver := keyFor(247), keyFor(248)
	runBounded(t, s, signed(alice, Command{Operation: "agent.register", Handle: "mpliplop"}))
	runBounded(t, s, signed(weaver, Command{Operation: "agent.register", Handle: "mweaver"}))
	post := func(key ed25519.PrivateKey, c Command) string { return runBounded(t, s, signed(key, c)).Receipt.ID }
	mention := post(weaver, Command{Operation: "post", Room: "lobby", Text: "about @mentions, @mpliplop"})
	edited := post(weaver, Command{Operation: "post", Room: "lobby", Text: "about @mentions, @mpliplop (edited)", Data: dataJSON(`"supersedes":"` + mention + `"`)})
	laptop := runBounded(t, s, signed(alice, Command{Operation: "updates.get", Target: keyID(alice), Cursor: "start", Data: `{"schema":1,"counts":true}`})).NextCursor
	u := unreadOf(t, s, alice, laptop)
	if !slices.Equal(u.Mentions, []string{edited}) || u.Total != 1 {
		t.Fatalf("the mention, as its newest version: %+v", u)
	}
	// The phone opens the page, which shows the newest version.
	runBounded(t, s, markReadCmd(alice, edited))
	post(weaver, Command{Operation: "post", Room: "lobby", Text: "unrelated traffic"})
	for i, cursor := range []string{laptop, "", "start", laptop} {
		if got := unreadOf(t, s, alice, cursor); got.Total != 0 {
			t.Fatalf("poll %d: the read mention came back: %+v", i, got)
		}
	}
}
