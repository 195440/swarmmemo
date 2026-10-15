package board

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pinnedDB builds a populated database at schema n from its pin
// (testdata/schemaN.sql, what the release that shipped n created): every
// table, then every index and trigger, user_version n, one row per table.
func pinnedDB(t *testing.T, n int) string {
	t.Helper()
	ddl, err := os.ReadFile(filepath.Join("testdata", fmt.Sprintf("schema%d.sql", n)))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), fmt.Sprintf("schema%d.db", n))
	db := rawDB(t, path)
	// The pin is ordered by type and name (indexes first): create the tables,
	// then everything that names them.
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
	if _, err = db.Exec(fmt.Sprintf("PRAGMA user_version=%d", n)); err != nil {
		t.Fatal(err)
	}
	seedEveryTable(t, db)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// A populated database at every pinned schema from 16 (the versioned schema)
// up to SchemaVersion-1 migrates in one open to exactly the schema a new
// database gets, keeps every row, leaves the tables it adds empty, and its
// second start runs no DDL. This is what a rollout that jumps several schema
// versions at once relies on (make-rollout.py; e.g. production 37 -> 40):
// migrateSchema is one keyed, idempotent sequence, so N -> SchemaVersion is
// the same code path for every N, and each N is proven here.
func TestSchemaUpgradeFromEveryPin(t *testing.T) {
	if SchemaVersion <= 16 {
		t.Fatalf("SchemaVersion %d: no pinned predecessor", SchemaVersion)
	}
	for n := 16; n < SchemaVersion; n++ {
		t.Run(fmt.Sprintf("%d->%d", n, SchemaVersion), func(t *testing.T) {
			path := pinnedDB(t, n)
			db := rawDB(t, path)
			before := tableCounts(t, db)
			_, fkBefore := integrity(t, db)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			openClose(t, path, Config{})
			db = rawDB(t, path)
			if v := pragmaInt(t, db, "user_version"); v != SchemaVersion {
				t.Fatalf("user_version %d after migration, want %d", v, SchemaVersion)
			}
			if ok, fk := integrity(t, db); ok != "ok" || fk != fkBefore {
				t.Fatalf("integrity %q, foreign key violations %d (before %d)", ok, fk, fkBefore)
			}
			if got, fresh := normalizedSchema(t, db), freshSchema(t, Config{}); got != fresh {
				t.Fatalf("upgraded schema differs from a new database's:\n%s", schemaDiff(got, fresh))
			}
			after := tableCounts(t, db)
			for table, c := range before {
				if after[table] < c {
					t.Errorf("%s: %d rows before, %d after: the migration removed rows", table, c, after[table])
				}
			}
			for table, c := range after {
				if _, old := before[table]; !old && c != 0 && !strings.HasPrefix(table, "tlog_") {
					t.Errorf("new table %s has %d rows", table, c)
				}
			}
			cookie := pragmaInt(t, db, "schema_version")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			openClose(t, path, Config{})
			if got := pragmaInt(t, rawDB(t, path), "schema_version"); got != cookie {
				t.Fatalf("schema cookie %d -> %d: the second start changed the schema", cookie, got)
			}
		})
	}
}
