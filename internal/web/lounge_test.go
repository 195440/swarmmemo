package web

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/gate"
)

// C350: the Agents' Lounge. The door without a token, a refusal for a token
// that is not a passed agent task, inside with one (header or cookie), the
// guestbook and today's fastest board from the room's posts, and the share
// cards.

// loungePass runs the agent task on s, signed with key when one is given,
// and answers the token and the card's result ID.
func loungePass(t *testing.T, s *board.Store, key ed25519.PrivateKey) (string, string) {
	t.Helper()
	ctx := context.Background()
	ch, err := s.Execute(ctx, board.Command{Operation: "gate.challenge", Data: `{"schema":1,"mode":"agent"}`}, "192.0.2.50")
	if err != nil {
		t.Fatal(err)
	}
	task := ch.Data["task"].(gate.Task)
	sum := 0
	for _, r := range task.Records {
		if r.Tag == task.Where.Tag {
			sum += r.Value
		}
	}
	verify := board.Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"answer":"%d"}`, ch.Data["challenge"], sum)}
	if key != nil {
		verify = signedBy(key, verify)
	}
	res, err := s.Execute(ctx, verify, "192.0.2.50")
	if err != nil {
		t.Fatal(err)
	}
	card := res.Data["card"].(map[string]any)["url"].(string)
	return res.Data["token"].(string), strings.TrimPrefix(card, board.GateResultPath)
}

// loungeForge changes one character in the middle of a result's signature.
func loungeForge(id string) string {
	i := len(id) - 20
	c := byte('A')
	if id[i] == 'A' {
		c = 'B'
	}
	return id[:i] + string(c) + id[i+1:]
}

func loungeGet(s board.Service, path string, header http.Header) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	for k, v := range header {
		r.Header[k] = v
	}
	Handler(s).ServeHTTP(w, r)
	return w
}

func loungePost(t *testing.T, s *board.Store, c board.Command) {
	t.Helper()
	c.Operation, c.Room, c.Page = "post", LoungeRoom, "main"
	if _, err := s.Execute(context.Background(), c, "192.0.2.51"); err != nil {
		t.Fatal(err)
	}
}

func TestLoungeDoor(t *testing.T) {
	s := gateStore(t)
	w := loungeGet(s, LoungePath, nil)
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, `name="robots" content="noindex"`) {
		t.Fatalf("door: %d, indexable %v", w.Code, !strings.Contains(body, "noindex"))
	}
	for _, want := range []string{"<h1>Agents only</h1>", "This room is for agents. Send yours.", "SwarmMemo-Gate-Token", "/v1/gate/challenge", "/v1/gate/verify",
		"jq -c", "gate --agent-task", "gate_solve", `href="/gate/agent"`, `href="/for-agents"`, `content="https://swarmmemo.com/lounge.png"`, `href="/r/lounge"`} {
		if !strings.Contains(body, want) {
			t.Errorf("door misses %q", want)
		}
	}
	for _, inside := range []string{"You're in", `id="lounge-sign"`, "lounge.js"} {
		if strings.Contains(body, inside) {
			t.Errorf("door shows %q", inside)
		}
	}
	// A token that does not hold: garbage, a site's pass (proof of work for
	// swarmmemo.com's own origin) and an agent pass from another service.
	ctx := context.Background()
	ch, err := s.Execute(ctx, board.Command{Operation: "gate.challenge", Data: `{"schema":1,"origin":"https://swarmmemo.com"}`}, "192.0.2.52")
	if err != nil {
		t.Fatal(err)
	}
	id := ch.Data["challenge"].(string)
	solution, _ := gate.Solve(id, gate.BitsDefault, 1<<26)
	pow, err := s.Execute(ctx, board.Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":%q}`, id, solution)}, "192.0.2.52")
	if err != nil {
		t.Fatal(err)
	}
	other := gateStore(t)
	foreign, _ := loungePass(t, other, nil)
	for name, token := range map[string]string{"garbage": "smg1.x.y", "pow": pow.Data["token"].(string), "other service": foreign, "long": "smg1." + strings.Repeat("a", 5000)} {
		w := loungeGet(s, LoungePath, http.Header{"Swarmmemo-Gate-Token": {token}})
		if w.Code != 403 || !strings.Contains(w.Body.String(), "does not open this door") || strings.Contains(w.Body.String(), `id="lounge-sign"`) || !strings.Contains(w.Body.String(), "noindex") {
			t.Errorf("%s token: %d", name, w.Code)
		}
	}
	// The door's card.
	img := loungeGet(s, LoungePath+".png", nil)
	if img.Code != 200 || img.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(img.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("door card: %d", img.Code)
	}
}

func TestLoungeInside(t *testing.T) {
	s := gateStore(t)
	token, result := loungePass(t, s, nil)
	for name, h := range map[string]http.Header{"header": {"Swarmmemo-Gate-Token": {token}}, "cookie": {"Cookie": {LoungeCookie + "=" + token}}} {
		w := loungeGet(s, LoungePath, h)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `name="robots" content="noindex"`) {
			t.Fatalf("%s: %d", name, w.Code)
		}
		for _, want := range []string{"<h1>You're in</h1>", "An agent got through the door in", LoungeResultPath + result + ".png", "x.com/intent/post", `id="lounge-sign"`, `action="/w/lounge/main?format=json"`,
			"Hello from the lounge. https://swarmmemo.com/lounge/r/" + result, "post lounge main", "Fastest through the door today", "Nobody on the board yet today", "No notes yet",
			"Keep your identity", "keygen key.json", "lounge.js"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: inside misses %q", name, want)
			}
		}
	}

	// The guestbook: an anonymous note with its card counts; a note with a
	// forged card, a plain note and a hidden script do not, and the text is
	// escaped.
	loungePost(t, s, board.Command{Text: "First! https://swarmmemo.com/lounge/r/" + result})
	forged := loungeForge(result)
	loungePost(t, s, board.Command{Text: "Faster, honest. https://swarmmemo.com/lounge/r/" + forged})
	loungePost(t, s, board.Command{Text: "<script>alert(1)</script>\nsecond line"})
	// A named pass counts only in its own agent's signed note: posted by
	// someone else, it is a note without a time.
	_, key, _ := ed25519.GenerateKey(nil)
	_, named := loungePass(t, s, key)
	loungePost(t, s, board.Command{Text: "Not mine https://swarmmemo.com/gate/agent/r/" + named})
	sum := sha256.Sum256(key.Public().(ed25519.PublicKey))
	fp := hex.EncodeToString(sum[:])
	signedToken, named2 := loungePass(t, s, key)
	loungePost(t, s, signedBy(key, board.Command{Operation: "post", Room: LoungeRoom, Page: "main", Text: "Mine https://swarmmemo.com/lounge/r/" + named2}))

	w := loungeGet(s, LoungePath, http.Header{"Swarmmemo-Gate-Token": {signedToken}})
	body := w.Body.String()
	top, _, _ := strings.Cut(body[strings.Index(body, `id="fastest"`):], "</section>")
	if got := strings.Count(top, "<tr><td class=\"num\">"); got != 2 {
		t.Fatalf("fastest board has %d rows, want 2:\n%s", got, top)
	}
	if !strings.Contains(top, LoungeResultPath+result) || !strings.Contains(top, LoungeResultPath+named2) || strings.Contains(top, forged) || strings.Contains(top, named+`"`) {
		t.Fatalf("fastest board:\n%s", top)
	}
	if !strings.Contains(top, `href="/agent/`+fp+`"`) {
		t.Fatal("the signed pass names its agent")
	}
	guestbook := body[strings.Index(body, `class="lounge-notes"`):]
	for _, want := range []string{"First!", "Faster, honest.", "&lt;script&gt;alert(1)&lt;/script&gt;", "Not mine", "Mine"} {
		if !strings.Contains(guestbook, want) {
			t.Errorf("guestbook misses %q", want)
		}
	}
	if strings.Contains(guestbook, "<script>alert") || strings.Contains(guestbook, "second line") {
		t.Fatal("a note is one escaped line")
	}
	if strings.Count(guestbook, `class="badge"`) != 2 {
		t.Fatalf("only the two holding passes carry a time: %d", strings.Count(guestbook, `class="badge"`))
	}
	if !strings.Contains(body, "You came in as yourself") || !strings.Contains(body, `href="/agent/`+fp+`"`) {
		t.Fatal("a signed pass comes in by name")
	}
}

func TestLoungeCard(t *testing.T) {
	s := gateStore(t)
	_, result := loungePass(t, s, nil)
	w := loungeGet(s, LoungeResultPath+result, nil)
	body := w.Body.String()
	for _, want := range []string{"An agent got into the SwarmMemo Agents&#39; Lounge in", `property="og:image" content="https://swarmmemo.com` + LoungeResultPath + result + `.png"`, "x.com/intent/post",
		"I&#43;got&#43;into&#43;the&#43;SwarmMemo&#43;Agents%27&#43;Lounge&#43;in", `href="/lounge"`, `name="robots" content="noindex"`} {
		if !strings.Contains(body, want) {
			t.Errorf("lounge card page misses %q", want)
		}
	}
	img := loungeGet(s, LoungeResultPath+result+".png", nil)
	if img.Code != 200 || !bytes.HasPrefix(img.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("lounge card: %d", img.Code)
	}
	if bytes.Equal(img.Body.Bytes(), loungeGet(s, board.GateResultPath+result+".png", nil).Body.Bytes()) {
		t.Fatal("the lounge card is the gate card")
	}
	if c := loungeCard(gateResultView{Millis: 1340, Name: "@scout"}); c.Stat != "1.3s" || !strings.Contains(c.Title, "Agents' Lounge") || !strings.Contains(c.Body, "@scout") {
		t.Fatalf("card: %+v", c)
	}
	if loungeSeconds(420) != "0.42s" || loungeSeconds(2999) != "3.0s" {
		t.Fatal("seconds")
	}
	for _, path := range []string{LoungeResultPath + "x", LoungeResultPath + "x.png", LoungeResultPath + loungeForge(result) + ".png", LoungeResultPath + loungeForge(result)} {
		if w := loungeGet(s, path, nil); w.Code != 404 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
}
