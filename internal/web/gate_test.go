package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/gate"
)

// C325: the SwarmMemo Gate's pages, script and windows.

func gateStore(t *testing.T) *board.Store {
	t.Helper()
	s, err := board.Open(filepath.Join(t.TempDir(), "board.sqlite"), board.Config{ServiceID: "swarmmemo.com", Features: board.Features{Services: []string{"notary"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func gateGet(s board.Service, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	Handler(s).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestGatePages(t *testing.T) {
	s := gateStore(t)
	w := gateGet(s, GatePath)
	body := w.Body.String()
	for _, want := range []string{"A CAPTCHA alternative that lets real agents in", `<script src="/gate/v1.js" data-min="0" data-fail="closed"></script>`, "signing now",
		`id="gate-strictness"`, `value="backed" data-min="50"`, `value="strict" data-min="0" data-bits="24"`, `<option value="100">at least $1</option>`, `name="invisible"`, `id="gate-demo-decision"`,
		`<select name="bits">`, `<select name="mode">`, "<code>data-bits</code>", "<code>data-mode</code>", "Compared with Turnstile and reCAPTCHA", "Invisible mode", "a browser&#39;s minutes add a fraction of a cent",
		`id="gate-generator"`, `data-frame="`, "Fail open or fail closed", "/gate/agent", "gate-page.js", "check-gate.mjs", "swarmmemo-gate-token"} {
		if !strings.Contains(body, want) {
			t.Errorf("/gate misses %q", want)
		}
	}
	if w.Code != 200 || strings.Contains(body, `name="robots" content="noindex"`) {
		t.Fatalf("/gate: %d, indexable", w.Code)
	}
	// Without the notary key, the status says so.
	off, err := board.Open(filepath.Join(t.TempDir(), "off.sqlite"), board.Config{ServiceID: "swarmmemo.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer off.Close()
	if !strings.Contains(gateGet(off, GatePath).Body.String(), "unavailable on this service") {
		t.Fatal("/gate shows signing without a key")
	}
	agent := gateGet(s, GateAgentPath).Body.String()
	for _, want := range []string{"The CAPTCHA humans can&#39;t pass", `id="gate-agent-start"`, "developer tools can pass it too", "a demo, not security", "gate --agent-task", fmt.Sprint(gate.AgentDeadlineMillis), "/v1/gate/challenge"} {
		if !strings.Contains(agent, want) {
			t.Errorf("/gate/agent misses %q", want)
		}
	}
	var doc gateDocument
	if w := gateGet(s, "/gate.json"); json.Unmarshal(w.Body.Bytes(), &doc) != nil || doc.Snippet != GateSnippet || doc.CheckNode != GateCheckNode {
		t.Fatalf("/gate.json: %s", w.Body.String())
	}
}

func TestGateScript(t *testing.T) {
	w := gateGet(nil, GateScriptPath)
	h := w.Header()
	if w.Code != 200 || h.Get("Access-Control-Allow-Origin") != "*" || !strings.HasPrefix(h.Get("Content-Type"), "text/javascript") || h.Get("Content-Security-Policy") != "" {
		t.Fatalf("script: %d %v", w.Code, h)
	}
	js := w.Body.String()
	if !strings.HasPrefix(js, "(() => {\nconst powSource = \"") || !strings.Contains(js, "SwarmMemoGatePow") || !strings.Contains(js, "swarmmemo-gate-token") {
		t.Fatal("the bundle is the solver and the widget in one closure")
	}
	// The widget never puts a token in a URL, and posts only to its own origin.
	for _, banned := range []string{"?token=", "&token=", "localStorage", "document.cookie"} {
		if strings.Contains(js, banned) {
			t.Errorf("the gate script uses %q", banned)
		}
	}
	var z bytes.Buffer
	gz := gzip.NewWriter(&z)
	_, _ = gz.Write(w.Body.Bytes())
	_ = gz.Close()
	// 8 KiB: C348's refusal, ways through and invisible dialog included.
	if z.Len() > 8<<10 {
		t.Fatalf("gate script is %d bytes gzipped, over 8 KiB", z.Len())
	}
	t.Logf("gate script: %d bytes, %d gzipped", w.Body.Len(), z.Len())
}

func TestGateWindows(t *testing.T) {
	pass := gateGet(nil, GatePassPath+"?origin=https://shop.example&min=250&nonce=abcdefgh12")
	if pass.Code != 200 || pass.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(pass.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("pass window: %d %v", pass.Code, pass.Header())
	}
	for _, want := range []string{"https://shop.example", "at least $2.50", `data-min="250"`, `id="gate-pass-keep"`, "browser-key.js", "gate-pass.js"} {
		if !strings.Contains(pass.Body.String(), want) {
			t.Errorf("pass window misses %q", want)
		}
	}
	if strings.Contains(pass.Body.String(), `id="gate-pass-keep" checked`) {
		t.Fatal("keeping an identity is never ticked for the visitor")
	}
	// C348: the ways through, and the site's work.
	earn := gateGet(nil, GatePassPath+"?origin=https://shop.example&min=50&way=earn&bits=24")
	for _, want := range []string{`data-way="earn"`, `data-bits="24"`, `id="gate-earn-plan"`, `id="gate-earn-progress"`, "gate-earn.js", ">Start</button>", "standing belongs to a key"} {
		if !strings.Contains(earn.Body.String(), want) {
			t.Errorf("earn window misses %q", want)
		}
	}
	wallet := gateGet(nil, GatePassPath+"?origin=https://shop.example&min=50&way=wallet")
	for _, want := range []string{`data-way="wallet"`, `id="gate-wallet-cents"`, ">Link a wallet</button>", "never passes a site alone"} {
		if !strings.Contains(wallet.Body.String(), want) {
			t.Errorf("wallet window misses %q", want)
		}
	}
	if strings.Contains(wallet.Body.String(), "gate-earn.js") || strings.Contains(pass.Body.String(), "gate-earn.js") {
		t.Fatal("only the earn window loads the worker")
	}
	frame := gateGet(nil, GateFramePath+"?origin=https://shop.example&fail=open&bits=22")
	if frame.Code != 200 || frame.Header().Get("X-Frame-Options") != "" || !strings.Contains(frame.Header().Get("Content-Security-Policy"), "frame-ancestors *") ||
		!strings.Contains(frame.Body.String(), `data-gate-frame="1"`) || !strings.Contains(frame.Body.String(), `data-bits="22"`) {
		t.Fatalf("frame: %d %v %s", frame.Code, frame.Header(), frame.Body.String())
	}
	for _, path := range []string{
		GatePassPath + "?origin=https://shop.example/path", GatePassPath + "?origin=javascript:alert(1)", GatePassPath + "?origin=https://a.example&x=1",
		GatePassPath + "?origin=https://a.example&origin=https://b.example", GatePassPath + "?origin=https://a.example&min=-1", GatePassPath + "?origin=https://a.example&via=top",
		GatePassPath + "?origin=https://a.example&nonce=<b>", GateFramePath + "?origin=https://a.example&bits=40", GateFramePath + "?origin=https://a.example&via=frame",
		GatePassPath + "?origin=https://a.example&way=pay", GatePassPath + "?origin=https://a.example&bits=19", GateFramePath + "?origin=https://a.example&way=earn",
	} {
		if w := gateGet(nil, path); w.Code != 400 || strings.Contains(w.Body.String(), "gate-pass.js") || strings.Contains(w.Body.String(), "gate-embed.js") {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	if gateDollars(250) != "$2.50" || gateDollars(500) != "$5" || gateDollars(205) != "$2.05" {
		t.Fatal("dollars")
	}
}

func TestGateResultCard(t *testing.T) {
	s := gateStore(t)
	ctx := context.Background()
	ch, err := s.Execute(ctx, board.Command{Operation: "gate.challenge", Data: `{"schema":1,"origin":"https://swarmmemo.com","mode":"agent"}`}, "192.0.2.4")
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
	res, err := s.Execute(ctx, board.Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"answer":"%d"}`, ch.Data["challenge"], sum)}, "192.0.2.4")
	if err != nil {
		t.Fatal(err)
	}
	card := res.Data["card"].(map[string]any)
	page := gateGet(s, card["url"].(string))
	body := page.Body.String()
	for _, want := range []string{"An agent passed the CAPTCHA humans can&#39;t pass in", `property="og:image" content="https://swarmmemo.com` + card["image"].(string), `content="1200"`, "x.com/intent/post", "Keep your identity", `name="robots" content="noindex"`} {
		if !strings.Contains(body, want) {
			t.Errorf("result page misses %q", want)
		}
	}
	img := gateGet(s, card["image"].(string))
	if img.Code != 200 || img.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(img.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("card image: %d %v", img.Code, img.Header())
	}
	// C348: the share card is dark, with the time as its biggest thing.
	decoded, err := png.Decode(bytes.NewReader(img.Body.Bytes()))
	if err != nil || decoded.Bounds().Dx() != 1200 || decoded.Bounds().Dy() != 630 {
		t.Fatalf("card: %v", err)
	}
	dark, green := 0, 0
	for y := 0; y < 630; y += 3 {
		for x := 0; x < 1200; x += 3 {
			r, g, b, _ := decoded.At(x, y).RGBA()
			if r>>8 < 40 && g>>8 < 40 && b>>8 < 40 {
				dark++
			}
			if g>>8 > 200 && r>>8 < 120 {
				green++
			}
		}
	}
	if total := (1200 / 3) * (630 / 3); dark < total*6/10 || green < total/50 {
		t.Fatalf("card: %d dark, %d green of %d sampled pixels", dark, green, total)
	}
	if c := gateCard(gateResultView{Millis: 843, Name: "@scout", When: "15 Oct 2026"}); c.Stat != "843 ms" || c.Kind != "g" || !strings.Contains(c.Body, "@scout") {
		t.Fatalf("card fields: %+v", c)
	}
	id := strings.TrimPrefix(card["url"].(string), board.GateResultPath)
	forged := id[:len(id)-2] + "AA"
	if forged == id { // one signature in 4,096 ends in AA already
		forged = id[:len(id)-2] + "BA"
	}
	for _, path := range []string{board.GateResultPath + forged, board.GateResultPath + forged + ".png", board.GateResultPath + "x.png", board.GateResultPath + "<script>"} {
		if w := gateGet(s, path); w.Code != 404 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
}
