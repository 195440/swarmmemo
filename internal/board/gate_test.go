package board

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/gate"
	"swarmmemo/internal/services"
)

// C325: the SwarmMemo Gate on the real engine: challenge and verify for each
// proof, single use, origin and nonce in the token, standing from the
// latest trust run, and the refusals.

func gateStore(t *testing.T, f Features) *Store {
	t.Helper()
	f.Services = append(f.Services, "notary")
	return openTest(t, Config{Features: f})
}

func gateChallengeOf(t *testing.T, s *Store, data string) map[string]any {
	t.Helper()
	return run(t, s, Command{Operation: "gate.challenge", Data: data}).Data
}

func gateTokenOf(t *testing.T, s *Store, res Result) services.GatePayload {
	t.Helper()
	pub, _ := s.NotaryPublicKey()
	p, err := services.VerifyGateToken(pub, res.Data["token"].(string), res.Data["origin"].(string), s.now().Unix())
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return p
}

func TestGatePowPassAndSingleUse(t *testing.T) {
	s := gateStore(t, Features{})
	ch := gateChallengeOf(t, s, `{"schema":1,"origin":"https://shop.example","nonce":"session-1234"}`)
	id := ch["challenge"].(string)
	pow := ch["pow"].(map[string]any)
	if pow["prefix"] != id || pow["bits"] != gate.BitsDefault || ch["nonce"] != "session-1234" || ch["statement"] != "swarmmemo-gate:1:swarmmemo.com:https://shop.example:"+id {
		t.Fatalf("challenge: %+v", ch)
	}
	solution, ok := gate.Solve(id, gate.BitsDefault, 1<<26)
	if !ok {
		t.Fatal("no solution")
	}
	verify := Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":%q}`, id, solution)}
	res := run(t, s, verify)
	p := gateTokenOf(t, s, res)
	if !p.Passed || p.Origin != "https://shop.example" || p.Nonce != "session-1234" || p.Challenge != id || !slices.Equal(p.Proofs, []string{"pow"}) ||
		p.Subject != "" || p.ExpiresAt != s.now().Unix()+int64(gate.TokenTTL.Seconds()) || p.Band != "anonymous" || p.Bits != gate.BitsDefault {
		t.Fatalf("payload: %+v", p)
	}
	if res.Data["keep"] == nil {
		t.Fatal("an unsigned pass names the identity next")
	}
	// Replay: the challenge is spent.
	fails(t, s, verify, "challenge_expired")
	// A wrong solution spends the challenge too.
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://shop.example","bits":28}`)
	bad := Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":"0"}`, ch["challenge"])}
	fails(t, s, bad, "invalid_solution")
	fails(t, s, bad, "challenge_expired")
}

func TestGateChallengeExpires(t *testing.T) {
	s := gateStore(t, Features{})
	ch := gateChallengeOf(t, s, `{"schema":1,"origin":"https://shop.example"}`)
	id := ch["challenge"].(string)
	solution, _ := gate.Solve(id, gate.BitsDefault, 1<<26)
	s.now = func() time.Time { return time.Unix(testTime, 0).Add(gate.ChallengeTTL) }
	fails(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":%q}`, id, solution)}, "challenge_expired")
}

func TestGateRefusals(t *testing.T) {
	off := openTest(t, Config{})
	fails(t, off, Command{Operation: "gate.challenge", Data: `{"schema":1,"origin":"https://shop.example"}`}, "service_unavailable")
	s := gateStore(t, Features{})
	for data, code := range map[string]string{
		`{"schema":1,"origin":"https://shop.example/login"}`: "invalid_origin",
		`{"schema":1,"origin":"http://shop.example"}`:        "invalid_origin",
		`{"schema":1}`: "invalid_origin",
		`{"schema":1,"origin":"https://shop.example","x":1}`: "invalid_gate_data",
		`not json`: "invalid_gate_data",
	} {
		fails(t, s, Command{Operation: "gate.challenge", Data: data}, code)
	}
	fails(t, s, Command{Operation: "gate.verify", Data: `{"schema":1,"challenge":"` + strings.Repeat("a", 32) + `","solution":"1"}`}, "challenge_expired")
	fails(t, s, Command{Operation: "gate.verify", Data: `{"schema":1,"challenge":"` + strings.Repeat("a", 32) + `"}`}, "invalid_gate_data")
	// A site that asks for standing takes no work.
	ch := gateChallengeOf(t, s, `{"schema":1,"origin":"https://shop.example","min":100}`)
	if ways := ch["ways"].([]string); len(ways) != 1 || !strings.HasPrefix(ways[0], "identity") {
		t.Fatalf("ways with a minimum: %v", ways)
	}
	solution, _ := gate.Solve(ch["challenge"].(string), gate.BitsDefault, 1<<26)
	fails(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":%q}`, ch["challenge"], solution)}, "standing_required")
}

func TestGateIdentityStandingAndProofs(t *testing.T) {
	s := gateStore(t, Features{Trust: TrustOn})
	alice := keyFor(71)
	postAs(t, s, alice, Command{Room: "lobby", Text: "alice arrives", RequestID: "gate-alice"})
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO trust_runs(id,state,as_of,params_version,started_at,finished_at,inputs) VALUES(1,'done',?,8,?,?,'{}')", testTime, testTime, testTime)
	exec("INSERT INTO trust_scores(run_id,account,root,proof_collateral,flow_a,flow_b,flow,collateral,tier,weight_ppm,parts) VALUES(1,?,?,0,0,0,0,450,3,1000000,'{}')", keyID(alice), keyID(alice))
	exec("INSERT INTO trust_current(account,run_id) VALUES(?,1)", keyID(alice))
	exec("INSERT INTO identity_links(agent,kind,value,state,created_at) VALUES(?,'github','alice','verified',?),(?,'domain','alice.example','claimed',?)", keyID(alice), testTime, keyID(alice), testTime)

	for _, tc := range []struct {
		min    int64
		passed bool
	}{{100, true}, {450, true}, {451, false}} {
		ch := gateChallengeOf(t, s, fmt.Sprintf(`{"schema":1,"origin":"https://forum.example","min":%d}`, tc.min))
		res := run(t, s, signedNow(s, alice, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q}`, ch["challenge"])}))
		p := gateTokenOf(t, s, res)
		if p.Passed != tc.passed || p.StandingCents != 450 || p.MinCents != tc.min || p.Subject != keyID(alice) || !slices.Equal(p.Proofs, []string{"identity", "github"}) || p.Band == "" || p.Band == "anonymous" {
			t.Fatalf("min %d: %+v", tc.min, p)
		}
	}
	// A signed verify takes no wallet beside it.
	ch := gateChallengeOf(t, s, `{"schema":1,"origin":"https://forum.example"}`)
	fails(t, s, signedNow(s, alice, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"wallet":"0x%s","signature":"0x%s"}`, ch["challenge"], strings.Repeat("a", 40), strings.Repeat("1", 130))}), "invalid_gate_data")
	// A key with no standing (one that never wrote) passes only with the
	// work beside it: keys cost nothing to make.
	fresh := keyFor(72)
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://forum.example"}`)
	fails(t, s, signedNow(s, fresh, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q}`, ch["challenge"])}), "standing_required")
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://forum.example"}`)
	solution, _ := gate.Solve(ch["challenge"].(string), gate.BitsDefault, 1<<26)
	p := gateTokenOf(t, s, run(t, s, signedNow(s, fresh, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":%q}`, ch["challenge"], solution)})))
	if !p.Passed || p.StandingCents != 0 || p.Subject != keyID(fresh) || !slices.Equal(p.Proofs, []string{"pow", "identity"}) {
		t.Fatalf("fresh key: %+v", p)
	}
	// C325-sec: a verified link that adds no standing (a fresh wallet's
	// signature costs nothing) does not lift the work: zero standing
	// brings the work.
	linked := keyFor(74)
	postAs(t, s, linked, Command{Room: "lobby", Text: "linked arrives", RequestID: "gate-linked"})
	exec("INSERT INTO identity_links(agent,kind,value,state,created_at) VALUES(?,'wallet','0x0000000000000000000000000000000000000001','verified',?)", keyID(linked), testTime)
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://forum.example"}`)
	fails(t, s, signedNow(s, linked, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q}`, ch["challenge"])}), "standing_required")
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://forum.example"}`)
	solution, _ = gate.Solve(ch["challenge"].(string), gate.BitsDefault, 1<<26)
	p = gateTokenOf(t, s, run(t, s, signedNow(s, linked, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":%q}`, ch["challenge"], solution)})))
	if !p.Passed || p.StandingCents != 0 || !slices.Equal(p.Proofs, []string{"pow", "identity", "wallet"}) {
		t.Fatalf("linked key with work: %+v", p)
	}
	// A wrong solution under a signature fails as work does.
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://forum.example","bits":28}`)
	fails(t, s, signedNow(s, alice, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":"0"}`, ch["challenge"])}), "invalid_solution")
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM identities WHERE id=?", keyID(fresh)).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a gate pass is a read: %d identities rows, %v", n, err)
	}
}

func TestGateAgentTask(t *testing.T) {
	s := gateStore(t, Features{})
	start := time.Unix(testTime, 0)
	clock := start
	s.now = func() time.Time { return clock }
	answer := func(ch map[string]any) string {
		raw, _ := json.Marshal(ch["task"])
		var task gate.Task
		if err := json.Unmarshal(raw, &task); err != nil {
			t.Fatal(err)
		}
		sum := 0
		for _, r := range task.Records {
			if r.Tag == task.Where.Tag {
				sum += r.Value
			}
		}
		return fmt.Sprint(sum)
	}
	ch := gateChallengeOf(t, s, `{"schema":1,"origin":"https://swarmmemo.com","mode":"agent"}`)
	if ch["pow"] != nil || ch["task"] == nil {
		t.Fatalf("agent challenge: %+v", ch)
	}
	// C325-sec: the task costs a script nothing, so it is SwarmMemo's own
	// demo: a site's origin gets no agent challenge (its token would pass
	// any site's check for free), and a left-out origin is the service's.
	fails(t, s, Command{Operation: "gate.challenge", Data: `{"schema":1,"origin":"https://shop.example","mode":"agent"}`}, "invalid_origin")
	if own := gateChallengeOf(t, s, `{"schema":1,"mode":"agent"}`); own["origin"] != "https://swarmmemo.com" || own["task"] == nil {
		t.Fatalf("agent challenge with no origin: %+v", own)
	}
	fails(t, s, Command{Operation: "gate.challenge", Data: `{"schema":1}`}, "invalid_origin")
	clock = start.Add(412 * time.Millisecond)
	bob := keyFor(73)
	res := run(t, s, signedNow(s, bob, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"answer":%q}`, ch["challenge"], answer(ch))}))
	p := gateTokenOf(t, s, res)
	if !p.Passed || p.Millis != 412 || !slices.Equal(p.Proofs, []string{"agent_task", "identity"}) || p.Subject != keyID(bob) {
		t.Fatalf("agent pass: %+v", p)
	}
	card := res.Data["card"].(map[string]any)
	id := strings.TrimPrefix(card["url"].(string), GateResultPath)
	if r, ok := s.GateResult(id); !ok || r.Millis != 412 || r.Agent != keyID(bob) {
		t.Fatalf("result %q: %+v %v", id, r, ok)
	}
	// Too slow, and a wrong answer, and work on an agent challenge.
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://swarmmemo.com","mode":"agent"}`)
	clock = clock.Add(gate.AgentDeadlineMillis*time.Millisecond + time.Millisecond)
	fails(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"answer":%q}`, ch["challenge"], answer(ch))}, "gate_too_slow")
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://swarmmemo.com","mode":"agent"}`)
	fails(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"answer":"-1"}`, ch["challenge"])}, "invalid_solution")
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://swarmmemo.com","mode":"agent"}`)
	fails(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":"1"}`, ch["challenge"])}, "invalid_gate_data")
	// An unsigned pass has no name on its card.
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://swarmmemo.com","mode":"agent"}`)
	res = run(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"answer":%q}`, ch["challenge"], answer(ch))})
	id = strings.TrimPrefix(res.Data["card"].(map[string]any)["url"].(string), GateResultPath)
	if r, ok := s.GateResult(id); !ok || r.Agent != "" {
		t.Fatalf("anonymous result: %+v", r)
	}
}

func TestGateWallet(t *testing.T) {
	s := gateStore(t, Features{})
	address := walletAddress(t)
	sign := func(text string) string {
		_, sig, err := services.SignPersonal(walletKey, []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return sig
	}
	// C325-sec: a wallet is free to make (a script mints one per challenge),
	// so its signature alone is no pass: it was a zero-cost bypass of the
	// work. It names the subject beside the work.
	ch := gateChallengeOf(t, s, `{"schema":1,"origin":"https://shop.example"}`)
	sig := sign(ch["statement"].(string))
	fails(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"wallet":%q,"signature":%q}`, ch["challenge"], address, sig)}, "invalid_gate_data")
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://shop.example"}`)
	sig = sign(ch["statement"].(string))
	solution, _ := gate.Solve(ch["challenge"].(string), gate.BitsDefault, 1<<26)
	res := run(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":%q,"wallet":%q,"signature":%q}`, ch["challenge"], solution, address, sig)})
	p := gateTokenOf(t, s, res)
	if !p.Passed || p.Subject != "wallet:eip155:1:"+strings.ToLower(address) || !slices.Equal(p.Proofs, []string{"pow", "wallet"}) {
		t.Fatalf("wallet: %+v", p)
	}
	// Another origin's statement does not pass here, nor a replayed
	// signature of another challenge.
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://shop.example"}`)
	solution, _ = gate.Solve(ch["challenge"].(string), gate.BitsDefault, 1<<26)
	bad := sign(strings.Replace(ch["statement"].(string), "shop.example", "evil.example", 1))
	fails(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":%q,"wallet":%q,"signature":%q}`, ch["challenge"], solution, address, bad)}, "invalid_wallet_signature")
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://shop.example"}`)
	solution, _ = gate.Solve(ch["challenge"].(string), gate.BitsDefault, 1<<26)
	fails(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":%q,"wallet":%q,"signature":%q}`, ch["challenge"], solution, address, sig)}, "invalid_wallet_signature")
}

// C325-sec: a site that asks for standing is never passed by a token that
// does not show it, whatever the proof: passed is false below min, an
// unsigned verify is refused, and another key's standing is not the
// signer's.
func TestGateStandingMinIsNotBypassed(t *testing.T) {
	s := gateStore(t, Features{Trust: TrustOn})
	rich, poor := keyFor(75), keyFor(76)
	postAs(t, s, rich, Command{Room: "lobby", Text: "rich arrives", RequestID: "gate-rich"})
	postAs(t, s, poor, Command{Room: "lobby", Text: "poor arrives", RequestID: "gate-poor"})
	if _, err := s.db.Exec("INSERT INTO trust_runs(id,state,as_of,params_version,started_at,finished_at,inputs) VALUES(1,'done',?,8,?,?,'{}')", testTime, testTime, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO trust_scores(run_id,account,root,proof_collateral,flow_a,flow_b,flow,collateral,tier,weight_ppm,parts) VALUES(1,?,?,0,0,0,0,5000,3,1000000,'{}')", keyID(rich), keyID(rich)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO trust_current(account,run_id) VALUES(?,1)", keyID(rich)); err != nil {
		t.Fatal(err)
	}
	ch := gateChallengeOf(t, s, `{"schema":1,"origin":"https://forum.example","min":500}`)
	solution, _ := gate.Solve(ch["challenge"].(string), gate.BitsDefault, 1<<26)
	// The poor key signs, with work, and names the rich key as data: the
	// strict data refuses the extra field; signed alone, it shows its own.
	fails(t, s, signedNow(s, poor, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"subject":%q}`, ch["challenge"], keyID(rich))}), "invalid_gate_data")
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://forum.example","min":500}`)
	p := gateTokenOf(t, s, run(t, s, signedNow(s, poor, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q}`, ch["challenge"])})))
	if p.Passed || p.Subject != keyID(poor) || p.StandingCents != 0 || p.MinCents != 500 {
		t.Fatalf("poor key under a minimum: %+v", p)
	}
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://forum.example","min":500}`)
	fails(t, s, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q,"solution":%q}`, ch["challenge"], solution)}, "standing_required")
	ch = gateChallengeOf(t, s, `{"schema":1,"origin":"https://forum.example","min":500}`)
	p = gateTokenOf(t, s, run(t, s, signedNow(s, rich, Command{Operation: "gate.verify", Data: fmt.Sprintf(`{"schema":1,"challenge":%q}`, ch["challenge"])})))
	if !p.Passed || p.Subject != keyID(rich) || p.StandingCents != 5000 {
		t.Fatalf("rich key: %+v", p)
	}
}

// TestFunnelGateEntry: a visitor who keeps the identity a gate pass offers
// signs its first write with ref=gate, and the funnel names the gate as its
// entry point.
func TestFunnelGateEntry(t *testing.T) {
	s := openTest(t, Config{})
	s.funnel.lastFlush = time.Unix(1<<40, 0)
	w := &funnelWorld{t: t, s: s, base: testTime - testTime%86400, keys: map[string]ed25519.PrivateKey{"visitor": keyFor(93), "other": keyFor(94)}}
	w.day(0, 3600)
	w.exec("visitor", Command{Operation: "agent.register", RequestID: randomID()}, GateRef)
	w.exec("other", Command{Operation: "agent.register", RequestID: randomID()}, "qa-glama")
	if err := s.FlushFunnel(testContext); err != nil {
		t.Fatal(err)
	}
	w.day(1, 3600)
	if _, err := s.FunnelRecompute(testContext); err != nil {
		t.Fatal(err)
	}
	rows, err := s.FunnelRows(testContext)
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{}
	for _, r := range rows {
		entries[r.Account] = r.Entry
	}
	if entries[keyID(keyFor(93))] != "gate" || entries[keyID(keyFor(94))] != "ref:qa-glama" {
		t.Fatalf("entries: %v", entries)
	}
	if EntryPoints[0].Name != "gate" {
		t.Fatal("gate is tested before any other ref")
	}
}

func TestGateNetworkRate(t *testing.T) {
	s := gateStore(t, Features{})
	for i := 0; i < gate.NetworkChallengesMax; i++ {
		if _, err := s.Execute(testContext, Command{Operation: "gate.challenge", Data: `{"schema":1,"origin":"https://shop.example"}`}, "192.0.2.50"); err != nil {
			t.Fatalf("challenge %d: %v", i, err)
		}
	}
	_, err := s.Execute(testContext, Command{Operation: "gate.challenge", Data: `{"schema":1,"origin":"https://shop.example"}`}, "192.0.2.50")
	if e, ok := err.(*Error); !ok || e.Code != "challenge_rate" || e.RetryAfter == 0 {
		t.Fatalf("over the network cap: %v", err)
	}
	res, err := s.Execute(testContext, Command{Operation: "gate.challenge", Data: `{"schema":1,"origin":"https://shop.example"}`}, "192.0.2.51")
	if err != nil || res.Data["pow"].(map[string]any)["bits"] != gate.BitsDefault {
		t.Fatalf("another network: %v %v", res.Data, err)
	}
}
