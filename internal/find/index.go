package find

// The index: an in-memory SQLite FTS5 table built from the sources' docs,
// immutable once built and replaced whole by the next build. It is a cache
// of the sources, never a source: nothing is written to the board's
// database, and losing it costs one rebuild.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // the board's SQLite, which carries FTS5
)

// Column weights for BM25: a word in the name counts most, then the
// category, the description and the other terms (URLs, keywords).
const bm25Weights = "10.0, 5.0, 2.0, 1.0"

// candidatesMax bounds the matches one search ranks.
const candidatesMax = 500

var indexSeq atomic.Int64

// Index is one built index.
type Index struct {
	db      *sql.DB
	docs    []Doc
	vocab   Vocab
	BuiltAt time.Time
	Counts  map[string]int
}

// Build indexes docs. The index lives in its own in-memory database on one
// connection.
func Build(ctx context.Context, docs []Doc, vocab Vocab, at time.Time) (*Index, error) {
	// A named in-memory database per index: each build is its own.
	db, err := sql.Open("sqlite", fmt.Sprintf("file:find-%d?mode=memory&cache=private", indexSeq.Add(1)))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	fail := func(err error) (*Index, error) {
		db.Close()
		return nil, err
	}
	if _, err = db.ExecContext(ctx, `CREATE VIRTUAL TABLE docs USING fts5(name, category, body, terms,
 type UNINDEXED, price UNINDEXED, output UNINDEXED, network UNINDEXED, protocols UNINDEXED,
 tokenize='porter unicode61 remove_diacritics 2')`); err != nil {
		return fail(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO docs(rowid,name,category,body,terms,type,price,output,network,protocols) VALUES(?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		tx.Rollback()
		return fail(err)
	}
	ix := &Index{db: db, vocab: vocab, BuiltAt: at, Counts: map[string]int{}}
	for _, d := range docs {
		if d.Type == "" || d.ID == "" {
			continue
		}
		ix.docs = append(ix.docs, d)
		if _, err = stmt.ExecContext(ctx, len(ix.docs), d.Name, d.Category, d.Description, d.Terms, d.Type, d.Price, d.Output, d.Network, protocolFamilies(d.Protocols)); err != nil {
			stmt.Close()
			tx.Rollback()
			return fail(err)
		}
		ix.Counts[d.Type]++
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	return ix, nil
}

// Close frees the index.
func (ix *Index) Close() error {
	if ix == nil || ix.db == nil {
		return nil
	}
	return ix.db.Close()
}

// Len is the number of docs.
func (ix *Index) Len() int { return len(ix.docs) }

// match is one candidate: the doc's place and its BM25 (lower is better,
// as FTS5 reports it; 0 when browsing without words).
type match struct {
	doc  int
	bm25 float64
}

// query is the FTS5 expression for the words: each word, a prefix of it
// (four letters or more) and the categories it stands for, OR'd, so a doc
// matching more of them ranks higher. Words are quoted: nothing in a query
// is FTS syntax.
func (ix *Index) query(words []string) (string, map[string][]string) {
	var groups []string
	expanded := map[string][]string{}
	for _, w := range words {
		alts := []string{`"` + w + `"`}
		if len([]rune(w)) >= 4 {
			alts = append(alts, `"`+w+`"*`)
		}
		for _, syn := range ix.vocab.synonyms(w) {
			alts = append(alts, `category:"`+syn+`"`)
			expanded[w] = append(expanded[w], syn)
		}
		groups = append(groups, "("+strings.Join(alts, " OR ")+")")
	}
	return strings.Join(groups, " OR "), expanded
}

// search returns the candidates for words under f, best BM25 first; with no
// words, every doc f admits (relevance is then equal).
func (ix *Index) search(ctx context.Context, words []string, f Filters) ([]match, map[string][]string, error) {
	where, args := []string{}, []any{}
	if len(f.Types) > 0 {
		where = append(where, "type IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.Types)), ",")+")")
		for _, t := range f.Types {
			args = append(args, t)
		}
	}
	switch {
	case f.Category != "":
		where, args = append(where, "category = ?"), append(args, f.Category)
	default:
		// Adult listings are found only by asking for that category.
		where = append(where, "category <> 'adult'")
	}
	if p := f.maxMicro(); p >= 0 {
		where, args = append(where, "price >= 0 AND price <= ?"), append(args, p)
	}
	if f.Output != "" {
		where, args = append(where, "output = ?"), append(args, f.Output)
	}
	if f.Network != "" {
		where, args = append(where, "network = ?"), append(args, f.Network)
	}
	if f.Protocol != "" {
		where, args = append(where, "instr(protocols, ?) > 0"), append(args, " "+f.Protocol+" ")
	}
	var expanded map[string][]string
	q := "SELECT rowid, 0.0 FROM docs"
	if len(words) > 0 {
		var expr string
		expr, expanded = ix.query(words)
		q = "SELECT rowid, bm25(docs, " + bm25Weights + ") FROM docs WHERE docs MATCH ?"
		args = append([]any{expr}, args...)
		if len(where) > 0 {
			q += " AND " + strings.Join(where, " AND ")
		}
		q += " ORDER BY bm25(docs, " + bm25Weights + ") LIMIT ?"
		args = append(args, candidatesMax)
	} else {
		if len(where) > 0 {
			q += " WHERE " + strings.Join(where, " AND ")
		}
		q += " LIMIT ?"
		args = append(args, candidatesMax*4)
	}
	rows, err := ix.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []match
	for rows.Next() {
		var m match
		if err = rows.Scan(&m.doc, &m.bm25); err != nil {
			return nil, nil, err
		}
		m.doc--
		if m.doc >= 0 && m.doc < len(ix.docs) {
			out = append(out, m)
		}
	}
	return out, expanded, rows.Err()
}
