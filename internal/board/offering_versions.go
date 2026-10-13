package board

// Offering versions (C255). An offering's rev counts every signed publish;
// its version counts only the publishes that made it a different thing to
// review: a new title, or an input a caller of the old one could no longer
// send (a different set of required property names, or a property both
// schemas name with a different type). A description, price, wallet or
// timing edit keeps the version. Each review keeps the version of its
// subject when it was written (reviews.version); the subject carries the
// offering's current version (subjects.version), so a review of an earlier
// version is still listed, labelled "about an earlier version", and left
// out of the subject's counts, aggregate and summaries. Every other subject
// stays at version 1.

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"

	"swarmmemo/internal/reviews"
)

// EarlierVersionLabel is the label a review of an earlier offering version
// carries on every surface.
const EarlierVersionLabel = "about an earlier version"

// aboutCurrentVersion holds for a review r about its subject's current
// version.
const aboutCurrentVersion = "r.version=(SELECT sv.version FROM subjects sv WHERE sv.id=r.subject)"

// versionColumns34 are schema 34's columns, each added after its table's
// existing ones, so an upgraded database matches a new one.
var versionColumns34 = []struct{ table, name, decl string }{
	{"offerings", "version", "INTEGER NOT NULL DEFAULT 1 CHECK(version > 0)"},
	{"subjects", "version", "INTEGER NOT NULL DEFAULT 1 CHECK(version > 0)"},
	{"reviews", "version", "INTEGER NOT NULL DEFAULT 1 CHECK(version > 0)"},
}

// migrateOfferingVersions34 adds offerings.version, subjects.version and
// reviews.version (schema 34), keyed on the columns themselves. Every
// existing row starts at version 1; backfillOfferingVersions then replays
// each offering's signed revisions.
func migrateOfferingVersions34(tx *sql.Tx) error {
	for _, c := range versionColumns34 {
		var exists int
		if err := tx.QueryRow("SELECT count(*) FROM pragma_table_info(?) WHERE name=?", c.table, c.name).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := tx.Exec("ALTER TABLE " + c.table + " ADD COLUMN " + c.name + " " + c.decl); err != nil {
				return err
			}
		}
	}
	return nil
}

// offeringChange says whether next is a new version of prev, and why in a
// few words ("" when it is not).
func offeringChange(prev, next offeringListing) string {
	if !strings.EqualFold(strings.Join(strings.Fields(prev.Title), " "), strings.Join(strings.Fields(next.Title), " ")) {
		return "its title changed"
	}
	a, b := inputShape(prev.Input), inputShape(next.Input)
	if !slices.Equal(a.required, b.required) {
		return "its required input changed"
	}
	for name, t := range a.types {
		if u, ok := b.types[name]; ok && u != t {
			return "input " + name + " changed type"
		}
	}
	return ""
}

// shape is what offeringChange compares of an input schema: the sorted
// required names and each property's type.
type shape struct {
	required []string
	types    map[string]string
}

func inputShape(raw json.RawMessage) shape {
	out := shape{types: map[string]string{}}
	if len(raw) == 0 || string(raw) == "null" {
		return out
	}
	sc, err := parseInputSchema(raw)
	if err != nil {
		return out
	}
	out.required = slices.Clone(sc.required)
	slices.Sort(out.required)
	out.required = slices.Compact(out.required)
	for name, p := range sc.props {
		out.types[name] = p.typ
	}
	return out
}

// offeringSubjectIDs are the subject ids an offering is reviewed under:
// x402 and mpp, at its provider's current handle, former handles and
// fingerprint.
func (s *Store) offeringSubjectIDs(ctx context.Context, tx *sql.Tx, account, name string) ([]string, error) {
	_, handle, err := providerOf(ctx, tx, account)
	if err != nil {
		return nil, err
	}
	former, err := formerHandles(ctx, tx, account)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, h := range append([]string{handle, ""}, former...) {
		for _, typ := range []string{reviews.TypeX402, reviews.TypeMPP} {
			if sub, err := s.offeringSubjectAs(typ, account, h, name); err == nil && !slices.Contains(ids, sub.ID) {
				ids = append(ids, sub.ID)
			}
		}
	}
	return ids, nil
}

// setOfferingSubjectsVersion moves the offering's existing subjects to
// version, in tx.
func (s *Store) setOfferingSubjectsVersion(ctx context.Context, tx *sql.Tx, account, name string, version int64) (int, error) {
	ids, err := s.offeringSubjectIDs(ctx, tx, account, name)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	args := []any{version}
	for _, id := range ids {
		args = append(args, id)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE subjects SET version=? WHERE id IN ("+placeholders(len(ids))+")", args...); err != nil {
		return 0, err
	}
	var earlier int
	err = tx.QueryRowContext(ctx, "SELECT count(*) FROM reviews r JOIN events e ON e.id=r.event_id WHERE r.subject IN ("+placeholders(len(ids))+") AND r.current=1 AND e.hidden=0 AND r.relationship NOT IN ('lab','outside_source') AND r.version<?",
		append(args[1:], version)...).Scan(&earlier)
	return earlier, err
}

// subjectVersion is the version a review of sub is about now: an
// offering's current version, 1 for every other subject.
func (s *Store) subjectVersion(ctx context.Context, tx *sql.Tx, sub reviews.Subject) (int64, error) {
	account, _, name, ok, err := offeringOwner(ctx, tx, sub, s.subjectOptions())
	if err != nil || !ok {
		return 1, err
	}
	var v int64
	if err = tx.QueryRowContext(ctx, "SELECT version FROM offerings WHERE account=? AND name=?", account, name).Scan(&v); err != nil {
		return 1, err
	}
	return v, nil
}

// backfillOfferingVersions runs once, in the schema-34 migration: it
// replays each offering's signed revisions with offeringChange, sets the
// offering's version, and moves each of its subjects to that version with
// every review to the version current when it was written. A listing
// replaced wholesale before schema 34 has its earlier reviews marked as
// about an earlier version, as if the rule had applied all along.
func backfillOfferingVersions(ctx context.Context, tx *sql.Tx, opt reviews.Options) error {
	type start struct{ version, at int64 }
	type key struct{ account, name string }
	starts := map[key][]start{}
	rows, err := tx.QueryContext(ctx, "SELECT account,name,listing,created_at FROM offering_revisions ORDER BY account,name,rev")
	if err != nil {
		return err
	}
	prev := map[key]offeringListing{}
	for rows.Next() {
		var k key
		var raw string
		var at int64
		if err = rows.Scan(&k.account, &k.name, &raw, &at); err != nil {
			rows.Close()
			return err
		}
		var l offeringListing
		_ = json.Unmarshal([]byte(raw), &l)
		list := starts[k]
		if p, seen := prev[k]; !seen {
			list = append(list, start{1, at})
		} else if offeringChange(p, l) != "" {
			list = append(list, start{list[len(list)-1].version + 1, at})
		}
		starts[k], prev[k] = list, l
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for k, list := range starts {
		if v := list[len(list)-1].version; v > 1 {
			if _, err = tx.ExecContext(ctx, "UPDATE offerings SET version=? WHERE account=? AND name=?", v, k.account, k.name); err != nil {
				return err
			}
		}
	}
	rows, err = tx.QueryContext(ctx, "SELECT id,type,key FROM subjects WHERE type IN (?,?)", reviews.TypeX402, reviews.TypeMPP)
	if err != nil {
		return err
	}
	var subs []reviews.Subject
	for rows.Next() {
		var sub reviews.Subject
		if err = rows.Scan(&sub.ID, &sub.Type, &sub.Key); err != nil {
			rows.Close()
			return err
		}
		subs = append(subs, sub)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, sub := range subs {
		account, _, name, ok, err := offeringOwner(ctx, tx, sub, opt)
		if err != nil {
			return err
		}
		list := starts[key{account, name}]
		if !ok || len(list) < 2 {
			continue
		}
		if _, err = tx.ExecContext(ctx, "UPDATE subjects SET version=? WHERE id=?", list[len(list)-1].version, sub.ID); err != nil {
			return err
		}
		for _, st := range list[1:] {
			if _, err = tx.ExecContext(ctx, "UPDATE reviews SET version=? WHERE subject=? AND created_at>=?", st.version, sub.ID, st.at); err != nil {
				return err
			}
		}
	}
	return nil
}
