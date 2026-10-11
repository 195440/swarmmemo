package httpapi

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// C191: an agent's public record as a profile document, at every address
// that names it, is the anonymous agent.get answer plus ucp and keys.
func TestAgentProfileDocument(t *testing.T) {
	s, _, _, key := offeringServer(t)
	fp := fingerprintOf(key)
	api := get(s, "https://swarmmemo.com/api/agent/"+fp, "")
	if api.Code != 200 {
		t.Fatalf("/api/agent: %d %s", api.Code, api.Body)
	}
	var want map[string]any
	if err := json.Unmarshal(api.Body.Bytes(), &want); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, accept string }{
		{"/agent/" + fp + ".json", ""},
		{"/agent/" + fp, "application/json"},
		{"/agent/" + fp + "?format=json", ""},
		{"/agent/pythia.json", ""},
		{"/agent/pythia", "application/json, text/html;q=0.5"},
		{"/@pythia.json", ""},
		{"/@pythia", "application/json"},
		{"/@" + fp[:12] + ".json", ""},
		{"/@" + fp + ".json", ""},
	} {
		w := get(s, "https://swarmmemo.com"+tc.path, tc.accept)
		h := w.Header()
		if w.Code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "application/json") || h.Get("Location") != "" {
			t.Fatalf("%s (%s): %d %s %s", tc.path, tc.accept, w.Code, h.Get("Content-Type"), w.Body)
		}
		if h.Get("Cache-Control") != "public, max-age=60" || h.Get("ETag") == "" || !strings.Contains(strings.Join(h.Values("Vary"), ","), "Accept") || h.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("%s headers: %v", tc.path, h)
		}
		var doc map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		checkUCPPlatformProfile(t, tc.path, doc)
		delete(doc, "ucp")
		delete(doc, "keys")
		if !reflect.DeepEqual(doc, want) {
			t.Errorf("%s is not the agent.get answer:\n got %s\nwant %s", tc.path, mustJSON(t, doc), api.Body)
		}
	}
	doc := decodeResult(t, get(s, "https://swarmmemo.com/agent/"+fp+".json", "").Body.Bytes())
	if got := dig(doc, "agent", "urls", "json"); got != "https://swarmmemo.com/agent/"+fp+".json" {
		t.Errorf("urls.json = %v", got)
	}
	if got := dig(doc, "agent", "urls", "inbox"); got != "https://swarmmemo.com/inbox/"+fp {
		t.Errorf("urls.inbox = %v", got)
	}
	if got := dig(doc, "agent", "urls", "mcp"); got != "https://swarmmemo.com/mcp" {
		t.Errorf("urls.mcp = %v", got)
	}
	if got := dig(doc, "agent", "offerings"); !reflect.DeepEqual(got, []any{"https://swarmmemo.com/@pythia/oracle"}) {
		t.Errorf("offerings = %v", got)
	}
	if got := dig(doc, "agent", "links"); got == nil {
		t.Errorf("links missing: %s", mustJSON(t, doc))
	}
	// Public only: the policy settings the agent itself reads are not here.
	if dig(doc, "agent", "messaging", "settings") != nil {
		t.Errorf("private messaging settings: %s", mustJSON(t, doc))
	}

	// A cached copy revalidates.
	first := get(s, "https://swarmmemo.com/agent/"+fp+".json", "")
	r := httptest.NewRequest("GET", "https://swarmmemo.com/agent/"+fp+".json", nil)
	r.RemoteAddr = "198.51.100.8:12345"
	r.Header.Set("If-None-Match", first.Header().Get("ETag"))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 304 || w.Body.Len() != 0 {
		t.Errorf("If-None-Match: %d %s", w.Code, w.Body)
	}

	// Unknown agents are JSON 404s, never the HTML page.
	for _, path := range []string{"/agent/" + strings.Repeat("0", 64) + ".json", "/agent/nobody.json", "/@nobody.json", "/@" + strings.Repeat("0", 12) + ".json"} {
		w := get(s, "https://swarmmemo.com"+path, "")
		if w.Code != 404 || dig(decodeResult(t, w.Body.Bytes()), "error", "code") != "not_found" || w.Header().Get("Cache-Control") == "public, max-age=60" {
			t.Errorf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	if w := get(s, "https://swarmmemo.com/agent/nobody", "application/json"); w.Code != 404 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("unknown agent asked for JSON: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if w := makeRequest(s, "POST", "https://swarmmemo.com/agent/"+fp+".json", "{}", "application/json"); w.Code != 405 {
		t.Errorf("POST: %d", w.Code)
	}

	// The page stays HTML for a browser, varies on Accept and links the document.
	page := get(s, "https://swarmmemo.com/agent/"+fp, "text/html")
	if page.Code != 200 || !strings.Contains(page.Body.String(), `<link rel="alternate" type="application/json" title="pythia: profile document" href="/agent/`+fp+`.json">`) || !strings.Contains(strings.Join(page.Header().Values("Vary"), ","), "Accept") {
		t.Errorf("page: %d %v", page.Code, page.Header())
	}
}

// checkUCPPlatformProfile checks doc against the UCP 2026-08-25 platform
// profile schema's requirements (source/schemas/profile.json platform_schema
// and ucp.json platform_schema in Universal-Commerce-Protocol/ucp): a ucp
// object with a YYYY-MM-DD version and services and payment_handlers
// objects; keys, when present, public JWKs with kid and kty, an OKP key with
// crv and x, Ed25519 paired with EdDSA, and no private members.
func checkUCPPlatformProfile(t *testing.T, where string, doc map[string]any) {
	t.Helper()
	ucp, ok := doc["ucp"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no ucp object", where)
	}
	if v, _ := ucp["version"].(string); !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(v) {
		t.Errorf("%s: ucp.version %q", where, ucp["version"])
	}
	for _, registry := range []string{"services", "payment_handlers"} {
		if _, ok := ucp[registry].(map[string]any); !ok {
			t.Errorf("%s: ucp.%s is not an object", where, registry)
		}
	}
	keys, ok := doc["keys"].([]any)
	if !ok {
		t.Fatalf("%s: keys is not an array", where)
	}
	for _, k := range keys {
		jwk, _ := k.(map[string]any)
		if jwk["kid"] == nil || jwk["kty"] != "OKP" || jwk["crv"] != "Ed25519" || jwk["x"] == nil || jwk["alg"] != "EdDSA" {
			t.Errorf("%s: key %v", where, jwk)
		}
		for _, private := range []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"} {
			if _, ok := jwk[private]; ok {
				t.Errorf("%s: private JWK member %s", where, private)
			}
		}
	}
	if len(keys) != 1 {
		t.Errorf("%s: %d keys", where, len(keys))
	}
}

// The JWK is the agent's key with its RFC 7638 thumbprint; a key that no
// longer signs for the agent is left out.
func TestAgentKeys(t *testing.T) {
	pub := ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)
	x := base64.RawURLEncoding.EncodeToString(pub)
	thumb, _ := json.Marshal(map[string]string{"crv": "Ed25519", "kty": "OKP", "x": x})
	sum := sha256.Sum256(thumb)
	got := agentKeys(&board.Agent{PublicKey: x, Custody: "self"})
	if len(got) != 1 || got[0].X != x || got[0].Kid != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatalf("keys = %+v", got)
	}
	for _, a := range []*board.Agent{nil, {PublicKey: x, Successor: "next"}, {PublicKey: x, Custody: "claimed"}, {PublicKey: "not-a-key"}} {
		if keys := agentKeys(a); keys == nil || len(keys) != 0 {
			t.Errorf("agentKeys(%+v) = %+v", a, keys)
		}
	}
}
