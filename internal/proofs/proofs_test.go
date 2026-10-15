package proofs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"swarmmemo/internal/services"
	"swarmmemo/internal/trust"
)

var testNow = time.Unix(1790553600, 0)

const testFP = "aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11aa11"

// fake is one httptest server standing in for every provider's API: the
// first path segment is the host the provider called (Config.TestBaseURL).
type fake struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	calls  []string
	// challenges are the PKCE challenges handed out by AuthURL, by state.
	challenges map[string]string
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, routes: map[string]http.HandlerFunc{}, challenges: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.EscapedPath())
		h := f.routes[r.Method+" "+r.URL.EscapedPath()]
		f.mu.Unlock()
		if h == nil {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) on(method, path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[method+" "+path] = h
}

func (f *fake) json(method, path string, v any) {
	f.on(method, path, func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(v) })
}

func (f *fake) registry(c Config) *Registry {
	c.Client, c.TestBaseURL = f.srv.Client(), f.srv.URL
	return New(c)
}

// oauthToken serves a token endpoint that accepts only code "good" with
// the right PKCE verifier, redirect URI and client authentication.
func (f *fake) oauthToken(path, id, secret string, basic bool) {
	f.on("POST", path, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotID, gotSecret := r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		if basic {
			var ok bool
			gotID, gotSecret, ok = r.BasicAuth()
			if !ok || r.PostForm.Get("client_secret") != "" {
				http.Error(w, `{"error":"invalid_client"}`, 401)
				return
			}
		}
		verifier := r.PostForm.Get("code_verifier")
		f.mu.Lock()
		want := f.challenges["good"]
		f.mu.Unlock()
		if gotID != id || gotSecret != secret || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("code") != "good" ||
			r.PostForm.Get("redirect_uri") != "https://swarmmemo.test/proofs/oauth/cb" || PKCEChallenge(verifier) != want {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-123", "token_type": "bearer", "expires_in": 3600})
	})
}

func needBearer(next any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			http.Error(w, "{}", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(next)
	}
}

// runOAuth runs the whole provider flow: AuthURL, then Verify with the
// code the fake accepts.
func runOAuth(t *testing.T, f *fake, o *OAuth) (Proof, error) {
	t.Helper()
	key := []byte("server-key-0123456789abcdef012345")
	verifier := PKCEVerifier(key, "state-1")
	u, err := url.Parse(o.AuthURL("state-1", verifier, "https://swarmmemo.test/proofs/oauth/cb"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("state") != "state-1" || q.Get("redirect_uri") != "https://swarmmemo.test/proofs/oauth/cb" ||
		q.Get("response_type") != "code" || q.Get("client_id") == "" || strings.Contains(u.String(), "secret") {
		t.Fatalf("auth URL %s", u)
	}
	for _, s := range strings.Fields(q.Get("scope")) {
		if !slices.Contains([]string{"openid", "profile", "identify", "identity", "users.read", "tweet.read"}, s) {
			t.Fatalf("%s asks scope %q: identity scopes only", o.spec.Kind, s)
		}
	}
	f.mu.Lock()
	f.challenges["good"] = q.Get("code_challenge")
	f.mu.Unlock()
	return o.Verify(context.Background(), Evidence{Fingerprint: testFP, Code: "good", Verifier: verifier, RedirectURI: "https://swarmmemo.test/proofs/oauth/cb", Now: testNow})
}

func creds(kind string) Config {
	return Config{OAuth: map[string]OAuthCredentials{kind: {ClientID: kind + "-id", ClientSecret: kind + "-secret"}}}
}

func TestOAuthProviders(t *testing.T) {
	year := testNow.Add(-2 * 365 * 24 * time.Hour)
	cases := []struct {
		kind, token, profile string
		basic                bool
		answer               any
		extra                func(f *fake)
		id                   string
		in                   Inputs
	}{
		{"google", "/oauth2.googleapis.com/token", "/openidconnect.googleapis.com/v1/userinfo", false, map[string]any{"sub": "1080001", "email": "never@kept"}, nil,
			"1080001", Inputs{}},
		{"x", "/api.x.com/2/oauth2/token", "/api.x.com/2/users/me", true,
			map[string]any{"data": map[string]any{"id": "42", "username": "someone", "created_at": year.UTC().Format(time.RFC3339), "public_metrics": map[string]any{"followers_count": 300}}}, nil,
			"42", Inputs{"created_at": year.Unix(), "followers": 300}},
		{"discord", "/discord.com/api/oauth2/token", "/discord.com/api/users/@me", true, map[string]any{"id": "175928847299117063", "username": "x"}, nil,
			"175928847299117063", Inputs{"created_at": SnowflakeTime("175928847299117063")}},
		{"reddit", "/www.reddit.com/api/v1/access_token", "/oauth.reddit.com/api/v1/me", true,
			map[string]any{"id": "abc12", "name": "redditor", "created_utc": float64(year.Unix()) + 0.5, "total_karma": 5000}, nil,
			"abc12", Inputs{"created_at": year.Unix(), "karma": 5000}},
		{"huggingface", "/huggingface.co/oauth/token", "/huggingface.co/oauth/userinfo", true,
			map[string]any{"sub": "65f0aa", "preferred_username": "alice"}, func(f *fake) {
				f.json("GET", "/huggingface.co/api/users/alice/overview", map[string]any{"createdAt": year.UTC().Format(time.RFC3339), "numFollowers": 12})
			}, "65f0aa", Inputs{"created_at": year.Unix(), "followers": 12}},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			f := newFake(t)
			f.oauthToken(c.token, c.kind+"-id", c.kind+"-secret", c.basic)
			f.on("GET", c.profile, needBearer(c.answer))
			if c.extra != nil {
				c.extra(f)
			}
			reg := f.registry(creds(c.kind))
			p, _ := reg.Get(c.kind)
			o := p.(*OAuth)
			if !o.Enabled() || reg.Status(c.kind) != "enabled" || New(Config{}).Status(c.kind) != "disabled-no-credentials" {
				t.Fatal("status")
			}
			proof, err := runOAuth(t, f, o)
			if err != nil {
				t.Fatalf("verify: %v (calls %v)", err, f.calls)
			}
			if proof.Value != c.id || proof.Root != c.kind+":"+c.id || proof.VerifiedAt != testNow.Unix() {
				t.Fatalf("proof %+v", proof)
			}
			if len(proof.Inputs) != len(c.in) {
				t.Fatalf("inputs %v, want %v", proof.Inputs, c.in)
			}
			for k, v := range c.in {
				if proof.Inputs[k] != v {
					t.Fatalf("inputs %v, want %v", proof.Inputs, c.in)
				}
			}
			raw, _ := json.Marshal(proof)
			if strings.Contains(string(raw), "tok-123") || strings.Contains(string(raw), "never@kept") || strings.Contains(string(raw), "secret") {
				t.Fatalf("the proof keeps a token or more than the id and inputs: %s", raw)
			}
			row := trust.DefaultParams().Proofs[c.kind]
			params := trust.DefaultParams()
			if got := Price(&params, o, proof, testNow); got <= 0 || got > min(row.Forge, row.Rent) {
				t.Fatalf("price %d (row %+v)", got, row)
			}
			// A wrong code, a wrong verifier or another redirect URI is refused.
			for _, e := range []Evidence{
				{Code: "bad", Verifier: PKCEVerifier([]byte("server-key-0123456789abcdef012345"), "state-1"), RedirectURI: "https://swarmmemo.test/proofs/oauth/cb"},
				{Code: "good", Verifier: PKCEVerifier([]byte("another-key"), "state-1"), RedirectURI: "https://swarmmemo.test/proofs/oauth/cb"},
				{Code: "good", Verifier: PKCEVerifier([]byte("server-key-0123456789abcdef012345"), "state-1"), RedirectURI: "https://evil.test/cb"},
				{Code: "", Verifier: "v", RedirectURI: "r"},
			} {
				e.Now = testNow
				if _, err := o.Verify(context.Background(), e); !errors.Is(err, ErrFailed) {
					t.Errorf("%+v: %v, want ErrFailed", e, err)
				}
			}
			// Without credentials the provider is off.
			off, _ := f.registry(Config{}).Get(c.kind)
			if _, err := Check(context.Background(), off, Evidence{Code: "good"}); !errors.Is(err, ErrDisabled) {
				t.Fatalf("disabled: %v", err)
			}
			// A recheck keeps the proof and age raises nothing past the cap.
			again, err := o.Recheck(context.Background(), proof, Evidence{Now: testNow.Add(400 * 24 * time.Hour)})
			if err != nil || again.Root != proof.Root {
				t.Fatalf("recheck %+v %v", again, err)
			}
		})
	}
}

// An unreachable or failing provider is inconclusive, never a pass.
func TestOAuthProviderDown(t *testing.T) {
	f := newFake(t)
	f.on("POST", "/oauth2.googleapis.com/token", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "down", 503) })
	p, _ := f.registry(creds("google")).Get("google")
	if _, err := runOAuth(t, f, p.(*OAuth)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("%v", err)
	}
	// A profile id that is not an id is refused.
	f.oauthToken("/oauth2.googleapis.com/token", "google-id", "google-secret", false)
	f.on("GET", "/openidconnect.googleapis.com/v1/userinfo", needBearer(map[string]any{"sub": "../../etc"}))
	if _, err := runOAuth(t, f, p.(*OAuth)); !errors.Is(err, ErrFailed) {
		t.Fatalf("%v", err)
	}
}

func telegramFields(token string, fields map[string]string) url.Values {
	var lines []string
	v := url.Values{}
	for k, x := range fields {
		lines = append(lines, k+"="+x)
		v.Set(k, x)
	}
	sort.Strings(lines)
	key := sha256.Sum256([]byte(token))
	mac := hmac.New(sha256.New, key[:])
	mac.Write([]byte(strings.Join(lines, "\n")))
	v.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return v
}

func TestTelegramWidget(t *testing.T) {
	reg := New(Config{TelegramBotToken: "123:abc", TelegramBot: "swarmmemo_bot"})
	p, _ := reg.Get("telegram")
	at := itoa(testNow.Unix() - 30)
	good := telegramFields("123:abc", map[string]string{"id": "777", "first_name": "A", "username": "a", "auth_date": at})
	good.Set("state", "our-state") // the board's own parameter is not signed
	proof, err := p.Verify(context.Background(), Evidence{Fields: good, Now: testNow})
	if err != nil || proof.Root != "telegram:777" || proof.Value != "777" || len(proof.Inputs) != 0 {
		t.Fatalf("%+v %v", proof, err)
	}
	tampered := url.Values{}
	for k, v := range good {
		tampered[k] = v
	}
	tampered.Set("id", "778")
	stale := telegramFields("123:abc", map[string]string{"id": "777", "auth_date": itoa(testNow.Unix() - TelegramMaxAge - 1)})
	otherBot := telegramFields("999:zzz", map[string]string{"id": "777", "auth_date": at})
	for name, v := range map[string]url.Values{"tampered": tampered, "stale": stale, "other bot": otherBot, "no hash": {"id": {"777"}, "auth_date": {at}}} {
		if _, err := p.Verify(context.Background(), Evidence{Fields: v, Now: testNow}); !errors.Is(err, ErrFailed) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if New(Config{TelegramBotToken: "123:abc"}).Status("telegram") != "disabled-no-credentials" {
		t.Error("a bot token without a bot name is not enough")
	}
}

func TestFarcaster(t *testing.T) {
	f := newFake(t)
	statement := Statement("farcaster", "swarmmemo.test", testFP, "4242", "n0nce")
	reg := f.registry(Config{FarcasterHub: "https://hub.test", FarcasterHubKey: "k"})
	p, _ := reg.Get("farcaster")
	f.on("GET", "/hub.test/v1/castsByFid", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" || r.URL.Query().Get("fid") != "4242" {
			http.Error(w, "{}", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []any{
			map[string]any{"data": map[string]any{"fid": 4242, "castAddBody": map[string]any{"text": "gm"}}},
			map[string]any{"data": map[string]any{"fid": 4242, "castAddBody": map[string]any{"text": "linking: " + statement}}},
		}})
	})
	reg2 := testNow.Add(-400 * 24 * time.Hour).Unix()
	f.json("GET", "/hub.test/v1/onChainEventsByFid", map[string]any{"events": []any{map[string]any{"blockTimestamp": reg2 + 50}, map[string]any{"blockTimestamp": reg2}}})
	follows := make([]any, 150)
	for i := range follows {
		follows[i] = map[string]any{"data": map[string]any{"fid": i + 1}}
	}
	f.json("GET", "/hub.test/v1/linksByTargetFid", map[string]any{"messages": follows})
	proof, err := p.Verify(context.Background(), Evidence{Value: "4242", Statement: statement, Proof: "cast", Now: testNow})
	if err != nil || proof.Root != "farcaster:4242" || proof.Inputs["created_at"] != reg2 || proof.Inputs["followers"] != 150 {
		t.Fatalf("cast: %+v %v", proof, err)
	}
	if c := p.Assess(proof, testNow); c <= 100 || c > 200 {
		t.Fatalf("assess %d", c)
	}
	// Another statement is not in the casts.
	if _, err := p.Verify(context.Background(), Evidence{Value: "4242", Statement: statement + "x", Proof: "cast", Now: testNow}); !errors.Is(err, ErrFailed) {
		t.Fatalf("missing cast: %v", err)
	}
	// The custody address's signature.
	key := sha256.Sum256([]byte("custody"))
	addr, sig, err := services.SignPersonal(key[:], []byte(statement))
	if err != nil {
		t.Fatal(err)
	}
	f.on("GET", "/hub.test/v1/onChainIdRegistryEventByAddress", func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.URL.Query().Get("address"), addr.String()) {
			_ = json.NewEncoder(w).Encode(map[string]any{"fid": 4242})
			return
		}
		http.NotFound(w, r)
	})
	f.json("GET", "/hub.test/v1/verificationsByFid", map[string]any{"messages": []any{}})
	if proof, err = p.Verify(context.Background(), Evidence{Value: "4242", Statement: statement, Proof: sig, Now: testNow}); err != nil || proof.Root != "farcaster:4242" {
		t.Fatalf("custody signature: %+v %v", proof, err)
	}
	// Someone else's key: neither custody nor verified.
	other := sha256.Sum256([]byte("other"))
	_, sig2, _ := services.SignPersonal(other[:], []byte(statement))
	if _, err = p.Verify(context.Background(), Evidence{Value: "4242", Statement: statement, Proof: sig2, Now: testNow}); !errors.Is(err, ErrFailed) {
		t.Fatalf("other signer: %v", err)
	}
	// A verified address counts like custody.
	otherAddr, _, _ := services.SignPersonal(other[:], []byte("x"))
	f.json("GET", "/hub.test/v1/verificationsByFid", map[string]any{"messages": []any{map[string]any{"data": map[string]any{"fid": 4242, "verificationAddAddressBody": map[string]any{"address": strings.ToLower(otherAddr.String())}}}}})
	if _, err = p.Verify(context.Background(), Evidence{Value: "4242", Statement: statement, Proof: sig2, Now: testNow}); err != nil {
		t.Fatalf("verified address: %v", err)
	}
	if _, err = p.Verify(context.Background(), Evidence{Value: "01", Statement: statement, Proof: "cast", Now: testNow}); !errors.Is(err, ErrFailed) {
		t.Fatalf("bad fid: %v", err)
	}
}

// rpcFake is a JSON-RPC endpoint answering ENS's calls for one name.
func rpcFake(t *testing.T, f *fake, path, name, wallet, primary string, registered int64) {
	resolver := "0x" + strings.Repeat("ab", 20)
	word := func(addr string) string {
		return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(strings.ToLower(addr), "0x")
	}
	node := Namehash(name)
	rev := Namehash(strings.TrimPrefix(strings.ToLower(wallet), "0x") + ".addr.reverse")
	if strings.HasSuffix(name, ".base.eth") {
		rev = Namehash(strings.TrimPrefix(strings.ToLower(wallet), "0x") + ".80002105.reverse")
	}
	f.on("POST", path, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(raw, &req)
		answer := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": v}) }
		switch req.Method {
		case "eth_call":
			var call struct{ To, Data string }
			_ = json.Unmarshal(req.Params[0], &call)
			sel, arg := call.Data[2:10], call.Data[10:]
			switch {
			case sel == "0178b8bf" && (arg == hex.EncodeToString(node[:]) || arg == hex.EncodeToString(rev[:])):
				answer(word(resolver))
			case sel == "3b3b57de" && call.To == resolver && arg == hex.EncodeToString(node[:]):
				answer(word(wallet))
			case sel == "691f3431" && call.To == resolver && arg == hex.EncodeToString(rev[:]):
				s := []byte(primary)
				out := make([]byte, 64+((len(s)+31)/32)*32)
				out[31] = 32
				out[63] = byte(len(s))
				copy(out[64:], s)
				answer("0x" + hex.EncodeToString(out))
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": 3, "message": "execution reverted"}})
			}
		case "eth_getLogs":
			answer([]any{map[string]any{"blockNumber": "0x10"}})
		case "eth_getBlockByNumber":
			answer(map[string]any{"timestamp": "0x" + strings.TrimLeft(hex.EncodeToString([]byte{byte(registered >> 24), byte(registered >> 16), byte(registered >> 8), byte(registered)}), "0")})
		default:
			http.Error(w, "{}", 400)
		}
	})
}

func TestENS(t *testing.T) {
	wallet := "0x" + strings.Repeat("12", 20)
	registered := testNow.Add(-800 * 24 * time.Hour).Unix()
	for _, c := range []struct{ name, path string }{{"alice.eth", "/eth.test/"}, {"bob.base.eth", "/base.test/"}} {
		f := newFake(t)
		rpcFake(t, f, c.path, c.name, wallet, c.name, registered)
		p, _ := f.registry(Config{ENSRPC: "https://eth.test/", BaseRPC: "https://base.test/"}).Get("ens")
		proof, err := p.Verify(context.Background(), Evidence{Value: strings.ToUpper(c.name), Wallets: []string{"0x" + strings.Repeat("99", 20), wallet}, Now: testNow})
		if err != nil || proof.Root != "ens:"+c.name || proof.Inputs["created_at"] != registered {
			t.Fatalf("%s: %+v %v (%v)", c.name, proof, err, f.calls)
		}
		if a := p.Assess(proof, testNow); a < 150 || a > 200 {
			t.Fatalf("assess %d", a)
		}
		// Not one of the key's wallets; no wallet at all.
		for _, ws := range [][]string{{"0x" + strings.Repeat("99", 20)}, nil} {
			if _, err := p.Verify(context.Background(), Evidence{Value: c.name, Wallets: ws, Now: testNow}); !errors.Is(err, ErrFailed) {
				t.Fatalf("%s wallets %v: %v", c.name, ws, err)
			}
		}
	}
	// The wallet's primary name is another name: the wallet did not choose it.
	f := newFake(t)
	rpcFake(t, f, "/eth.test/", "alice.eth", wallet, "mallory.eth", registered)
	p, _ := f.registry(Config{ENSRPC: "https://eth.test/"}).Get("ens")
	if _, err := p.Verify(context.Background(), Evidence{Value: "alice.eth", Wallets: []string{wallet}, Now: testNow}); !errors.Is(err, ErrFailed) {
		t.Fatalf("primary name mismatch: %v", err)
	}
	for _, bad := range []string{"a.eth", "sub.alice.eth", "alice.com", "al--ice.eth", "ålice.eth", ""} {
		if _, ok := NormalizeENS(bad); ok && bad != "sub.alice.eth" {
			t.Errorf("%q accepted", bad)
		}
	}
	if n, ok := NormalizeENS("sub.alice.eth"); ok {
		t.Errorf("a subname %q is accepted", n)
	}
	// EIP-137's test vector.
	if h := Namehash("eth"); hex.EncodeToString(h[:]) != "93cdeb708b7545dc668eb9280176169d1c33cfd8ed6f04690a0bcc88a93fc4ae" {
		t.Fatalf("namehash(eth) %x", h)
	}
}

func TestPackages(t *testing.T) {
	created := testNow.Add(-3 * 365 * 24 * time.Hour)
	t.Run("npm", func(t *testing.T) {
		f := newFake(t)
		p, _ := f.registry(Config{}).Get("npm")
		st := Statement("npm", "swarmmemo.test", testFP, "@acme/tool", "n1")
		f.json("GET", "/registry.npmjs.org/@acme%2Ftool/latest", map[string]any{"name": "@acme/tool", "swarmmemo": st, "keywords": []string{"x"}})
		f.json("GET", "/registry.npmjs.org/@acme%2Ftool", map[string]any{"time": map[string]string{"created": created.UTC().Format(time.RFC3339Nano), "modified": testNow.UTC().Format(time.RFC3339)}})
		f.json("GET", "/api.npmjs.org/downloads/point/last-month/@acme%2Ftool", map[string]any{"downloads": 12345})
		proof, err := p.Verify(context.Background(), Evidence{Value: "@acme/tool", Statement: st, Now: testNow})
		if err != nil || proof.Root != "npm:@acme/tool" || proof.Inputs["created_at"] != created.Unix() || proof.Inputs["downloads"] != 12345 {
			t.Fatalf("%+v %v (%v)", proof, err, f.calls)
		}
		if a := p.Assess(proof, testNow); a < 150 || a > 200 {
			t.Fatalf("assess %d", a)
		}
		// The statement was removed by a later release: the recheck fails.
		f.json("GET", "/registry.npmjs.org/@acme%2Ftool/latest", map[string]any{"name": "@acme/tool", "description": "no statement"})
		if _, err = p.Recheck(context.Background(), proof, Evidence{Statement: st, Now: testNow}); !errors.Is(err, ErrFailed) {
			t.Fatalf("recheck: %v", err)
		}
		// A missing package fails; a registry outage is inconclusive.
		if _, err = p.Verify(context.Background(), Evidence{Value: "nope", Statement: st, Now: testNow}); !errors.Is(err, ErrFailed) {
			t.Fatalf("missing: %v", err)
		}
		f.on("GET", "/registry.npmjs.org/down/latest", func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", 502) })
		if _, err = p.Verify(context.Background(), Evidence{Value: "down", Statement: st, Now: testNow}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("outage: %v", err)
		}
		if _, ok := NormalizeNPM("../x"); ok {
			t.Fatal("bad name accepted")
		}
	})
	t.Run("pypi", func(t *testing.T) {
		f := newFake(t)
		p, _ := f.registry(Config{}).Get("pypi")
		st := Statement("pypi", "swarmmemo.test", testFP, "swarm-tool", "n2")
		f.json("GET", "/pypi.org/pypi/swarm-tool/json", map[string]any{"info": map[string]any{"summary": "s", "description": "# Tool\n\n" + st + "\n", "keywords": "", "project_urls": map[string]string{"Home": "https://x"}},
			"releases": map[string]any{"0.1": []any{map[string]any{"upload_time_iso_8601": created.UTC().Format("2006-01-02T15:04:05.000000Z")}}, "0.2": []any{map[string]any{"upload_time_iso_8601": testNow.UTC().Format(time.RFC3339)}}}})
		f.json("GET", "/pypistats.org/api/packages/swarm-tool/recent", map[string]any{"data": map[string]any{"last_month": 50}})
		proof, err := p.Verify(context.Background(), Evidence{Value: "Swarm_Tool", Statement: st, Now: testNow})
		if err != nil || proof.Root != "pypi:swarm-tool" || proof.Inputs["created_at"] != created.Unix() || proof.Inputs["downloads"] != 50 {
			t.Fatalf("%+v %v (%v)", proof, err, f.calls)
		}
		if _, err = p.Verify(context.Background(), Evidence{Value: "swarm-tool", Statement: st + "?", Now: testNow}); !errors.Is(err, ErrFailed) {
			t.Fatalf("no statement: %v", err)
		}
	})
}

// The C144 kinds keep their values: GitHub through trust.GitHubCents, pow
// in work units, the wallet at Corroborate's total; the wallet signature
// and the proof-of-work hash are checked here.
func TestExistingKinds(t *testing.T) {
	reg := Default
	gh, _ := reg.Get("github")
	in := Inputs{"created_at": testNow.Unix() - 400*86400, "public_repos": 7, "followers": 30}
	if got, want := gh.Assess(Proof{Inputs: in}, testNow), trust.GitHubCents(400, 7, 30); got != want {
		t.Fatalf("github %d, want %d", got, want)
	}
	w, _ := reg.Get("wallet")
	if w.Assess(Proof{Inputs: Inputs{"cents": 420}}, testNow) != 420 {
		t.Fatal("wallet")
	}
	key := sha256.Sum256([]byte("wallet"))
	addr, sig, _ := services.SignPersonal(key[:], []byte("message"))
	if pr, err := w.Verify(context.Background(), Evidence{Value: addr.String(), Statement: "message", Proof: sig, Now: testNow}); err != nil || pr.Root != WalletRoot(addr.String()) {
		t.Fatalf("wallet verify %+v %v", pr, err)
	}
	if _, err := w.Verify(context.Background(), Evidence{Value: addr.String(), Statement: "other", Proof: sig, Now: testNow}); !errors.Is(err, ErrFailed) {
		t.Fatal("wallet: another message passes")
	}
	pow, _ := reg.Get("pow")
	prefix := "swarmmemo-pow:1:svc:" + testFP + ":n:"
	var solution string
	for i := 0; ; i++ {
		s := itoa(int64(i))
		if LeadingZeroBits(sha256.Sum256([]byte(prefix+s))) >= 8 {
			solution = s
			break
		}
	}
	pr, err := pow.Verify(context.Background(), Evidence{Fingerprint: testFP, Statement: prefix, Proof: solution, Bits: 8, Now: testNow})
	if err != nil || pr.Root != "pow:"+testFP || pow.Assess(pr, testNow) != trust.PowUnits(8) {
		t.Fatalf("pow %+v %v", pr, err)
	}
	if _, err = pow.Verify(context.Background(), Evidence{Fingerprint: testFP, Statement: prefix, Proof: solution, Bits: 60, Now: testNow}); !errors.Is(err, ErrFailed) {
		t.Fatal("pow: too little work passes")
	}
	for _, k := range []string{"domain", "github"} {
		p, _ := reg.Get(k)
		if _, err := p.Verify(context.Background(), Evidence{}); !errors.Is(err, ErrNotHere) {
			t.Fatalf("%s: %v", k, err)
		}
	}
}

// Every kind has one provider and a price row; every priced row has a
// provider; OAuth and widget kinds need credentials, the rest none.
func TestRegistry(t *testing.T) {
	p := trust.DefaultParams()
	kinds := Default.Kinds()
	seen := map[string]bool{}
	for _, k := range kinds {
		if seen[k] {
			t.Fatalf("%s twice", k)
		}
		seen[k] = true
		prov, _ := Default.Get(k)
		s := prov.Spec()
		if s.Kind != k || s.Label == "" || s.Proves == "" || s.Reveals == "" || s.Flow == "" || s.Root == "" || s.Priced == "" || s.Action == "" {
			t.Errorf("%s: incomplete spec %+v", k, s)
		}
		row, ok := p.Proofs[k]
		if !ok || (k != "domain" && row.Assess == "") {
			t.Errorf("%s has no assessed price row", k)
		}
		needs := s.Flow == FlowOAuth || s.Flow == FlowWidget
		if needs != (len(s.Env) > 0) || needs == (Default.Status(k) == "enabled") {
			t.Errorf("%s: env %v, status %s", k, s.Env, Default.Status(k))
		}
	}
	for k, row := range p.Proofs {
		if (row.Assess != "") != seen[k] {
			t.Errorf("price row %s has no provider", k)
		}
	}
	env := map[string]string{"PROOF_GOOGLE_CLIENT_ID": " id ", "PROOF_GOOGLE_CLIENT_SECRET": "s", "PROOF_TELEGRAM_BOT_NAME": "@bot", "PROOF_TELEGRAM_BOT_TOKEN": "t"}
	c := ConfigFromEnv(func(k string) string { return env[k] })
	r := New(c)
	if r.Status("google") != "enabled" || r.Status("telegram") != "enabled" || r.Status("x") != "disabled-no-credentials" || c.OAuth["google"].ClientID != "id" || c.TelegramBot != "bot" {
		t.Fatalf("env: %+v", c)
	}
}

// The framework through the trust run: an assessment above its row is
// capped, two proofs of one root on one account count once (saturation),
// one proof root claimed by two accounts is split (root uniqueness), and a
// burned root prices 0 on any key that links it later.
func TestFrameworkThroughTheRun(t *testing.T) {
	acct := func(n string) string { s := sha256.Sum256([]byte(n)); return hex.EncodeToString(s[:]) }
	asOf := testNow.Unix()
	day := int64(86400)
	reg := Default
	params := trust.DefaultParams()
	snap := trust.Snapshot{Params: params, Meta: trust.Meta{Schema: trust.SnapshotSchema, AsOf: asOf}}
	for _, n := range []string{"ann", "ben", "cat", "dan", "eve"} {
		snap.Accounts = append(snap.Accounts, trust.Record{Type: "account", Account: acct(n), FirstSeen: asOf - 90*day})
	}
	add := func(name, kind, value string, in Inputs) {
		prov, _ := reg.Get(kind)
		snap.Proofs = append(snap.Proofs, Record(prov, acct(name), Proof{Kind: kind, Value: value, Root: kind + ":" + value, Inputs: in}, asOf-30*day, asOf-day))
	}
	old := asOf - 5*365*day
	add("ann", "bond", "b1", Inputs{"cents": 900})                     // above the row: capped at 500
	add("ann", "x", "x1", Inputs{"created_at": old, "followers": 1e6}) // another root: adds
	add("ben", "npm", "pkg", Inputs{"created_at": old, "downloads": 1e6})
	add("ben", "npm", "pkg", Inputs{"created_at": old, "downloads": 10}) // the same root twice: its best claim once
	add("cat", "discord", "d1", Inputs{"created_at": old})
	add("dan", "discord", "d1", Inputs{"created_at": old}) // one Discord account, two keys: split
	add("eve", "github", "666", Inputs{"created_at": old, "public_repos": 50})
	snap.Penalties = append(snap.Penalties, trust.Record{Type: "penalty", ID: "ring-1", Kind: trust.TriggerRing, Account: acct("mallory"), CreatedAt: asOf - 10*day,
		EndsAt: asOf + 20*day, Value: 1, State: "applied", Evidence: "ring"})
	snap.Burns = append(snap.Burns, trust.Record{Type: "burn", ID: "ring-1", Root: "github:666"})
	out, err := trust.Compute(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	proofCents := map[string]map[string]int64{}
	states := map[string]string{}
	for _, sc := range out.Scores {
		for _, it := range sc.Parts.World {
			if it.Kind != "proof" {
				continue
			}
			if proofCents[sc.Account] == nil {
				proofCents[sc.Account] = map[string]int64{}
			}
			proofCents[sc.Account][it.Root] += it.Cents
			states[it.Root] = it.State
		}
	}
	xp, _ := reg.Get("x")
	npm, _ := reg.Get("npm")
	at := time.Unix(asOf-day, 0)
	if got := proofCents[acct("ann")]; got["bond:b1"] != 500 || got["x:x1"] != xp.Assess(Proof{Inputs: Inputs{"created_at": old, "followers": 1e6}}, at) || got["x:x1"] < 140 {
		t.Errorf("ann (capped, two roots add): %v", got)
	}
	if got := proofCents[acct("ben")]; got["npm:pkg"] != npm.Assess(Proof{Inputs: Inputs{"created_at": old, "downloads": 1e6}}, at) {
		t.Errorf("ben (one root, best claim once): %v", got)
	}
	cat, dan := proofCents[acct("cat")]["discord:d1"], proofCents[acct("dan")]["discord:d1"]
	if cat+dan > 100 || cat != dan || cat == 0 {
		t.Errorf("one Discord account on two keys: %d + %d, want a split of at most 100", cat, dan)
	}
	if proofCents[acct("eve")]["github:666"] != 0 || states["github:666"] != "burned" {
		t.Errorf("a burned root counts: %v %s", proofCents[acct("eve")], states["github:666"])
	}
}

// Statements bind the kind, the service, the key, the value and the nonce.
func TestStatement(t *testing.T) {
	a := Statement("npm", "svc", testFP, "p", "n")
	for _, b := range []string{Statement("pypi", "svc", testFP, "p", "n"), Statement("npm", "svc2", testFP, "p", "n"), Statement("npm", "svc", strings.Repeat("b", 64), "p", "n"),
		Statement("npm", "svc", testFP, "q", "n"), Statement("npm", "svc", testFP, "p", "m")} {
		if a == b {
			t.Fatal("statement does not bind every part")
		}
	}
	if v := PKCEVerifier([]byte("k"), "s"); len(v) < 43 || len(v) > 128 || strings.ContainsAny(v, "+/=") || v == PKCEVerifier([]byte("k2"), "s") {
		t.Fatalf("verifier %q", v)
	}
	if _, err := base64.RawURLEncoding.DecodeString(PKCEChallenge("v")); err != nil {
		t.Fatal(err)
	}
}
