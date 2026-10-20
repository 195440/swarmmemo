package board

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// C493: every operation's data is strict JSON, refused at one choke point
// (strictCommandData) before any parser reads it or anything is written,
// with the code the operation's own parser refuses its data with.

// Each parser that answers a board Error refuses malformed data with the
// code dataRefusals gives its operation, so the strict check answers as the
// parser would.
func TestDataRefusalsAreTheParsers(t *testing.T) {
	for op, probe := range dataProbes {
		var e *Error
		if !asError(probe(`{`), &e) {
			continue // a services.StrictObject or gate parser: its Store method maps the code
		}
		if got := dataRefusal(op); got != e.Code {
			t.Errorf("%s: dataRefusals gives %s, its parser refuses with %s", op, got, e.Code)
		}
	}
	for op, want := range map[string]string{
		"work.reviewer.set": "invalid_work_data", "feed.profile.put": "invalid_feed_profile", "conversation.seal": "invalid_seal",
		"conversation.get": "invalid_conversation", "updates.get": "invalid_request", "room.style.check": "invalid_style",
	} {
		if got := dataRefusal(op); got != want {
			t.Errorf("%s: %s, want %s", op, got, want)
		}
	}
}

func TestStrictCommandDataShapes(t *testing.T) {
	deep := strings.Repeat(`{"a":`, 33) + "1" + strings.Repeat("}", 33)
	for _, tc := range []struct{ data, says string }{
		{`{"schema":1,"x":{"k":1,"k":2}}`, "repeats a field"},
		{`{"schema":1,"x":[{"k":1},{"k":1,"K":2}]}`, "repeats a field"},
		{`{"schema":1} {}`, "nothing after it"},
		{`1,"x":1`, "nothing after it"},
		{"{\"schema\":1,\"x\":\"\xff\"}", "UTF-8"},
		{deep, "deeper than 32"},
	} {
		err := strictCommandData(Command{Operation: "vouch", Data: tc.data})
		var e *Error
		if !asError(err, &e) || e.Status != 400 || e.Code != "invalid_vouch" || !strings.Contains(e.Message, tc.says) {
			t.Errorf("%q: %v", tc.data, err)
		}
	}
	// Malformed JSON and other shapes go on to the operation's parser, in
	// its own words; raw data is not JSON and is never read here.
	for _, c := range []Command{
		{Operation: "vote", Data: "1"},
		{Operation: "updates.get", Data: "not json"},
		{Operation: "updates.get", Data: "null"},
		{Operation: "feed.get", Data: `{"profile":`},
		{Operation: "post", Data: strings.Repeat(`{"a":`, 32) + "1" + strings.Repeat("}", 32)},
		{Operation: "blob.put", Data: "eyJhIjoxLCJhIjoyfQ"},
		{Operation: "service.call", Data: `{"schema":1,"schema":1}`},
		{Operation: "trust.get", Data: "viewer"},
		{Operation: "rooms.list", Data: `{"a":1,"a":2}`}, // no data spec: refused as an unexpected field
	} {
		if err := strictCommandData(c); err != nil {
			t.Errorf("%s %q: %v", c.Operation, c.Data, err)
		}
	}
	// The private plane names nothing.
	var e *Error
	if err := strictCommandData(Command{Operation: "private_read.revoke", Data: `{"schema":1,"schema":1}`}); !asError(err, &e) || e.Code != "invalid_private_read_data" || e.Message != "Use the exact supported private read schema and fields." {
		t.Errorf("private read: %v", err)
	}
}

// A work verdict whose checks repeat a key (in any case) is refused before
// anything moves; the same verdict with strict data then lands.
func TestWorkDataStrictAtEveryDepth(t *testing.T) {
	s := openTest(t, Config{})
	owner, worker := keyFor(201), keyFor(202)
	id := createTestWork(t, s, owner, "lobby", "request", 0)
	run(t, s, workCommand(s, worker, Command{Operation: "work.claim", MessageID: id, TTL: 600}))
	run(t, s, workCommand(s, worker, Command{Operation: "work.submit", MessageID: id, Amount: 1, Target: workResult(t, s, worker, id, "lobby")}))
	accept := func(checks string) Command {
		c := Command{Operation: "work.accept", MessageID: id, Amount: 1, Timestamp: s.now().Unix()}
		c.Data = fmt.Sprintf(`{"schema":1,"generation":%q,"checks":%s}`, s.generation, checks)
		return signed(owner, c)
	}
	for _, checks := range []string{
		`[{"property":"integrity","state":"pass","state":"fail"}]`,
		`[{"property":"integrity","state":"pass","STATE":"fail"}]`,
		`[{"property":"integrity","state":"pass"},{"property":"conformance","state":"not_checked","note":"a","note":"b"}]`,
	} {
		if msg := failsWith(t, s, accept(checks), "invalid_work_data"); !strings.HasPrefix(msg, "data repeats a field") {
			t.Fatalf("%s: %q", checks, msg)
		}
		if w := getTestWork(t, s, id); w.State != "submitted" || w.VerdictChecks != nil {
			t.Fatalf("after a refused accept: %+v", w)
		}
	}
	run(t, s, accept(`[{"property":"integrity","state":"pass"},{"property":"conformance","state":"not_checked","note":"later"}]`))
	if w := getTestWork(t, s, id); w.State != "accepted" || w.VerdictChecks == nil || len(w.VerdictChecks.Checks) != 2 {
		t.Fatalf("strict accept: %+v", w)
	}
}

// A witness whose nested checks repeat a key is refused before it is
// recorded.
func TestWitnessDataStrictAtEveryDepth(t *testing.T) {
	s, _ := linkTest(t)
	a, other, b := keyFor(203), keyFor(204), keyFor(205)
	registerAll(t, s, a, b)
	nonce := "witness-strict-nonce-01234567"
	value := provenLink(t, s, a, other, nonce)
	witness := func(checks string) Command {
		head, _ := json.Marshal(map[string]any{"schema": 1, "agent": keyID(a), "kind": "ed25519", "value": value, "nonce": nonce, "verdict": "verified"})
		return signed(b, Command{Operation: "identity.witness", Data: strings.TrimSuffix(string(head), "}") + `,"checks":` + checks + `}`})
	}
	if msg := failsWith(t, s, witness(`[{"property":"signature","state":"pass","tool":"a","tool":"b"}]`), "invalid_witness"); !strings.HasPrefix(msg, "data repeats a field") {
		t.Fatalf("nested duplicate: %q", msg)
	}
	if l := linkOf(t, s, a, "ed25519"); len(l.Witnesses) != 0 {
		t.Fatalf("a refused witness was recorded: %+v", l.Witnesses)
	}
	run(t, s, witness(`[{"property":"signature","state":"pass","tool":"a"}]`))
	if l := linkOf(t, s, a, "ed25519"); len(l.Witnesses) != 1 || len(l.Witnesses[0].Checks) != 1 {
		t.Fatalf("strict witness: %+v", l.Witnesses)
	}
}

// A messaging policy whose nested object repeats a key leaves the stored
// settings as they were.
func TestMessagingPolicyDataStrictAtEveryDepth(t *testing.T) {
	s := openTest(t, Config{})
	key := keyFor(206)
	setPolicy(t, s, key, `{"schema":1,"preset":"closed"}`)
	stored := func() string {
		var settings string
		if err := s.db.QueryRowContext(testContext, "SELECT settings FROM messaging_settings WHERE account=?", keyID(key)).Scan(&settings); err != nil {
			t.Fatal(err)
		}
		return settings
	}
	before := stored()
	for _, data := range []string{
		`{"schema":1,"inbound_policy":{"schema":1,"preset":"closed","preset":"open"}}`,
		`{"schema":1,"inbound_policy":{"schema":1,"rules":[{"if":{"contact":true,"contact":false},"then":"deliver"}]}}`,
	} {
		if msg := failsWith(t, s, signed(key, Command{Operation: "messaging.policy.set", Data: data}), "invalid_messaging_policy"); !strings.HasPrefix(msg, "data repeats a field") {
			t.Fatalf("%s: %q", data, msg)
		}
		if after := stored(); after != before {
			t.Fatalf("settings changed by a refused set:\n%s\n%s", before, after)
		}
	}
	setPolicy(t, s, key, `{"schema":1,"rules":[{"if":{"contact":true},"then":"deliver"}]}`)
	if stored() == before {
		t.Fatal("strict nested data did not land")
	}
}

// Raw data is skipped: blob bytes are stored as sent, and a service call's
// data keeps the service parser's own refusal.
func TestRawDataUnaffectedByStrictData(t *testing.T) {
	s, _, _ := openServiceTest(t)
	key := keyFor(207)
	text := `{"a":1,"a":2}`
	run(t, s, signed(key, Command{Operation: "room.create", Room: "strict-raw", Visibility: "private"}))
	blob := upload(t, s, key, "strict-raw", text, 0)
	if got := run(t, s, signed(key, Command{Operation: "blob.get", MessageID: blob.ID})); got.Data["data"] != base64.RawURLEncoding.EncodeToString([]byte(text)) {
		t.Fatalf("blob bytes: %v", got.Data["data"])
	}
	err := func() error {
		_, err := s.Execute(testContext, signed(key, Command{Operation: "service.call", Target: "memory", Data: `{"schema":1,"method":"put","args":{"key":"a","key":"b","value":"v"},"max_cost":300}`, RequestID: "strict-raw-1"}), "test-origin")
		return err
	}()
	var e *Error
	if !asError(err, &e) || e.Code != "invalid_service_data" {
		t.Fatalf("service.call with a repeated arg: %v", err)
	}
	run(t, s, svcCall(key, "memory", "put", map[string]string{"key": "a", "value": "v"}, 300, "strict-raw-2"))
}
