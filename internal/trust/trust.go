// Package trust is the trust module (RFC 0021, one trust graph): it
// estimates the collateral an account has here and could lose, in cents,
// as a capacity-bounded max-flow from the world node over judgement edges.
// It never answers "is this a human" and never as a boolean: every answer
// carries its parts, sources, as-of time and parameter version.
//
// Compute is a pure function of a Snapshot, so a third party can recompute
// a run from its published snapshot (scripts/trust/recompute.py). Run pages
// the inputs, computes without holding the database, and records the
// result, the new penalty log entries and the graph in short transactions.
package trust

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"strconv"
	"strings"

	"swarmmemo/internal/allowance"
)

// Schema is migration fragment D (RFC0012 §7), applied after services.Schema.
// trust_scores keeps RFC 0012's column names; from parameter version 8 they
// hold: proof_collateral the direct costs, flow_a the cents received over
// judgement edges, flow_b the cents passed on, flow the cents kept of its own
// costs, collateral the standing, tier the band (allowance.Band) and
// weight_ppm v(s); parts is Parts. trust_evidence, trust_penalties,
// trust_sponsorships and trust_dividends hold retired runs' rows, kept
// (never deleted) and no longer written.
const Schema = `
CREATE TABLE IF NOT EXISTS trust_runs (
 id INTEGER PRIMARY KEY AUTOINCREMENT, as_of INTEGER NOT NULL, params_version INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('running','done','aborted')), inputs TEXT NOT NULL,
 nodes INTEGER NOT NULL DEFAULT 0, edges INTEGER NOT NULL DEFAULT 0, work INTEGER NOT NULL DEFAULT 0,
 capture_bound TEXT NOT NULL DEFAULT '', output_sha256 TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
 started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS trust_scores (
 run_id INTEGER NOT NULL REFERENCES trust_runs(id), account TEXT NOT NULL, root TEXT NOT NULL,
 proof_collateral INTEGER NOT NULL, flow_a INTEGER NOT NULL, flow_b INTEGER NOT NULL, flow INTEGER NOT NULL,
 collateral INTEGER NOT NULL, tier INTEGER NOT NULL, weight_ppm INTEGER NOT NULL, parts TEXT NOT NULL,
 PRIMARY KEY(run_id,account));
CREATE INDEX IF NOT EXISTS trust_scores_account ON trust_scores(account,run_id);
CREATE TABLE IF NOT EXISTS trust_current (account TEXT PRIMARY KEY, run_id INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS trust_evidence (
 id TEXT PRIMARY KEY, run_id INTEGER NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('funnel','ring')),
 members TEXT NOT NULL, detail TEXT NOT NULL, detector_version INTEGER NOT NULL,
 created_at INTEGER NOT NULL, lifted_at INTEGER NOT NULL DEFAULT 0, lift_reason TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS trust_penalties (
 id INTEGER PRIMARY KEY AUTOINCREMENT, account TEXT NOT NULL, evidence_id TEXT NOT NULL REFERENCES trust_evidence(id),
 fraction_ppm INTEGER NOT NULL, starts_at INTEGER NOT NULL, ends_at INTEGER NOT NULL, UNIQUE(account,evidence_id));
CREATE INDEX IF NOT EXISTS trust_penalties_account ON trust_penalties(account,ends_at);
CREATE TABLE IF NOT EXISTS trust_sponsorships (
 invitee TEXT PRIMARY KEY, sponsor TEXT NOT NULL, record_seq INTEGER NOT NULL,
 created_at INTEGER NOT NULL, ends_at INTEGER NOT NULL, high_water INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS trust_dividends (
 run_id INTEGER NOT NULL, sponsor TEXT NOT NULL, invitee TEXT NOT NULL, units INTEGER NOT NULL,
 paid INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(run_id,sponsor,invitee));
CREATE INDEX IF NOT EXISTS trust_sponsorships_sponsor ON trust_sponsorships(sponsor);
CREATE INDEX IF NOT EXISTS trust_dividends_sponsor ON trust_dividends(sponsor,run_id);
CREATE TABLE IF NOT EXISTS trust_run_snapshots (
 run_id INTEGER PRIMARY KEY REFERENCES trust_runs(id), sha256 TEXT NOT NULL, bytes INTEGER NOT NULL, gz BLOB NOT NULL);
`

// PenaltyLogSchema is schema 38 (RFC 0021 §8, C320a): the public penalty
// log and each run's flow graph (for per-viewer standing). Every penalty,
// moderation decision and operator override is an entry; a lift is an
// entry that reverses another (lifts). members, burned and backers are
// canonical JSON arrays; evidence is the trigger's evidence (a detector's
// detail, the hidden posts of a moderation decision).
const PenaltyLogSchema = `
CREATE TABLE IF NOT EXISTS trust_penalty_log (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 trigger TEXT NOT NULL CHECK(trigger IN ('ring','funnel','shared_wallet','moderation','override','lift')),
 state TEXT NOT NULL CHECK(state IN ('pending','applied')), members TEXT NOT NULL DEFAULT '[]',
 evidence TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL, author TEXT NOT NULL, detector_version INTEGER NOT NULL DEFAULT 0,
 run_id INTEGER NOT NULL DEFAULT 0, level INTEGER NOT NULL DEFAULT 0, starts_at INTEGER NOT NULL, ends_at INTEGER NOT NULL DEFAULT 0,
 burned TEXT NOT NULL DEFAULT '[]', backers TEXT NOT NULL DEFAULT '[]', burned_cents INTEGER NOT NULL DEFAULT 0,
 lifts TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS trust_penalty_log_lifts ON trust_penalty_log(lifts) WHERE lifts<>'';
CREATE TABLE IF NOT EXISTS trust_run_graphs (run_id INTEGER PRIMARY KEY REFERENCES trust_runs(id), gz BLOB NOT NULL);
`

// DB runs short transactions. The board implements it on its one
// connection; the trust module never holds a transaction while computing.
type DB interface {
	Read(ctx context.Context, fn func(q allowance.Querier) error) error
	Write(ctx context.Context, fn func(q allowance.Querier) error) error
}

// RunSummary is one finished (or aborted) run.
type RunSummary struct {
	ID            int64
	AsOf          int64
	ParamsVersion int64
	State         string // "done", "aborted"
	Nodes, Edges  int
	Work          int64
	OutputSHA256  string
	Error         string
	// SnapshotSHA256 and SnapshotBytes describe the run's input snapshot
	// (JSONL), published at /api/trust/runs/ID/snapshot when it fits
	// SnapshotBytesMax.
	SnapshotSHA256 string
	SnapshotBytes  int64
	// Penalties are the log entries the run opened or applied.
	Penalties []PenaltyOut
}

// ErrBusy is returned when a run is already in progress.
var ErrBusy = errors.New("trust: a run is already in progress")

// Run computes one run as of asOf from in and records it. The snapshot is
// read page by page (each page its own transaction), the computation holds
// no transaction, and the result is written in short transactions of at
// most 500 rows. On ErrWorkExceeded the run is recorded as aborted and the
// previous run stays current. computed, when set, is called between the
// computation and the writes (tests use it).
func Run(ctx context.Context, db DB, in Inputs, p Params, asOf, now int64, computed func()) (RunSummary, error) {
	var id int64
	err := db.Write(ctx, func(q allowance.Querier) error {
		// A run left running for an hour was interrupted: mark it, never delete.
		if _, err := q.ExecContext(ctx, "UPDATE trust_runs SET state='aborted',error='interrupted',finished_at=? WHERE state='running' AND started_at<=?", now, now-3600); err != nil {
			return err
		}
		var running int
		if err := q.QueryRowContext(ctx, "SELECT count(*) FROM trust_runs WHERE state='running' AND started_at>?", now-3600).Scan(&running); err != nil {
			return err
		}
		if running > 0 {
			return ErrBusy
		}
		res, err := q.ExecContext(ctx, "INSERT INTO trust_runs(as_of,params_version,state,inputs,started_at) VALUES(?,?,'running','{}',?)", asOf, p.Version, now)
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return RunSummary{}, err
	}
	sum := RunSummary{ID: id, AsOf: asOf, ParamsVersion: p.Version, State: "aborted"}
	fail := func(cause error) (RunSummary, error) {
		sum.Error = cause.Error()
		if len(sum.Error) > 500 {
			sum.Error = sum.Error[:500]
		}
		keep := context.WithoutCancel(ctx)
		_ = db.Write(keep, func(q allowance.Querier) error {
			_, err := q.ExecContext(keep, "UPDATE trust_runs SET state='aborted',error=?,work=?,finished_at=? WHERE id=?", sum.Error, sum.Work, now, id)
			return err
		})
		return sum, cause
	}
	snap := Snapshot{Params: p, Meta: Meta{Schema: SnapshotSchema, AsOf: asOf}}
	if err = in.Read(ctx, asOf, p, func(r Record) error {
		if r.Type == "params" {
			return fmt.Errorf("trust inputs: the reader may not replace the parameters")
		}
		return snap.Add(r)
	}); err != nil {
		return fail(err)
	}
	snap.Meta.AsOf, snap.Meta.Schema = asOf, SnapshotSchema
	out, err := Compute(ctx, snap)
	sum.Work = out.Work
	if err != nil {
		return fail(err)
	}
	if computed != nil {
		computed()
	}
	canonical := out.Canonical()
	sum.OutputSHA256 = sha256Hex(canonical)
	sum.Penalties = out.Penalties
	// The inputs exactly as computed, published so anyone can recompute the
	// run (recompute.py run). Kept only when within SnapshotBytesMax.
	snapshot, snapErr := packSnapshot(&snap)
	sum.SnapshotSHA256, sum.SnapshotBytes = snapshot.sha256, snapshot.bytes
	sum.Nodes, sum.Edges = int(out.Nodes), int(out.Edges)
	for start := 0; start < len(out.Scores); start += 500 {
		batch := out.Scores[start:min(start+500, len(out.Scores))]
		if err = db.Write(ctx, func(q allowance.Querier) error {
			for _, sc := range batch {
				band, _ := allowance.ParseBand(sc.Band)
				if _, err := q.ExecContext(ctx, "INSERT INTO trust_scores(run_id,account,root,proof_collateral,flow_a,flow_b,flow,collateral,tier,weight_ppm,parts) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
					id, sc.Account, sc.Root, sc.Parts.DirectCents, sc.Parts.ReceivedCents, sc.Parts.PassedCents, sc.Parts.OwnCents, sc.Cents, int64(band), sc.VoteWeightPPM,
					string(Canonical(sc.Parts))); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return fail(err)
		}
	}
	graph, err := packGraph(out.Graph)
	if err != nil {
		return fail(err)
	}
	record := map[string]any{"summary": out.Inputs, "params_version": p.Version, "params_sha256": out.ParamsSHA256, "params": json.RawMessage(p.Body()),
		"standing": out.Summary, "carry_in_sha256": out.CarryIn, "carry": out.Carry, "evidence": out.Evidence, "penalties": out.Penalties,
		"snapshot_sha256": snapshot.sha256, "snapshot_bytes": snapshot.bytes}
	inputs := Canonical(record)
	err = db.Write(ctx, func(q allowance.Querier) error {
		for _, pen := range out.Penalties {
			if err := recordPenalty(ctx, q, id, pen, out.Evidence, p.Detectors.Version, now); err != nil {
				return err
			}
		}
		if snapErr == nil {
			if _, err := q.ExecContext(ctx, "INSERT INTO trust_run_snapshots(run_id,sha256,bytes,gz) VALUES(?,?,?,?)", id, snapshot.sha256, snapshot.bytes, snapshot.gz); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(ctx, "INSERT INTO trust_run_graphs(run_id,gz) VALUES(?,?)", id, graph); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, "INSERT INTO trust_current(account,run_id) SELECT account,run_id FROM trust_scores WHERE run_id=? ON CONFLICT(account) DO UPDATE SET run_id=excluded.run_id", id); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, "UPDATE trust_runs SET state='done',inputs=?,nodes=?,edges=?,work=?,capture_bound=?,output_sha256=?,finished_at=? WHERE id=?",
			string(inputs), out.Nodes, out.Edges, out.Work, string(Canonical(out.Summary)), sum.OutputSHA256, now, id)
		return err
	})
	if err != nil {
		return fail(err)
	}
	sum.State = "done"
	return sum, nil
}

// recordPenalty writes a run's penalty: a detector's finding as a new
// applied entry, or a pending entry (moderation, override) as applied, with
// its level, end, burned roots and backers.
func recordPenalty(ctx context.Context, q allowance.Querier, run int64, pen PenaltyOut, evidence []EvidenceOut, detector, now int64) error {
	backers := Canonical(pen.Backers)
	burned := Canonical(pen.Burned)
	if !pen.Opened {
		_, err := q.ExecContext(ctx, "UPDATE trust_penalty_log SET state='applied',run_id=?,level=?,ends_at=?,burned=?,backers=?,burned_cents=? WHERE id=? AND state='pending'",
			run, pen.Level, pen.EndsAt, string(burned), string(backers), pen.BurnedCents, pen.ID)
		return err
	}
	reason := "detector " + pen.Trigger + " v" + strconv.FormatInt(detector, 10) + " in trust run " + strconv.FormatInt(run, 10)
	_, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO trust_penalty_log(id,trigger,state,members,evidence,reason,author,detector_version,run_id,level,starts_at,ends_at,burned,backers,burned_cents,created_at)
 VALUES(?,?,'applied',?,?,?,?,?,?,?,?,?,?,?,?,?)`, pen.ID, pen.Trigger, string(Canonical(pen.Members)), pen.Evidence, reason, "run:"+strconv.FormatInt(run, 10),
		detector, run, pen.Level, pen.StartsAt, pen.EndsAt, string(burned), string(backers), pen.BurnedCents, now)
	return err
}

// Score is one account's row in the latest done run.
type Score struct {
	RunID         int64
	Account       string
	Root          string
	Cents         int64
	Band          allowance.Band
	VoteWeightPPM int64
	Parts         Parts
}

// Current reads an account's score in the latest done run; ok is false when
// that run has no row for it.
func Current(ctx context.Context, q allowance.Querier, account string) (score Score, ok bool, err error) {
	m, err := CurrentScores(ctx, q, []string{account})
	if err != nil {
		return Score{}, false, err
	}
	score, ok = m[account]
	return score, ok, nil
}

// currentScoresChunk bounds the accounts one CurrentScores query names.
const currentScoresChunk = 200

// CurrentScores is Current for many accounts in a few reads: each named
// account's score in the latest done run, absent when that run has no row
// for it (standing 0). Rows of a retired run (before parameter version 8)
// are not standing and read as absent.
func CurrentScores(ctx context.Context, q allowance.Querier, accounts []string) (map[string]Score, error) {
	out := map[string]Score{}
	for start := 0; start < len(accounts); start += currentScoresChunk {
		chunk := accounts[start:min(len(accounts), start+currentScoresChunk)]
		args := make([]any, len(chunk))
		for i, a := range chunk {
			args[i] = a
		}
		rows, err := q.QueryContext(ctx, `SELECT s.run_id,s.account,s.root,s.collateral,s.tier,s.weight_ppm,s.parts
 FROM trust_current c JOIN trust_scores s ON s.run_id=c.run_id AND s.account=c.account
 WHERE c.account IN (?`+strings.Repeat(",?", len(chunk)-1)+`) AND c.run_id=(SELECT max(id) FROM trust_runs WHERE state='done' AND params_version>=`+strconv.Itoa(UnifiedVersion)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var score Score
			var parts string
			var band int64
			if err = rows.Scan(&score.RunID, &score.Account, &score.Root, &score.Cents, &band, &score.VoteWeightPPM, &parts); err != nil {
				rows.Close()
				return nil, err
			}
			score.Band = allowance.Band(band)
			if err = json.Unmarshal([]byte(parts), &score.Parts); err != nil {
				rows.Close()
				return nil, err
			}
			out[score.Account] = score
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// StaleAfter is how long a run stays fresh.
const StaleAfter = 2 * day

// LatestRun is the latest done run of the one trust graph (parameter
// version 8 on): its id, as-of time, parameter version, when it finished
// and whether the latest attempt since was aborted.
type LatestRun struct {
	ID, AsOf, Version, ComputedAt int64
	LastAborted                   bool
}

// Stale reports whether the run is too old to rely on at now.
func (r LatestRun) Stale(now int64) bool { return r.LastAborted || now-r.AsOf > StaleAfter }

// Latest reads the latest done run; ok is false before any.
func Latest(ctx context.Context, q allowance.Querier) (run LatestRun, ok bool, err error) {
	err = q.QueryRowContext(ctx, "SELECT id,as_of,params_version,finished_at FROM trust_runs WHERE state='done' AND params_version>=? ORDER BY id DESC LIMIT 1", UnifiedVersion).
		Scan(&run.ID, &run.AsOf, &run.Version, &run.ComputedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return LatestRun{}, false, nil
	}
	if err != nil {
		return LatestRun{}, false, err
	}
	var last string
	err = q.QueryRowContext(ctx, "SELECT state FROM trust_runs WHERE state<>'running' ORDER BY id DESC LIMIT 1").Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LatestRun{}, false, err
	}
	run.LastAborted = last == "aborted"
	return run, true, nil
}

// RunSummaryOf reads a done run's summary (Output.Summary, stored as its
// capture_bound) and its carry's as-of time; ok false when the run is not
// a done run of the one trust graph.
func RunSummaryOf(ctx context.Context, q allowance.Querier, id int64) (Summary, bool, error) {
	var raw string
	err := q.QueryRowContext(ctx, "SELECT capture_bound FROM trust_runs WHERE id=? AND state='done' AND params_version>=?", id, UnifiedVersion).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Summary{}, false, nil
	}
	if err != nil {
		return Summary{}, false, err
	}
	var s Summary
	if err = json.Unmarshal([]byte(raw), &s); err != nil {
		return Summary{}, false, err
	}
	return s, true, nil
}

// LatestCarry reads the carry of the latest done run of the one trust graph
// as snapshot records (nil before any: the next run starts without one).
func LatestCarry(ctx context.Context, q allowance.Querier) ([]Record, error) {
	var raw sql.NullString
	err := q.QueryRowContext(ctx, "SELECT json_extract(inputs,'$.carry') FROM trust_runs WHERE state='done' AND params_version>=? ORDER BY id DESC LIMIT 1", UnifiedVersion).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!raw.Valid || raw.String == "" || raw.String == "null") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Carry
	if err = json.Unmarshal([]byte(raw.String), &c); err != nil {
		return nil, fmt.Errorf("trust: the latest run's carry: %w", err)
	}
	return c.Records(), nil
}

// Histogram is the trust distribution for /stats: the latest run's standing
// in log10 bins of cents and its accounts per band.
type Histogram struct {
	RunID       int64
	AsOf        int64
	Stale       bool
	CentsLog10  []int64          // bin i: 10^i ≤ cents < 10^(i+1); bin 0 also holds 0
	BandCounts  map[string]int64 // signed, proven, trusted
	AboveFloor  int64            // accounts at or above v_floor
	VFloorCents int64
}

// Distribution is the public trust distribution of the latest done run.
func Distribution(ctx context.Context, q allowance.Querier, now int64, p Params) (Histogram, error) {
	h := Histogram{CentsLog10: make([]int64, 8), BandCounts: map[string]int64{}, Stale: true, VFloorCents: p.VFloorCents}
	for _, b := range []allowance.Band{allowance.BandSigned, allowance.BandProven, allowance.BandTrusted} {
		h.BandCounts[b.Name()] = 0
	}
	run, ok, err := Latest(ctx, q)
	if err != nil || !ok {
		return h, err
	}
	h.RunID, h.AsOf, h.Stale = run.ID, run.AsOf, run.Stale(now)
	rows, err := q.QueryContext(ctx, "SELECT max(0,length(CAST(collateral AS TEXT))-1)*(collateral>0),tier,collateral>=?,count(*) FROM trust_scores WHERE run_id=? GROUP BY 1,2,3", p.VFloorCents, h.RunID)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var bin, band, above, n int64
		if err = rows.Scan(&bin, &band, &above, &n); err != nil {
			return h, err
		}
		h.CentsLog10[min(bin, int64(len(h.CentsLog10)-1))] += n
		h.BandCounts[allowance.Band(band).Name()] += n
		if above > 0 {
			h.AboveFloor += n
		}
	}
	return h, rows.Err()
}

// Runs lists runs newest first, before the given id (0: from the newest).
func Runs(ctx context.Context, q allowance.Querier, before int64, limit int) ([]map[string]any, error) {
	if before <= 0 {
		before = math.MaxInt64
	}
	rows, err := q.QueryContext(ctx, "SELECT id,as_of,params_version,state,nodes,edges,work,output_sha256,started_at,finished_at FROM trust_runs WHERE id<? ORDER BY id DESC LIMIT ?", before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, asOf, version, nodes, edges, work, started, finished int64
		var state, hash string
		if err = rows.Scan(&id, &asOf, &version, &state, &nodes, &edges, &work, &hash, &started, &finished); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "as_of": asOf, "params_version": version, "state": state, "nodes": nodes, "edges": edges, "work": work,
			"output_sha256": hash, "started_at": started, "finished_at": finished})
	}
	return out, rows.Err()
}

// RunDetail is one run with its inputs and parameters.
func RunDetail(ctx context.Context, q allowance.Querier, id int64) (map[string]any, bool, error) {
	var asOf, version, nodes, edges, work, started, finished int64
	var state, inputs, hash, failure string
	err := q.QueryRowContext(ctx, "SELECT as_of,params_version,state,inputs,nodes,edges,work,output_sha256,error,started_at,finished_at FROM trust_runs WHERE id=?", id).
		Scan(&asOf, &version, &state, &inputs, &nodes, &edges, &work, &hash, &failure, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	out := map[string]any{"id": id, "as_of": asOf, "params_version": version, "state": state, "inputs": json.RawMessage(inputs), "nodes": nodes, "edges": edges,
		"work": work, "output_sha256": hash, "error": failure, "started_at": started, "finished_at": finished, "snapshot": nil, "recompute": RecomputeText(id, version, false)}
	var snapSHA string
	var snapBytes int64
	err = q.QueryRowContext(ctx, "SELECT sha256,bytes FROM trust_run_snapshots WHERE run_id=?", id).Scan(&snapSHA, &snapBytes)
	switch {
	case err == nil:
		out["snapshot"] = map[string]any{"url": fmt.Sprintf("/api/trust/runs/%d/snapshot", id), "sha256": snapSHA, "bytes": snapBytes}
		out["recompute"] = RecomputeText(id, version, true)
	case !errors.Is(err, sql.ErrNoRows):
		return nil, false, err
	}
	return out, true, nil
}

// RecomputeText says how to verify a run.
func RecomputeText(id, version int64, snapshot bool) string {
	if !snapshot {
		return "This run kept no snapshot; its inputs and output_sha256 stay published."
	}
	if version < UnifiedVersion && version > 6 {
		return fmt.Sprintf("Run %d used retired parameter version %d (RFC 0020's marks), which no verifier recomputes; its snapshot and output_sha256 stay published.", id, version)
	}
	return fmt.Sprintf("Download /api/trust/runs/%d/snapshot (its sha256 is snapshot.sha256), run scripts/trust/recompute.py run on it, and compare the sha256 of its output with output_sha256.", id)
}

// SnapshotBytesMax bounds one run's published input snapshot (JSONL,
// uncompressed). A larger snapshot is not kept; the run still records its
// sha256 and size.
const SnapshotBytesMax = 64 << 20

var errSnapshotTooLarge = errors.New("trust: the input snapshot is larger than SnapshotBytesMax")

type packedSnapshot struct {
	sha256 string
	bytes  int64
	gz     []byte
}

// packSnapshot writes the snapshot's JSONL form, gzipped, with its sha256 and
// uncompressed size. Past SnapshotBytesMax it stops keeping the bytes but
// finishes the hash and the count.
func packSnapshot(s *Snapshot) (packedSnapshot, error) {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	h := sha256.New()
	w := &snapshotWriter{zw: zw, h: h}
	if err := s.WriteJSONL(w); err != nil {
		return packedSnapshot{}, err
	}
	if err := zw.Close(); err != nil {
		return packedSnapshot{}, err
	}
	p := packedSnapshot{sha256: hex.EncodeToString(h.Sum(nil)), bytes: w.n}
	if w.over {
		return p, errSnapshotTooLarge
	}
	p.gz = buf.Bytes()
	return p, nil
}

type snapshotWriter struct {
	zw   *gzip.Writer
	h    hash.Hash
	n    int64
	over bool
}

func (w *snapshotWriter) Write(b []byte) (int, error) {
	w.h.Write(b)
	w.n += int64(len(b))
	if w.n > SnapshotBytesMax {
		w.over = true
	}
	if !w.over {
		if _, err := w.zw.Write(b); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}

// OpenSnapshot opens run id's input snapshot as JSONL (decompressed as it is
// read), with its sha256 and size; ok is false when the run kept none.
func OpenSnapshot(ctx context.Context, q allowance.Querier, id int64) (r io.Reader, sha string, n int64, ok bool, err error) {
	var gz []byte
	err = q.QueryRowContext(ctx, "SELECT sha256,bytes,gz FROM trust_run_snapshots WHERE run_id=?", id).Scan(&sha, &n, &gz)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", 0, false, nil
	}
	if err != nil {
		return nil, "", 0, false, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, "", 0, false, err
	}
	return io.LimitReader(zr, SnapshotBytesMax), sha, n, true, nil
}

// packGraph is a run's graph as gzipped JSON.
func packGraph(g *Graph) ([]byte, error) {
	if g == nil {
		g = &Graph{}
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if _, err = zw.Write(raw); err != nil {
		return nil, err
	}
	if err = zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GraphBytesMax bounds a stored graph read back.
const GraphBytesMax = 256 << 20

// LoadGraph reads run id's flow graph; ok is false when the run kept none.
func LoadGraph(ctx context.Context, q allowance.Querier, id int64) (*Graph, bool, error) {
	var gz []byte
	err := q.QueryRowContext(ctx, "SELECT gz FROM trust_run_graphs WHERE run_id=?", id).Scan(&gz)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, false, err
	}
	var g Graph
	if err = json.NewDecoder(io.LimitReader(zr, GraphBytesMax)).Decode(&g); err != nil {
		return nil, false, err
	}
	g.Reindex()
	return &g, true, nil
}
