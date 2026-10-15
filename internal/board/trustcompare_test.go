package board

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/allowance"
)

// trust compare RUN_OLD RUN_NEW (C320c): an old run (version 6: tiers, every
// signed account's weight 1) against a new one; per account the band, the
// standing, the vote weight (floored for a seasoned account) and the share
// per resource against what the waterfall gave it, the transition floor
// keeping it; then the counts.
func TestTrustCompare(t *testing.T) {
	s := openTest(t, Config{Features: Features{Trust: TrustOn, Ledger: LedgerOn, Services: []string{"memory"}}})
	weaver, member, fresh := keyFor(91), keyFor(92), keyFor(93)
	s.now = func() time.Time { return time.Unix(testTime-3*86400, 0) }
	postAs(t, s, weaver, Command{Room: "lobby", Text: "bounties", RequestID: "c-w"})
	postAs(t, s, member, Command{Room: "lobby", Text: "hello", RequestID: "c-m"})
	s.now = func() time.Time { return time.Unix(testTime, 0) }
	postAs(t, s, fresh, Command{Room: "lobby", Text: "new", RequestID: "c-f"})
	now := s.now().Unix()
	day := now / 86400

	// The old run: version 6, tier 1 for weaver, 3 for the others, weight 1.
	res, err := s.db.Exec("INSERT INTO trust_runs(as_of,params_version,state,inputs,started_at,finished_at) VALUES(?,6,'done','{}',?,?)", now-86400, now, now)
	if err != nil {
		t.Fatal(err)
	}
	oldRun, _ := res.LastInsertId()
	for k, tier := range map[string]int64{keyID(weaver): 1, keyID(member): 3} {
		if _, err = s.db.Exec("INSERT INTO trust_scores(run_id,account,root,proof_collateral,flow_a,flow_b,flow,collateral,tier,weight_ppm,parts) VALUES(?,?,?,0,0,0,0,0,?,1000000,'{}')", oldRun, k, k, tier); err != nil {
			t.Fatal(err)
		}
	}
	// What the waterfall gave them yesterday (a day with its tier-4 row).
	for tier := 0; tier <= 4; tier++ {
		if _, err = s.db.Exec("INSERT INTO allowance_pools(resource,day,tier,size,want,expected_units,expected_weight) VALUES('post_bytes',?,?,0,0,0,0)", day-1, tier); err != nil {
			t.Fatal(err)
		}
	}
	for k, ent := range map[string]int64{keyID(weaver): 16 << 20, keyID(member): 512 << 10} {
		if _, err = s.db.Exec("INSERT INTO allowance_claims(resource,day,subject,tier,weight,root,entitlement,granted,source,lot_id,created_at) VALUES('post_bytes',?,?,1,1000000,?,?,0,'tier',0,?)", day-1, k, k, ent, now-86400); err != nil {
			t.Fatal(err)
		}
	}
	newRun := setStanding(t, s, map[string]int64{keyID(weaver): 750, keyID(member): 60, keyID(fresh): 0})

	c, err := s.TrustCompare(testContext, oldRun, newRun)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]TrustCompareRow{}
	for _, r := range c.Rows {
		rows[r.Account] = r
	}
	w, m, f := rows[keyID(weaver)], rows[keyID(member)], rows[keyID(fresh)]
	if w.Old.Band != "tier 1" || w.New.Band != "trusted" || w.New.Cents != 750 || w.New.VotePPM != 1e6 || !w.Seasoned {
		t.Fatalf("weaver %+v", w)
	}
	// The member: seasoned, so its vote stays whole at 60¢ of standing.
	if m.New.VotePPM != 1e6 || m.Old.VotePPM != 1e6 || !m.Seasoned {
		t.Fatalf("member %+v", m)
	}
	// A fresh key: not seasoned, no standing, no vote weight; it had none.
	if f.Seasoned || f.New.VotePPM != 0 || f.Old.Band != "none" {
		t.Fatalf("fresh %+v", f)
	}
	post := w.Shares[allowance.PostBytes]
	if post.Old != 16<<20 || post.New < post.Old {
		t.Fatalf("weaver's posting share %+v: below the waterfall's", post)
	}
	if ms := m.Shares[allowance.PostBytes]; ms.New < ms.Old || ms.New != ms.Rule {
		t.Fatalf("member's share %+v: the share rule's own floor is above its 512 KiB", ms)
	}
	if c.VoteFell != 0 || c.ShareFell[allowance.PostBytes] != 0 || c.OldStanding != 0 || c.NewStanding != 2 {
		t.Fatalf("counts %+v", c)
	}
	var out bytes.Buffer
	if err = WriteTrustComparison(&out, c); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"trust compare: run", "(version 6)", "tier 1 -> trusted", "0 -> 750", "1.00 -> 1.00", "16384 -> 16384",
		"accounts with standing: 0 -> 2; vote weight fell: 0; share below the waterfall's: post_bytes 0, memory_bytes 0, credit 0"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	t.Logf("\n%s", text)
	if _, err = s.TrustCompare(testContext, 999, newRun); errCode(err) != "not_found" {
		t.Fatalf("an unknown run: %v", err)
	}
}
