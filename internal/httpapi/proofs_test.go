package httpapi

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/proofs"
	"swarmmemo/internal/web"
)

// The browser half of an OAuth proof (proofs.go): the start page names the
// agent and binds the browser; the callback needs that browser's cookie,
// and nothing else in the request (redirect URI, kind) is taken on trust.
func TestProofBrowserFlow(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2.googleapis.com/token":
			_ = r.ParseForm()
			if r.PostForm.Get("code") != "good" || r.PostForm.Get("redirect_uri") != "https://swarmmemo.com/proofs/oauth/google/callback" || r.PostForm.Get("code_verifier") == "" {
				http.Error(w, `{"error":"invalid_grant"}`, 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "bearer"})
		case "/openidconnect.googleapis.com/v1/userinfo":
			_ = json.NewEncoder(w).Encode(map[string]any{"sub": "777000"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.Close)
	reg := proofs.New(proofs.Config{OAuth: map[string]proofs.OAuthCredentials{"google": {ClientID: "gid", ClientSecret: "gsecret"}}, Client: provider.Client(), TestBaseURL: provider.URL})
	features := board.Features{Trust: board.TrustOn}
	store, err := board.Open(filepath.Join(t.TempDir(), "proofs.db"), board.Config{ServiceID: "swarmmemo.com", Features: features, Proofs: reg, PublicURL: "https://swarmmemo.com"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	s := New(store, web.Handler(store), Config{ServiceID: "swarmmemo.com", Features: features})
	seed := make([]byte, 32)
	seed[0] = 91
	key := ed25519.NewKeyFromSeed(seed)
	sum := sha256.Sum256(key.Public().(ed25519.PublicKey))
	fingerprint := hex.EncodeToString(sum[:])
	if w := tlsCommand(s, signedLinkCommand(key, "agent.register", "", 0)); w.Code != 200 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	w := tlsCommand(s, signedStanding(key, "standing.challenge", `{"schema":1,"kind":"google"}`, 1))
	var ch struct {
		Data struct {
			AuthURL string `json:"auth_url"`
			Nonce   string `json:"nonce"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ch); err != nil || w.Code != 200 || ch.Data.AuthURL == "" {
		t.Fatalf("challenge: %d %s", w.Code, w.Body.String())
	}
	state := ch.Data.Nonce
	get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "198.51.100.8:12345"
		r.TLS = &tls.ConnectionState{}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	start := get(strings.TrimPrefix(ch.Data.AuthURL, "https://swarmmemo.com"), nil)
	body := start.Body.String()
	if start.Code != 200 || !strings.Contains(body, fingerprint) || !strings.Contains(body, "accounts.google.com/o/oauth2/v2/auth?") || strings.Contains(body, "gsecret") {
		t.Fatalf("start: %d %s", start.Code, body)
	}
	if csp := start.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "script-src") {
		t.Fatalf("csp %q", csp)
	}
	var cookie *http.Cookie
	for _, c := range start.Result().Cookies() {
		if c.Name == "__Host-sm_proof" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode || cookie.Value == state || cookie.Value != proofTag(state) {
		t.Fatalf("cookie %+v", cookie)
	}
	callback := "/proofs/oauth/google/callback?code=good&state=" + state
	// Another browser (no cookie, or another state's) cannot complete it,
	// and does not spend the state.
	if w := get(callback, nil); w.Code != 400 || !strings.Contains(w.Body.String(), "did not start in this browser") {
		t.Fatalf("no cookie: %d", w.Code)
	}
	if w := get(callback, &http.Cookie{Name: "__Host-sm_proof", Value: proofTag("other")}); w.Code != 400 {
		t.Fatalf("other cookie: %d", w.Code)
	}
	// The provider's refusal, from the same browser, links nothing.
	if w := get("/proofs/oauth/google/callback?error=access_denied&state="+state, cookie); w.Code != 400 || !strings.Contains(w.Body.String(), "Nothing was linked") {
		t.Fatalf("denied: %d", w.Code)
	}
	ok := get(callback, cookie)
	if ok.Code != 200 || !strings.Contains(ok.Body.String(), "google:777000") || strings.Contains(ok.Body.String(), "tok") && strings.Contains(ok.Body.String(), "access_token") {
		t.Fatalf("callback: %d %s", ok.Code, ok.Body.String())
	}
	if again := get(callback, cookie); again.Code != 409 {
		t.Fatalf("replay: %d", again.Code)
	}
	for path, code := range map[string]int{"/proofs/oauth/google/other": 404, "/proofs/oauth/github/start?state=" + state: 404, "/proofs/elsewhere": 404} {
		if w := get(path, nil); w.Code != code {
			t.Errorf("%s: %d, want %d", path, w.Code, code)
		}
	}
	r := httptest.NewRequest("POST", callback, nil)
	r.RemoteAddr = "198.51.100.8:12345"
	pw := httptest.NewRecorder()
	s.ServeHTTP(pw, r)
	if pw.Code != 405 {
		t.Errorf("POST: %d", pw.Code)
	}
	// /capabilities lists every kind with its status and price row.
	caps := s.standingCapabilities()["raise"].(map[string]any)["roots"].(map[string]any)
	if caps["google"].(map[string]any)["status"] != "enabled" || caps["reddit"].(map[string]any)["status"] != "disabled-no-credentials" || caps["reddit"].(map[string]any)["cap_cents"] != int64(200) {
		t.Fatalf("capabilities: %v", caps)
	}
}
