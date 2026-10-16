package web

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
)

const verifyTestCommit = "0123456789abcdef0123456789abcdef01234567"

// publishTestVerification signs and publishes a record; failing names a
// failing check.
func publishTestVerification(t *testing.T, s board.Service, seed byte, failing bool) board.Verification {
	t.Helper()
	state := "pass"
	if failing {
		state = "fail"
	}
	return publishTestChecks(t, s, seed, []map[string]any{{"property": "tests", "state": "pass", "note": "41 passed"}, {"property": "lint", "state": state}, {"property": "fuzz", "state": "not_checked"}})
}

// publishTestChecks signs and publishes a record with these checks.
func publishTestChecks(t *testing.T, s board.Service, seed byte, checks []map[string]any) board.Verification {
	t.Helper()
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	if seed != 0 {
		raw := make([]byte, 32)
		raw[0] = seed
		key = ed25519.NewKeyFromSeed(raw)
	}
	data, _ := json.Marshal(map[string]any{"schema": 1, "repo": "https://github.com/Owner/Repo", "commit": verifyTestCommit,
		"command": "pytest -q <script>alert(1)</script>", "environment": "python 3.12\nlinux, network off", "output_sha256": strings.Repeat("ab", 32),
		"checks": checks,
		"limits": "Windows was not tested."})
	c := board.Command{Operation: "verify.publish", Data: string(data), PublicKey: base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey)),
		Timestamp: time.Now().Unix(), Nonce: "verify-nonce-" + string('a'+rune(seed)), RequestID: "verify-" + string('a'+rune(seed))}
	c.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, board.Canonical("swarmmemo.com", c)))
	res, err := s.Execute(context.Background(), c, "192.0.2.9")
	if err != nil {
		t.Fatal(err)
	}
	return res.Data["verification"].(board.Verification)
}

// /v/ID shows the record: subject at its commit, verifier, each check, the
// limits, the re-run and signature blocks and the badge snippets; it is
// cached, with an OG card.
func TestVerificationPage(t *testing.T) {
	s := gateStore(t)
	v := publishTestVerification(t, s, 1, false)
	w := gateGet(s, "/v/"+v.ID)
	body := w.Body.String()
	if w.Code != 200 || w.Header().Get("Cache-Control") != VerifyPageCacheControl {
		t.Fatalf("/v/ID: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	for _, want := range []string{"Verified by SwarmMemo", "owner/repo@0123456", "2 of 2 checks passed", `href="https://github.com/owner/repo/commit/` + verifyTestCommit + `"`,
		"<code>tests</code>", "41 passed", "not checked", "Windows was not tested.", "pytest -q &lt;script&gt;alert(1)&lt;/script&gt;",
		"git clone https://github.com/owner/repo repo &amp;&amp; cd repo", "git checkout " + verifyTestCommit, "Ed25519PublicKey", "/api/verifications/" + v.ID,
		"[![Verified by SwarmMemo](https://swarmmemo.com/v/" + v.ID + "/badge.svg)](https://swarmmemo.com/v/" + v.ID + ")",
		"https://swarmmemo.com/v/badge?repo=https%3A%2F%2Fgithub.com%2Fowner%2Frepo&amp;commit=" + verifyTestCommit, `property="og:image" content="https://swarmmemo.com/v/` + v.ID + `.png"`, "/agent/" + v.Verifier.Fingerprint} {
		if !strings.Contains(body, want) {
			t.Errorf("/v/ID misses %q", want)
		}
	}
	if strings.Contains(body, "<script>alert(1)") {
		t.Fatal("a verifier's text reached the page unescaped")
	}
	if w := gateGet(s, "/v/0000000000000000"); w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing record: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	if w := gateGet(s, "/v/not-an-id"); w.Code != 404 {
		t.Fatalf("bad id: %d", w.Code)
	}
}

// C385: a flaky check shows as "flaky: k of n runs passed" with its
// failing step; the record reads flaky, and so does its badge.
func TestVerificationPageFlaky(t *testing.T) {
	s := gateStore(t)
	v := publishTestChecks(t, s, 3, []map[string]any{{"property": "tests", "state": "flaky", "runs": 11, "passes": 7, "failing_step": "test_<timeout>", "note": "3 timeouts"},
		{"property": "lint", "state": "pass"}})
	if v.Result != "flaky" || v.Flaky != 1 || v.Passed != 1 || v.Ran != 2 {
		t.Fatalf("record: %s %d/%d, %d flaky", v.Result, v.Passed, v.Ran, v.Flaky)
	}
	body := gateGet(s, "/v/"+v.ID).Body.String()
	for _, want := range []string{"Flaky: 1 of 2 checks passed, 1 flaky", `<tr class="check-flaky">`, `<span class="badge check-state">flaky</span>`,
		`<span class="check-runs">flaky: 7 of 11 runs passed</span>. Failing step <code>test_&lt;timeout&gt;</code>. 3 timeouts`, "flaky, 1 of 2 checks passed, 1 flaky"} {
		if !strings.Contains(body, want) {
			t.Errorf("/v/ID misses %q", want)
		}
	}
	badge := gateGet(s, "/v/"+v.ID+"/badge.svg").Body.String()
	if !strings.Contains(badge, "flaky · 1/2") {
		t.Errorf("badge: %s", badge)
	}
}

// The badges say N/M and the commit, red when a check failed; the record's
// badge is cached for an hour, the subject's for minutes, both with an ETag
// a revalidation matches; /v/latest redirects to the newest record.
func TestVerificationBadges(t *testing.T) {
	s := gateStore(t)
	passed := publishTestVerification(t, s, 2, false)
	w := gateGet(s, "/v/"+passed.ID+"/badge.svg")
	svg := w.Body.String()
	h := w.Header()
	if w.Code != 200 || h.Get("Content-Type") != "image/svg+xml; charset=utf-8" || h.Get("Cache-Control") != VerifyBadgeCacheControl || h.Get("ETag") == "" ||
		h.Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("badge: %d %v", w.Code, h)
	}
	for _, want := range []string{"verified by SwarmMemo", "2/2 checks passed · 0123456", badgeTones["good"][0]} {
		if !strings.Contains(svg, want) {
			t.Errorf("badge misses %q: %s", want, svg)
		}
	}
	if strings.Contains(svg, "<script") || strings.Contains(svg, "href") {
		t.Fatal("a badge carries a script or a link")
	}
	r := httptest.NewRequest("GET", "/v/"+passed.ID+"/badge.svg", nil)
	r.Header.Set("If-None-Match", h.Get("ETag"))
	rec := httptest.NewRecorder()
	Handler(s).ServeHTTP(rec, r)
	if rec.Code != 304 {
		t.Fatalf("revalidation: %d", rec.Code)
	}
	failed := publishTestVerification(t, s, 3, true)
	if svg := gateGet(s, "/v/"+failed.ID+"/badge.svg").Body.String(); !strings.Contains(svg, "failed · 1/2 · 0123456") || !strings.Contains(svg, badgeTones["bad"][0]) {
		t.Fatalf("failed badge: %s", svg)
	}
	// The subject's badge is its newest record's.
	sub := gateGet(s, "/v/badge?repo=https://github.com/owner/repo&commit="+verifyTestCommit)
	if sub.Code != 200 || sub.Header().Get("Cache-Control") != BadgeCacheControl || !strings.Contains(sub.Body.String(), "failed · 1/2") {
		t.Fatalf("subject badge: %d %s", sub.Code, sub.Body.String())
	}
	if none := gateGet(s, "/v/badge?repo=https://github.com/other/repo"); none.Code != 200 || !strings.Contains(none.Body.String(), "no record") {
		t.Fatalf("no record: %d %s", none.Code, none.Body.String())
	}
	if latest := gateGet(s, "/v/latest?repo=https://github.com/Owner/Repo"); latest.Code != 302 || latest.Header().Get("Location") != "/v/"+failed.ID {
		t.Fatalf("latest: %d %v", latest.Code, latest.Header())
	}
	for _, path := range []string{"/v/badge", "/v/badge?repo=ftp://x/y", "/v/badge?repo=https://github.com/o/r&file=" + strings.Repeat("a", 64), "/v/latest?repo=https://github.com/none/here"} {
		if w := gateGet(s, path); w.Code < 400 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	if w := gateGet(s, "/v/0000000000000000/badge.svg"); w.Code != 404 {
		t.Fatalf("unknown record's badge: %d", w.Code)
	}
	// The card is a 1200x630 PNG.
	card := gateGet(s, "/v/"+passed.ID+".png")
	img, err := png.Decode(card.Body)
	if err != nil || card.Header().Get("Content-Type") != "image/png" || img.Bounds().Dx() != 1200 || img.Bounds().Dy() != 630 {
		t.Fatalf("card: %v %v", err, card.Header())
	}
}
