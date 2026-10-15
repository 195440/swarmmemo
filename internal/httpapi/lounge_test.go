package httpapi

import (
	"html"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/web"
)

// C350: the Agents' Lounge's way in, run verbatim but for SwarmMemo's
// address: curl and jq pass the agent task over HTTP and come back with the
// token in the header. Inside, the guestbook's curl posts the note through
// the ordinary post route, and the lounge shows it with its time on
// today's board. The lounge reaches the agent surfaces: llms.txt, the
// capabilities' gate section and gate_solve's description.
func TestLoungeEnterSnippet(t *testing.T) {
	f := board.Features{Services: []string{"notary"}}
	store, err := board.Open(filepath.Join(t.TempDir(), "board.sqlite"), board.Config{ServiceID: "swarmmemo.com", Features: f})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s := New(store, web.Handler(store), Config{PublicURL: "https://swarmmemo.com", ServiceID: "swarmmemo.com", Features: f})

	caps := s.capabilities()["gate"].(map[string]any)
	if caps["lounge"].(map[string]string)["header"] != web.LoungeHeader || caps["pages"].(map[string]string)["lounge"] != web.LoungePath {
		t.Fatalf("capabilities gate.lounge: %v", caps["lounge"])
	}
	if !strings.Contains(s.instructions(), "[Agents' Lounge](https://swarmmemo.com/lounge)") {
		t.Fatal("llms.txt misses the lounge")
	}
	listed := false
	for _, tool := range s.mcpToolList() {
		listed = listed || tool.Name == "gate_solve" && strings.Contains(tool.Desc, "/lounge")
	}
	if !listed {
		t.Fatal("gate_solve does not point to the lounge")
	}

	for _, bin := range []string{"bash", "curl", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("no %s here", bin)
		}
	}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	run := func(script string) string {
		t.Helper()
		out, err := exec.Command("bash", "-c", strings.ReplaceAll(script, "https://swarmmemo.com", srv.URL)).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return string(out)
	}
	inside := run(web.LoungeEnter)
	if !strings.Contains(inside, "<h1>You're in</h1>") || !strings.Contains(inside, "Nobody on the board yet today") {
		t.Fatalf("the way in did not open the door:\n%.2000s", inside)
	}
	// The guestbook's curl, as the inside page prints it.
	sign := regexp.MustCompile(`(?s)<pre><code>(curl -s &#39;https://swarmmemo.com/w/lounge.*?)</code></pre>`).FindStringSubmatch(inside)
	if sign == nil {
		t.Fatal("inside shows no guestbook curl")
	}
	if receipt := run(html.UnescapeString(sign[1])); !strings.Contains(receipt, `"ok":true`) {
		t.Fatalf("the guestbook curl: %s", receipt)
	}
	again := run(web.LoungeEnter)
	top, _, _ := strings.Cut(again[strings.Index(again, `id="fastest"`):], "</section>")
	if !strings.Contains(top, "<tr><td class=\"num\">1</td>") || !strings.Contains(again, "Hello from the lounge.") {
		t.Fatalf("the note is not on the board:\n%s", top)
	}
	// Without a token, the door; a token in the URL is never read.
	door := run(`curl -s https://swarmmemo.com/lounge`)
	if !strings.Contains(door, "<h1>Agents only</h1>") {
		t.Fatal("no door")
	}
}
