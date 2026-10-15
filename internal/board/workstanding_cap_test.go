package board

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"swarmmemo/internal/allowance"
)

// C346: a standing task's results_max caps the results it takes over its
// life (accepted and waiting; rejected and lapsed ones free their place).
// Full, it reads standing.open 0 and standing.full true and refuses a claim
// with results_max_reached; work.cancel on it closes it early.
func TestWorkStandingResultsMax(t *testing.T) {
	s := rewardStore(t)
	owner, w1, w2, w3, w4, w5 := keyFor(230), keyFor(231), keyFor(232), keyFor(233), keyFor(234), keyFor(235)
	mintCredit(t, s, keyID(owner), allowance.Paid, 5000)
	id := rewardRequest(t, s, owner, "lobby")
	ttl := int64(10 * 86400)

	for _, bad := range []map[string]any{
		{"seats": 2, "reward": 300, "results_max": 1},
		{"seats": 2, "reward": 300, "results_max": WorkStandingResultsMax + 1},
		{"seats": 2, "reward": 300, "results_max": "3"},
		{"reward": 300, "results_max": 3},
	} {
		fails(t, s, standingCreate(s, owner, id, bad, ttl), "invalid_work_data")
	}
	run(t, s, standingCreate(s, owner, id, map[string]any{"seats": 2, "reward": 300, "day_max": 10, "results_max": 3}, ttl))
	if st := standingAt(t, s, id); st.ResultsMax != 3 || st.Full || st.Open != 2 {
		t.Fatalf("new capped task: %+v", st)
	}

	r1, c1 := standingClaim(t, s, w1, id, nil)
	if ack := run(t, s, c1).Data["ack"].(WorkAck); !strings.Contains(ack.Note, "result 1 of the 3 it takes in all") {
		t.Fatalf("first result's note: %q", ack.Note)
	}
	r2, c2 := standingClaim(t, s, w2, id, nil)
	run(t, s, c2)
	run(t, s, workCommand(s, owner, Command{Operation: "work.accept", MessageID: r1, Amount: 1}))
	// One accepted, one waiting: one place left, and one seat.
	if st := standingAt(t, s, id); st.Full || st.Open != 1 || st.Paid != 1 || st.Submitted != 1 {
		t.Fatalf("one place left: %+v", st)
	}
	r3, c3 := standingClaim(t, s, w3, id, nil)
	run(t, s, c3)
	// Three results: full, though a seat is free once r2 is judged.
	run(t, s, workCommand(s, owner, Command{Operation: "work.accept", MessageID: r3, Amount: 1}))
	st := standingAt(t, s, id)
	if !st.Full || st.Open != 0 || st.Paid != 2 || st.Submitted != 1 {
		t.Fatalf("full: %+v", st)
	}
	_, c4 := standingClaim(t, s, w4, id, nil)
	_, err := s.Execute(testContext, c4, "test-origin")
	var e *Error
	if !errors.As(err, &e) || e.Status != 409 || e.Code != "results_max_reached" || !strings.Contains(e.Message, "3 results in all") || !strings.Contains(e.Message, "2 accepted and 1 waiting") {
		t.Fatalf("claim past the cap: %v", err)
	}
	if n := sqlCount(t, s, "SELECT count(*) FROM works WHERE standing_id=?", id); n != 3 {
		t.Fatalf("a refused claim opened a result: %d", n)
	}

	// works.list carries the same full summary, and the wire says so.
	listed := run(t, s, Command{Operation: "works.list", Data: `{"schema":1,"standing":"` + id + `"}`})
	summary, _ := listed.Data["standing"].(*WorkStanding)
	raw, _ := json.Marshal(summary)
	if summary == nil || !strings.Contains(string(raw), `"open":0`) || !strings.Contains(string(raw), `"full":true`) || !strings.Contains(string(raw), `"results_max":3`) {
		t.Fatalf("works.list standing: %s", raw)
	}

	// A reject frees a place: the task takes one more result.
	run(t, s, workCommand(s, owner, Command{Operation: "work.reject", MessageID: r2, Amount: 1, Reason: "dup"}))
	if st = standingAt(t, s, id); st.Full || st.Open != 1 {
		t.Fatalf("after a reject: %+v", st)
	}
	r4, c4 := standingClaim(t, s, w4, id, nil)
	run(t, s, c4)
	if st = standingAt(t, s, id); !st.Full || st.Open != 0 {
		t.Fatalf("full again: %+v", st)
	}

	// The requester closes the task early: no claim gets in, the waiting
	// result is still judged and paid.
	run(t, s, workCommand(s, owner, Command{Operation: "work.cancel", MessageID: id, Reason: "closed early"}))
	_, c5 := standingClaim(t, s, w5, id, nil)
	fails(t, s, c5, "work_state_conflict")
	run(t, s, workCommand(s, owner, Command{Operation: "work.accept", MessageID: r4, Amount: 1}))
	if got := creditIn(t, s, keyID(w4), "remaining"); got != 300 {
		t.Fatalf("the waiting result after close paid %d", got)
	}
	if w := getTestWork(t, s, id); w.State != "cancelled" || w.Standing.Open != 0 || w.Standing.Paid != 3 {
		t.Fatalf("closed task: %s %+v", w.State, w.Standing)
	}
	if err := s.Integrity(testContext); err != nil {
		t.Fatal(err)
	}
}

// A standing task without results_max reads as before C346: no results_max
// and no full on the wire, so older clients that check its keys still pass.
func TestWorkStandingNoResultsMaxUnchanged(t *testing.T) {
	s := rewardStore(t)
	owner := keyFor(236)
	mintCredit(t, s, keyID(owner), allowance.Paid, 5000)
	id := rewardRequest(t, s, owner, "lobby")
	run(t, s, standingCreate(s, owner, id, map[string]any{"seats": 1, "reward": 300, "day_max": 10}, 10*86400))
	for i := range 3 {
		r, c := standingClaim(t, s, keyFor(byte(237+i)), id, nil)
		run(t, s, c)
		run(t, s, workCommand(s, owner, Command{Operation: "work.accept", MessageID: r, Amount: 1}))
	}
	st := standingAt(t, s, id)
	raw, _ := json.Marshal(st)
	if st.Paid != 3 || st.Open != 1 || strings.Contains(string(raw), "results_max") || strings.Contains(string(raw), "full") {
		t.Fatalf("uncapped task: %s", raw)
	}
}

// TestSchema41UpgradeFrom40: a schema 40 database gains
// work_standing.results_max, 0 (no cap) for every existing standing task,
// and ends with a new database's schema.
func TestSchema41UpgradeFrom40(t *testing.T) {
	ddl, err := os.ReadFile(filepath.Join("testdata", "schema40.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "schema40.db")
	db := rawDB(t, path)
	_, body, _ := strings.Cut(string(ddl), "do not edit.\n")
	blocks := strings.Split(strings.TrimPrefix(body, "-- "), "\n-- ")
	for _, tables := range []bool{true, false} {
		for _, block := range blocks {
			head, statement, _ := strings.Cut(block, "\n")
			if strings.HasPrefix(head, "table ") != tables {
				continue
			}
			if _, err = db.Exec(statement); err != nil {
				t.Fatalf("%s: %v", head, err)
			}
		}
	}
	if _, err = db.Exec("PRAGMA user_version=40"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO work_standing(work_id,seats,per_agent_day,day_max,tiers,verdict_ttl) VALUES(?,2,1,2,'[{"name":"reward","credits":5}]',259200)`, strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	openClose(t, path, Config{})
	db = rawDB(t, path)
	if v := pragmaInt(t, db, "user_version"); v != SchemaVersion || SchemaVersion < 41 {
		t.Fatalf("user_version %d, SchemaVersion %d", v, SchemaVersion)
	}
	if got, fresh := normalizedSchema(t, db), freshSchema(t, Config{}); got != fresh {
		t.Fatalf("upgraded schema differs from a new database's:\n%s", schemaDiff(got, fresh))
	}
	var capped int64
	if err = db.QueryRow("SELECT results_max FROM work_standing").Scan(&capped); err != nil || capped != 0 {
		t.Fatalf("existing standing task's results_max %d (%v)", capped, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
}
