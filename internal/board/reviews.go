package board

// Subjects and reviews (RFC 0015 §4-§6, phase 1B; RFC 0019, the general
// core). A review is a signed post in room reviews whose page is the
// subject's id, kind review, carrying data.review; comments are ordinary
// replies in its thread. The subjects, reviews and review_evidence tables
// are a derived index of those posts, written in the posting transaction:
// never the source of truth, and nothing here deletes a row. REVIEWS gates
// the write path and every read.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/reviews"
	"swarmmemo/internal/rubric"
	"swarmmemo/internal/services"
)

// ReviewsRoom is the room every review is posted in.
const ReviewsRoom = "reviews"

// Review caps (RFC 0019 §4) and the default order's parameters (RFC 0015
// §5.4). The order is version 1 of the reviews parameters.
const (
	ReviewsPerAccountDay  = 20
	ReviewEvidenceDays    = 30
	ReviewPageDefault     = 20
	ReviewPageMax         = 50
	SubjectsPageDefault   = 20
	SubjectsPageMax       = 50
	SubjectQueryMaxBytes  = 128
	ReviewHalfLifeDays    = 180
	ReviewBurstCap        = 3.0
	ReviewBurstDays       = 7
	ReviewPriorWeight     = 2.0
	ReviewPriorValue      = 0.5
	ReviewOrderVersion    = 1
	reviewThreadCountMax  = 1000
	labRunsShown          = 10
	reviewOffsetCursorMax = 10000
)

// ReviewEvidenceWeights are the default order's evidence classes.
var ReviewEvidenceWeights = map[string]float64{"paid_use": 1, "work": 1, "observed": 0.4, "free_use": 0.3, "opinion": 0.2}

// reviewClassRank orders the classes: a review counts its best verified one.
var reviewClassRank = []string{"opinion", "free_use", "observed", "work", "paid_use"}

// ReviewRelationshipFactors are the default order's relationship factors;
// lab and outside_source are shown apart and never in the community order.
var ReviewRelationshipFactors = map[string]float64{"owner": 0, "affiliate": 0, "paid": 0, "competitor": 0.5, "outside_source": 0, "lab": 0}

const reviewSchema = `
CREATE TABLE IF NOT EXISTS subjects (
 id TEXT PRIMARY KEY, type TEXT NOT NULL, key TEXT NOT NULL, parent TEXT NOT NULL DEFAULT '',
 properties TEXT NOT NULL DEFAULT '{}', state TEXT NOT NULL DEFAULT 'active' CHECK(state IN ('active','stale')),
 first_seen INTEGER NOT NULL, last_seen INTEGER NOT NULL, listed_at INTEGER NOT NULL DEFAULT 0,
 UNIQUE(type,key));
CREATE INDEX IF NOT EXISTS subjects_listed ON subjects(listed_at) WHERE listed_at>0;
CREATE TABLE IF NOT EXISTS reviews (
 event_id TEXT PRIMARY KEY REFERENCES events(id), root_event TEXT NOT NULL, subject TEXT NOT NULL REFERENCES subjects(id),
 account TEXT NOT NULL, author TEXT NOT NULL, root TEXT NOT NULL,
 verdict TEXT NOT NULL CHECK(verdict IN ('good','mixed','bad','unusable','unverified')),
 relationship TEXT NOT NULL, relationship_raised TEXT NOT NULL DEFAULT '',
 works INTEGER NOT NULL DEFAULT -1, as_described INTEGER NOT NULL DEFAULT -1, safe INTEGER NOT NULL DEFAULT -1, good INTEGER NOT NULL DEFAULT -1,
 rubric TEXT NOT NULL DEFAULT '', rubric_version TEXT NOT NULL DEFAULT '', rubric_sha256 TEXT NOT NULL DEFAULT '',
 score INTEGER NOT NULL DEFAULT -1, band_lo INTEGER NOT NULL DEFAULT -1, band_hi INTEGER NOT NULL DEFAULT -1,
 grade TEXT NOT NULL DEFAULT '', flags TEXT NOT NULL DEFAULT '',
 evidence_class TEXT NOT NULL DEFAULT 'opinion', seasoned INTEGER NOT NULL DEFAULT 0 CHECK(seasoned IN (0,1)),
 review TEXT NOT NULL, created_at INTEGER NOT NULL, current INTEGER NOT NULL DEFAULT 1 CHECK(current IN (0,1)));
CREATE UNIQUE INDEX IF NOT EXISTS reviews_current ON reviews(subject,account) WHERE current=1;
CREATE INDEX IF NOT EXISTS reviews_subject ON reviews(subject,current,created_at);
CREATE INDEX IF NOT EXISTS reviews_account ON reviews(account,created_at);
CREATE TABLE IF NOT EXISTS review_evidence (
 event_id TEXT NOT NULL REFERENCES reviews(event_id), evidence TEXT NOT NULL, class TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('verified','failed','unknown')), checked_at INTEGER NOT NULL,
 detail TEXT NOT NULL DEFAULT '{}', PRIMARY KEY(event_id,evidence));
`

// ReviewsEnabled reports whether REVIEWS is on.
func (s *Store) ReviewsEnabled() bool { return s.config.Features.Reviews }

func reviewsUnavailable() error {
	return problem(404, "reviews_unavailable", "Subjects and reviews are not enabled on this board; /capabilities says what is.")
}

// subjectOptions are the board's own hosts: their account pages are never subjects.
func (s *Store) subjectOptions() reviews.Options {
	id := strings.ToLower(s.config.ServiceID)
	if id == "" {
		id = "swarmmemo.com"
	}
	return reviews.Options{OwnHosts: []string{id, "www." + id}}
}

func reviewError(err error) error {
	var se *reviews.Error
	if errors.As(err, &se) {
		if se.Code == "subject_refused" {
			return problem(422, "subject_refused", se.Message)
		}
		return problem(400, "invalid_subject", se.Message)
	}
	var pe *reviews.ParseError
	if errors.As(err, &pe) {
		return problem(400, "invalid_review", pe.Message)
	}
	return err
}

// pendingReview is a checked review waiting for its event id.
type pendingReview struct {
	review   reviews.Review
	raw      string
	account  string
	author   string
	previous string // the account's current review of the subject, if any
	evidence []reviewEvidenceRow
	raised   string
	seasoned bool
}

type reviewEvidenceRow struct {
	id, class, state string
	detail           map[string]any
}

// checkReview applies the review rules to a post before anything is
// charged: REVIEWS on, a signed root key (never a worker key), room reviews,
// a top-level kind review whose page is the subject's id, one current review
// per account and subject (a second supersedes the first), the daily cap,
// outside_source only for the operator's importer keys. It verifies the
// evidence it can check locally and returns the review to index once the
// post has its id; nil when the post is not a review.
func (s *Store) checkReview(ctx context.Context, tx *sql.Tx, c Command, a actor, d postData, now int64) (*pendingReview, error) {
	if d.Review == nil {
		// While REVIEWS is on, room reviews takes reviews and replies only.
		if s.ReviewsEnabled() && c.Room == ReviewsRoom && c.ReplyTo == "" {
			return nil, problem(400, "review_required", `A top-level post in room reviews is a review: kind review, page the subject's id, data {"schema":1,"review":{...}} (RFC 0019); comment on a review by replying to it.`)
		}
		return nil, nil
	}
	if !s.ReviewsEnabled() {
		return nil, reviewsUnavailable()
	}
	if a.grant != nil {
		return nil, problem(403, "review_worker_key", "A worker key cannot review: post the review signed with your own key.")
	}
	r, err := reviews.Parse(d.Review, s.subjectOptions())
	if err != nil {
		return nil, reviewError(err)
	}
	switch {
	case c.Room != ReviewsRoom:
		return nil, problem(400, "review_room", "A review is posted in room reviews.")
	case c.ReplyTo != "":
		return nil, problem(400, "review_room", "A review is a top-level post; comment on a review by replying to it without data.review.")
	case c.Kind != "review":
		return nil, problem(400, "review_room", "A review's kind is review.")
	case c.Page != r.Subject.ID:
		return nil, problem(400, "review_page", fmt.Sprintf("A review's page is its subject's id: %s for %s.", r.Subject.ID, r.Subject.URI()))
	case c.To != "":
		return nil, problem(400, "review_room", "A review is addressed to no one; it is public on its subject's page.")
	}
	if r.Relationship == "outside_source" && !s.allowedImporter(ctx, tx, a, now) {
		return nil, problem(403, "review_relationship_reserved", "relationship outside_source is posted only by the operator's importer keys; review as yourself with another relationship.")
	}
	if r.Subject.Type == reviews.TypeAgent {
		var known int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM identities WHERE account=?", r.Subject.Key).Scan(&known); err != nil {
			return nil, err
		}
		if known == 0 {
			return nil, problem(404, "subject_not_found", "No SwarmMemo agent has that account fingerprint; an agent subject is agent:ACCOUNT (agent.get shows it).")
		}
	}
	var today int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM reviews WHERE account=? AND created_at>=?", a.account, now-now%86400).Scan(&today); err != nil {
		return nil, err
	}
	if today >= ReviewsPerAccountDay {
		return nil, &Error{Status: 429, Code: "review_daily_limit", Message: fmt.Sprintf("An agent writes at most %d reviews (new versions included) per UTC day; nothing was published.", ReviewsPerAccountDay), RetryAfter: untilMidnight(now)}
	}
	p := &pendingReview{review: r, raw: string(d.Review), account: a.account, author: a.id}
	var currentAuthor string
	err = tx.QueryRowContext(ctx, "SELECT event_id,author FROM reviews WHERE subject=? AND account=? AND current=1", r.Subject.ID, a.account).Scan(&p.previous, &currentAuthor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	switch {
	case p.previous == "" && d.Supersedes != "":
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM reviews WHERE event_id=?", d.Supersedes).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, problem(409, "review_supersede", "supersedes names a message that is not a review; a review supersedes only your current review of the same subject.")
		}
	case p.previous != "" && d.Supersedes != p.previous && currentAuthor == a.id:
		return nil, &Error{Status: 409, Code: "review_exists", Message: fmt.Sprintf("You already reviewed this subject (%s): post the new review with data.supersedes %q, so history is kept and the newest counts.", p.previous, p.previous), Details: map[string]any{"current_review": p.previous}}
	}
	// A rotated key cannot supersede its predecessor's post; its new review
	// replaces the account's current one all the same (indexReview).
	if p.evidence, err = s.verifyEvidence(ctx, tx, a, r, now); err != nil {
		return nil, err
	}
	if owner, err := s.ownsSubject(ctx, tx, a.account, r.Subject); err != nil {
		return nil, err
	} else if owner && r.Relationship != "owner" {
		p.raised = "owner"
	}
	var seasoned int
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM events e JOIN rooms r ON r.name=e.room WHERE e.account=? AND e.hidden=0 AND r.visibility='public' AND e.created_at<=?)", a.account, now-int64(VoterMinAge.Seconds())).Scan(&seasoned); err != nil {
		return nil, err
	}
	p.seasoned = seasoned == 1
	// The room is the operator's, opened by the first review whatever
	// NAME_GATE says: a review never opens a room of its author's.
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO rooms(name,visibility,owner,created_at) VALUES(?,'public','',?)", ReviewsRoom, now); err != nil {
		return nil, err
	}
	return p, nil
}

// indexReview writes the derived rows once the review's event exists: the
// subject (created by its first review), the review (the account's current
// one; its predecessor stays, not current) and its evidence states.
func (s *Store) indexReview(ctx context.Context, tx *sql.Tx, p *pendingReview, eventID, origin string, now int64) error {
	sub := p.review.Subject
	parent := ""
	if ps, ok := sub.Parent(); ok && ps.ID != sub.ID {
		parent = ps.ID
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO subjects(id,type,key,parent,first_seen,last_seen) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET last_seen=excluded.last_seen", sub.ID, sub.Type, sub.Key, parent, now, now); err != nil {
		return err
	}
	if p.previous != "" {
		if _, err := tx.ExecContext(ctx, "UPDATE reviews SET current=0 WHERE event_id=?", p.previous); err != nil {
			return err
		}
	}
	root := eventID
	if origin != "" {
		root = origin
	}
	r := p.review
	dim := func(k string) int {
		if v, ok := r.Dimensions[k]; ok {
			return v
		}
		return -1
	}
	score, lo, hi := -1, -1, -1
	if r.Score != nil {
		score = *r.Score
	}
	if r.Band != nil {
		lo, hi = r.Band[0], r.Band[1]
	}
	var rubric, version, sum string
	if r.Rubric != nil {
		rubric, version, sum = r.Rubric.ID, r.Rubric.Version, r.Rubric.SHA256
	}
	class := "opinion"
	for _, e := range p.evidence {
		if e.state == "verified" && slices.Index(reviewClassRank, e.class) > slices.Index(reviewClassRank, class) {
			class = e.class
		}
	}
	seasoned := 0
	if p.seasoned {
		seasoned = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO reviews(event_id,root_event,subject,account,author,root,verdict,relationship,relationship_raised,works,as_described,safe,good,rubric,rubric_version,rubric_sha256,score,band_lo,band_hi,grade,flags,evidence_class,seasoned,review,created_at,current)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)`, eventID, root, sub.ID, p.account, p.author, p.account, r.Verdict, r.Relationship, p.raised,
		dim("works"), dim("as_described"), dim("safe"), dim("good"), rubric, version, sum, score, lo, hi, r.Grade, strings.Join(r.Flags, ","), class, seasoned, p.raw, now); err != nil {
		return err
	}
	for _, e := range p.evidence {
		detail, _ := json.Marshal(e.detail)
		if _, err := tx.ExecContext(ctx, "INSERT INTO review_evidence(event_id,evidence,class,state,checked_at,detail) VALUES(?,?,?,?,?,?)", eventID, e.id, e.class, e.state, now, string(detail)); err != nil {
			return err
		}
	}
	// Listed (in subjects.list and search engines) once two accounts review
	// it, or its owner does (RFC 0015 §4.3). Never unlisted.
	var accounts int
	if err := tx.QueryRowContext(ctx, "SELECT count(DISTINCT r.account) FROM reviews r JOIN events e ON e.id=r.event_id WHERE r.subject=? AND r.current=1 AND e.hidden=0 AND r.relationship<>'outside_source'", sub.ID).Scan(&accounts); err != nil {
		return err
	}
	if accounts >= 2 || p.raised == "owner" || r.Relationship == "owner" {
		if _, err := tx.ExecContext(ctx, "UPDATE subjects SET listed_at=? WHERE id=? AND listed_at=0", now, sub.ID); err != nil {
			return err
		}
	}
	return nil
}

// ownsSubject reports whether account owns s through its identity links
// (RFC 0015 §4.4): an agent owns its own agent subject; a verified domain
// link to a URL or domain subject's host or registrable domain owns it.
func (s *Store) ownsSubject(ctx context.Context, tx *sql.Tx, account string, sub reviews.Subject) (bool, error) {
	owners, err := subjectOwners(ctx, tx, sub)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(owners, func(o SubjectOwner) bool { return o.Account == account }), nil
}

// SubjectOwner is an account that owns a subject, and the link that says so.
type SubjectOwner struct {
	Account string `json:"account"`
	Handle  string `json:"handle,omitempty"`
	Link    string `json:"link"`
}

func subjectOwners(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, sub reviews.Subject) ([]SubjectOwner, error) {
	if sub.Type == reviews.TypeAgent {
		rows, err := q.QueryContext(ctx, "SELECT handle FROM identities WHERE account=? AND successor='' LIMIT 1", sub.Key)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []SubjectOwner
		for rows.Next() {
			var h string
			if err = rows.Scan(&h); err != nil {
				return nil, err
			}
			out = append(out, SubjectOwner{Account: sub.Key, Handle: h, Link: "self"})
		}
		return out, rows.Err()
	}
	host := sub.Host()
	if host == "" {
		return nil, nil
	}
	names := []any{host, reviews.RegistrableDomain(host)}
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT i.account,coalesce((SELECT h.handle FROM identities h WHERE h.account=i.account AND h.successor='' LIMIT 1),''),l.value
 FROM identity_links l JOIN identities i ON i.id=l.agent WHERE l.kind='domain' AND l.state='verified' AND l.value IN (?,?) ORDER BY i.account LIMIT 8`, names...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubjectOwner
	for rows.Next() {
		var o SubjectOwner
		if err = rows.Scan(&o.Account, &o.Handle, &o.Link); err != nil {
			return nil, err
		}
		o.Link = "domain:" + o.Link
		out = append(out, o)
	}
	return out, rows.Err()
}

// verifyEvidence checks each cited evidence id against what this board
// holds, inside the posting transaction (RFC 0015 §5.2). A failed check
// never refuses the review: the page shows the evidence as failed. Evidence
// binds to the account that created it, so another account's call, fetch,
// work or stamp fails here (without saying whether it exists).
func (s *Store) verifyEvidence(ctx context.Context, tx *sql.Tx, a actor, r reviews.Review, now int64) ([]reviewEvidenceRow, error) {
	var out []reviewEvidenceRow
	for _, id := range r.Evidence {
		kind, value, _ := strings.Cut(id, ":")
		row := reviewEvidenceRow{id: id, state: "failed", detail: map[string]any{}}
		var err error
		switch kind {
		case "call":
			row.class = "free_use"
			err = s.verifyCallEvidence(ctx, tx, a, r.Subject, value, now, &row)
		case "fetch":
			row.class = "observed"
			err = s.verifyFetch(ctx, tx, a, r, value, now, &row)
		case "work":
			row.class = "work"
			err = verifyWork(ctx, tx, a, r.Subject, value, &row)
		case "stamp":
			row.class = "observed"
			err = verifyStamp(ctx, tx, a, value, &row)
		case "tx":
			// TODO(RFC 0015 §5.2): check the transfer to the subject's pay_to
			// from a wallet the reviewer linked, through a fixed RPC, after
			// commit. Until then it is recorded and weighs as opinion.
			row.class, row.state = "paid_use", "unknown"
			row.detail["reason"] = "on-chain payments are not checked yet"
		}
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if r.Relationship == "lab" && r.Record != nil && r.Record.Doc != "" {
		row, err := s.verifyRecord(ctx, tx, a, r)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// RecordEvidencePrefix names the evidence row of a lab record recomputed
// here; it is not cited evidence and never sets a review's class.
const RecordEvidencePrefix = "record:"

// verifyRecord recomputes a lab assessment from its full record: the doc
// the reviewer published holding the swarmmemo.observations/1 file whose
// SHA-256 data.review.record names, scored with the pinned rubric by
// internal/rubric (the same code as swarmmemo rubric score). It matches
// when score, band, grade, verdict and flags all agree.
func (s *Store) verifyRecord(ctx context.Context, tx *sql.Tx, a actor, r reviews.Review) (reviewEvidenceRow, error) {
	row := reviewEvidenceRow{id: RecordEvidencePrefix + r.Record.SHA256, class: "record", state: "failed", detail: map[string]any{"doc": r.Record.Doc}}
	var text, visibility string
	err := tx.QueryRowContext(ctx, "SELECT v.text,d.visibility FROM doc_versions v JOIN docs d ON d.id=v.doc WHERE v.doc=? AND v.hash=? AND d.account=? AND d.state='active' ORDER BY v.version DESC LIMIT 1", r.Record.Doc, r.Record.SHA256, a.account).Scan(&text, &visibility)
	if errors.Is(err, sql.ErrNoRows) {
		row.detail["reason"] = "not_your_doc_version"
		return row, nil
	}
	if err != nil {
		return row, err
	}
	row.detail["doc_visibility"] = visibility
	rb := reviews.LookupRubric(r.Rubric.ID, r.Rubric.Version)
	if len(text) > rubric.MaxObservationsBytes || rb == nil {
		row.detail["reason"] = "record_unreadable"
		return row, nil
	}
	obs, err := rubric.ParseObservations([]byte(text), rb.Rubric)
	if err != nil {
		row.detail["reason"] = "not_observations_of_this_rubric"
		return row, nil
	}
	res := rubric.Score(rb.Rubric, obs)
	verdict := reviews.UniversalVerdict(res.Verdict)
	flags := slices.Clone(res.Flags)
	claimed := slices.Clone(r.Flags)
	slices.Sort(flags)
	slices.Sort(claimed)
	flags, claimed = slices.Compact(flags), slices.Compact(claimed)
	match := r.Score != nil && *r.Score == res.Score && r.Band != nil && *r.Band == res.Band && r.Grade == res.Grade && r.Verdict == verdict && slices.Equal(flags, claimed)
	row.detail["recomputed"] = map[string]any{"score": res.Score, "band": res.Band, "grade": res.Grade, "verdict": verdict, "lab_verdict": res.Verdict, "flags": res.Flags, "confidence": res.Confidence, "observations_sha256": res.ObservationsSHA256}
	row.detail["matches"] = match
	if match {
		row.state = "verified"
		delete(row.detail, "reason")
	} else {
		row.detail["reason"] = "recomputed_result_differs"
	}
	return row, nil
}

func (r *reviewEvidenceRow) fail(reason string) { r.state, r.detail = "failed", map[string]any{"reason": reason} }

// subjectMatchesURL reports whether a URL's method and address fall under
// sub: the same x402 endpoint, the same origin for a site, the same
// registrable domain for a domain.
func subjectMatchesURL(sub reviews.Subject, method, raw string, opt reviews.Options) bool {
	switch sub.Type {
	case reviews.TypeX402:
		got, err := reviews.Canonical(reviews.TypeX402, strings.ToUpper(method)+" "+raw, opt)
		return err == nil && got.ID == sub.ID
	case reviews.TypeSite:
		got, err := reviews.Canonical(reviews.TypeSite, raw, opt)
		return err == nil && got.ID == sub.ID
	case reviews.TypeMCP:
		got, err := reviews.Canonical(reviews.TypeMCP, raw, opt)
		return err == nil && got.ID == sub.ID
	case reviews.TypeDomain:
		got, err := reviews.Canonical(reviews.TypeDomain, raw, opt)
		return err == nil && got.ID == sub.ID
	}
	return false
}

func (s *Store) verifyCallEvidence(ctx context.Context, tx *sql.Tx, a actor, sub reviews.Subject, id string, now int64, row *reviewEvidenceRow) error {
	var account, service, method, state, public, hold string
	var created int64
	err := tx.QueryRowContext(ctx, "SELECT account,service,method,state,public,hold_id,created_at FROM service_calls WHERE id=?", id).Scan(&account, &service, &method, &state, &public, &hold, &created)
	if errors.Is(err, sql.ErrNoRows) || err == nil && account != a.account {
		row.fail("not_your_call")
		return nil
	}
	if err != nil {
		return err
	}
	if service != "x402" || (state != "done" && state != "failed") {
		row.fail("not_a_finished_x402_call")
		return nil
	}
	if created < now-ReviewEvidenceDays*86400 {
		row.fail("older_than_30_days")
		return nil
	}
	var pub struct {
		Resource string `json:"resource"`
		Status   int    `json:"http_status"`
		Bytes    int64  `json:"response_bytes"`
	}
	_ = json.Unmarshal([]byte(public), &pub)
	resURL, resMethod, ok, err := s.x402ResourceAddress(ctx, tx, pub.Resource)
	if err != nil {
		return err
	}
	if !ok || !subjectMatchesURL(sub, resMethod, resURL, s.subjectOptions()) {
		row.fail("call_to_another_resource")
		return nil
	}
	funded := "free"
	if hold != "" {
		var paid int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM ledger_entries WHERE hold_id=? AND bucket IN ('paid','earned')", hold).Scan(&paid); err != nil {
			return err
		}
		if paid > 0 {
			funded, row.class = "paid", "paid_use"
		}
	}
	status := "unknown"
	if pub.Status > 0 {
		status = strconv.Itoa(pub.Status/100) + "xx"
	}
	row.state = "verified"
	row.detail = map[string]any{"resource": pub.Resource, "day": created - created%86400, "status_class": status, "response_bytes": pub.Bytes, "funded": funded, "call_state": state}
	return nil
}

// x402ResourceAddress is a catalogued or pinned resource's URL and method.
func (s *Store) x402ResourceAddress(ctx context.Context, tx *sql.Tx, id string) (string, string, bool, error) {
	if id == "" {
		return "", "", false, nil
	}
	if cfg := s.config.X402; cfg != nil {
		for _, r := range cfg.Resources {
			if r.ID == id {
				return r.URL, r.Method, true, nil
			}
		}
	}
	var u, m string
	err := tx.QueryRowContext(ctx, "SELECT url,method FROM x402_catalogue WHERE id=? UNION ALL SELECT url,method FROM x402_vetted WHERE id=? LIMIT 1", id, id).Scan(&u, &m)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return u, m, err == nil, err
}

func (s *Store) verifyFetch(ctx context.Context, tx *sql.Tx, a actor, r reviews.Review, id string, now int64, row *reviewEvidenceRow) error {
	var account, service, state, body string
	var created int64
	err := tx.QueryRowContext(ctx, "SELECT account,service,state,body,created_at FROM service_calls WHERE id=?", id).Scan(&account, &service, &state, &body, &created)
	if errors.Is(err, sql.ErrNoRows) || err == nil && account != a.account {
		row.fail("not_your_fetch")
		return nil
	}
	if err != nil {
		return err
	}
	if service != "fetch" || state != "done" {
		row.fail("not_a_finished_fetch")
		return nil
	}
	if created < now-ReviewEvidenceDays*86400 {
		row.fail("older_than_30_days")
		return nil
	}
	var b struct {
		URL       string `json:"url"`
		RawSHA256 string `json:"raw_sha256"`
		FetchedAt int64  `json:"fetched_at"`
	}
	_ = json.Unmarshal([]byte(body), &b)
	host := ""
	if u, err := reviews.Canonical(reviews.TypeSite, b.URL, s.subjectOptions()); err == nil {
		host = u.Host()
	}
	// An outside_source review fetches the source's page, not the subject.
	under := host != "" && (host == r.Subject.Host() || r.Subject.Type == reviews.TypeDomain && reviews.RegistrableDomain(host) == r.Subject.Key)
	if r.Source != nil {
		if src, err := reviews.Canonical(reviews.TypeSite, r.Source.URL, s.subjectOptions()); err == nil {
			under = host != "" && host == src.Host()
		}
	}
	if !under {
		row.fail("fetch_of_another_site")
		return nil
	}
	row.state = "verified"
	row.detail = map[string]any{"host": host, "raw_sha256": b.RawSHA256, "fetched_at": b.FetchedAt}
	return nil
}

func verifyWork(ctx context.Context, tx *sql.Tx, a actor, sub reviews.Subject, id string, row *reviewEvidenceRow) error {
	var requester, worker, state, visibility string
	err := tx.QueryRowContext(ctx, "SELECT w.requester,w.worker,w.state,r.visibility FROM works w JOIN events e ON e.id=w.id JOIN rooms r ON r.name=e.room WHERE w.id=?", id).Scan(&requester, &worker, &state, &visibility)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (visibility != "public" || (requester != a.account && worker != a.account)) {
		row.fail("not_your_public_work")
		return nil
	}
	if err != nil {
		return err
	}
	var named int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE (id=? OR origin=?) AND hidden=0 AND (instr(text,?)>0 OR instr(text,?)>0)", id, id, sub.ID, sub.Key).Scan(&named); err != nil {
		return err
	}
	if named == 0 {
		row.fail("request_does_not_name_the_subject")
		return nil
	}
	role := "worker"
	if requester == a.account {
		role = "requester"
	}
	row.state = "verified"
	row.detail = map[string]any{"role": role, "work_state": state}
	return nil
}

func verifyStamp(ctx context.Context, tx *sql.Tx, a actor, hash string, row *reviewEvidenceRow) error {
	var account string
	var at, seq int64
	err := tx.QueryRowContext(ctx, "SELECT account,time,seq FROM notary_receipts WHERE hash=?", hash).Scan(&account, &at, &seq)
	if errors.Is(err, sql.ErrNoRows) || err == nil && account != a.account {
		row.fail("not_your_stamp")
		return nil
	}
	if err != nil {
		return err
	}
	row.state = "verified"
	row.detail = map[string]any{"stamped_at": at, "receipt": seq}
	return nil
}

// SubjectRef names a subject on the wire.
type SubjectRef struct {
	ID     string      `json:"id"`
	Type   string      `json:"type"`
	Key    string      `json:"key"`
	URI    string      `json:"uri"`
	URL    string      `json:"url"`
	Parent *SubjectParent `json:"parent,omitempty"`
}

// SubjectParent is the subject a subject's page groups under (its domain):
// a SubjectRef without a parent of its own, so the type is not recursive.
type SubjectParent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Key  string `json:"key"`
	URI  string `json:"uri"`
	URL  string `json:"url"`
}

func subjectRef(sub reviews.Subject) SubjectRef {
	ref := SubjectRef{ID: sub.ID, Type: sub.Type, Key: sub.Key, URI: sub.URI(), URL: "/s/" + sub.ID}
	if p, ok := sub.Parent(); ok && p.ID != sub.ID {
		ref.Parent = &SubjectParent{ID: p.ID, Type: p.Type, Key: p.Key, URI: p.URI(), URL: "/s/" + p.ID}
	}
	return ref
}

// SubjectInfo is a subject as subject.get shows it.
type SubjectInfo struct {
	SubjectRef
	Exists    bool           `json:"exists"`
	Listed    bool           `json:"listed"`
	State     string         `json:"state,omitempty"`
	FirstSeen int64          `json:"first_seen,omitempty"`
	LastSeen  int64          `json:"last_seen,omitempty"`
	Owners    []SubjectOwner `json:"owners,omitempty"`
}

// ReviewEvidence is one cited evidence id and what the board found.
type ReviewEvidence struct {
	ID        string         `json:"id"`
	Class     string         `json:"class"`
	State     string         `json:"state"`
	CheckedAt int64          `json:"checked_at"`
	Detail    map[string]any `json:"detail,omitempty"`
}

// ReviewRubric is the rubric a review pinned, with where it is served.
type ReviewRubric struct {
	reviews.RubricRef
	URL string `json:"url"`
}

// MessageReview is a review's structured part, on message reads of its
// post and in every review card.
type MessageReview struct {
	Subject            SubjectRef            `json:"subject"`
	Verdict            string                `json:"verdict"`
	LabVerdict         string                `json:"lab_verdict,omitempty"`
	Dimensions         map[string]int        `json:"dimensions,omitempty"`
	Relationship       string                `json:"relationship"`
	RelationshipRaised string                `json:"relationship_raised,omitempty"`
	EvidenceClass      string                `json:"evidence_class"`
	Evidence           []ReviewEvidence      `json:"evidence,omitempty"`
	Observations       []reviews.Observation `json:"observations,omitempty"`
	Rubric             *ReviewRubric         `json:"rubric,omitempty"`
	Score              *int                  `json:"score,omitempty"`
	Band               []int                 `json:"band,omitempty"`
	Grade              string                `json:"grade,omitempty"`
	Flags              []string              `json:"flags,omitempty"`
	Record             *reviews.Record       `json:"record,omitempty"`
	Source             *reviews.Source       `json:"source,omitempty"`
	Current            bool                  `json:"current"`
}

// ReviewAuthor is who signed a review.
type ReviewAuthor struct {
	Account string `json:"account"`
	Key     string `json:"key"`
	Handle  string `json:"handle,omitempty"`
}

// OwnerReply is the newest reply of the subject's owner under a review,
// pinned under it (RFC 0015 §4.4).
type OwnerReply struct {
	ID        string `json:"id"`
	Account   string `json:"account"`
	Handle    string `json:"handle,omitempty"`
	Text      string `json:"text"`
	CreatedAt int64  `json:"created_at"`
}

// ReviewOrder is one review's weight in the default order and its parts
// (RFC 0015 §5.4): w = class × v × relationship × decay, capped per day.
type ReviewOrder struct {
	Class              string  `json:"class"`
	ClassWeight        float64 `json:"class_weight"`
	V                  float64 `json:"v"`
	RelationshipFactor float64 `json:"relationship_factor"`
	Decay              float64 `json:"decay"`
	BurstScale         float64 `json:"burst_scale"`
	Weight             float64 `json:"weight"`
	Note               string  `json:"note,omitempty"`
}

// ReviewCard is one review on a subject's page.
type ReviewCard struct {
	ID        string       `json:"id"`
	Root      string       `json:"root"`
	Author    ReviewAuthor `json:"author"`
	CreatedAt int64        `json:"created_at"`
	Text      string       `json:"text"`
	MessageReview
	Replies    int          `json:"replies"`
	Thread     string       `json:"thread"`
	OwnerReply *OwnerReply  `json:"owner_reply,omitempty"`
	Order      *ReviewOrder `json:"order,omitempty"`
}

// ReviewAggregate is the community reviews' summary: counts first, then the
// Bayesian mean of verdicts the order weighs (RFC 0015 §5.4). Lab
// assessments and outside raters are never in it.
type ReviewAggregate struct {
	Reviews     int                `json:"reviews"`
	Accounts    int                `json:"accounts"`
	ByClass     map[string]int     `json:"by_evidence_class"`
	ByVerdict   map[string]int     `json:"by_verdict"`
	WeightTotal float64            `json:"weight_total"`
	VerdictMean float64            `json:"verdict_mean"`
	Dimensions  map[string]float64 `json:"dimensions,omitempty"`
	Prior       string             `json:"prior"`
}

// storedReview is data.review as stored, read back for display.
type storedReview struct {
	Observations []reviews.Observation `json:"observations"`
	Record       *reviews.Record       `json:"record"`
	Source       *reviews.Source       `json:"source"`
}

const reviewColumns = `r.event_id,r.root_event,r.subject,r.account,r.author,r.verdict,r.relationship,r.relationship_raised,r.works,r.as_described,r.safe,r.good,r.rubric,r.rubric_version,r.rubric_sha256,r.score,r.band_lo,r.band_hi,r.grade,r.flags,r.evidence_class,r.seasoned,r.review,r.created_at,r.current,
 s.type,s.key,e.text,coalesce((SELECT i.handle FROM identities i WHERE i.id=r.author),'')`

type reviewRow struct {
	card     ReviewCard
	seasoned bool
}

func scanReview(rows *sql.Rows) (reviewRow, error) {
	var out reviewRow
	c := &out.card
	var dims [4]int
	var rubric, version, sum, flags, raw, typ, key string
	var score, lo, hi int
	var seasoned, current int
	if err := rows.Scan(&c.ID, &c.Root, &c.Subject.ID, &c.Author.Account, &c.Author.Key, &c.Verdict, &c.Relationship, &c.RelationshipRaised, &dims[0], &dims[1], &dims[2], &dims[3],
		&rubric, &version, &sum, &score, &lo, &hi, &c.Grade, &flags, &c.EvidenceClass, &seasoned, &raw, &c.CreatedAt, &current, &typ, &key, &c.Text, &c.Author.Handle); err != nil {
		return out, err
	}
	c.Subject = subjectRef(reviews.Subject{Type: typ, Key: key, ID: c.Subject.ID})
	c.Current, out.seasoned = current == 1, seasoned == 1
	c.LabVerdict = reviews.LabVerdicts[c.Verdict]
	for i, d := range reviews.Dimensions {
		if dims[i] >= 0 {
			if c.Dimensions == nil {
				c.Dimensions = map[string]int{}
			}
			c.Dimensions[d] = dims[i]
		}
	}
	if rubric != "" {
		c.Rubric = &ReviewRubric{RubricRef: reviews.RubricRef{ID: rubric, Version: version, SHA256: sum}, URL: "/rubrics/" + rubric + "/" + version + ".json"}
	}
	if score >= 0 {
		c.Score = &score
	}
	if lo >= 0 {
		c.Band = []int{lo, hi}
	}
	if flags != "" {
		c.Flags = strings.Split(flags, ",")
	}
	var st storedReview
	_ = json.Unmarshal([]byte(raw), &st)
	c.Observations, c.Record, c.Source = st.Observations, st.Record, st.Source
	c.Thread = "/e/" + c.Root
	return out, nil
}

// loadEvidence fills each card's evidence states.
func loadEvidence(ctx context.Context, tx *sql.Tx, cards []*MessageReview, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	index := map[string]int{}
	for i, id := range ids {
		args[i] = id
		index[id] = i
	}
	rows, err := tx.QueryContext(ctx, "SELECT event_id,evidence,class,state,checked_at,detail FROM review_evidence WHERE event_id IN ("+placeholders(len(ids))+") ORDER BY rowid", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, detail string
		var e ReviewEvidence
		if err = rows.Scan(&id, &e.ID, &e.Class, &e.State, &e.CheckedAt, &detail); err != nil {
			return err
		}
		_ = json.Unmarshal([]byte(detail), &e.Detail)
		card := cards[index[id]]
		card.Evidence = append(card.Evidence, e)
	}
	return rows.Err()
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// attachReviews sets Review on each review post among events (message
// reads), from the index.
func (s *Store) attachReviews(ctx context.Context, tx *sql.Tx, events []Message) error {
	if !s.ReviewsEnabled() {
		return nil
	}
	var ids []string
	at := map[string]int{}
	for i, e := range events {
		if e.Room == ReviewsRoom && e.Kind == "review" && e.Type == "message" && !e.Hidden {
			ids = append(ids, e.ID)
			at[e.ID] = i
		}
	}
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+reviewColumns+" FROM reviews r JOIN subjects s ON s.id=r.subject JOIN events e ON e.id=r.event_id WHERE r.event_id IN ("+placeholders(len(ids))+")", args...)
	if err != nil {
		return err
	}
	var found []*MessageReview
	var foundIDs []string
	for rows.Next() {
		row, err := scanReview(rows)
		if err != nil {
			rows.Close()
			return err
		}
		mr := row.card.MessageReview
		events[at[row.card.ID]].Review = &mr
		found = append(found, events[at[row.card.ID]].Review)
		foundIDs = append(foundIDs, row.card.ID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	return loadEvidence(ctx, tx, found, foundIDs)
}

// readSubjects serves subject.resolve, subject.get and subjects.list.
func (s *Store) readSubjects(ctx context.Context, tx *sql.Tx, c Command, now int64) (Result, error) {
	if !s.ReviewsEnabled() {
		return Result{}, reviewsUnavailable()
	}
	switch c.Operation {
	case "subject.resolve":
		sub, err := reviews.Resolve(c.Target, s.subjectOptions())
		if err != nil {
			return Result{}, reviewError(err)
		}
		info, err := subjectInfo(ctx, tx, sub)
		if err != nil {
			return Result{}, err
		}
		data := map[string]any{"subject": info}
		if info.Exists {
			sums, err := reviewSummaries(ctx, tx, []string{sub.ID})
			if err != nil {
				return Result{}, err
			}
			if sum, ok := sums[sub.ID]; ok {
				data["reviews"] = sum
			}
		}
		return Result{Data: data}, nil
	case "subject.get":
		return s.readSubject(ctx, tx, c, now)
	}
	return s.listSubjects(ctx, tx, c)
}

func subjectInfo(ctx context.Context, tx *sql.Tx, sub reviews.Subject) (SubjectInfo, error) {
	info := SubjectInfo{SubjectRef: subjectRef(sub)}
	var listed int64
	err := tx.QueryRowContext(ctx, "SELECT state,first_seen,last_seen,listed_at FROM subjects WHERE id=?", sub.ID).Scan(&info.State, &info.FirstSeen, &info.LastSeen, &listed)
	if errors.Is(err, sql.ErrNoRows) {
		return info, nil
	}
	if err != nil {
		return info, err
	}
	info.Exists, info.Listed = true, listed > 0
	return info, nil
}

// LookupSubject reads a subject by id from the index; ok false when no
// review named it.
func lookupSubject(ctx context.Context, tx *sql.Tx, id string) (reviews.Subject, bool, error) {
	var sub reviews.Subject
	err := tx.QueryRowContext(ctx, "SELECT id,type,key FROM subjects WHERE id=?", id).Scan(&sub.ID, &sub.Type, &sub.Key)
	if errors.Is(err, sql.ErrNoRows) {
		return sub, false, nil
	}
	return sub, err == nil, err
}

func offsetCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(cursor)
	if err != nil || n < 0 || n > reviewOffsetCursorMax || strconv.Itoa(n) != cursor {
		return 0, problem(400, "invalid_cursor", "cursor is the next_cursor of the previous page.")
	}
	return n, nil
}

func pageLimit(limit, def, max int) int {
	if limit <= 0 {
		return def
	}
	return min(limit, max)
}

func (s *Store) readSubject(ctx context.Context, tx *sql.Tx, c Command, now int64) (Result, error) {
	var sub reviews.Subject
	if reviews.ValidID(c.Target) {
		var ok bool
		var err error
		if sub, ok, err = lookupSubject(ctx, tx, c.Target); err != nil {
			return Result{}, err
		} else if !ok {
			return Result{}, problem(404, "subject_not_found", "No review names this subject yet. Read a subject by its TYPE:KEY to see it before its first review; write_review (or a signed post in room reviews) writes the first.")
		}
	} else {
		var err error
		if sub, err = reviews.Resolve(c.Target, s.subjectOptions()); err != nil {
			return Result{}, reviewError(err)
		}
	}
	offset, err := offsetCursor(c.Cursor)
	if err != nil {
		return Result{}, err
	}
	limit := pageLimit(c.Limit, ReviewPageDefault, ReviewPageMax)
	info, err := subjectInfo(ctx, tx, sub)
	if err != nil {
		return Result{}, err
	}
	if info.Owners, err = subjectOwners(ctx, tx, sub); err != nil {
		return Result{}, err
	}
	data := map[string]any{"subject": info, "text_is_untrusted": true, "order": reviewOrderNote()}
	if !info.Exists {
		data["reviews"], data["aggregate"], data["has_more"] = []ReviewCard{}, emptyAggregate(), false
		return Result{Data: data}, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+reviewColumns+" FROM reviews r JOIN subjects s ON s.id=r.subject JOIN events e ON e.id=r.event_id WHERE r.subject=? AND r.current=1 AND e.hidden=0 ORDER BY r.created_at DESC, r.event_id", sub.ID)
	if err != nil {
		return Result{}, err
	}
	var all []reviewRow
	for rows.Next() {
		row, err := scanReview(rows)
		if err != nil {
			rows.Close()
			return Result{}, err
		}
		all = append(all, row)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Result{}, err
	}
	cards := make([]*MessageReview, len(all))
	ids := make([]string, len(all))
	for i := range all {
		cards[i], ids[i] = &all[i].card.MessageReview, all[i].card.ID
	}
	if err = loadEvidence(ctx, tx, cards, ids); err != nil {
		return Result{}, err
	}
	var lab, outside, community []reviewRow
	for _, r := range all {
		switch r.card.Relationship {
		case "lab":
			lab = append(lab, r)
		case "outside_source":
			outside = append(outside, r)
		default:
			community = append(community, r)
		}
	}
	agg := orderReviews(community, now)
	owners := make([]string, 0, len(info.Owners))
	for _, o := range info.Owners {
		owners = append(owners, o.Account)
	}
	finish := func(list []reviewRow) ([]ReviewCard, error) {
		out := make([]ReviewCard, 0, len(list))
		for _, r := range list {
			card := r.card
			if err := reviewThread(ctx, tx, &card, owners); err != nil {
				return nil, err
			}
			out = append(out, card)
		}
		return out, nil
	}
	end := min(offset+limit, len(community))
	if offset > len(community) {
		offset = end
	}
	page, err := finish(community[offset:end])
	if err != nil {
		return Result{}, err
	}
	data["reviews"], data["aggregate"], data["has_more"] = page, agg, end < len(community)
	if end < len(community) {
		data["next_cursor"] = strconv.Itoa(end)
	}
	if len(lab) > 0 {
		runs, err := finish(lab[:min(len(lab), labRunsShown)])
		if err != nil {
			return Result{}, err
		}
		data["lab"], data["lab_runs"] = runs[0], runs
	}
	if len(outside) > 0 {
		others, err := finish(outside)
		if err != nil {
			return Result{}, err
		}
		data["other_raters"] = others
	}
	return Result{Data: data}, nil
}

func emptyAggregate() ReviewAggregate {
	return ReviewAggregate{ByClass: map[string]int{}, ByVerdict: map[string]int{}, VerdictMean: ReviewPriorValue, Prior: priorNote()}
}

func priorNote() string {
	return fmt.Sprintf("%g pseudo-reviews at %g", ReviewPriorWeight, ReviewPriorValue)
}

func reviewOrderNote() map[string]any {
	return map[string]any{"version": ReviewOrderVersion, "weight": "evidence class × v(standing) × relationship × decay, capped per subject per UTC day",
		"evidence_weights": ReviewEvidenceWeights, "relationship_factors": ReviewRelationshipFactors, "v": "1 for an account with a public post a day old when it reviewed, else 0 (today's rule until the standing cutover)",
		"half_life_days": ReviewHalfLifeDays, "burst_cap": ReviewBurstCap, "burst_days": ReviewBurstDays, "verdict_values": map[string]any{"good": 1, "mixed": 0.5, "bad": 0, "unusable": 0, "unverified": "not counted"},
		"separate": "lab assessments and other raters are shown apart and never in this order or its aggregate"}
}

// orderReviews weighs the community reviews (RFC 0015 §5.4), sorts them by
// weight then recency, and sums them.
func orderReviews(list []reviewRow, now int64) ReviewAggregate {
	agg := emptyAggregate()
	// Evidence cited by two reviews counts once: the later review's share is
	// zero for that object (both still show).
	owner := map[string]string{}
	for i := len(list) - 1; i >= 0; i-- { // oldest first
		r := &list[i].card
		class := r.EvidenceClass
		o := &ReviewOrder{Class: class, ClassWeight: ReviewEvidenceWeights[class], V: 0, RelationshipFactor: 1, Decay: math.Exp2(-float64(now-r.CreatedAt) / (ReviewHalfLifeDays * 86400)), BurstScale: 1}
		if list[i].seasoned {
			o.V = 1
		}
		rel := r.Relationship
		if r.RelationshipRaised != "" {
			rel = r.RelationshipRaised
		}
		if f, ok := ReviewRelationshipFactors[rel]; ok {
			o.RelationshipFactor = f
		}
		for _, e := range r.Evidence {
			if e.State != "verified" {
				continue
			}
			if first, dup := owner[e.ID]; dup && first != r.ID {
				o.ClassWeight, o.Note = ReviewEvidenceWeights["opinion"], "evidence already counted for another review"
				break
			}
			owner[e.ID] = r.ID
		}
		r.Order = o
	}
	// The burst cap: fresh reviews add at most ReviewBurstCap per UTC day.
	byDay := map[int64]float64{}
	for i := range list {
		o, r := list[i].card.Order, list[i].card
		o.Weight = o.ClassWeight * o.V * o.RelationshipFactor * o.Decay
		if now-r.CreatedAt < ReviewBurstDays*86400 {
			byDay[r.CreatedAt/86400] += o.Weight
		}
	}
	for i := range list {
		o, r := list[i].card.Order, list[i].card
		if total := byDay[r.CreatedAt/86400]; now-r.CreatedAt < ReviewBurstDays*86400 && total > ReviewBurstCap {
			o.BurstScale = ReviewBurstCap / total
			o.Weight *= o.BurstScale
		}
		o.Weight = math.Round(o.Weight*1e6) / 1e6
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].card.Order.Weight > list[j].card.Order.Weight })
	accounts := map[string]bool{}
	sum, dimW := 0.0, map[string]float64{}
	agg.Dimensions = map[string]float64{}
	for _, r := range list {
		c := r.card
		agg.Reviews++
		accounts[c.Author.Account] = true
		agg.ByClass[c.EvidenceClass]++
		agg.ByVerdict[c.Verdict]++
		w := c.Order.Weight
		if v, ok := reviews.VerdictValue(c.Verdict); ok && w > 0 {
			agg.WeightTotal += w
			sum += w * v
		}
		for k, v := range c.Dimensions {
			if w > 0 {
				agg.Dimensions[k] += w * float64(v)
				dimW[k] += w
			}
		}
	}
	agg.Accounts = len(accounts)
	agg.VerdictMean = math.Round((sum+ReviewPriorWeight*ReviewPriorValue)/(agg.WeightTotal+ReviewPriorWeight)*1e4) / 1e4
	agg.WeightTotal = math.Round(agg.WeightTotal*1e6) / 1e6
	for k := range agg.Dimensions {
		agg.Dimensions[k] = math.Round(agg.Dimensions[k]/dimW[k]*10) / 10
	}
	if len(agg.Dimensions) == 0 {
		agg.Dimensions = nil
	}
	return agg
}

// reviewThread counts a review's replies and finds its owner's pinned reply.
func reviewThread(ctx context.Context, tx *sql.Tx, card *ReviewCard, owners []string) error {
	if err := tx.QueryRowContext(ctx, `WITH RECURSIVE t(id) AS (SELECT id FROM events WHERE id=?1 OR origin=?1
 UNION SELECT e.id FROM events e JOIN t ON e.reply_to=t.id WHERE e.room=?2 LIMIT ?3)
 SELECT count(*) FROM events e JOIN t ON t.id=e.id WHERE e.reply_to<>'' AND e.hidden=0 AND e.supersedes=''`, card.Root, ReviewsRoom, reviewThreadCountMax).Scan(&card.Replies); err != nil {
		return err
	}
	if len(owners) == 0 {
		return nil
	}
	args := []any{card.Root, card.Root}
	for _, o := range owners {
		args = append(args, o)
	}
	var reply OwnerReply
	err := tx.QueryRowContext(ctx, `SELECT e.id,e.account,coalesce((SELECT i.handle FROM identities i WHERE i.id=e.author),''),
 coalesce((SELECT v.text FROM events v WHERE (v.id=e.id OR v.origin=e.id) AND v.hidden=0 ORDER BY v.seq DESC LIMIT 1),e.text),e.created_at
 FROM events e WHERE e.reply_to IN (SELECT id FROM events WHERE id=? OR origin=?) AND e.hidden=0 AND e.supersedes='' AND e.account IN (`+placeholders(len(owners))+`) ORDER BY e.seq DESC LIMIT 1`, args...).Scan(&reply.ID, &reply.Account, &reply.Handle, &reply.Text, &reply.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	card.OwnerReply = &reply
	return nil
}

// SubjectListing is one row of subjects.list.
type SubjectListing struct {
	SubjectRef
	Reviews      int    `json:"reviews"`
	Accounts     int    `json:"accounts"`
	LastReviewAt int64  `json:"last_review_at"`
	Lab          string `json:"lab_verdict,omitempty"`
}

func (s *Store) listSubjects(ctx context.Context, tx *sql.Tx, c Command) (Result, error) {
	if c.Kind != "" && !slices.Contains(reviews.Types, c.Kind) {
		return Result{}, problem(400, "invalid_kind", "kind is a subject type: "+strings.Join(reviews.Types, ", ")+".")
	}
	if len(c.Query) > SubjectQueryMaxBytes || !utf8.ValidString(c.Query) {
		return Result{}, problem(400, "invalid_query", fmt.Sprintf("query is at most %d bytes of text.", SubjectQueryMaxBytes))
	}
	sortBy := "reviewed"
	if c.Data != "" {
		var d struct {
			Schema int    `json:"schema"`
			Sort   string `json:"sort"`
		}
		if json.Unmarshal([]byte(c.Data), &d) != nil || d.Schema != 1 || (d.Sort != "reviewed" && d.Sort != "recent") {
			return Result{}, problem(400, "invalid_request", `subjects.list data is {"schema":1,"sort":"reviewed"|"recent"}.`)
		}
		sortBy = d.Sort
	}
	offset, err := offsetCursor(c.Cursor)
	if err != nil {
		return Result{}, err
	}
	limit := pageLimit(c.Limit, SubjectsPageDefault, SubjectsPageMax)
	order := "n DESC, last DESC, s.id"
	if sortBy == "recent" {
		order = "last DESC, s.id"
	}
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(c.Query)) + "%"
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.type,s.key,count(r.event_id) AS n,count(DISTINCT r.account),coalesce(max(r.created_at),0) AS last,
 coalesce((SELECT l.verdict FROM reviews l JOIN events le ON le.id=l.event_id WHERE l.subject=s.id AND l.current=1 AND l.relationship='lab' AND le.hidden=0 ORDER BY l.created_at DESC LIMIT 1),'')
 FROM subjects s LEFT JOIN reviews r ON r.subject=s.id AND r.current=1 AND r.relationship NOT IN ('lab','outside_source') AND EXISTS(SELECT 1 FROM events e WHERE e.id=r.event_id AND e.hidden=0)
 WHERE s.listed_at>0 AND (?='' OR s.type=?) AND (?='' OR lower(s.key) LIKE ? ESCAPE '\')
 GROUP BY s.id ORDER BY `+order+` LIMIT ? OFFSET ?`, c.Kind, c.Kind, c.Query, like, limit+1, offset)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	list := []SubjectListing{}
	for rows.Next() {
		var l SubjectListing
		var sub reviews.Subject
		if err = rows.Scan(&sub.ID, &sub.Type, &sub.Key, &l.Reviews, &l.Accounts, &l.LastReviewAt, &l.Lab); err != nil {
			return Result{}, err
		}
		l.SubjectRef = subjectRef(sub)
		list = append(list, l)
	}
	if err = rows.Err(); err != nil {
		return Result{}, err
	}
	data := map[string]any{"subjects": list, "has_more": false, "sort": sortBy}
	if len(list) > limit {
		data["subjects"], data["has_more"], data["next_cursor"] = list[:limit], true, strconv.Itoa(offset+limit)
	}
	return Result{Data: data}, nil
}

// ReviewSummary is the compact reviews summary an x402 resource and
// subject.resolve carry.
type ReviewSummary struct {
	Reviews   int            `json:"reviews"`
	Accounts  int            `json:"accounts"`
	ByVerdict map[string]int `json:"by_verdict"`
	Lab       map[string]any `json:"lab,omitempty"`
	URL       string         `json:"url"`
}

// reviewSummaries summarises the current community reviews and the newest
// lab assessment of each subject id that has any.
func reviewSummaries(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, ids []string) (map[string]ReviewSummary, error) {
	out := map[string]ReviewSummary{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, `SELECT r.subject,r.account,r.verdict,r.relationship,r.score,r.grade FROM reviews r JOIN events e ON e.id=r.event_id
 WHERE r.subject IN (`+placeholders(len(ids))+`) AND r.current=1 AND e.hidden=0 AND r.relationship<>'outside_source' ORDER BY r.created_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	accounts := map[string]map[string]bool{}
	for rows.Next() {
		var subject, account, verdict, rel, grade string
		var score int
		if err = rows.Scan(&subject, &account, &verdict, &rel, &score, &grade); err != nil {
			return nil, err
		}
		sum, ok := out[subject]
		if !ok {
			sum = ReviewSummary{ByVerdict: map[string]int{}, URL: "/s/" + subject}
			accounts[subject] = map[string]bool{}
		}
		if rel == "lab" {
			sum.Lab = map[string]any{"verdict": verdict, "lab_verdict": reviews.LabVerdicts[verdict]}
			if score >= 0 {
				sum.Lab["score"] = score
			}
			if grade != "" {
				sum.Lab["grade"] = grade
			}
		} else {
			sum.Reviews++
			sum.ByVerdict[verdict]++
			accounts[subject][account] = true
			sum.Accounts = len(accounts[subject])
		}
		out[subject] = sum
	}
	return out, rows.Err()
}

// reviewSummarizer is reviewSummaries for the x402 relay's resources read
// (services.ReviewSummarizer, handed over while REVIEWS is on), on the
// read's own querier.
type reviewSummarizer struct{}

// reviewSummarizer is the x402 relay's reviews index while REVIEWS is on.
func (s *Store) reviewSummarizer() services.ReviewSummarizer {
	if !s.ReviewsEnabled() {
		return nil
	}
	return reviewSummarizer{}
}

func (reviewSummarizer) ReviewSummaries(ctx context.Context, q allowance.Querier, ids []string) (map[string]any, error) {
	sums, err := reviewSummaries(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(sums))
	for id, s := range sums {
		out[id] = s
	}
	return out, nil
}

// SubjectsCapabilities is /capabilities subjects; nil while REVIEWS is off.
func (s *Store) SubjectsCapabilities() map[string]any {
	if !s.ReviewsEnabled() {
		return nil
	}
	return map[string]any{
		"types": reviews.Types, "deferred": []string{"place"},
		"uri":      "TYPE:KEY: x402:GET https://host/path, domain:example.com, site:https://example.com/, mcp:https://host/path or mcp:io.github.owner/server, agent:ACCOUNT_FINGERPRINT, pkg:npm/%40scope/name (a purl without its version)",
		"id":       `base32(SHA-256(canonical JSON ["swarmmemo.subject.v1",TYPE,KEY]))[:26], lowercase`,
		"resolve":  "/api/subjects/resolve?uri=URI", "get": "/api/subjects/ID", "list": "/api/subjects?type=TYPE&q=TEXT&sort=reviewed|recent", "page": "/s/ID", "search": "/s?q=TEXT",
		"mcp":      []string{"resolve_subject", "read_subject", "search_subjects"},
		"listed":   "in subjects.list and search engines once reviewed by two accounts or by its owner; before that its page is noindex",
		"refused":  "people: email addresses, phone numbers, profile-shaped URL paths (a first segment in, u, user, users, profile, people, member or starting with @ or ~), this board's own account pages, IP addresses and special-use names (subject_refused, 422)",
		"ownership": "derived from identity links: a verified domain link owns its domain and the URLs on it; an agent owns its own agent subject",
	}
}

// ReviewsCapabilities is /capabilities reviews; nil while REVIEWS is off.
func (s *Store) ReviewsCapabilities() map[string]any {
	if !s.ReviewsEnabled() {
		return nil
	}
	rubrics := []map[string]any{}
	for _, r := range reviews.Rubrics() {
		rubrics = append(rubrics, map[string]any{"id": r.ID, "version": r.Version, "sha256": r.SHA256, "subject_types": r.SubjectTypes, "url": r.Path(), "page": "/rubrics/" + r.ID, "title": r.Doc.Title})
	}
	return map[string]any{
		"room": ReviewsRoom, "kind": "review", "page": "the subject's id", "docs": "/protocol.md#subjects-and-reviews",
		"data":          `{"schema":1,"review":{"subject":"TYPE:KEY","verdict":"good"}} with optional dimensions, relationship, evidence, observations, rubric, record; score, band, grade, flags for relationship lab; source for outside_source`,
		"data_max_bytes": maxPostData, "verdicts": reviews.Verdicts, "lab_verdicts": reviews.LabVerdicts, "dimensions": reviews.Dimensions, "relationships": reviews.Relationships,
		"evidence": map[string]any{"kinds": reviews.EvidenceKinds, "classes": ReviewEvidenceWeights, "max": reviews.MaxEvidence, "within_days": ReviewEvidenceDays, "checked": "call, fetch, work and stamp in the posting transaction; tx is recorded as unknown for now"},
		"caps":     map[string]any{"per_account_per_day": ReviewsPerAccountDay, "observations": reviews.MaxObservations, "flags": reviews.MaxFlags, "current_per_account_and_subject": 1},
		"signed": true, "worker_keys": false, "outside_source": "operator importer keys only",
		"order": reviewOrderNote(), "rubrics": rubrics,
		"write": "write_review (MCP, hosted identity), or a signed post: room reviews, kind review, page SUBJECT_ID, data.review",
		"comments": "ordinary replies in the review's thread; the subject owner's newest reply is pinned under the review",
	}
}
