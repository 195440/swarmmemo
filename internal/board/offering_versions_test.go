package board

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/services/servicestest"
)

// C255: a seller replaced a product wholesale under the same slug (seen on
// prod: json-schema-validate became a CSV converter), so the old product's
// reviews described a different one. A new title or an incompatible input
// starts a new version of the offering; reviews of an earlier version stay
// listed, labelled, and out of every count and score.

func TestOfferingChangeRule(t *testing.T) {
	base := offeringListing{Title: "Validate JSON", Input: json.RawMessage(`{"type":"object","properties":{"doc":{"type":"string"},"strict":{"type":"boolean"}},"required":["doc"]}`)}
	for _, c := range []struct {
		name  string
		next  offeringListing
		want  bool
		cause string
	}{
		{"same", base, false, ""},
		{"title case and spacing", offeringListing{Title: " validate   json ", Input: base.Input}, false, ""},
		{"description and price", offeringListing{Title: base.Title, Description: "new words", Price: "9.00", Input: base.Input}, false, ""},
		{"optional property added", offeringListing{Title: base.Title, Input: json.RawMessage(`{"type":"object","properties":{"doc":{"type":"string","maxLength":9},"strict":{"type":"boolean"},"pretty":{"type":"boolean"}},"required":["doc"]}`)}, false, ""},
		{"new title", offeringListing{Title: "Convert CSV", Input: base.Input}, true, "its title changed"},
		{"required set", offeringListing{Title: base.Title, Input: json.RawMessage(`{"type":"object","properties":{"csv":{"type":"string"}},"required":["csv"]}`)}, true, "its required input changed"},
		{"input dropped", offeringListing{Title: base.Title}, true, "its required input changed"},
		{"property type", offeringListing{Title: base.Title, Input: json.RawMessage(`{"type":"object","properties":{"doc":{"type":"string"},"strict":{"type":"integer"}},"required":["doc"]}`)}, true, "input strict changed type"},
	} {
		got := offeringChange(base, c.next)
		if (got != "") != c.want || (c.cause != "" && got != c.cause) {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}

func TestOfferingVersionKeepsEarlierReviewsApart(t *testing.T) {
	s := surfacesStore(t)
	key, first, second := keyFor(201), keyFor(202), keyFor(203)
	provider(t, s, key, "tuancodes")
	register(t, s, first)
	register(t, s, second)
	publish(t, s, key, listing("json-schema-validate", map[string]any{"title": "Validate JSON against a schema"}))
	const uri = "x402:POST https://swarmmemo.com/@tuancodes/json-schema-validate"
	old := run(t, s, reviewPost(t, first, uri, map[string]any{"verdict": "good"}, "")).Receipt.ID

	// A description and price edit keeps the version and the count.
	res := run(t, s, signed(key, Command{Operation: "offering.publish", Data: listing("json-schema-validate", map[string]any{"title": "Validate JSON against a schema", "description": "Faster now.", "price": "6.00"})}))
	if res.Data["new_version"] != nil || res.Data["offering"].(map[string]any)["version"] != int64(1) {
		t.Fatalf("an edit started a version: %+v", res.Data)
	}
	if agg := readSubjectData(t, s, uri)["aggregate"].(ReviewAggregate); agg.Reviews != 1 || agg.EarlierVersion != 0 {
		t.Fatalf("after an edit %+v", agg)
	}

	// The wholesale replacement starts version 2 and says so.
	res = run(t, s, signed(key, Command{Operation: "offering.publish", Data: listing("json-schema-validate", map[string]any{"title": "Convert CSV to JSON",
		"input": map[string]any{"type": "object", "properties": map[string]any{"csv": map[string]any{"type": "string"}}, "required": []string{"csv"}}})}))
	nv, _ := res.Data["new_version"].(map[string]any)
	if nv == nil || nv["version"] != int64(2) || nv["earlier_reviews"] != 1 || !strings.Contains(nv["note"].(string), EarlierVersionLabel) || res.Data["offering"].(map[string]any)["version"] != int64(2) {
		t.Fatalf("publish answer %+v", res.Data)
	}
	d := readSubjectData(t, s, uri)
	if info := d["subject"].(SubjectInfo); info.Version != 2 {
		t.Fatalf("subject version %d", info.Version)
	}
	agg := d["aggregate"].(ReviewAggregate)
	if agg.Reviews != 0 || agg.Accounts != 0 || len(agg.ByVerdict) != 0 || agg.EarlierVersion != 1 {
		t.Fatalf("aggregate counts an earlier version %+v", agg)
	}
	if cards := d["reviews"].([]ReviewCard); len(cards) != 0 {
		t.Fatalf("community reviews %+v", cards)
	}
	earlier, _ := d["earlier_version_reviews"].([]ReviewCard)
	if len(earlier) != 1 || earlier[0].ID != old || !earlier[0].EarlierVersion || earlier[0].VersionNote != EarlierVersionLabel || earlier[0].Version != 1 {
		t.Fatalf("earlier version reviews %+v", earlier)
	}
	// The post's own review part carries the label too.
	if m := run(t, s, Command{Operation: "message.get", MessageID: old}).Messages[0]; m.Review == nil || !m.Review.EarlierVersion || m.Review.VersionNote != EarlierVersionLabel {
		t.Fatalf("the review's post is not labelled as about an earlier version: %+v", m.Review)
	}

	// A review now is about version 2 and counts; the old one does not.
	fresh := run(t, s, reviewPost(t, second, uri, map[string]any{"verdict": "bad"}, "")).Receipt.ID
	d = readSubjectData(t, s, uri)
	agg = d["aggregate"].(ReviewAggregate)
	if agg.Reviews != 1 || agg.ByVerdict["bad"] != 1 || agg.ByVerdict["good"] != 0 || agg.EarlierVersion != 1 {
		t.Fatalf("aggregate %+v", agg)
	}
	if cards := d["reviews"].([]ReviewCard); len(cards) != 1 || cards[0].ID != fresh || cards[0].EarlierVersion || cards[0].Version != 2 {
		t.Fatalf("community reviews %+v", cards)
	}
	// The compact summaries agree: offering.get, subject.resolve, subjects.list.
	sum := run(t, s, Command{Operation: "offering.get", Target: "tuancodes/json-schema-validate"}).Data["offering"].(map[string]any)["reviews"].(*SubjectReviews)
	if sum.Reviews != 1 || sum.Accounts != 1 || sum.ByVerdict["good"] != 0 || sum.EarlierVersion != 1 {
		t.Fatalf("offering summary %+v", sum.ReviewSummary)
	}
	if rs := run(t, s, Command{Operation: "subject.resolve", Target: uri}).Data["reviews"].(ReviewSummary); rs.Reviews != 1 || rs.EarlierVersion != 1 {
		t.Fatalf("resolve summary %+v", rs)
	}
	for _, l := range run(t, s, Command{Operation: "subjects.list"}).Data["subjects"].([]SubjectListing) {
		if l.URI == uri && (l.Reviews != 1 || l.Accounts != 1) {
			t.Fatalf("subjects.list %+v", l)
		}
	}
	// A property's type change is a version too; nothing is deleted.
	res = run(t, s, signed(key, Command{Operation: "offering.publish", Data: listing("json-schema-validate", map[string]any{"title": "Convert CSV to JSON",
		"input": map[string]any{"type": "object", "properties": map[string]any{"csv": map[string]any{"type": "integer"}}, "required": []string{"csv"}}})}))
	if nv, _ := res.Data["new_version"].(map[string]any); nv == nil || nv["version"] != int64(3) || nv["earlier_reviews"] != 2 {
		t.Fatalf("type change %+v", res.Data["new_version"])
	}
	var rows int
	if err := s.db.QueryRow("SELECT count(*) FROM reviews").Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("reviews kept: %d %v", rows, err)
	}
}

// Schema 34 on an existing database: a schema-33 file whose offering was
// already replaced wholesale gets its versions from its signed revisions,
// each review the version current when it was written; the populated
// schema-33 testdata migrates to exactly a new database's schema.
func TestSchema34Upgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "board.sqlite")
	c := updatesConfig()
	c.Features = Features{Reviews: true, InboxEntries: InboxRead}
	c.Offerings = topupConfig(t, &fakeFacilitator{}, "")
	c.EchoSimulate = true
	open := func() *Store {
		s, err := Open(path, c)
		if err != nil {
			t.Fatal(err)
		}
		s.UseServiceMeter(servicestest.NewMeter(1<<30), &servicestest.Params{})
		return s
	}
	s := open()
	clock := int64(testTime)
	s.now = func() time.Time { return time.Unix(clock, 0) }
	key, first, second := keyFor(204), keyFor(205), keyFor(206)
	provider(t, s, key, "tuancodes")
	register(t, s, first)
	register(t, s, second)
	publish(t, s, key, listing("json-schema-validate", map[string]any{"title": "Validate JSON"}))
	const uri = "x402:POST https://swarmmemo.com/@tuancodes/json-schema-validate"
	clock += 100
	old := run(t, s, reviewPost(t, first, uri, map[string]any{}, "")).Receipt.ID
	clock += 100
	publish(t, s, key, listing("json-schema-validate", map[string]any{"title": "Convert CSV"}))
	clock += 100
	fresh := run(t, s, reviewPost(t, second, uri, map[string]any{"verdict": "mixed"}, "")).Receipt.ID
	s.stopServices()
	s.stopOfferings()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Back to schema 33: no version columns at all.
	db := rawDB(t, path)
	for _, col := range versionColumns34 {
		if _, err := db.Exec("ALTER TABLE " + col.table + " DROP COLUMN " + col.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA user_version=33"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s = open()
	t.Cleanup(func() { s.stopServices(); s.stopOfferings(); _ = s.Close() })
	versions := map[string]int{}
	rows, err := s.db.Query("SELECT event_id,version FROM reviews")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var v int
		if err = rows.Scan(&id, &v); err != nil {
			t.Fatal(err)
		}
		versions[id] = v
	}
	rows.Close()
	var offering, subject int
	if err = s.db.QueryRow("SELECT (SELECT version FROM offerings WHERE name='json-schema-validate'),(SELECT version FROM subjects WHERE id=?)", reviewSubjectOf(t, uri).ID).Scan(&offering, &subject); err != nil {
		t.Fatal(err)
	}
	if offering != 2 || subject != 2 || versions[old] != 1 || versions[fresh] != 2 {
		t.Fatalf("after the upgrade: offering %d, subject %d, reviews %v", offering, subject, versions)
	}
	if agg := readSubjectData(t, s, uri)["aggregate"].(ReviewAggregate); agg.Reviews != 1 || agg.EarlierVersion != 1 || agg.ByVerdict["mixed"] != 1 {
		t.Fatalf("aggregate after the upgrade %+v", agg)
	}

	// The populated schema-33 testdata: the same schema as a new database,
	// every row kept, every existing row at version 1, and no DDL on the
	// second start.
	ddl, err := os.ReadFile(filepath.Join("testdata", "schema33.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "schema33.db")
	db = rawDB(t, path)
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
	if _, err = db.Exec("PRAGMA user_version=33"); err != nil {
		t.Fatal(err)
	}
	seedEveryTable(t, db)
	before := tableCounts(t, db)
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	openClose(t, path, Config{})
	db = rawDB(t, path)
	if v := pragmaInt(t, db, "user_version"); v != SchemaVersion || SchemaVersion < 34 {
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
	for _, col := range versionColumns34 {
		var n, one int
		if err = db.QueryRow(fmt.Sprintf("SELECT count(*),count(*) FILTER (WHERE version=1) FROM %s", col.table)).Scan(&n, &one); err != nil || n == 0 || n != one {
			t.Fatalf("%s: %d rows, %d at version 1 (%v)", col.table, n, one, err)
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
