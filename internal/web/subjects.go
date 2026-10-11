package web

// Subject pages (RFC 0015 §6.1, RFC 0019): /s/ID, a subject with its lab
// assessment, other raters and community reviews; /s?q=, search; the
// review card on /e/ID; and /rubrics/ID, a rubric's method page, with its
// exact bytes at /rubrics/ID.json and /rubrics/ID/VERSION.json. Every page
// is server-rendered and reads without JavaScript. REVIEWS gates them all.

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"swarmmemo/internal/board"
	"swarmmemo/internal/reviews"
)

// subjectPage is /s/ID as the template shows it.
type subjectPage struct {
	Info board.SubjectInfo
	// Lab is the newest lab assessment whose record recomputed; without
	// one, SelfReported is the newest lab run, shown as its lab's claim.
	Lab          *board.ReviewCard
	SelfReported *board.ReviewCard
	LabRuns      int
	Others       []board.ReviewCard
	Reviews      []board.ReviewCard
	Aggregate    board.ReviewAggregate
	Counts       string
	Verdicts     string
	NextCursor   string
	Relay        bool
	// Reward is the standing review reward offer while slots are left.
	Reward *board.ReviewRewardHint
	// Embed is the subject's "Rated on SwarmMemo" badge to copy.
	Embed *badgeEmbed
}

// subjectSearch is /s and /s?q=.
type subjectSearch struct {
	Query    string
	Resolved *board.SubjectInfo
	Refusal  string
	Results  []board.SubjectListing
}

// reviewFacts is a review's structured part, ready for the page.
type reviewFacts struct {
	Verdict, LabVerdict, Relationship, Raised string
	// Lab is a lab assessment; Recomputed, that its record recomputed to
	// what it claims. A lab without it is self-reported.
	Lab, Recomputed bool
	Score           *int
	Band            []int
	Grade           string
	Flags           []string
	Dims            []dimFact
	Evidence        []evidenceFact
	Rubric          *board.ReviewRubric
	Record          *evidenceFact
	Source          *reviews.Source
	Observations    int
}

type dimFact struct {
	Name  string
	Value int
}

type evidenceFact struct {
	ID, Class, State, Note string
}

var dimensionLabels = map[string]string{"works": "Works", "as_described": "As described", "safe": "Safe", "good": "Good"}

// evidenceNotes say in words what a check found.
var evidenceNotes = map[string]string{
	"not_your_call": "not a call of this reviewer's", "not_a_finished_x402_call": "not a finished x402 call", "older_than_30_days": "older than 30 days",
	"call_to_another_resource": "a call to another resource", "not_your_fetch": "not a fetch of this reviewer's", "not_a_finished_fetch": "not a finished fetch",
	"fetch_of_another_site": "a fetch of another site", "not_your_public_work": "not public work of this reviewer's", "request_does_not_name_the_subject": "the request does not name this subject",
	"not_your_stamp": "not a stamp of this reviewer's", "on-chain payments are not checked yet": "on-chain payments are not checked yet",
	"not_your_doc_version": "no such doc version of this reviewer's", "record_unreadable": "the record could not be read", "not_observations_of_this_rubric": "not observations for this rubric",
	"recomputed_result_differs":       "recomputed with the rubric, the result differs",
	board.RecordPending:               "waiting to be recomputed",
	"observations_of_another_subject": "observations of another subject", "as_of_after_the_review": "observations dated after the review", "as_of_older_than_30_days": "observations older than 30 days",
	"work_not_accepted_between_two_accounts": "not accepted work between two accounts", "work_reward_not_paid": "no reward paid for this work",
	"stamp_not_of_a_cited_fetch": "not a stamp of a cited fetch: weighs as opinion",
	"not_a_paid_offering_call":   "not an offering call whose payment settled", "paid_from_the_providers_wallet": "paid from the provider's own wallet",
}

// reviewFactsOf prepares a review card (board.ReviewCard) or a post's review
// (*board.MessageReview) for the page.
func reviewFactsOf(v any) *reviewFacts {
	var m *board.MessageReview
	switch r := v.(type) {
	case board.ReviewCard:
		m = &r.MessageReview
	case *board.ReviewCard:
		m = &r.MessageReview
	case *board.MessageReview:
		m = r
	}
	if m == nil {
		return nil
	}
	f := &reviewFacts{Verdict: m.Verdict, LabVerdict: m.LabVerdict, Relationship: m.Relationship, Raised: m.RelationshipRaised, Lab: m.Relationship == "lab",
		Score: m.Score, Band: m.Band, Grade: m.Grade, Flags: m.Flags, Rubric: m.Rubric, Source: m.Source, Observations: len(m.Observations)}
	for _, d := range reviews.Dimensions {
		if v, ok := m.Dimensions[d]; ok {
			f.Dims = append(f.Dims, dimFact{dimensionLabels[d], v})
		}
	}
	for _, e := range m.Evidence {
		reason, _ := e.Detail["reason"].(string)
		if note, ok := e.Detail["note"].(string); ok {
			reason = note
		}
		fact := evidenceFact{ID: e.ID, Class: strings.ReplaceAll(e.Class, "_", " "), State: e.State, Note: evidenceNotes[reason]}
		if e.Detail["counted_for"] != nil {
			fact.Note = "already counted for another review"
		}
		if strings.HasPrefix(e.ID, board.RecordEvidencePrefix) {
			if e.State == "verified" {
				f.Recomputed = true
				fact.Note = "the score, band, grade, verdict and flags recompute from the lab's published observations with the rubric; that checks the arithmetic, not the observations"
			}
			f.Record = &fact
			continue
		}
		f.Evidence = append(f.Evidence, fact)
	}
	return f
}

// countsLine leads a subject's community reviews with counts (RFC 0015 §5.4).
func countsLine(a board.ReviewAggregate) (string, string) {
	if a.Reviews == 0 {
		return "", ""
	}
	plural := func(n int, one, many string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, one)
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	line := plural(a.Reviews, "review", "reviews") + " from " + plural(a.Accounts, "account", "accounts")
	var parts []string
	for _, c := range []struct{ key, label string }{{"paid_use", "with paid use"}, {"work", "with work"}, {"observed", "observed"}, {"free_use", "with free use"}, {"opinion", "opinion only"}} {
		if n := a.ByClass[c.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, c.label))
		}
	}
	if len(parts) > 0 {
		line += ": " + strings.Join(parts, ", ")
	}
	var verdicts []string
	for _, v := range reviews.Verdicts {
		if n := a.ByVerdict[v]; n > 0 {
			verdicts = append(verdicts, fmt.Sprintf("%d %s", n, v))
		}
	}
	return line + ".", strings.Join(verdicts, " · ")
}

// loadSubject fills p from subject.get; the status is the page's.
func loadSubject(r *http.Request, p *page, execute func(board.Command) (board.Result, error)) int {
	id := strings.TrimPrefix(r.URL.Path, "/s/")
	missing := func(status int, notice string) int {
		p.View, p.NoIndex, p.Title, p.Notice = "missing", true, "Subject not found", notice
		return status
	}
	if !reviews.ValidID(id) {
		return missing(404, "")
	}
	res, err := execute(board.Command{Operation: "subject.get", Target: id, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		var be *board.Error
		if errors.As(err, &be) && be.Status < 500 {
			return missing(404, "No review names this subject yet. Search for it, or write the first review.")
		}
		return missing(503, "Subjects are temporarily unavailable.")
	}
	v := &subjectPage{}
	v.Info, _ = res.Data["subject"].(board.SubjectInfo)
	if lab, ok := res.Data["lab"].(board.ReviewCard); ok {
		v.Lab = &lab
	}
	runs, _ := res.Data["lab_runs"].([]board.ReviewCard)
	v.LabRuns = len(runs)
	if v.Lab == nil && len(runs) > 0 {
		v.SelfReported = &runs[0]
	}
	v.Others, _ = res.Data["other_raters"].([]board.ReviewCard)
	v.Reviews, _ = res.Data["reviews"].([]board.ReviewCard)
	v.Aggregate, _ = res.Data["aggregate"].(board.ReviewAggregate)
	v.NextCursor, _ = res.Data["next_cursor"].(string)
	v.Reward, _ = res.Data["review_reward"].(*board.ReviewRewardHint)
	v.Counts, v.Verdicts = countsLine(v.Aggregate)
	v.Relay = v.Info.Type == reviews.TypeX402
	v.Embed = newBadgeEmbed("/s/"+v.Info.ID+"/badge.svg", "/s/"+v.Info.ID, v.Info.ID)
	p.View, p.SubjectView = "subject", v
	p.Title = v.Info.URI
	p.Description = "Reviews of " + v.Info.URI + " on SwarmMemo: signed, with checked evidence, lab assessments and other raters kept apart."
	if v.Counts != "" {
		p.Description = v.Info.URI + ": " + v.Counts
	}
	p.Canonical = v.Info.URL
	// A subject is indexed once it is listed (two accounts or its owner
	// reviewed it, RFC 0015 §4.3).
	if !v.Info.Listed {
		p.NoIndex = true
	}
	return 200
}

// loadSubjectSearch fills p for /s?q=: what the query names, and the
// listed subjects whose key holds it.
func loadSubjectSearch(r *http.Request, p *page, execute func(board.Command) (board.Result, error)) int {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	v := &subjectSearch{Query: q}
	p.View, p.SubjectSearch = "subjects", v
	p.Title = "Subjects"
	p.Description = "Find reviews of x402 APIs, MCP servers, sites, packages and agents: signed reviews with checked evidence, and lab assessments beside them."
	if len(q) > board.SubjectQueryMaxBytes {
		v.Refusal = fmt.Sprintf("A search is at most %d bytes.", board.SubjectQueryMaxBytes)
		return 400
	}
	if q != "" {
		if res, err := execute(board.Command{Operation: "subject.resolve", Target: q}); err == nil {
			if info, ok := res.Data["subject"].(board.SubjectInfo); ok {
				v.Resolved = &info
			}
		}
	}
	res, err := execute(board.Command{Operation: "subjects.list", Query: q, Limit: board.SubjectsPageMax})
	if err != nil {
		var be *board.Error
		if errors.As(err, &be) && be.Status < 500 {
			v.Refusal = be.Message
			return be.Status
		}
		p.Notice = "Subjects are temporarily unavailable."
		return 503
	}
	v.Results, _ = res.Data["subjects"].([]board.SubjectListing)
	return 200
}

// rubricPage is /rubrics/ID as the template shows it.
type rubricPage struct {
	R    *reviews.Rubric
	Dims []rubricDim
}

type rubricDim struct {
	ID, Label string
	Weight    float64
	Checks    []rubricCheckRow
}

type rubricCheckRow struct {
	ID, Title, Measure, Validity, Kind, Metric, OnRevision string
	Weight, HalfLife                                       float64
}

// rubricRoute serves a rubric's bytes: /rubrics/ID.json (the newest version)
// and /rubrics/ID/VERSION.json. ok is false for any other path.
func rubricRoute(w http.ResponseWriter, r *http.Request) bool {
	rest, found := strings.CutPrefix(r.URL.Path, "/rubrics/")
	if !found || !strings.HasSuffix(rest, ".json") {
		return false
	}
	id, version, _ := strings.Cut(strings.TrimSuffix(rest, ".json"), "/")
	rb := reviews.LookupRubric(id, version)
	if rb == nil || strings.Contains(version, "/") || (version == "" && strings.Contains(rest, "/")) {
		http.Error(w, "No such rubric; /capabilities reviews.rubrics lists them.", http.StatusNotFound)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("ETag", `"`+rb.SHA256+`"`)
	w.Header().Set("X-Rubric-SHA256", rb.SHA256)
	if version != "" {
		w.Header().Set("Cache-Control", "public, max-age=86400")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}
	if r.Method == http.MethodHead {
		return true
	}
	_, _ = w.Write(rb.Raw)
	return true
}

// loadRubric fills p for /rubrics/ID and /rubrics/ID/VERSION.
func loadRubric(r *http.Request, p *page) int {
	id, version, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/rubrics/"), "/")
	rb := reviews.LookupRubric(id, version)
	if rb == nil || strings.Contains(version, "/") {
		p.View, p.NoIndex, p.Title = "missing", true, "Rubric not found"
		return 404
	}
	v := &rubricPage{R: rb}
	for _, d := range rb.Dimensions {
		dim := rubricDim{ID: d.ID, Label: dimensionLabels[d.ID], Weight: d.Weight}
		for _, c := range rb.Checks {
			if c.Dimension != d.ID {
				continue
			}
			doc := rb.CheckDoc(c.ID)
			dim.Checks = append(dim.Checks, rubricCheckRow{ID: c.ID, Title: doc.Title, Measure: doc.Measure, Validity: doc.Validity, Kind: c.Kind, Metric: c.Metric, OnRevision: c.OnRevision, Weight: c.Weight, HalfLife: c.HalfLife})
		}
		v.Dims = append(v.Dims, dim)
	}
	p.View, p.RubricView = "rubric", v
	p.Title = "Rubric: " + rb.Doc.Title + " " + rb.Version
	p.Description = rb.Doc.Description
	p.Canonical = "/rubrics/" + url.PathEscape(rb.ID) + "/" + url.PathEscape(rb.Version)
	return 200
}
