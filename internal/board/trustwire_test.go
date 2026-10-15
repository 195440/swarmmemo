package board

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/trust"
)

// trustFixture opens a store with TRUST on and three signed agents with
// public posts two days before the run: seed, alice and bob. The seed paid
// SwarmMemo $10 (a credit top-up) and vouches for alice; alice and the seed
// vote for bob's post (the seed's vote unsigned: no judgement).
func trustFixture(t *testing.T) (s *Store, seed, alice, bob ed25519.PrivateKey, bobPost string) {
	t.Helper()
	s = openTest(t, Config{Features: Features{Trust: TrustOn}})
	seed, alice, bob = keyFor(61), keyFor(62), keyFor(63)
	s.now = func() time.Time { return time.Unix(testTime-2*86400, 0) }
	postAs(t, s, seed, Command{Room: "lobby", Text: "seed here", RequestID: "t-seed"})
	alicePost := postAs(t, s, alice, Command{Room: "lobby", Text: "alice here", RequestID: "t-alice"})
	bobPost = postAs(t, s, bob, Command{Room: "lobby", Text: "bob here", RequestID: "t-bob"})
	postAs(t, s, bob, Command{Room: "lobby", Text: "thanks alice", ReplyTo: alicePost, RequestID: "t-bob-reply"})
	s.now = func() time.Time { return time.Unix(testTime+3600, 0) } // 01:00 UTC, run day
	sig := "sig"
	records := []EndorsementRecord{
		{Type: "vouch", Seq: 1, Voter: keyID(seed), Target: keyID(alice), Value: 1, CreatedAt: testTime - 86400, Signature: &sig},
		{Type: "vote", Seq: 2, Voter: keyID(alice), MessageID: bobPost, Value: 1, CreatedAt: testTime - 86400, Signature: &sig},
		{Type: "vote", Seq: 3, Voter: keyID(seed), MessageID: bobPost, Value: 1, CreatedAt: testTime - 86400}, // unsigned
	}
	s.trust.endorsements = func(ctx context.Context, after int64, limit int) ([]EndorsementRecord, int64, error) {
		var out []EndorsementRecord
		for _, r := range records {
			if r.Seq > after && len(out) < limit {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil, after, nil
		}
		return out, out[len(out)-1].Seq, nil
	}
	paidIn(t, s, seed, 1000, testTime-2*86400)
	return s, seed, alice, bob, bobPost
}

// paidIn records a credited top-up of cents for key at, money paid to
// SwarmMemo (RFC 0021 §7).
func paidIn(t *testing.T, s *Store, key ed25519.PrivateKey, cents, at int64) {
	t.Helper()
	id := "topup-" + keyID(key)[:8] + "-" + strconv.FormatInt(at, 10)
	if _, err := s.db.Exec(`INSERT INTO credit_topups(id,account,agent,amount,day,network,asset,pay_to,payer,auth_nonce,valid_before,state,created_at,settled_at)
 VALUES(?,?,?,?,?,'base','usdc','0xpay','0xpayer',?,0,'credited',?,?)`, id, keyID(key), keyID(key), cents*(CreditsPerUSD/100), at/86400, id, at, at); err != nil {
		t.Fatal(err)
	}
}

func trustGet(t *testing.T, s *Store, target string) map[string]any {
	t.Helper()
	res, err := s.Execute(testContext, Command{Operation: "trust.get", Target: target}, "test-origin")
	if err != nil {
		t.Fatal(err)
	}
	return res.Data
}

func standingOf(t *testing.T, data map[string]any) *trust.StandingView {
	t.Helper()
	v, ok := data["standing"].(*trust.StandingView)
	if !ok {
		t.Fatalf("standing: %T %+v", data["standing"], data["standing"])
	}
	return v
}

func TestTrustOffIsUnchanged(t *testing.T) {
	s := openTest(t, Config{})
	if _, err := s.Execute(testContext, Command{Operation: "trust.get", Target: keyID(keyFor(1))}, "test-origin"); errCode(err) != "service_unavailable" {
		t.Fatalf("trust.get with TRUST off: %v", err)
	}
	if _, err := s.RunTrust(testContext); errCode(err) != "service_unavailable" {
		t.Fatalf("run with TRUST off: %v", err)
	}
	if _, err := s.TrustDistribution(testContext); errCode(err) != "service_unavailable" {
		t.Fatalf("distribution with TRUST off: %v", err)
	}
	s.StartRFC0012(testContext)
	s.StopRFC0012()
	var runs int
	if err := s.db.QueryRow("SELECT count(*) FROM trust_runs").Scan(&runs); err != nil || runs != 0 {
		t.Fatalf("runs with TRUST off: %d %v", runs, err)
	}
}

func TestTrustRunAndStanding(t *testing.T) {
	s, seed, alice, bob, _ := trustFixture(t)
	held := false
	s.trust.computed = func() {
		// The computation holds no connection: another query gets one at once.
		ctx, cancel := context.WithTimeout(testContext, 2*time.Second)
		defer cancel()
		var one int
		held = s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one) != nil
	}
	before := s.trust.pages.Load()
	sum, err := s.RunTrust(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if held {
		t.Fatal("the run held the database connection while computing")
	}
	if s.trust.pages.Load()-before < 5 {
		t.Fatal("inputs are read in pages")
	}
	if sum.State != "done" || sum.AsOf != testTime || sum.ParamsVersion != trust.DefaultVersion || len(sum.OutputSHA256) != 64 {
		t.Fatalf("summary: %+v", sum)
	}
	// The seed's $10 is its world edge: it keeps node_cap and passes the rest.
	sd := trustGet(t, s, keyID(seed))
	v := standingOf(t, sd)
	if v.StandingCents != 750 || v.Band != "trusted" || v.Run != sum.ID || v.Stale || sd["boolean"] != false || sd["passed_cents"].(int64) <= 0 {
		t.Fatalf("seed: %+v %+v", v, sd)
	}
	world := sd["world"].([]trust.WorldItem)
	if len(world) != 1 || world[0].Kind != "money" || world[0].Cents != 1000 {
		t.Fatalf("seed's own costs: %+v", world)
	}
	// Alice is backed by the seed's vouch (an opinion: 20¢ at most).
	a := trustGet(t, s, keyID(alice))
	if av := standingOf(t, a); av.StandingCents <= 0 || av.StandingCents > 20+10 || a["received_cents"].(int64) <= 0 || len(a["backers"].([]trust.FlowPart)) != 1 {
		t.Fatalf("alice: %+v", a)
	}
	for _, key := range []string{"world", "backers", "backs", "accuracy", "inactivity", "penalty", "penalties", "burn", "next_steps", "bands", "viewer", "caveats"} {
		if _, ok := a[key]; !ok {
			t.Fatalf("breakdown lacks %s: %+v", key, a)
		}
	}
	if len(a["next_steps"].([]StandingWay)) == 0 {
		t.Fatal("no next steps")
	}
	// The viewer's view: bob's from the seed's.
	out, err := s.TrustStanding(testContext, keyID(bob), keyID(seed))
	if err != nil {
		t.Fatal(err)
	}
	if view, ok := out["viewer"].(map[string]any); !ok || view["computed"] != true {
		t.Fatalf("viewer: %+v", out["viewer"])
	}
	// The classifier and v(s) read the same standing.
	tx, err := s.db.BeginTx(testContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.classifier().Classify(testContext, tx, allowance.Subject{ID: keyID(seed), Signed: true}, s.now().Unix())
	w, werr := s.voteWeightPPM(testContext, tx, keyID(seed), false, s.now().Unix())
	tx.Rollback()
	if err != nil || werr != nil || st.Cents != 750 || st.Band != allowance.BandTrusted || st.ShareCents != 750 || w != 1e6 {
		t.Fatalf("classifier %+v %v, v(s) %d %v", st, err, w, werr)
	}
	dist, err := s.TrustDistribution(testContext)
	if err != nil || dist["run"] != sum.ID || dist["bands"].(map[string]int64)["trusted"] != 1 {
		t.Fatalf("distribution: %+v %v", dist, err)
	}
}

func TestTrustRunIsRecomputable(t *testing.T) {
	s, _, _, _, _ := trustFixture(t)
	var inputs bytes.Buffer
	if err := s.WriteTrustInputs(testContext, &inputs); err != nil {
		t.Fatal(err)
	}
	sum, err := s.RunTrust(testContext)
	if err != nil {
		t.Fatal(err)
	}
	raw := append([]byte(nil), inputs.Bytes()...)
	snap, err := trust.ReadJSONL(&inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Payments) != 1 || len(snap.Endorsements) != 3 {
		t.Fatalf("snapshot: %d payments, %d endorsements", len(snap.Payments), len(snap.Endorsements))
	}
	out, err := trust.Compute(testContext, snap)
	if err != nil {
		t.Fatal(err)
	}
	if got := sha256hex(out.Canonical()); got != sum.OutputSHA256 {
		t.Fatalf("recomputed %s, recorded %s", got, sum.OutputSHA256)
	}
	// The independent verifier agrees.
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	file := filepath.Join(t.TempDir(), "snapshot.jsonl")
	if err = os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(python, "-B", filepath.Join("..", "..", "scripts", "trust", "recompute.py"), "run", file).Output()
	if err != nil {
		t.Fatalf("recompute.py: %v", err)
	}
	if sha256hex(got) != sum.OutputSHA256 {
		t.Fatalf("recompute.py %s, recorded %s", sha256hex(got), sum.OutputSHA256)
	}
	// The next run reads this run's carry.
	s.now = func() time.Time { return time.Unix(testTime+86400+3600, 0) }
	snap2, err := s.TrustInputs(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap2.Carry) == 0 {
		t.Fatal("the next run reads no carry")
	}
}

// A stored parameter version older than the compiled-in one is ignored:
// deploying the binary activates its version (C320a); a newer stored one
// applies.
func TestTrustParamsCompiledInWins(t *testing.T) {
	s, _, _, _, _ := trustFixture(t)
	if _, err := s.db.Exec("INSERT INTO params VALUES('trust',7,'{\"retired\":true}','',0,0,'test','an old version')"); err != nil {
		t.Fatal(err)
	}
	p, err := trustParams(testContext, s.db, s.now().Unix())
	if err != nil || p.Version != trust.DefaultVersion {
		t.Fatalf("with a stored retired version: %d %v", p.Version, err)
	}
	q := trust.DefaultParams()
	q.GraceDays = 90
	if _, err = s.db.Exec("INSERT INTO params VALUES('trust',9,?,'',0,0,'test','shorter grace')", string(q.Body())); err != nil {
		t.Fatal(err)
	}
	if p, err = trustParams(testContext, s.db, s.now().Unix()); err != nil || p.Version != 9 || p.GraceDays != 90 {
		t.Fatalf("with a newer stored version: %d %d %v", p.Version, p.GraceDays, err)
	}
}

func TestTrustAbortKeepsPreviousRun(t *testing.T) {
	s, _, alice, _, _ := trustFixture(t)
	first, err := s.RunTrust(testContext)
	if err != nil {
		t.Fatal(err)
	}
	p := trust.DefaultParams()
	p.MaxWork = 1
	if _, err = s.db.Exec("INSERT INTO params VALUES('trust',9,?,'',0,0,'test','tiny work bound')", string(p.Body())); err != nil {
		t.Fatal(err)
	}
	s.trust.forgetParams()
	s.now = func() time.Time { return time.Unix(testTime+86400+3600, 0) }
	if _, err = s.RunTrust(testContext); !errors.Is(err, trust.ErrWorkExceeded) {
		t.Fatalf("expected an abort: %v", err)
	}
	a := standingOf(t, trustGet(t, s, keyID(alice)))
	if a.Run != first.ID || !a.Stale {
		t.Fatalf("after abort: run=%v stale=%v", a.Run, a.Stale)
	}
	var state string
	if err = s.db.QueryRow("SELECT state FROM trust_runs ORDER BY id DESC LIMIT 1").Scan(&state); err != nil || state != "aborted" {
		t.Fatalf("aborted run recorded as %q %v", state, err)
	}
}

func TestTrustNightlyRunsOncePerDay(t *testing.T) {
	s, _, _, _, _ := trustFixture(t)
	s.now = func() time.Time { return time.Unix(testTime+600, 0) } // 00:10: not yet due
	s.trustNightly(testContext)
	s.now = func() time.Time { return time.Unix(testTime+2400, 0) } // 00:40
	s.trustNightly(testContext)
	s.trustNightly(testContext)
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM trust_runs WHERE as_of=?", testTime).Scan(&n); err != nil || n != 1 {
		t.Fatalf("runs today: %d %v", n, err)
	}
}

// The public penalty log (RFC 0021 §8): an operator's moderation penalty is
// pending until a run applies it (the member holds 0, its entry gains its
// level, end and backers), it is listed by trust.penalties and
// /api/trust/penalties, and a lift (an override with a reason) reverses it
// from the next run on.
func TestTrustPenaltyLog(t *testing.T) {
	s, seed, alice, _, _ := trustFixture(t)
	if _, err := s.PenalizeTrust(testContext, "moderation", []string{keyID(seed)}, "", "phishing", "op"); err == nil {
		t.Fatal("a moderation penalty needs its evidence")
	}
	if _, err := s.PenalizeTrust(testContext, "ring", []string{keyID(seed)}, "x", "y", "op"); err == nil {
		t.Fatal("an operator may not file a detector's trigger")
	}
	s.now = func() time.Time { return time.Unix(testTime-3600, 0) }
	id, err := s.PenalizeTrust(testContext, "moderation", []string{keyID(alice)}, "post p1 (phishing link)", "phishing in #lobby", "op")
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Unix(testTime+3600, 0) }
	entries, _, err := s.TrustPenalties(testContext, 0, 10, "")
	if err != nil || len(entries) != 1 || entries[0].State != "pending" || entries[0].Active {
		t.Fatalf("before the run: %+v %v", entries, err)
	}
	sum, err := s.RunTrust(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum.Penalties) != 1 || sum.Penalties[0].ID != id || sum.Penalties[0].Level != 1 {
		t.Fatalf("the run applied: %+v", sum.Penalties)
	}
	entries, _, err = s.TrustPenalties(testContext, 0, 10, keyID(alice))
	if err != nil || len(entries) != 1 || entries[0].State != "applied" || !entries[0].Active || entries[0].DurationDays != 30 || len(entries[0].Backers) != 1 {
		t.Fatalf("after the run: %+v %v", entries, err)
	}
	if a := standingOf(t, trustGet(t, s, keyID(alice))); a.StandingCents != 0 {
		t.Fatalf("a member holds %d¢", a.StandingCents)
	}
	res, err := s.Execute(testContext, Command{Operation: "trust.penalties", Target: keyID(alice)}, "test-origin")
	if err != nil || len(res.Data["entries"].([]trust.PenaltyEntry)) != 1 {
		t.Fatalf("trust.penalties: %+v %v", res.Data, err)
	}
	// A lift with a reason reverses it.
	if _, err = s.LiftTrustPenalty(testContext, id, "", "op"); err == nil {
		t.Fatal("a lift needs a public reason")
	}
	if _, err = s.LiftTrustPenalty(testContext, id, "the link was a false positive", "op"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LiftTrustPenalty(testContext, id, "again", "op"); err == nil {
		t.Fatal("lifted twice")
	}
	s.now = func() time.Time { return time.Unix(testTime+86400+3600, 0) }
	if _, err = s.RunTrust(testContext); err != nil {
		t.Fatal(err)
	}
	if a := standingOf(t, trustGet(t, s, keyID(alice))); a.StandingCents == 0 {
		t.Fatal("a lifted member still holds 0")
	}
	entries, _, _ = s.TrustPenalties(testContext, 0, 10, "")
	if len(entries) != 2 || entries[0].Trigger != "lift" || entries[1].LiftedBy == "" || entries[1].Active {
		t.Fatalf("the log after a lift: %+v", entries)
	}
}

// Fragment D and schema 38 only create tables and indexes: an earlier
// database without them migrates in place and passes integrity.
func TestTrustSchemaFragmentMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema14.sqlite")
	s, err := Open(path, Config{})
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return time.Unix(testTime, 0) }
	postAs(t, s, keyFor(64), Command{Room: "lobby", Text: "before trust", RequestID: "pre"})
	for _, table := range []string{"trust_dividends", "trust_sponsorships", "trust_penalties", "trust_evidence", "trust_current", "trust_scores", "trust_penalty_log", "trust_run_graphs", "trust_run_snapshots", "trust_runs"} {
		if _, err = s.db.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.db.Exec("PRAGMA user_version=15"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	for i := 0; i < 2; i++ { // the second open finds everything in place
		if s, err = Open(path, Config{}); err != nil {
			t.Fatal(err)
		}
		if got := sqlCount(t, s, "PRAGMA user_version"); got != int64(SchemaVersion) || SchemaVersion < 38 {
			t.Fatalf("user_version %d", got)
		}
		if n := sqlCount(t, s, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name LIKE 'trust_%'"); n != 10 {
			t.Fatalf("%d trust tables", n)
		}
		if err = s.Integrity(testContext); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
