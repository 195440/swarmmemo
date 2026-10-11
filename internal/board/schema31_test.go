package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Schema 31 (inbox_entries.read_at and the unread index; inbox_unread.go,
// C199): a populated schema-29 or 30 database (testdata/schemaN.sql) migrates to
// exactly the schema a new database gets, keeps every row, marks every entry
// already written read when it was created, and its second start runs no DDL.
func TestSchema31Upgrade(t *testing.T) {
	for _, from := range []int{29, 30} {
		t.Run(fmt.Sprint(from), func(t *testing.T) { schema31UpgradeFrom(t, from) })
	}
}

func schema31UpgradeFrom(t *testing.T, from int) {
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
	if _, err = db.Exec(`INSERT INTO inbox_entries(id,account,kind,reasons,subject,event_seq,created_at) VALUES('schema31-entry','acct','reply','reply','msg',1,12345)`); err != nil {
		t.Fatal(err)
	}
	before := tableCounts(t, db)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	openClose(t, path, Config{})
	db = rawDB(t, path)
	if v := pragmaInt(t, db, "user_version"); v != SchemaVersion || SchemaVersion < 31 {
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
	var unread, readAt int64
	if err = db.QueryRow("SELECT (SELECT count(*) FROM inbox_entries WHERE read_at=0 AND created_at>0),(SELECT read_at FROM inbox_entries WHERE id='schema31-entry')").Scan(&unread, &readAt); err != nil || unread != 0 || readAt != 12345 {
		t.Fatalf("entries after the upgrade: %d unread, read_at %d (%v); want every entry read when it was created", unread, readAt, err)
	}
	for _, object := range []string{"inbox_unread"} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name=?", object).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s after the upgrade: %d (%v)", object, n, err)
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
