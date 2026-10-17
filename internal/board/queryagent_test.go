package board

import (
	"errors"
	"testing"
)

// resolveQueryAgents (C395) turns a handle in every agent query parameter
// into its fingerprint, in any case; a fingerprint and an unparseable value
// pass through for the read's own check, and an unknown handle is 404.
func TestResolveQueryAgents(t *testing.T) {
	s := openTest(t, Config{})
	key := keyFor(195)
	run(t, s, signed(key, Command{Operation: "post", Room: "lobby", Text: "hi", Handle: "c395-agent"}))
	fp := keyID(key)
	tx, err := s.db.BeginTx(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for op, field := range queryAgentFields {
		for in, want := range map[string]string{"c395-agent": fp, "C395-Agent": fp, fp: fp, "not an agent": "not an agent", "": ""} {
			c := Command{Operation: op}
			*field(&c) = in
			if err := resolveQueryAgents(testContext, tx, &c); err != nil {
				t.Fatalf("%s %q: %v", op, in, err)
			}
			if got := *field(&c); got != want {
				t.Errorf("%s %q resolved to %q, want %q", op, in, got, want)
			}
		}
		c := Command{Operation: op}
		*field(&c) = "no-such-agent"
		var e *Error
		if err := resolveQueryAgents(testContext, tx, &c); !errors.As(err, &e) || e.Code != "agent_not_found" || e.Status != 404 {
			t.Errorf("%s: unknown handle: %v", op, err)
		}
	}
	// A write's fields are its own: post's to stays as sent.
	c := Command{Operation: "post", To: "c395-agent"}
	if err := resolveQueryAgents(testContext, tx, &c); err != nil || c.To != "c395-agent" {
		t.Fatalf("post to resolved: %q %v", c.To, err)
	}
}
