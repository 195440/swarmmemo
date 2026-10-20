package httpapi

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/services"
)

// TestReceiveMailEndToEnd: on a store opened through its migrations,
// receiver create shows an email address next to the URL; the email
// worker's POST /in/mail/TOKEN stores the mail as an item; GET answers the
// reachability check; a wrong token is 404; rotate replaces the address.
func TestReceiveMailEndToEnd(t *testing.T) {
	_, h := receiverServer(t)
	seed := make([]byte, 32)
	seed[0] = 9
	key := ed25519.NewKeyFromSeed(seed)
	send := func(c board.Command) map[string]any {
		t.Helper()
		body, _ := json.Marshal(signService(key, c))
		w := makeRequest(h, "POST", "https://swarmmemo.com/v1/command", string(body), "application/json")
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", c.Operation, w.Code, w.Body.String())
		}
		return decodeResult(t, w.Body.Bytes())
	}
	address := regexp.MustCompile(`^rcv\.([a-z2-7]{32})@swarmmemo\.com$`)
	created := send(board.Command{Operation: "service.call", Target: "receiver", Data: `{"schema":1,"method":"create","args":{"screen":false},"max_cost":5}`, RequestID: "mail-create"})
	email, _ := dig(created, "data", "result", "email").(string)
	m := address.FindStringSubmatch(email)
	if m == nil || dig(created, "data", "result", "url") == nil {
		t.Fatalf("create: %+v", created)
	}
	token := m[1]
	post := func(token, contentType, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest("POST", "https://swarmmemo.com"+services.ReceiverMailPath+token, strings.NewReader(body))
		req.RemoteAddr = "104.30.0.9:12345"
		req.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	mail := `{"kind":"email","from":"noreply@example.org","subject":"Verify","date":"","message_id":"<a@example.org>","text":"code 4242"}`
	if code, out := post(token, "application/json", mail); code != 202 || out["ok"] != true {
		t.Fatalf("mail delivery: %d %+v", code, out)
	}
	if code, _ := post(strings.Repeat("a", 32), "application/json", mail); code != 404 {
		t.Fatalf("unknown address: %d", code)
	}
	if code, _ := post(token, "text/plain", "hi"); code != 415 {
		t.Fatalf("mail path takes JSON only: %d", code)
	}
	probe := httptest.NewRequest("GET", "https://swarmmemo.com"+services.ReceiverMailPath+token, nil)
	probe.RemoteAddr = "104.30.0.9:12345"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, probe)
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Fatalf("probe: %d %s", w.Code, w.Body.String())
	}
	read := send(board.Command{Operation: "service.read", Target: "receiver", Data: `{"schema":1,"method":"items","args":{}}`})
	items, _ := dig(read, "data", "result", "items").([]any)
	if len(items) != 1 || dig(items[0], "body") != mail {
		t.Fatalf("items: %+v", read)
	}
	id, _ := dig(created, "data", "result", "receiver", "id").(string)
	rotated := send(board.Command{Operation: "service.call", Target: "receiver", Data: `{"schema":1,"method":"rotate","args":{"id":"` + id + `"},"max_cost":1}`, RequestID: "mail-rotate"})
	fresh := address.FindStringSubmatch(dig(rotated, "data", "result", "email").(string))
	if fresh == nil || fresh[1] == token {
		t.Fatalf("rotate: %+v", rotated)
	}
	if code, _ := post(token, "application/json", mail); code != 404 {
		t.Fatalf("old address after rotate: %d", code)
	}
	if code, _ := post(fresh[1], "application/json", mail); code != 202 {
		t.Fatalf("new address: %d", code)
	}
}
