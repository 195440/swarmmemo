package find

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Source gives every doc to index, read fresh from the single sources.
type Source func(ctx context.Context) ([]Doc, error)

// Config is an engine's wiring.
type Config struct {
	Source Source
	Model  Model // nil: the rules parse every query
	Vocab  Vocab
	// TTL is how long an index serves before a rebuild starts (in the
	// background: the old one serves meanwhile).
	TTL time.Duration
	Now func() time.Time
}

// Index lifetimes.
const (
	DefaultTTL   = 5 * time.Minute
	buildTimeout = 60 * time.Second
	closeAfter   = 2 * time.Minute // an index replaced is closed this much later
	matchWordMax = 12
	// After a source changed (Invalidate), the next search waits up to
	// freshWait for a rebuild. Rebuilds after changes start at most once per
	// freshEvery: one that must wait for its turn runs in the background,
	// and searches meanwhile are served from the index they have.
	freshWait  = 3 * time.Second
	freshEvery = 15 * time.Second
)

// Engine is Find: an intent parser and an index, rebuilt from Source.
type Engine struct {
	cfg    Config
	intent *Intent

	cur      atomic.Pointer[Index]
	dirty    atomic.Bool // a source changed since the current index was built
	mu       sync.Mutex
	building chan struct{} // closed when the running build ends; nil when none runs
	delay    time.Duration // how long the running build waits before it starts
	freshAt  time.Time     // when the last rebuild after a change starts
	buildErr error
}

// NewEngine is an engine; nothing is built until the first search.
func NewEngine(cfg Config) *Engine {
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultTTL
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Engine{cfg: cfg, intent: NewIntent(cfg.Model, cfg.Vocab, cfg.Now)}
}

// Vocab is the engine's category vocabulary.
func (e *Engine) Vocab() Vocab { return e.cfg.Vocab }

// Close frees the current index.
func (e *Engine) Close() {
	if ix := e.cur.Swap(nil); ix != nil {
		ix.Close()
	}
}

// Invalidate says a source changed: the next search rebuilds first (within
// freshWait), so what was just published is found.
func (e *Engine) Invalidate() { e.dirty.Store(true) }

// index is a fresh enough index: the current one while it is young and no
// source changed; after a change, a rebuild waited for briefly (unless it
// must wait its turn); when it is old, the current one while a rebuild
// runs; with none yet, the one being built (waited for).
func (e *Engine) index(ctx context.Context) (*Index, error) {
	ix := e.cur.Load()
	if ix != nil && e.dirty.Load() {
		done, delay := e.startBuild(true)
		if delay > 0 {
			return ix, nil
		}
		wait := time.NewTimer(freshWait)
		defer wait.Stop()
		select {
		case <-done:
			if fresh := e.cur.Load(); fresh != nil {
				return fresh, nil
			}
		case <-wait.C:
		case <-ctx.Done():
		}
		return ix, nil
	}
	if ix != nil && e.cfg.Now().Sub(ix.BuiltAt) < e.cfg.TTL {
		return ix, nil
	}
	done, _ := e.startBuild(false)
	if ix != nil {
		return ix, nil
	}
	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if ix = e.cur.Load(); ix != nil {
		return ix, nil
	}
	e.mu.Lock()
	err := e.buildErr
	e.mu.Unlock()
	if err == nil {
		err = errors.New("find: index unavailable")
	}
	return nil, err
}

// startBuild starts a build unless one runs; it returns the running
// build's done channel and how long it waits before it starts. A spaced
// build (after a change) starts no sooner than freshEvery after the last
// one: writes make the board rebuild at most that often, and the first
// change after a quiet spell is found at once.
func (e *Engine) startBuild(spaced bool) (chan struct{}, time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.building != nil {
		return e.building, e.delay
	}
	var delay time.Duration
	if spaced {
		if !e.freshAt.IsZero() {
			delay = max(0, freshEvery-time.Since(e.freshAt))
		}
		e.freshAt = time.Now().Add(delay)
	}
	done := make(chan struct{})
	e.building, e.delay = done, delay
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		e.dirty.Store(false)
		err := e.Rebuild(context.Background())
		e.mu.Lock()
		e.buildErr, e.building, e.delay = err, nil, 0
		e.mu.Unlock()
		close(done)
	}()
	return done, delay
}

// Rebuild builds a new index from the source now and swaps it in.
func (e *Engine) Rebuild(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, buildTimeout)
	defer cancel()
	docs, err := e.cfg.Source(ctx)
	if err != nil {
		return err
	}
	ix, err := Build(ctx, docs, e.cfg.Vocab, e.cfg.Now())
	if err != nil {
		return err
	}
	if old := e.cur.Swap(ix); old != nil {
		time.AfterFunc(closeAfter, func() { old.Close() })
	}
	return nil
}

// Find answers one search.
func (e *Engine) Find(ctx context.Context, req Request) (Answer, error) {
	q := normalizeSpace(req.Query)
	ans := Answer{Query: q, Order: OrderNote, Untrusted: true, Results: []Hit{}}
	var parsed Filters
	switch req.Parse {
	case ModeOff:
		ans.Text, ans.ParsedBy = q, ModeOff
	default:
		mode := req.Parse
		if mode == "" {
			mode = ModeAuto
		}
		p := e.intent.Parse(ctx, q, mode)
		parsed, ans.Text, ans.ParsedBy, ans.Cached, ans.ParseNote = p.filters, p.text, p.by, p.cached, p.note
	}
	ans.Filters = merge(parsed, req.Set, req.SetKeys)
	ix, err := e.index(ctx)
	if err != nil {
		return ans, err
	}
	words := MatchWords(ans.Text)
	cands, expanded, err := ix.search(ctx, words, ans.Filters)
	if err != nil {
		return ans, err
	}
	if inferredNarrows(parsed, req.SetKeys) {
		// A type, category or output read from the query (never one the
		// caller set) can hide the match: "csv" read as an output, "PyPI" as
		// a package, "agent" in an offering's name as the agent type. Search
		// the whole query without them too, and answer with that when the
		// filtered search matched nothing or matched clearly worse. A price,
		// network, protocol or sign-up door the query states is a limit,
		// kept, as is every filter the caller set.
		set := merge(Filters{MaxPrice: parsed.MaxPrice, Network: parsed.Network, Protocol: parsed.Protocol, Door: parsed.Door}, req.Set, req.SetKeys)
		all := MatchWords(q)
		wide, wideExpanded, err := ix.search(ctx, all, set)
		if err != nil {
			return ans, err
		}
		relax := len(wide) > 0 && len(cands) == 0
		if len(wide) > 0 && len(cands) > 0 && len(all) > 0 {
			// Both sides are scored on the whole query's words, so the words
			// the parse took out do not tip it by themselves.
			narrow, _, err := ix.search(ctx, all, ans.Filters)
			if err != nil {
				return ans, err
			}
			relax = clearlyBetter(wide, narrow)
		}
		if relax {
			cands, expanded, words = wide, wideExpanded, all
			ans.Filters, ans.Text = set, q
			ans.ParseNote = strings.TrimSpace(ans.ParseNote + " " + RelaxedNote)
		}
	}
	if len(expanded) > 0 {
		ans.Expanded = map[string]any{}
		for w, cats := range expanded {
			ans.Expanded[w] = cats
		}
	}
	ranked := ix.rank(cands, words, ans.Filters.Door != "", signupQuestion(q, ans.Filters.Door))
	ans.Matched = len(ranked)
	ans.Index = IndexInfo{BuiltAt: ix.BuiltAt.Unix(), Docs: ix.Counts, Match: "sqlite fts5 bm25, porter stemming, category synonyms"}
	limit := req.Limit
	if limit <= 0 {
		limit = PageDefault
	}
	limit = min(limit, PageMax)
	start := min(max(req.Offset, 0), len(ranked))
	end := min(start+limit, len(ranked))
	for _, s := range ranked[start:end] {
		ans.Results = append(ans.Results, s.hit(req.Explain))
	}
	if end < len(ranked) && end < OffsetMax {
		ans.NextCursor = strconv.Itoa(end)
	}
	return ans, nil
}

// RelaxRatio is how much stronger the whole query's best match, searched
// without the type, category and output the parse inferred, must be than the
// best match those filters admit (BM25, on the same words) for Find to answer
// with it. At 1.5 the filtered best is at most two thirds of the relaxed
// one: further apart than quality can ever move a result (QualityShift),
// and about what a second rare query word, or a word in the name rather
// than the description, is worth; a near tie keeps the filters the query
// asked for.
const RelaxRatio = 1.5

// inferredNarrows reports whether the parse read a type, category or output
// the caller did not set.
func inferredNarrows(parsed Filters, set map[string]bool) bool {
	return (len(parsed.Types) > 0 && !set["type"]) || (parsed.Category != "" && !set["category"]) || (parsed.Output != "" && !set["output"])
}

// clearlyBetter reports whether wide's best match beats narrow's by
// RelaxRatio. Candidates come best first; FTS5's bm25 is negative, lower is
// better, so -bm25 is the strength.
func clearlyBetter(wide, narrow []match) bool {
	if len(wide) == 0 {
		return false
	}
	if len(narrow) == 0 {
		return true
	}
	w, n := -wide[0].bm25, -narrow[0].bm25
	return w > 0 && w >= RelaxRatio*n
}

// MatchWords are the words of text a search matches: no filler, at most
// matchWordMax, each once.
func MatchWords(text string) []string {
	var out []string
	for _, w := range Words(text) {
		if stopWords[w] || len(w) < 2 || slices.Contains(out, w) {
			continue
		}
		if out = append(out, w); len(out) == matchWordMax {
			break
		}
	}
	return out
}

// ParseTypes reads a comma-separated type list ("service,agent"); "sites"
// style plurals and the chip names are accepted.
func ParseTypes(s string) []string {
	var out []string
	for _, t := range strings.Split(strings.ToLower(s), ",") {
		t = strings.TrimSpace(t)
		switch t {
		case "":
			continue
		case "services", "apis", "api", "tools", "tool":
			t = TypeService
		case "offerings":
			t = TypeOffering
		case "agents":
			t = TypeAgent
		case "sites":
			out = append(out, TypeSite)
			t = TypeDomain
		case "domains":
			t = TypeDomain
		case "packages", "package":
			t = TypePkg
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	return out
}
