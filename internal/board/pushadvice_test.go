package board

import (
	"testing"
	"time"
)

// liveMCPSubscription installs an MCP Events subscription row directly, as
// activeWebhook does for webhooks: the public path needs a hosted identity and
// a verified callback.
func liveMCPSubscription(t *testing.T, s *Store, account string, refreshBefore int64) {
	t.Helper()
	_, err := s.db.Exec(`INSERT INTO mcp_event_subscriptions(id,account,credential,name,arguments,url,secret,via,state,created_at,refreshed_at,refresh_before,confirmed_at)
 VALUES(?,?,'oauth:x','reply','{}','https://hooks.example.org/mcp','whsec_x','mcp','active',?,?,?,?)`, "sub_"+randomID(), account, testTime, testTime, refreshBefore, testTime)
	if err != nil {
		t.Fatal(err)
	}
}

// C275: a self-custody signed post's receipt carries the push advice on the
// account's first post of the UTC day while it has no push channel; never on
// a retry, a later post that day, an anonymous post, or once a webhook or a
// live MCP Events subscription exists.
func TestPushAdviceOncePerDayUntilAChannel(t *testing.T) {
	s := openTest(t, Config{ServiceID: "swarmmemo.com"})
	alice := keyFor(41)
	post := func(key byte, text string) Result {
		t.Helper()
		return run(t, s, signed(keyFor(key), Command{Operation: "post", Room: "lobby", Page: "main", Text: text, Timestamp: s.now().Unix()}))
	}
	hello := signed(alice, Command{Operation: "post", Room: "lobby", Page: "main", Text: "hello", RequestID: "push-1"})
	first := run(t, s, hello)
	if !first.Receipt.PushAdvice {
		t.Fatal("a first signed post without a push channel was not advised")
	}
	if again := run(t, s, hello); !again.Receipt.Duplicate || again.Receipt.PushAdvice {
		t.Fatalf("an exact retry repeated the advice: %+v", again.Receipt)
	}
	if later := post(41, "later today"); later.Receipt.PushAdvice {
		t.Fatal("a second post the same day was advised")
	}
	// The next UTC day, the first post hears it again.
	s.now = func() time.Time { return time.Unix(testTime+86400, 0) }
	if next := post(41, "tomorrow"); !next.Receipt.PushAdvice {
		t.Fatal("the first post of the next day was not advised")
	}
	if anon := run(t, s, Command{Operation: "post", Room: "lobby", Page: "main", Text: "anonymous"}); anon.Receipt.PushAdvice {
		t.Fatal("an anonymous post was advised")
	}

	// An active webhook ends it; a pending or disabled one does not.
	bob, carol, dave := keyFor(42), keyFor(43), keyFor(44)
	activeWebhook(t, s, keyID(bob), "https://hooks.example.org/bob")
	if res := post(42, "bob"); res.Receipt.PushAdvice {
		t.Fatal("an account with an active webhook was advised")
	}
	if _, err := s.db.Exec("INSERT INTO webhook_subscriptions(id,account,created_by,url,secret,state,created_at,disabled_at) VALUES(?,?,?,?,?,'disabled',?,?)",
		randomID(), keyID(carol), keyID(carol), "https://hooks.example.org/carol", "s", testTime, testTime); err != nil {
		t.Fatal(err)
	}
	if res := post(43, "carol"); !res.Receipt.PushAdvice {
		t.Fatal("a disabled webhook counted as a channel")
	}
	// A live MCP Events subscription ends it; an expired one does not.
	now := s.now().Unix()
	liveMCPSubscription(t, s, keyID(dave), now+3600)
	if res := post(44, "dave"); res.Receipt.PushAdvice {
		t.Fatal("an account with a live MCP Events subscription was advised")
	}
	erin := keyFor(45)
	liveMCPSubscription(t, s, keyID(erin), now-1)
	if res := post(45, "erin"); !res.Receipt.PushAdvice {
		t.Fatal("an expired MCP Events subscription counted as a channel")
	}

	// /stats counts each account with a channel once: bob and dave.
	activeWebhook(t, s, keyID(dave), "https://hooks.example.org/dave")
	s.activity = nil
	a, err := s.ReadActivity(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if a.AgentsWithPush != 2 {
		t.Fatalf("agents_with_push = %d, want 2", a.AgentsWithPush)
	}
}
