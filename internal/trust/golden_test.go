package trust

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite internal/trust/testdata golden files")

// goldenUnifiedSnapshot is RFC 0021's recomputability fixture (parameter
// version 8): every input a run reads. Proofs of each priced kind (a domain
// with its age and rank, a wallet shared by two roots, a GitHub id, proof of
// work), money paid here, independent and dependent payments with a payout
// address, replies, votes (signed, unsigned, down), vouches, work accepts,
// witness verdicts, reviews of agents and of an owned subject, a breaker, a
// funnel, a carry from a previous run (money, time present, a fading
// account, backing), and the penalty log: an applied entry with a burn and
// a liability, a lifted entry and a pending moderation entry.
// testdata/golden_inputs.jsonl and friends are the retired versions' frozen
// fixtures, which only scripts/trust/test_recompute.py still verifies.
func goldenUnifiedSnapshot() Snapshot {
	b := newBuilder()
	for _, name := range []string{"alice", "bob", "carol", "dave", "erin", "frank", "grace", "heidi", "ivan", "judy", "mallory", "oscar", "peggy", "trent"} {
		b.account(name, 60)
	}
	b.domain("alice", "alice.example", 900, 120).github("alice", "1001", 250).pay("alice", 1200, 40)
	b.wallet("bob", "0xb0b", 450).pay("bob", 300, 12)
	b.wallet("carol", "0xc4", 600).wallet("dave", "0xc4", 600) // one wallet, two accounts: one root
	b.snap.Proofs = append(b.snap.Proofs, Record{Type: "proof", Account: acct("erin"), Kind: "pow", LinkValue: "pow", State: "verified", CreatedAt: ago(30), CheckedAt: ago(2),
		Root: "pow:" + acct("erin"), Assessed: 50_000_000})
	b.link("frank", "erin")
	b.pay("grace", 900, 3).pay("heidi", 150, 100).pay("judy", 80, 5)
	b.payTo("alice", "bob", 400, 20, "").payTo("bob", "alice", 50, 18, "")          // a path back: counts 0
	b.payTo("grace", "heidi", 300, 9, "0x4e1d1").payTo("alice", "ivan", 200, 7, "") // independent
	b.reply("alice", "bob", 2).reply("bob", "carol", 3).reply("grace", "judy", 1).reply("grace", "judy", 4).reply("heidi", "ivan", 2)
	b.vote("alice", "grace", 1).vote("bob", "heidi", 2).vote("carol", "ivan", 5).endorse("vote", "dave", "ivan", -1, 3)
	b.snap.Endorsements = append(b.snap.Endorsements, Record{Type: "endorsement", Seq: 9000, Kind: "unsigned", Voter: acct("oscar"), Target: acct("alice"),
		MessageID: b.postBy("alice", 3), Value: 1, CreatedAt: ago(2)})
	b.vouch("grace", "judy", 6).vouch("alice", "heidi", 8).endorse("vouch", "bob", "heidi", 0, 4)
	b.accept("alice", "judy", 3).accept("grace", "ivan", 10)
	b.snap.Acts = append(b.snap.Acts, Record{Type: "edge", Kind: "witness", ID: "link:1", From: acct("bob"), To: acct("carol"), CreatedAt: ago(6), Value: 1},
		Record{Type: "edge", Kind: "witness", ID: "link:1", From: acct("heidi"), To: acct("carol"), CreatedAt: ago(5), Value: -1})
	for i, who := range []string{"alice", "bob", "grace", "heidi", "judy"} {
		b.review(who, "ivan", int64(60+8*i), []string{"paid_use", "work", "observed", "free_use", "opinion"}[i], int64(20-i*3))
	}
	b.review("ivan", "judy", 30, "work", 4)
	b.snap.Reviews = append(b.snap.Reviews, Record{Type: "review", ID: "r-sub", Account: acct("bob"), Target: "subject:s1", Value: 85, Kind: "paid_use", LinkValue: "competitor",
		State: "good", CreatedAt: ago(7)})
	b.snap.Owners = append(b.snap.Owners, Record{Type: "owner", Target: "subject:s1", Account: acct("grace")})
	b.snap.Breakers = append(b.snap.Breakers, Record{Type: "breaker", Account: acct("peggy"), StartedAt: ago(2), TrustUntil: testAsOf + 5*day})
	b.vote("peggy", "alice", 1)
	// A funnel: five farm keys send their unspent allowance to mallory.
	for i := 0; i < 5; i++ {
		f := "farm" + string(rune('a'+i))
		b.snap.Claims = append(b.snap.Claims, Record{Type: "claim", Account: acct(f), Day: dayOf(testAsOf) - 2, Claimed: 4096, Spent: 1})
		b.transfer(f, "mallory", 4000, 2)
	}
	b.transfer("trent", "oscar", 10, 30)
	// The previous run's carry.
	D := dayOf(testAsOf)
	b.snap.Carry = []Record{{Type: "carry", Kind: "run", AsOf: testAsOf - day}}
	b.carryWorld("alice", CarryAccount{Money: 1200 * mc, Time: 90 * mc, LastActive: D - 2, Direct: 1650, Standing: 750, Backed: 30 * mc})
	b.carryWorld("heidi", CarryAccount{Money: 150 * mc, LastActive: D - 140, FadePPM: 950000, Direct: 140, Standing: 140})
	b.carryWorld("grace", CarryAccount{Time: 40 * mc, LastActive: D - 1, Direct: 0, Standing: 40})
	// The penalty log.
	b.snap.Penalties = append(b.snap.Penalties,
		Record{Type: "penalty", ID: "ring-old", Kind: TriggerRing, Account: acct("trent"), CreatedAt: ago(10), EndsAt: ago(10) + 30*day, Value: 1, State: "applied", Evidence: "ring of 3"},
		Record{Type: "penalty", ID: "override-x", Kind: TriggerOverride, Account: acct("oscar"), CreatedAt: ago(20), EndsAt: ago(20) + 30*day, Value: 1, State: "applied", Evidence: "mistake"},
		Record{Type: "penalty", ID: "moderation-1", Kind: TriggerModeration, Account: acct("mallory"), CreatedAt: ago(1), State: "pending", Evidence: "post m1 (phishing)"})
	b.snap.Burns = append(b.snap.Burns, Record{Type: "burn", ID: "ring-old", Root: "github:666"})
	b.snap.Liabilities = append(b.snap.Liabilities, Record{Type: "liability", ID: "ring-old", Account: acct("peggy"), FractionPPM: 375000, CreatedAt: ago(10), EndsAt: ago(10) + 30*day, Value: 1})
	b.snap.Lifts = append(b.snap.Lifts, Record{Type: "lift", ID: "override-x", CreatedAt: ago(15)})
	b.snap.Meta.EventsSeq, b.snap.Meta.EndorsementsSeq, b.snap.Meta.LedgerSeq = 812, 9000, 77
	return b.snap
}

func TestGoldenFixture(t *testing.T) {
	inPath := filepath.Join("testdata", "golden_unified_inputs.jsonl")
	outPath := filepath.Join("testdata", "golden_unified_output.json")
	if *updateGolden {
		snap := goldenUnifiedSnapshot()
		var buf bytes.Buffer
		if err := snap.WriteJSONL(&buf); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(inPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := Compute(context.Background(), snap)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(outPath, out.Canonical(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(inPath)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := ReadJSONL(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err = snap.WriteJSONL(&again); err != nil || !bytes.Equal(again.Bytes(), raw) {
		t.Fatalf("the fixture inputs are not in canonical JSONL form (%v); rerun with -update", err)
	}
	out, err := Compute(context.Background(), snap)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Canonical(); !bytes.Equal(got, want) {
		t.Fatalf("golden output changed; if intended, rerun with -update and bump the parameter or detector version\n got %.600s", got)
	}
	// The fixture must exercise what it claims to.
	kinds := map[string]bool{}
	for _, sc := range out.Scores {
		for _, it := range sc.Parts.World {
			kinds[it.Kind+":"+it.Source+":"+it.State] = true
		}
	}
	for _, k := range []string{"proof:domain:verified", "proof:wallet:verified", "proof:github:verified", "proof:pow:verified", "money:paid_to_swarmmemo:counted",
		"time_present:active_days:counted", "payments:independent_payments:counted"} {
		if !kinds[k] {
			t.Errorf("fixture coverage: no %s world item (%v)", k, kinds)
		}
	}
	if len(out.Evidence) == 0 || len(out.Penalties) == 0 || out.Summary.Penalised == 0 || out.Summary.Liable == 0 || out.Summary.Marked == 0 || out.CarryIn == "" {
		t.Errorf("fixture coverage: evidence %d, penalties %d, summary %+v, carry in %q", len(out.Evidence), len(out.Penalties), out.Summary, out.CarryIn)
	}
	testRecomputePy(t, raw, out.Canonical())
}

// testRecomputePy runs the independent verifier (scripts/trust/recompute.py
// run) on the inputs and compares the sha256 of its output with Go's.
func testRecomputePy(t *testing.T, inputs, want []byte) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	file := filepath.Join(t.TempDir(), "inputs.jsonl")
	if err = os.WriteFile(file, inputs, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(python, "-B", filepath.Join("..", "..", "scripts", "trust", "recompute.py"), "run", file).Output()
	if err != nil {
		t.Fatalf("recompute.py run: %v", err)
	}
	g, w := sha256.Sum256(got), sha256.Sum256(want)
	if g != w {
		t.Fatalf("recompute.py output sha256 %s, Go %s", hex.EncodeToString(g[:]), hex.EncodeToString(w[:]))
	}
}
