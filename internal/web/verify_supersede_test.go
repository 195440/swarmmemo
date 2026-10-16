package web

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
)

// publishVerificationData signs verify.publish data with the key of seed.
func publishVerificationData(t *testing.T, s board.Service, seed byte, nonce string, d map[string]any) board.Verification {
	t.Helper()
	raw := make([]byte, 32)
	raw[0] = seed
	key := ed25519.NewKeyFromSeed(raw)
	base := map[string]any{"schema": 1, "commit": verifyTestCommit, "command": "make test", "environment": "linux",
		"output_sha256": strings.Repeat("cd", 32), "limits": "Nothing else.", "checks": []map[string]any{{"property": "tests", "state": "pass"}}}
	for k, v := range d {
		base[k] = v
	}
	data, _ := json.Marshal(base)
	c := board.Command{Operation: "verify.publish", Data: string(data), PublicKey: base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey)),
		Timestamp: time.Now().Unix(), Nonce: "supersede-nonce-" + nonce, RequestID: "supersede-" + nonce}
	c.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, board.Canonical("swarmmemo.com", c)))
	res, err := s.Execute(context.Background(), c, "192.0.2.9")
	if err != nil {
		t.Fatal(err)
	}
	return res.Data["verification"].(board.Verification)
}

// C371: a superseded record's page says what replaced it and the new one
// what it replaces; a subject's badge and /v/latest skip a record that was
// superseded, even by a record about another subject.
func TestVerificationSupersededPages(t *testing.T) {
	s := gateStore(t)
	const repo, moved = "https://github.com/owner/fixed", "https://github.com/owner/moved"
	earlier := publishVerificationData(t, s, 20, "a", map[string]any{"repo": repo})
	old := publishVerificationData(t, s, 21, "b", map[string]any{"repo": repo, "limits": "debug: stray line",
		"checks": []map[string]any{{"property": "tests", "state": "fail"}}})
	if latest := gateGet(s, "/v/latest?repo="+repo); latest.Code != 302 || latest.Header().Get("Location") != "/v/"+old.ID {
		t.Fatalf("latest before: %d %v", latest.Code, latest.Header())
	}
	fresh := publishVerificationData(t, s, 21, "c", map[string]any{"repo": moved, "supersedes": old.ID})

	page := gateGet(s, "/v/"+old.ID).Body.String()
	if !strings.Contains(page, `Superseded by <a href="/v/`+fresh.ID+`">/v/`+fresh.ID+`</a>`) || strings.Contains(page, "Supersedes <a") {
		t.Fatalf("the old page lacks its banner:\n%s", page)
	}
	page = gateGet(s, "/v/"+fresh.ID).Body.String()
	if !strings.Contains(page, `Supersedes <a href="/v/`+old.ID+`">/v/`+old.ID+`</a>`) || strings.Contains(page, "Superseded by") {
		t.Fatalf("the new page lacks its line:\n%s", page)
	}
	if latest := gateGet(s, "/v/latest?repo="+repo); latest.Code != 302 || latest.Header().Get("Location") != "/v/"+earlier.ID {
		t.Fatalf("latest after: %d %v", latest.Code, latest.Header())
	}
	if badge := gateGet(s, "/v/badge?repo="+repo+"&commit="+verifyTestCommit).Body.String(); !strings.Contains(badge, "1/1 checks passed") {
		t.Fatalf("subject badge: %s", badge)
	}
	if latest := gateGet(s, "/v/latest?repo="+moved); latest.Code != 302 || latest.Header().Get("Location") != "/v/"+fresh.ID {
		t.Fatalf("the new subject: %d %v", latest.Code, latest.Header())
	}
}
