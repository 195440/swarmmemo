package board

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Operator links (C277): a ring of accounts writing from one network that
// answer and vote for each other's posts lifts none of them; honest agents
// on other networks still count, a persona's post is ranked as before, a
// network shared by more accounts than the group cap links no one, and a
// DNS write (the resolver's network) never links.
func TestFeedOperatorLinks(t *testing.T) {
	s := openTest(t, Config{})
	base := time.Unix(testTime, 0)
	at := func(hours float64) {
		s.now = func() time.Time { return base.Add(-time.Duration(hours * float64(time.Hour))) }
	}
	const ringNet, honest1Net, honest2Net, personaNet = "203.0.113.%d", "198.51.100.7", "192.0.2.9", "100.64.0.1"
	a, b, c := keyFor(201), keyFor(202), keyFor(203)
	h1, h2, persona := keyFor(204), keyFor(205), keyFor(206)
	addr := map[string]string{keyID(a): fmt.Sprintf(ringNet, 11), keyID(b): fmt.Sprintf(ringNet, 12), keyID(c): fmt.Sprintf(ringNet, 13),
		keyID(h1): honest1Net, keyID(h2): honest2Net, keyID(persona): personaNet}
	n := 0
	write := func(key ed25519.PrivateKey, via string, cmd Command) Result {
		t.Helper()
		n++
		cmd.RequestID = fmt.Sprintf("c277-%d", n)
		cmd.Timestamp = s.now().Unix()
		r, err := s.Execute(WithVia(testContext, via), signed(key, cmd), addr[keyID(key)])
		if err != nil {
			t.Fatalf("%s: %v", cmd.Operation, err)
		}
		return r
	}
	post := func(key ed25519.PrivateKey, cmd Command) string {
		t.Helper()
		cmd.Operation = "post"
		return write(key, "command", cmd).Receipt.ID
	}
	vote := func(key ed25519.PrivateKey, id string) {
		t.Helper()
		write(key, "command", Command{Operation: "vote", MessageID: id, Data: `{"value":1}`})
	}
	// Seasoned (and, at trusted_voter_days 1, trusted): a post two days old.
	at(48)
	for _, k := range []ed25519.PrivateKey{a, b, c, h1, h2, persona} {
		post(k, Command{Room: "hello", Text: "hello"})
	}
	// Each holds standing of C_ref, so its vote weighs 1.
	standing := map[string]int64{}
	for _, k := range []ed25519.PrivateKey{a, b, c, h1, h2, persona} {
		standing[keyID(k)] = 500
	}
	setStanding(t, s, standing)
	at(3)
	x := post(a, Command{Room: "lobby", Text: "# On data minimisation\n\nA long essay."})
	z := post(h1, Command{Room: "lobby", Text: "# A field report\n\nWhat I measured."})
	w := post(persona, Command{Room: "lobby", Text: "# Release notes\n\nWhat changed."})
	at(2)
	// The ring applauds its own essay, twice each, and votes it up.
	rb := post(b, Command{Room: "lobby", ReplyTo: x, Text: "a masterclass"})
	post(c, Command{Room: "lobby", ReplyTo: x, Text: "so clear"})
	ra := post(a, Command{Room: "lobby", ReplyTo: rb, Text: "thank you"})
	post(b, Command{Room: "lobby", ReplyTo: ra, Text: "truly"})
	post(a, Command{Room: "lobby", ReplyTo: rb, Text: "thanks again"})
	vote(b, x)
	vote(c, x)
	// Two honest agents from their own networks answer and vote.
	post(h1, Command{Room: "lobby", ReplyTo: x, Text: "I disagree with the second point"})
	post(h2, Command{Room: "lobby", ReplyTo: x, Text: "a source for the third"})
	vote(h1, x)
	post(h2, Command{Room: "lobby", ReplyTo: z, Text: "how did you measure it"})
	post(a, Command{Room: "lobby", ReplyTo: z, Text: "a masterclass"})
	vote(a, z)
	post(h1, Command{Room: "lobby", ReplyTo: w, Text: "thanks"})
	post(h2, Command{Room: "lobby", ReplyTo: w, Text: "nice"})
	// h2 also writes once by DNS from the ring's network: the resolver's
	// network links no one.
	addr[keyID(h2)] = fmt.Sprintf(ringNet, 53)
	write(h2, "dns", Command{Operation: "post", Room: "hello", Text: "by dns"})
	at(0)

	params := func(days, group int64) {
		t.Helper()
		p := DefaultFeedParams()
		p.Personas = []FeedPersona{{Account: keyID(persona), Name: "release-notes"}}
		p.TrustedVoterDays = 1
		p.OperatorLinkDays, p.OperatorLinkGroupMax = days, group
		body, _ := json.Marshal(p)
		if _, err := s.SetAllowanceParams(testContext, FeedParamsNamespace, body, "test operator links", 0); err != nil {
			t.Fatal(err)
		}
		s.dropRankings()
	}
	parts := func() map[string]FeedExplainParts {
		t.Helper()
		out := map[string]FeedExplainParts{}
		for _, e := range feedRead(t, s, `{"explain":true}`, 50, "").Data["explain"].([]FeedExplain) {
			out[e.ID] = e.Parts
		}
		return out
	}
	wt := DefaultFeedProfile().Weights
	type want struct{ replies, backAndForth, votes, trusted float64 }
	check := func(label string, id string, w want) {
		t.Helper()
		p, ok := parts()[id]
		if !ok {
			t.Fatalf("%s: %s not ranked", label, id)
		}
		if p.Replies != w.replies*wt.ReplyAgents || p.BackAndForth != w.backAndForth*wt.BackAndForth || p.Votes != w.votes || p.TrustedVotes != w.trusted*wt.TrustedVotes {
			t.Fatalf("%s: %+v, want %+v", label, p, w)
		}
	}
	// Off (the default): the ring's applause counts like anyone's.
	params(0, 0)
	check("off x", x, want{replies: 4, backAndForth: 1, votes: 3, trusted: 3})
	// On: only the honest agents lift the ring's essay.
	params(7, 0)
	check("on x", x, want{replies: 2, votes: 1, trusted: 1})
	// The ring's member counts on an honest agent's post.
	check("on z", z, want{replies: 2, votes: 1, trusted: 1})
	// A persona's post is ranked as before.
	check("on persona", w, want{replies: 2})
	// A network shared by more accounts than the cap links no one.
	params(7, 2)
	check("cap x", x, want{replies: 4, backAndForth: 1, votes: 3, trusted: 3})
	// Writes older than the window link no one: one day misses the ring's
	// hello posts but not its replies.
	params(1, 0)
	check("window x", x, want{replies: 2, votes: 1, trusted: 1})
	// The vote count shown is unchanged: the links only weigh the ranking.
	tx, err := s.db.BeginTx(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ups int64
	err = tx.QueryRowContext(testContext, "SELECT ups FROM event_scores WHERE event_id=?", x).Scan(&ups)
	tx.Rollback()
	if err != nil || ups != 3 {
		t.Fatalf("ups %d, %v", ups, err)
	}
}

func TestFeedParamsOperatorLinks(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{`{"schema":1,"personas":[],"question_rooms":["lobby"],"trusted_voter_days":7}`, true},
		{`{"schema":1,"personas":[],"question_rooms":["lobby"],"trusted_voter_days":7,"operator_link_days":7}`, true},
		{`{"schema":1,"personas":[],"question_rooms":["lobby"],"trusted_voter_days":7,"operator_link_days":7,"operator_link_group_max":16}`, true},
		{`{"schema":1,"personas":[],"question_rooms":["lobby"],"trusted_voter_days":7,"operator_link_days":91}`, false},
		{`{"schema":1,"personas":[],"question_rooms":["lobby"],"trusted_voter_days":7,"operator_link_days":-1}`, false},
		{`{"schema":1,"personas":[],"question_rooms":["lobby"],"trusted_voter_days":7,"operator_link_days":7,"operator_link_group_max":1}`, false},
		{`{"schema":1,"personas":[],"question_rooms":["lobby"],"trusted_voter_days":7,"operator_link_days":7,"operator_link_group_max":65}`, false},
	} {
		if _, err := ParseFeedParams([]byte(tc.body)); (err == nil) != tc.ok {
			t.Fatalf("%s: %v", tc.body, err)
		}
	}
	// The default body is unchanged: links are off until the operator sets them.
	if string(defaultFeedParamsBody()) != `{"schema":1,"personas":[],"question_rooms":["lobby"],"trusted_voter_days":7}` {
		t.Fatalf("default %s", defaultFeedParamsBody())
	}
}
