package gate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func validOrigin(o string) bool {
	return strings.HasPrefix(o, "https://") && !strings.Contains(o[8:], "/")
}

func TestParseChallengeRequest(t *testing.T) {
	good := []string{
		`{"schema":1,"origin":"https://shop.example"}`,
		`{"schema":1,"origin":"https://shop.example","min":500,"nonce":"abcdefgh","bits":24}`,
		`{"schema":1,"origin":"https://shop.example","mode":"agent"}`,
		`{"schema":1,"mode":"agent"}`, // the board names its own origin
		`{"schema":1,"origin":"https://shop.example","bits":20}`,
	}
	for _, raw := range good {
		if _, err := ParseChallengeRequest([]byte(raw), validOrigin); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
	bad := map[string]error{
		`{"schema":2,"origin":"https://shop.example"}`:                     ErrRequest,
		`{"schema":1,"origin":"https://shop.example/path"}`:                ErrOrigin,
		`{"schema":1,"origin":"https://shop.example","min":-1}`:            ErrRequest,
		`{"schema":1,"origin":"https://shop.example","min":1000001}`:       ErrRequest,
		`{"schema":1,"origin":"https://shop.example","nonce":"short"}`:     ErrRequest,
		`{"schema":1,"origin":"https://shop.example","nonce":"a b c d e"}`: ErrRequest,
		`{"schema":1,"origin":"https://shop.example","bits":15}`:           ErrRequest,
		// C325-sec: the caller asks for the challenge, so it may not lower
		// the work below the default.
		`{"schema":1,"origin":"https://shop.example","bits":16}`: ErrRequest,
		`{"schema":1,"origin":"https://shop.example","bits":19}`: ErrRequest,
		`{"schema":1}`:              ErrOrigin,
		`{"schema":1,"mode":"pow"}`: ErrOrigin,
		`{"schema":1,"origin":"https://shop.example","bits":29}`:              ErrRequest,
		`{"schema":1,"origin":"https://shop.example","mode":"captcha"}`:       ErrRequest,
		`{"schema":1,"origin":"https://shop.example","mode":"agent","min":5}`: ErrRequest,
		`{"schema":1,"origin":"https://shop.example","extra":1}`:              ErrRequest,
		`{"schema":1,"origin":"https://shop.example"} {}`:                     ErrRequest,
		`[]`: ErrRequest,
		``:   ErrRequest,
		`{"schema":1,"origin":"https://shop.example","nonce":"` + strings.Repeat("a", 3000) + `"}`: ErrRequest,
	}
	for raw, want := range bad {
		if _, err := ParseChallengeRequest([]byte(raw), validOrigin); !errors.Is(err, want) {
			t.Errorf("%.80s: got %v, want %v", raw, err, want)
		}
	}
}

func TestParseVerifyRequest(t *testing.T) {
	id := strings.Repeat("a", 32)
	sig := "0x" + strings.Repeat("1", 130)
	wallet := "0x" + strings.Repeat("A", 40)
	cases := []struct {
		raw    string
		signed bool
		ok     bool
	}{
		{`{"schema":1,"challenge":"` + id + `","solution":"z9"}`, false, true},
		{`{"schema":1,"challenge":"` + id + `","answer":"1234"}`, false, true},
		{`{"schema":1,"challenge":"` + id + `","answer":"1234"}`, true, true},
		// C325-sec: a wallet is free to make, so it never passes alone; it
		// names the subject beside the work.
		{`{"schema":1,"challenge":"` + id + `","wallet":"` + wallet + `","signature":"` + sig + `"}`, false, false},
		{`{"schema":1,"challenge":"` + id + `","solution":"z9","wallet":"` + wallet + `","signature":"` + sig + `"}`, false, true},
		{`{"schema":1,"challenge":"` + id + `","answer":"1","wallet":"` + wallet + `","signature":"` + sig + `"}`, false, false},
		{`{"schema":1,"challenge":"` + id + `","solution":"z9","signature":"` + sig + `"}`, false, false},
		{`{"schema":1,"challenge":"` + id + `"}`, true, true},
		{`{"schema":1,"challenge":"` + id + `"}`, false, false},
		{`{"schema":1,"challenge":"` + id + `","solution":"z9"}`, true, true},
		{`{"schema":1,"challenge":"` + id + `","wallet":"` + wallet + `","signature":"` + sig + `"}`, true, false},
		{`{"schema":1,"challenge":"` + id + `","solution":"z9","answer":"1"}`, false, false},
		{`{"schema":1,"challenge":"` + id + `","solution":"a-b"}`, false, false},
		{`{"schema":1,"challenge":"` + id + `","solution":"` + strings.Repeat("a", 65) + `"}`, false, false},
		{`{"schema":1,"challenge":"` + id + `","answer":"12x"}`, false, false},
		{`{"schema":1,"challenge":"` + id + `","wallet":"` + wallet + `"}`, false, false},
		{`{"schema":1,"challenge":"XYZ","solution":"1"}`, false, false},
		{`{"schema":1,"challenge":"` + id + `","solution":"1","x":1}`, false, false},
	}
	for _, c := range cases {
		if _, err := ParseVerifyRequest([]byte(c.raw), c.signed); (err == nil) != c.ok {
			t.Errorf("%s signed=%v: err %v, want ok=%v", c.raw, c.signed, err, c.ok)
		}
	}
}

func TestIssueTakeIsSingleUseAndExpires(t *testing.T) {
	now := time.Unix(1790000000, 0)
	g := New()
	g.Now = func() time.Time { return now }
	req := ChallengeRequest{Schema: 1, Origin: "https://shop.example", Mode: ModePow}
	c, err := g.Issue(req, "192.0.2.1")
	if err != nil || len(c.ID) != 32 || c.Nonce != c.ID || c.Bits != BitsDefault {
		t.Fatalf("issue: %+v %v", c, err)
	}
	if _, err = g.Take(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = g.Take(c.ID); !errors.Is(err, ErrUnknown) {
		t.Fatalf("second take: %v", err)
	}
	c2, _ := g.Issue(req, "192.0.2.1")
	now = now.Add(ChallengeTTL)
	if _, err = g.Take(c2.ID); !errors.Is(err, ErrUnknown) {
		t.Fatalf("expired take: %v", err)
	}
	if _, err = g.Take("nope"); !errors.Is(err, ErrUnknown) {
		t.Fatal(err)
	}
}

func TestEscalationAndNetworkCap(t *testing.T) {
	for count, want := range map[int]int{1: 0, 8: 0, 9: 1, 16: 1, 17: 2, 32: 2, 33: 3, 1000: 7} {
		if got := Escalation(count); got != want {
			t.Errorf("Escalation(%d) = %d, want %d", count, got, want)
		}
	}
	now := time.Unix(1790000000, 0)
	g := New()
	g.Now = func() time.Time { return now }
	req := ChallengeRequest{Schema: 1, Origin: "https://shop.example", Mode: ModePow}
	var last Challenge
	for i := 0; i < NetworkChallengesMax; i++ {
		c, err := g.Issue(req, "198.51.100.7")
		if err != nil {
			t.Fatalf("challenge %d: %v", i, err)
		}
		last = c
	}
	if last.Bits != BitsDefault+Escalation(NetworkChallengesMax) {
		t.Fatalf("bits after %d: %d", NetworkChallengesMax, last.Bits)
	}
	if _, err := g.Issue(req, "198.51.100.7"); !errors.Is(err, ErrRate) {
		t.Fatalf("over the cap: %v", err)
	}
	// Another network is unaffected; the window ends.
	if c, err := g.Issue(req, "198.51.100.8"); err != nil || c.Bits != BitsDefault {
		t.Fatalf("other network: %+v %v", c, err)
	}
	now = now.Add(EscalateWindow)
	if c, err := g.Issue(req, "198.51.100.7"); err != nil || c.Bits != BitsDefault {
		t.Fatalf("next window: %+v %v", c, err)
	}
	// A site's harder floor and the ceiling.
	hard := req
	hard.Bits = BitsMax
	if c, _ := g.Issue(hard, "203.0.113.1"); c.Bits != BitsMax {
		t.Fatalf("site bits: %d", c.Bits)
	}
}

func TestNetworkKey(t *testing.T) {
	if NetworkKey("192.0.2.1") != "192.0.2.1" || NetworkKey("2001:db8::1") != NetworkKey("2001:db8::ffff") || NetworkKey("2001:db8:0:1::1") == NetworkKey("2001:db8::1") {
		t.Fatal("network keys")
	}
}

// C325-sec: a full gate drops its oldest pending challenge rather than
// refusing new ones, so a flood from a few hundred networks cannot shut
// the gate for every site (it answered 503 gate_busy to everyone before).
func TestPendingCapDropsOldest(t *testing.T) {
	now := time.Unix(1790000000, 0)
	g := New()
	g.Now = func() time.Time { return now }
	for i := 0; i < PendingMax; i++ {
		id := strconv.Itoa(i)
		g.pending[id] = Challenge{ID: id, IssuedAt: now}
		g.order = append(g.order, id)
	}
	// A taken challenge at the head is skipped, not counted.
	delete(g.pending, "0")
	g.pending["fill"] = Challenge{ID: "fill", IssuedAt: now}
	g.order = append(g.order, "fill")
	c, err := g.Issue(ChallengeRequest{Schema: 1, Origin: "https://a.example", Mode: ModePow}, "192.0.2.1")
	if err != nil {
		t.Fatalf("full gate: %v", err)
	}
	if len(g.pending) != PendingMax || g.pending["1"].ID != "" || g.pending["2"].ID != "2" {
		t.Fatalf("pending %d; oldest kept %v", len(g.pending), g.pending["1"])
	}
	if _, err = g.Take(c.ID); err != nil {
		t.Fatalf("the new challenge: %v", err)
	}
	// Expired challenges leave from the head as time passes, and the order
	// stays bounded.
	now = now.Add(ChallengeTTL)
	if _, err = g.Issue(ChallengeRequest{Schema: 1, Origin: "https://a.example", Mode: ModePow}, "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	if len(g.pending) != 1 || len(g.order)-g.head != 1 || len(g.order) > 2048 {
		t.Fatalf("after expiry: %d pending, order %d from %d", len(g.pending), len(g.order), g.head)
	}
}

// C325-sec: an IPv6 caller counts against its /48 too, so walking a /48's
// 65,536 /64s neither resets the escalation nor escapes the cap.
func TestWideNetworkEscalation(t *testing.T) {
	now := time.Unix(1790000000, 0)
	g := New()
	g.Now = func() time.Time { return now }
	req := ChallengeRequest{Schema: 1, Origin: "https://shop.example", Mode: ModePow}
	var last Challenge
	n := 0
	for ; n < NetworkChallengesMax*WideFactor; n++ {
		c, err := g.Issue(req, fmt.Sprintf("2001:db8:7:%x::1", n)) // a fresh /64 each time
		if err != nil {
			t.Fatalf("challenge %d: %v", n, err)
		}
		last = c
	}
	if want := BitsDefault + Escalation(NetworkChallengesMax*WideFactor/WideFactor); last.Bits != want || want == BitsDefault {
		t.Fatalf("bits after %d /64s: %d, want %d", n, last.Bits, want)
	}
	if _, err := g.Issue(req, "2001:db8:7:ffff::1"); !errors.Is(err, ErrRate) {
		t.Fatalf("over the /48 cap: %v", err)
	}
	if c, err := g.Issue(req, "2001:db8:8::1"); err != nil || c.Bits != BitsDefault {
		t.Fatalf("another /48: %+v %v", c, err)
	}
	if WideKey("192.0.2.1") != "" || WideKey("2001:db8:7:1::1") != WideKey("2001:db8:7:ffff::9") || WideKey("2001:db8:7::1") == WideKey("2001:db8:8::1") {
		t.Fatal("wide keys")
	}
}

// C325-sec: two verifies racing for one challenge: exactly one takes it.
func TestTakeRace(t *testing.T) {
	g := New()
	for round := 0; round < 200; round++ {
		c, err := g.Issue(ChallengeRequest{Schema: 1, Origin: "https://a.example", Mode: ModePow}, strconv.Itoa(round))
		if err != nil {
			t.Fatal(err)
		}
		var wins atomic.Int32
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := g.Take(c.ID); err == nil {
					wins.Add(1)
				}
			}()
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("round %d: %d verifies took one challenge", round, wins.Load())
		}
	}
}

func TestWorkAndTask(t *testing.T) {
	prefix := strings.Repeat("0f", 16)
	sol, ok := Solve(prefix, 12, 1<<20)
	if !ok || !CheckWork(prefix, sol, 12) || CheckWork(prefix, sol+"x", 30) {
		t.Fatalf("solve: %q %v", sol, ok)
	}
	var seed [32]byte
	seed[0] = 7
	task, answer := newTask(seed)
	again, answer2 := newTask(seed)
	if answer != answer2 || len(task.Records) != AgentRecords || task.DeadlineMS != AgentDeadlineMillis {
		t.Fatal("task is not deterministic")
	}
	// The answer is what the rule says: computed from the JSON a caller sees.
	raw, _ := json.Marshal(again)
	var seen Task
	if err := json.Unmarshal(raw, &seen); err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, r := range seen.Records {
		if r.Tag == seen.Where.Tag {
			sum += r.Value
		}
	}
	if strconv.Itoa(sum) != answer {
		t.Fatalf("answer %s, sum %d", answer, sum)
	}
}

// BenchmarkWork is the native cost of one default challenge's expected
// work: 2^BitsDefault hashes.
func BenchmarkWork(b *testing.B) {
	prefix := strings.Repeat("ab", 16)
	for i := 0; i < b.N; i++ {
		CheckWork(prefix, strconv.Itoa(i), BitsDefault)
	}
}

func FuzzParseChallengeRequest(f *testing.F) {
	f.Add([]byte(`{"schema":1,"origin":"https://shop.example","min":5,"nonce":"abcdefgh"}`))
	f.Add([]byte(`{"schema":1,"origin":"https://x","mode":"agent","bits":20}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		r, err := ParseChallengeRequest(raw, validOrigin)
		if err == nil && (r.Schema != 1 || r.Min < 0 || r.Min > MinCentsMax || (r.Mode != ModePow && r.Mode != ModeAgent) || len(r.Nonce) > 64) {
			t.Fatalf("accepted %q as %+v", raw, r)
		}
	})
}

func FuzzParseVerifyRequest(f *testing.F) {
	f.Add([]byte(`{"schema":1,"challenge":"`+strings.Repeat("a", 32)+`","solution":"z9"}`), false)
	f.Add([]byte(`{"schema":1,"challenge":"`+strings.Repeat("b", 32)+`"}`), true)
	f.Fuzz(func(t *testing.T, raw []byte, signed bool) {
		r, err := ParseVerifyRequest(raw, signed)
		if err == nil && (len(r.Challenge) != 32 || len(r.Solution) > SolutionMax || len(r.Answer) > AnswerMax || len(r.Signature) > 132 ||
			(r.Wallet != "" || r.Signature != "") && (r.Solution == "" || signed)) {
			t.Fatalf("accepted %q as %+v", raw, r)
		}
	})
}
