package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Schema 29 (offering_calls' answer columns and indexes; offering_answers.go,
// C173): a populated schema-26, 27 or 28 database
// (testdata/schemaN.sql) migrates to exactly the schema a new database
// gets, keeps every row, has the offering indexes, and its second start
// runs no DDL.
func TestSchema29Upgrade(t *testing.T) {
	for _, from := range []int{26, 27, 28} {
		t.Run(fmt.Sprint(from), func(t *testing.T) { schema29UpgradeFrom(t, from) })
	}
}

func schema29UpgradeFrom(t *testing.T, from int) {
	ddl, err := os.ReadFile(filepath.Join("testdata", fmt.Sprintf("schema%d.sql", from)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), fmt.Sprintf("schema%d.db", from))
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
	if _, err = db.Exec(fmt.Sprintf("PRAGMA user_version=%d", from)); err != nil {
		t.Fatal(err)
	}
	seedEveryTable(t, db)
	before := tableCounts(t, db)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	openClose(t, path, Config{})
	db = rawDB(t, path)
	if v := pragmaInt(t, db, "user_version"); v != SchemaVersion || SchemaVersion < 29 {
		t.Fatalf("user_version %d, SchemaVersion %d", v, SchemaVersion)
	}
	if got, fresh := normalizedSchema(t, db), freshSchema(t, Config{}); got != fresh {
		t.Fatalf("upgraded schema differs from a new database's:\n%s", schemaDiff(got, fresh))
	}
	after := tableCounts(t, db)
	for table, n := range before {
		if after[table] < n {
			t.Errorf("%s: %d rows before, %d after", table, n, after[table])
		}
	}
	for _, index := range []string{"offering_calls_tx", "offering_calls_open", "offering_calls_payer", "offering_calls_provider", "offerings_listed", "offering_revisions_day", "offering_calls_refund", "offering_calls_caller", "offering_calls_due", "offering_calls_lapsed"} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index %s after the upgrade: %d (%v)", index, n, err)
		}
	}
	cookie := pragmaInt(t, db, "schema_version")
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	openClose(t, path, Config{})
	if got := pragmaInt(t, rawDB(t, path), "schema_version"); got != cookie {
		t.Fatalf("schema cookie %d -> %d: the second start changed the schema", cookie, got)
	}
}

// The offering reads and sweeps that ran unindexed at schema 28 seek the
// schema-29 indexes: data.offering_answers by caller, the overdue sweep by
// due time, the lapsed closure by closing time, a refund's one-use check.
func TestSchema29OfferingPlans(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.db")
	openClose(t, path, Config{})
	db := rawDB(t, path)
	for query, index := range map[string]string{
		`SELECT c.id FROM offering_calls c INDEXED BY offering_calls_caller WHERE c.caller IN (SELECT id FROM identities WHERE account='a') AND c.caller<>'' AND c.answered_at>1 AND c.state='answered' ORDER BY c.answered_at DESC LIMIT 20`: "offering_calls_caller",
		`SELECT id FROM offering_calls WHERE state='paid' AND due_at>0 AND due_at<=5 ORDER BY due_at LIMIT 500`:                                                                                                                               "offering_calls_due",
		`SELECT id FROM offering_calls WHERE state='lapsed' AND closed_at>5-10`:                                                                                                                                                               "offering_calls_lapsed",
		`SELECT id FROM offering_calls WHERE ` + openCallsTerm + ` AND state='authorized' AND claim_by<=5 ORDER BY claim_by LIMIT 500`:                                                                                                        "offering_calls_open",
		`SELECT count(*) FROM offering_calls WHERE refund_tx='0x1' AND refund_tx<>''`:                                                                                                                                                         "offering_calls_refund",
		`SELECT count(*) FROM offering_calls WHERE tx_hash='0x1' AND tx_hash<>''`:                                                                                                                                                             "offering_calls_tx",
		`SELECT id FROM offering_calls WHERE ` + openCallsTerm + ` AND state='verifying' AND created_at<=5 ORDER BY created_at LIMIT 500`:                                                                                                     "offering_calls_open",
	} {
		rows, err := db.Query("EXPLAIN QUERY PLAN " + query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail + "\n")
		}
		rows.Close()
		if !strings.Contains(plan.String(), "INDEX "+index) || strings.Contains(plan.String(), "SCAN c\n") || strings.Contains(plan.String(), "SCAN offering_calls\n") {
			t.Errorf("%s:\n%s(want %s)", query, plan.String(), index)
		}
	}
}

// A schema-28 answer (one JSON envelope in answer) is split into the
// schema-29 columns once, the body staying in answer; a row that is not an
// envelope is left as it was; and the call reads as it did.
func TestSchema29AnswerEnvelope(t *testing.T) {
	ddl, err := os.ReadFile(filepath.Join("testdata", "schema28.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "schema28.db")
	db := rawDB(t, path)
	_, body, _ := strings.Cut(string(ddl), "do not edit.\n")
	blocks := strings.Split(strings.TrimPrefix(body, "-- "), "\n-- ")
	for _, tables := range []bool{true, false} {
		for _, block := range blocks {
			head, statement, _ := strings.Cut(block, "\n")
			if strings.HasPrefix(head, "table ") == tables {
				if _, err = db.Exec(statement); err != nil {
					t.Fatalf("%s: %v", head, err)
				}
			}
		}
	}
	envelope := `{"body":"{\"t\":70}","content_type":"application/json","screen":{"state":"flag","scores":{"injection":0.9}},"statement":"{\"schema\":\"swarmmemo-offering-answer/1\"}","stamp":"` + strings.Repeat("ab", 32) + `","late":true}`
	insert := `INSERT INTO offering_calls(id,account,name,rev,caller,payer,amount,network,asset,pay_to,input,input_sha256,auth_nonce,quote_hash,valid_before,state,poll_hash,answer,answer_sha256,created_at,claim_by,answered_at)
 VALUES(?,'acct','weather',1,'','0xpayer',1000,'base','0xasset','0xpayto','{}','in',?,'q',0,?,'poll',?,?,1,2,3)`
	for _, row := range [][]any{{"c1", "n1", "answered", envelope, "sum"}, {"c2", "n2", "paid", "", ""}, {"c3", "n3", "answered", "not json", "sum3"}} {
		if _, err = db.Exec(insert, row...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec("PRAGMA user_version=28"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	openClose(t, path, Config{})
	db = rawDB(t, path)
	call, err := scanCall(db.QueryRow("SELECT " + callCols + " FROM offering_calls WHERE id='c1'").Scan)
	if err != nil {
		t.Fatal(err)
	}
	if call.Answer != `{"t":70}` || call.AnswerContentType != "application/json" || call.AnswerScreen != "flag" || call.AnswerScores != `{"injection":0.9}` ||
		call.AnswerStatement != `{"schema":"swarmmemo-offering-answer/1"}` || call.AnswerStamp != strings.Repeat("ab", 32) || !call.AnswerLate {
		t.Fatalf("migrated answer %+v", call)
	}
	v := call.answerView()
	if v["body"] != `{"t":70}` || v["late"] != true || v["screen"].(map[string]any)["scores"].(map[string]float64)["injection"] != 0.9 || v["stamp"] == nil {
		t.Fatalf("migrated answer view %v", v)
	}
	var paid, odd string
	if err = db.QueryRow("SELECT answer||'|'||answer_content_type||'|'||answer_screen FROM offering_calls WHERE id='c2'").Scan(&paid); err != nil || paid != "||" {
		t.Fatalf("unanswered call %q (%v)", paid, err)
	}
	if err = db.QueryRow("SELECT answer||'|'||answer_content_type FROM offering_calls WHERE id='c3'").Scan(&odd); err != nil || odd != "not json|" {
		t.Fatalf("non-envelope answer %q (%v)", odd, err)
	}
	// A row still in the schema-28 shape reads as it did.
	legacy := callRow{Answer: envelope, AnswerSHA256: "sum"}
	if v = legacy.answerView(); v["body"] != `{"t":70}` || v["content_type"] != "application/json" || v["late"] != true {
		t.Fatalf("legacy answer view %v", v)
	}
}
