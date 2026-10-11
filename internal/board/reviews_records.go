package board

// Lab records recomputed after commit (RFC 0019 §3). A lab assessment that
// names its full record (a doc of the reviewer's holding the
// swarmmemo.observations/1 file) gets a record:SHA256 evidence row in the
// posting transaction, state unknown with reason pending. One background
// worker then reads the doc, checks that the observations are of this
// subject and recent, scores them with internal/rubric (the code behind
// swarmmemo rubric score) and writes verified or failed. Scoring runs a Monte
// Carlo band, so it never runs inside a transaction: the worker reads,
// releases the one connection, scores, then writes one row. Results are
// cached by (rubric SHA-256, record SHA-256), since Score is deterministic.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"swarmmemo/internal/reviews"
	"swarmmemo/internal/rubric"
)

const (
	// recordEvidenceEnd bounds the record rows of a review_evidence range:
	// every id from RecordEvidencePrefix ("record:") sorts before "record;".
	recordEvidenceEnd = "record;"
	// RecordPending is a record row's reason while it waits to be recomputed.
	RecordPending = "pending"
	// ReviewRecordAsOfSkew is how far past the review's own time a record's
	// as_of may be (clocks differ); ReviewEvidenceDays bounds how old it may be.
	ReviewRecordAsOfSkew = 300
	// ReviewRecordFailuresPerDay caps an account's lab records, posted this
	// UTC day, that failed or still wait to be recomputed.
	ReviewRecordFailuresPerDay = 5
	reviewRecordBatch          = 16
	reviewRecordSweep          = 10 * time.Minute
	reviewScoreCacheMax        = 128
)

// reviewRecordState is the recomputer: its wake-up (a committed review
// with a record), its lifetime, and the score cache.
type reviewRecordState struct {
	wake   chan struct{}
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	scores map[[2]string]*rubric.Result
	scored int // Score runs, for tests
}

// labVerified is the SQL condition that review alias r is a lab assessment
// whose record recomputed to what it claims: only such a review is shown as
// a subject's lab assessment, in subjects.list and in the x402 summary.
const labVerified = `EXISTS(SELECT 1 FROM review_evidence lv WHERE lv.event_id=r.event_id AND lv.evidence>='` + RecordEvidencePrefix + `' AND lv.evidence<'` + recordEvidenceEnd + `' AND lv.state='verified')`

// wakeReviewRecords nudges the recomputer. Called inside the posting
// transaction: the worker's read waits for the one connection, so it sees
// the row once committed, or nothing if it rolled back.
func (s *Store) wakeReviewRecords() {
	select {
	case s.reviewRecords.wake <- struct{}{}:
	default:
	}
}

func (s *Store) startReviewRecords(ctx context.Context) {
	if !s.ReviewsEnabled() || s.reviewRecords.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	s.reviewRecords.cancel = cancel
	s.reviewRecords.wg.Add(1)
	go func() {
		defer s.reviewRecords.wg.Done()
		ticker := time.NewTicker(reviewRecordSweep)
		defer ticker.Stop()
		for {
			// The first pass picks up what a restart left pending.
			for ctx.Err() == nil {
				n, err := survive(func() (bool, error) { n, err := s.recomputeRecords(ctx); return n > 0, err })
				if err != nil && ctx.Err() == nil {
					slog.Warn("reviews: lab records were not recomputed", "error", err)
				}
				if err != nil || !n {
					break
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-s.reviewRecords.wake:
			case <-ticker.C:
			}
		}
	}()
}

func (s *Store) stopReviewRecords() {
	if s.reviewRecords.cancel != nil {
		s.reviewRecords.cancel()
	}
	s.reviewRecords.wg.Wait()
}

// pendingRecord is one record row waiting, with its review.
type pendingRecord struct {
	eventID, evidence, account, doc, raw string
	createdAt                            int64
}

// recomputeRecords settles up to a batch of pending record rows and
// reports how many it settled. It holds no transaction: each read and the
// final write are single statements on the pool.
func (s *Store) recomputeRecords(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT v.event_id,v.evidence,v.detail,r.account,r.review,r.created_at FROM review_evidence v JOIN reviews r ON r.event_id=v.event_id
 WHERE v.state='unknown' AND v.evidence>=? AND v.evidence<? ORDER BY v.checked_at,v.event_id LIMIT ?`, RecordEvidencePrefix, recordEvidenceEnd, reviewRecordBatch)
	if err != nil {
		return 0, err
	}
	var batch []pendingRecord
	for rows.Next() {
		var p pendingRecord
		var detail string
		if err = rows.Scan(&p.eventID, &p.evidence, &detail, &p.account, &p.raw, &p.createdAt); err != nil {
			rows.Close()
			return 0, err
		}
		var d struct {
			Doc string `json:"doc"`
		}
		_ = json.Unmarshal([]byte(detail), &d)
		p.doc = d.Doc
		batch = append(batch, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, p := range batch {
		var text, visibility string
		err := s.db.QueryRowContext(ctx, "SELECT v.text,d.visibility FROM doc_versions v JOIN docs d ON d.id=v.doc WHERE v.doc=? AND v.hash=? AND d.account=? AND d.state='active' ORDER BY v.version DESC LIMIT 1",
			p.doc, p.evidence[len(RecordEvidencePrefix):], p.account).Scan(&text, &visibility)
		found := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		state, detail := s.recordOutcome(p, found, text, visibility)
		raw, _ := json.Marshal(detail)
		if _, err = s.db.ExecContext(ctx, "UPDATE review_evidence SET state=?,checked_at=?,detail=? WHERE event_id=? AND evidence=? AND state='unknown'", state, s.now().Unix(), string(raw), p.eventID, p.evidence); err != nil {
			return 0, err
		}
	}
	return len(batch), nil
}

// recordOutcome recomputes one lab assessment from its record (no
// database): the doc must be the reviewer's own version with the record's
// SHA-256, hold observations of this rubric about this subject with an as_of
// no later than the review (give or take ReviewRecordAsOfSkew) and no older
// than ReviewEvidenceDays before it, and score to exactly the claimed score,
// band, grade, verdict and flags. Verified says the score recomputes from
// the published observations; it does not say the observations are true.
func (s *Store) recordOutcome(p pendingRecord, found bool, text, visibility string) (string, map[string]any) {
	detail := map[string]any{"doc": p.doc}
	fail := func(reason string) (string, map[string]any) {
		detail["reason"] = reason
		return "failed", detail
	}
	if !found {
		return fail("not_your_doc_version")
	}
	detail["doc_visibility"] = visibility
	r, err := reviews.Parse([]byte(p.raw), s.subjectOptions())
	if err != nil || r.Rubric == nil {
		return fail("record_unreadable")
	}
	rb := reviews.LookupRubric(r.Rubric.ID, r.Rubric.Version)
	if rb == nil || len(text) > rubric.MaxObservationsBytes {
		return fail("record_unreadable")
	}
	obs, err := rubric.ParseObservations([]byte(text), rb.Rubric)
	if err != nil {
		return fail("not_observations_of_this_rubric")
	}
	if sub, err := reviews.Canonical(obs.Subject.Type, obs.Subject.Key, s.subjectOptions()); err != nil || sub.ID != r.Subject.ID {
		return fail("observations_of_another_subject")
	}
	asOf, err := time.Parse(time.RFC3339Nano, obs.AsOf)
	if err != nil {
		return fail("record_unreadable")
	}
	detail["as_of"] = obs.AsOf
	switch {
	case asOf.Unix() > p.createdAt+ReviewRecordAsOfSkew:
		return fail("as_of_after_the_review")
	case asOf.Unix() < p.createdAt-ReviewEvidenceDays*86400:
		return fail("as_of_older_than_30_days")
	}
	res := s.scoreRecord(rb, p.evidence[len(RecordEvidencePrefix):], obs)
	verdict := reviews.UniversalVerdict(res.Verdict)
	flags, claimed := slices.Clone(res.Flags), slices.Clone(r.Flags)
	slices.Sort(flags)
	slices.Sort(claimed)
	flags, claimed = slices.Compact(flags), slices.Compact(claimed)
	match := r.Score != nil && *r.Score == res.Score && r.Band != nil && *r.Band == res.Band && r.Grade == res.Grade && r.Verdict == verdict && slices.Equal(flags, claimed)
	detail["recomputed"] = map[string]any{"score": res.Score, "band": res.Band, "grade": res.Grade, "verdict": verdict, "lab_verdict": res.Verdict, "flags": res.Flags, "confidence": res.Confidence, "observations_sha256": res.ObservationsSHA256}
	detail["matches"] = match
	if !match {
		return fail("recomputed_result_differs")
	}
	return "verified", detail
}

// scoreRecord is rubric.Score, cached by the rubric's and the record's
// SHA-256 (the record's bytes fix the observations).
func (s *Store) scoreRecord(rb *reviews.Rubric, recordSHA string, obs *rubric.Observations) *rubric.Result {
	key := [2]string{rb.SHA256, recordSHA}
	c := &s.reviewRecords
	c.mu.Lock()
	res, ok := c.scores[key]
	c.mu.Unlock()
	if ok {
		return res
	}
	res = rubric.Score(rb.Rubric, obs)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scores == nil {
		c.scores = map[[2]string]*rubric.Result{}
	}
	if len(c.scores) >= reviewScoreCacheMax {
		for k := range c.scores {
			delete(c.scores, k) // any one: the cache only saves repeats
			break
		}
	}
	c.scores[key] = res
	c.scored++
	return res
}
