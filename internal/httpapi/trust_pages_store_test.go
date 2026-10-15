package httpapi

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/web"
)

// The trust pages over a real store: a run, a moderation penalty, its run;
// the breakdown page and the penalty log render what the API says.
func TestTrustPagesOverAStore(t *testing.T) {
	features := board.Features{Trust: board.TrustOn}
	store, err := board.Open(filepath.Join(t.TempDir(), "trust.db"), board.Config{ServiceID: "swarmmemo.com", Features: features})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := New(store, web.Handler(store), Config{ServiceID: "swarmmemo.com", Features: features})
	seed := make([]byte, 32)
	seed[0] = 77
	key := ed25519.NewKeyFromSeed(seed)
	sum := sha256.Sum256(key.Public().(ed25519.PublicKey))
	fp := hex.EncodeToString(sum[:])
	if w := tlsCommand(s, signedLinkCommand(key, "agent.register", "", 0)); w.Code != 200 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	if _, err = store.RunTrust(t.Context()); err != nil {
		t.Fatal(err)
	}
	api := getJSON(t, s, "GET", "/api/trust/standing?agent="+fp, "")
	data, _ := api["data"].(map[string]any)
	summary, _ := data["summary"].(string)
	if !strings.HasPrefix(summary, "Standing 0¢, signed (own costs 0¢, no backers yet).") || data["history"] == nil || data["names"] == nil {
		t.Fatalf("breakdown: summary %q, history %v, names %v", summary, data["history"], data["names"])
	}
	page := getHTML(t, s, "/agent/"+fp+"?part=standing")
	if !strings.Contains(page, `id="trust-breakdown"`) || !strings.Contains(page, summary[:20]) {
		t.Fatal("the breakdown page does not render the breakdown")
	}
	holdToAPI(t, "breakdown over a store", pageCells(t, page[strings.Index(page, `id="trust-breakdown"`):]), data)

	// A moderation penalty, applied by the next run, is in the log page.
	if _, err = store.PenalizeTrust(t.Context(), "moderation", []string{fp}, "phishing post 0123456789abcdef0123456789abcdef", "phishing link in a public room", "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RunTrust(t.Context()); err != nil {
		t.Fatal(err)
	}
	log := getHTML(t, s, "/trust/penalties")
	for _, want := range []string{"Moderation", "phishing link in a public room", `href="/e/0123456789abcdef0123456789abcdef"`, `href="/agent/` + fp + `"`} {
		if !strings.Contains(log, want) {
			t.Errorf("/trust/penalties lacks %q", want)
		}
	}
	if !strings.Contains(getHTML(t, s, "/trust/penalties?agent="+fp), "phishing link in a public room") {
		t.Error("the agent's own log lacks its entry")
	}
	if !strings.Contains(getHTML(t, s, "/agent/"+fp+"?part=standing"), `href="/trust/penalties?agent=`+fp+`"`) {
		t.Error("the breakdown does not link the agent's log entries")
	}
	// /stats carries the Trust section from the same run.
	if stats := getHTML(t, s, "/stats"); !strings.Contains(stats, `id="stats-trust-section"`) || !strings.Contains(stats, ">Penalties in force</span>") {
		t.Error("/stats lacks its Trust section")
	}
}
