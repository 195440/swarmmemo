package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// Every route that takes an agent query or path parameter takes a handle
// as well as a fingerprint, as the docs say of AGENT (C395): the handle
// reads exactly as the fingerprint does, an unknown handle is 404
// agent_not_found, and a fingerprint reads as before.
func TestQueryAgentTakesHandles(t *testing.T) {
	store, s := anonCallServer(t, 400_000)
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	sum := sha256.Sum256(key.Public().(ed25519.PublicKey))
	fp := hex.EncodeToString(sum[:])
	other := ed25519.NewKeyFromSeed([]byte(strings.Repeat("o", 32)))
	if _, err := store.Execute(context.Background(), signService(key, board.Command{Operation: "post", Room: "lobby", Text: "hello", Handle: "query-weaver"}), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Execute(context.Background(), signService(other, board.Command{Operation: "post", Room: "lobby", Text: "for you", To: fp}), "test"); err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{
		"/api/updates?agent=%s",
		"/api/allowance?agent=%s",
		"/api/ledger?agent=%s",
		"/api/works?target=%s",
		"/api/home?agent=%s",
		"/inbox/%s?format=json",
		"/api/messages?to=%s",
	} {
		byKey := get(s, strings.Replace(route, "%s", fp, 1), "application/json")
		if byKey.Code != 200 {
			t.Fatalf("%s by fingerprint: %d %s", route, byKey.Code, byKey.Body)
		}
		for _, handle := range []string{"query-weaver", "Query-Weaver"} {
			byHandle := get(s, strings.Replace(route, "%s", handle, 1), "application/json")
			if byHandle.Code != 200 {
				t.Fatalf("%s by handle %s: %d %s", route, handle, byHandle.Code, byHandle.Body)
			}
			if a, b := comparable(t, byKey.Body.Bytes()), comparable(t, byHandle.Body.Bytes()); a != b {
				t.Errorf("%s by handle differs from by fingerprint:\n%s\n%s", route, b, a)
			}
		}
		unknown := get(s, strings.Replace(route, "%s", "no-such-handle", 1), "application/json")
		if unknown.Code != 404 || !strings.Contains(unknown.Body.String(), "agent_not_found") {
			t.Errorf("%s by an unknown handle: %d %s", route, unknown.Code, unknown.Body)
		}
		junk := get(s, strings.Replace(route, "%s", "not%20an%20agent", 1), "application/json")
		if junk.Code != 400 {
			t.Errorf("%s by junk: %d %s", route, junk.Code, junk.Body)
		}
	}
}

// comparable is a response with the fields that vary per request (the
// sealed cursor, and the nudge, which follows the caller's read count) left out.
func comparable(t *testing.T, body []byte) string {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	for _, k := range []string{"request_id", "next_cursor", "next"} {
		delete(v, k)
	}
	out, _ := json.Marshal(v)
	return string(out)
}
