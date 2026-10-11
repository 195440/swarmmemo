package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Schema 32 (credit_topups and offering_calls protocol and payment_ref;
// mpp.go, C202b): a populated schema-30 or 31 database (testdata/schemaN.sql)
// migrates to exactly the schema a new database gets, keeps every row, reads
// every earlier payment as x402 with no challenge reference, and its second
// start runs no DDL.
func TestSchema32Upgrade(t *testing.T) {
	for _, from := range []int{30, 31} {
		t.Run(fmt.Sprint(from), func(t *testing.T) { schema32UpgradeFrom(t, from) })
	}
}

func schema32UpgradeFrom(t *testing.T, from int) {
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
	if _, err = db.Exec(`INSERT INTO credit_topups(id,account,agent,amount,day,network,asset,pay_to,payer,auth_nonce,valid_before,state,tx_hash,created_at,settled_at)
VALUES('tu_schema32','acct','agent',100000,1,'eip155:8453','0xasset','0xpayto','0xpayer','0x32nonce',99,'credited','0x32tx',1,2)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO offering_calls(id,account,name,rev,caller,payer,amount,network,asset,pay_to,input,input_sha256,auth,auth_key,auth_nonce,quote_hash,valid_before,state,poll_hash,created_at,claim_by)
VALUES('oc_schema32','acct','name',1,'','0xpayer',10000,'eip155:8453','0xasset','0xpayto','{}','sha','','','0x32callnonce','qh',99,'paid','ph',1,2)`); err != nil {
		t.Fatal(err)
	}
	before := tableCounts(t, db)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	openClose(t, path, Config{})
	db = rawDB(t, path)
	if v := pragmaInt(t, db, "user_version"); v != SchemaVersion || SchemaVersion < 32 {
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
	for _, table := range []string{"credit_topups", "offering_calls"} {
		var rows, x402 int
		if err = db.QueryRow("SELECT count(*),count(*) FILTER (WHERE protocol='x402' AND payment_ref='') FROM "+table).Scan(&rows, &x402); err != nil || rows == 0 || rows != x402 {
			t.Fatalf("%s after the upgrade: %d rows, %d read as x402 (%v); want every earlier payment x402", table, rows, x402, err)
		}
	}
	// The column takes only the two wires.
	if _, err = db.Exec("UPDATE credit_topups SET protocol='lightning' WHERE id='tu_schema32'"); err == nil {
		t.Fatal("protocol accepted a third wire")
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
