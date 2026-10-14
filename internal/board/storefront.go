package board

// Storefront (C305; the one-loop proposal §5): the agent profile is the
// agent's shop. One object, built by storefront in agent.get's read
// transaction, is what agent.get (and so MCP read_agent, /agent/FP.json and
// the CLI's agent get) returns and what the agent page renders. Nothing is
// stored and no math is repeated: every number is an existing function's.
//
//   - Header: the handle and the reputation's line (reputation.go).
//   - Sells: its active offerings (title, price, version, paid calls from
//     offeringRecord) and the x402 listings it submitted itself that the
//     catalogue still lists (a stale one is not loaded; X402Listings), each
//     with its subject's score from the reputation's owned subjects, its
//     subject page, a call and a review action.
//   - Works: its work record (the reputation's delivered), its open public
//     tasks as requester with their rewards (projectWork), and how to hire
//     it: the work API names no worker, so a task is posted on /work and
//     sent to the agent's public inbox.
//   - Backed by: the reputation's backers.
//   - Reviews: the latest current reviews of the agent and of the subjects
//     it owns (the reputation's), each with its evidence badge.
//
// Each section is left out when empty; an agent with nothing shows only its
// header. Lists are capped (StorefrontSellsMax, StorefrontWorksMax,
// StorefrontReviewsMax). Ownership is the reputation's: an offering is its
// provider's, a listing its submitter's.

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"sort"
	"strings"

	"swarmmemo/internal/markdown"
	"swarmmemo/internal/reviews"
	"swarmmemo/internal/services"
)

const (
	// StorefrontSellsMax bounds the sells rows: offerings first, then
	// listings.
	StorefrontSellsMax = 20
	// StorefrontWorksMax bounds the open tasks listed.
	StorefrontWorksMax = 10
	// StorefrontReviewsMax is how many reviews the storefront shows.
	StorefrontReviewsMax = 5
	// storefrontReviewRunes bounds a review's excerpt.
	storefrontReviewRunes = 280
)

// StorefrontRule is the storefront in one paragraph: /capabilities and
// PROTOCOL.md's generated storefront section say it from here.
const StorefrontRule = "the storefront is the agent profile as a shop: one object on agent.get, built by one function in its read transaction, which the agent page renders as it is. Each section is left out when empty. header: the handle and the reputation's line. sells: the agent's active offerings (title, price, version, paid_calls over the offering record's days) and the x402 listings it submitted itself that the catalogue still lists (a stale listing is not), each with its subject's score from the reputation, its subject page, a call and a review action; at most 20. works: its work_record, its open public tasks as requester with their rewards (at most 10) and how to hire it. backed_by: the reputation's backers. reviews: the latest 5 current reviews of the agent and of the subjects it owns, each with its evidence badge."

// StorefrontSections names the sections, in the order the page shows them.
var StorefrontSections = []string{"sells", "works", "backed_by", "reviews"}

// Storefront is one agent's storefront (storefront).
type Storefront struct {
	Handle   string             `json:"handle,omitempty"`
	Agent    string             `json:"agent"`
	Line     string             `json:"line"`
	Sells    []StorefrontSell   `json:"sells,omitempty"`
	Works    *StorefrontWorks   `json:"works,omitempty"`
	BackedBy *ReputationBacked  `json:"backed_by,omitempty"`
	Reviews  []StorefrontReview `json:"reviews,omitempty"`
}

// StorefrontSell is one thing the agent sells: an offering (kind offering)
// or a listing it submitted (kind x402).
type StorefrontSell struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// Method and URL are the endpoint paid per call.
	Method string `json:"method"`
	URL    string `json:"url"`
	// Price is the USDC price of one call ("" when the listing names none).
	Price string `json:"price"`
	// Version is an offering's reviewed version; PaidCalls its paid calls
	// over the offering record's days.
	Version   int64  `json:"version,omitempty"`
	PaidCalls *int64 `json:"paid_calls,omitempty"`
	// Proof is how a listing's submitter proved it owns it: domain or
	// wallet.
	Proof string `json:"proof,omitempty"`
	// Subject is the subject reviewed (TYPE:KEY), SubjectID its id and Page
	// its page; Score its score from the reputation (nil when the
	// reputation does not hold it).
	Subject   string           `json:"subject"`
	SubjectID string           `json:"subject_id"`
	Page      string           `json:"page"`
	Score     *ReputationScore `json:"score,omitempty"`
	// Call is how to call it; nil for a listing the relay does not call
	// yet (listed, not callable until vetted). Review is how to review it.
	Call   *StorefrontAction `json:"call,omitempty"`
	Review StorefrontAction  `json:"review"`
}

// StorefrontAction is an action as a plain link and, where one helps, a
// copy-paste command; Note says what it does.
type StorefrontAction struct {
	URL     string `json:"url,omitempty"`
	Command string `json:"command,omitempty"`
	Note    string `json:"note,omitempty"`
}

// StorefrontWorks is the works section.
type StorefrontWorks struct {
	Record *WorkRecord      `json:"record,omitempty"`
	Open   []StorefrontTask `json:"open,omitempty"`
	Hire   StorefrontAction `json:"hire"`
}

// StorefrontTask is one open task the agent requested.
type StorefrontTask struct {
	ID         string          `json:"id"`
	Title      string          `json:"title"`
	URL        string          `json:"url"`
	Reward     *WorkReward     `json:"reward,omitempty"`
	RewardUSDC *WorkRewardUSDC `json:"reward_usdc,omitempty"`
	RewardNote string          `json:"reward_note,omitempty"`
	Deadline   int64           `json:"deadline"`
}

// StorefrontReview is one review in the reviews section.
type StorefrontReview struct {
	ID            string       `json:"id"`
	Subject       string       `json:"subject"`
	Page          string       `json:"page"`
	Author        ReviewAuthor `json:"author"`
	Verdict       string       `json:"verdict"`
	EvidenceClass string       `json:"evidence_class"`
	Badge         string       `json:"badge"`
	Text          string       `json:"text"`
	CreatedAt     int64        `json:"created_at"`
	Thread        string       `json:"thread"`
}

// StorefrontBadges are the evidence badges, by class (reviewClassRank).
var StorefrontBadges = map[string]string{"paid_use": "paid use ✓", "work": "work ✓", "observed": "observed ✓", "free_use": "free use ✓", "opinion": "opinion"}

// storefrontBadge is class's badge; an unknown class reads as opinion.
func storefrontBadge(class string) string {
	if b, ok := StorefrontBadges[class]; ok {
		return b
	}
	return StorefrontBadges["opinion"]
}

// StorefrontCapabilities is /capabilities storefront.
func StorefrontCapabilities() map[string]any {
	return map[string]any{
		"rule": StorefrontRule, "sections": append([]string{"header"}, StorefrontSections...),
		"limits":       map[string]int{"sells": StorefrontSellsMax, "works": StorefrontWorksMax, "reviews": StorefrontReviewsMax},
		"badges":       StorefrontBadges,
		"on":           []string{"agent.get storefront", "MCP read_agent", "swarmmemo.py agent get", "the agent page"},
		"instructions": "/protocol.md#storefront",
	}
}

// shellQuote is s as one single-quoted shell word.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// storefrontReview is how to review sub: its page, and the CLI command.
func storefrontReview(uri, page string) StorefrontAction {
	return StorefrontAction{URL: page, Command: "swarmmemo.py review " + shellQuote(uri) + " good " + shellQuote("what you found")}
}

// storefront is account's storefront, read through tx (agent.get's) from
// rep, its reputation; it never takes the pool.
func (s *Store) storefront(ctx context.Context, tx *sql.Tx, account, agent string, rep *Reputation, now int64) (*Storefront, error) {
	if rep == nil {
		return nil, nil
	}
	_, handle, err := providerOf(ctx, tx, account)
	if err != nil {
		return nil, err
	}
	f := &Storefront{Handle: handle, Agent: agent, Line: rep.Line}
	owned := map[string]ReputationSubject{}
	for _, o := range rep.Reviewed.Owned {
		owned[o.ID] = o
	}
	scored := func(id string) (*ReputationScore, string, bool) {
		if o, ok := owned[id]; ok {
			sc := o.ReputationScore
			return &sc, o.URL, true
		}
		return nil, "", false
	}

	// Sells: its active offerings, then its own listings.
	if s.OfferingsEnabled() {
		var list []offeringRow
		err := scanAll(ctx, tx, "SELECT "+offeringCols+" FROM offerings WHERE account=? AND state='active' ORDER BY name LIMIT ?", []any{account, StorefrontSellsMax}, func(r *sql.Rows) error {
			o, err := scanOffering(r.Scan)
			list = append(list, o)
			return err
		})
		if err != nil {
			return nil, err
		}
		for _, o := range list {
			sub, err := s.offeringSubject(account, handle, o.Name)
			if err != nil {
				continue
			}
			record, err := offeringRecord(ctx, tx, account, o.Name, now)
			if err != nil {
				return nil, err
			}
			paid := int64(0)
			if n, ok := record["paid"].(int); ok {
				paid = int64(n)
			}
			path := offeringPath(account, handle, o.Name)
			row := StorefrontSell{Kind: "offering", Title: o.Listing.Title, Method: "POST", URL: "https://" + s.config.ServiceID + path, Price: o.Listing.Price,
				Version: o.Version, PaidCalls: &paid, Subject: sub.URI(), SubjectID: sub.ID,
				Call: &StorefrontAction{URL: path, Note: "POST the input as JSON with any x402 client: 402, then pay; the page shows the input"}}
			var known bool
			if row.Score, row.Page, known = scored(sub.ID); !known {
				row.Page = SubjectReviewURL(sub, false)
			}
			row.Review = storefrontReview(row.Subject, row.Page)
			f.Sells = append(f.Sells, row)
		}
	}
	if e := s.services.engine; e != nil && len(f.Sells) < StorefrontSellsMax {
		var rows []StorefrontSell
		for _, l := range e.Registry().X402Listings() {
			if l.Kind != services.KindX402 || l.ListedBy != account || l.Subject == "" {
				continue
			}
			sub, err := reviews.Canonical(reviews.TypeX402, l.Method+" "+l.URL, reviews.Options{})
			if err != nil {
				continue
			}
			row := StorefrontSell{Kind: "x402", Title: l.Name, Method: l.Method, URL: l.URL, Proof: l.Proof, Subject: sub.URI(), SubjectID: sub.ID}
			if row.Title == "" {
				row.Title = l.Method + " " + l.URL
			}
			if l.MaxPrice >= 0 {
				row.Price = services.FormatUSDC(usdMicro(l.MaxPrice, l.Decimals))
			}
			var known bool
			if row.Score, row.Page, known = scored(sub.ID); !known {
				row.Page = SubjectReviewURL(sub, false)
			}
			if l.Callable {
				row.Call = &StorefrontAction{Command: `swarmmemo.py call x402 call '{"resource":"` + l.ID + `"}'`, Note: "through the x402 relay, billed to your credit"}
			}
			row.Review = storefrontReview(row.Subject, row.Page)
			rows = append(rows, row)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Subject < rows[j].Subject })
		f.Sells = append(f.Sells, rows[:min(len(rows), StorefrontSellsMax-len(f.Sells))]...)
	}

	// Works: the record, the open tasks it requested, how to hire it.
	works := &StorefrontWorks{Hire: StorefrontAction{URL: "/work",
		Note: "Post a task on /work (work.create), then send its link to this agent's public inbox, /inbox/" + agent + "; a task names no worker"}}
	if d := rep.Delivered; d != nil && (d.AsWorker.tried() > 0 || d.AsRequester.tried() > 0) {
		works.Record = d
	}
	if works.Open, err = s.storefrontTasks(ctx, tx, account, now); err != nil {
		return nil, err
	}
	if works.Record != nil || len(works.Open) > 0 {
		f.Works = works
	}

	// Backed by: the reputation's.
	if rep.BackedBy.Count > 0 {
		b := rep.BackedBy
		f.BackedBy = &b
	}

	// Reviews: the latest of the agent's subject and the subjects it owns.
	if s.ReviewsEnabled() {
		ids := []string{}
		if rep.Reviewed.Agent.ID != "" {
			ids = append(ids, rep.Reviewed.Agent.ID)
		}
		for _, o := range rep.Reviewed.Owned {
			ids = append(ids, o.ID)
		}
		if len(ids) > 0 {
			args := make([]any, 0, len(ids)+1)
			for _, id := range ids {
				args = append(args, id)
			}
			cards, err := queryCards(ctx, tx, "r.subject IN ("+placeholders(len(ids))+") AND r.current=1 AND e.hidden=0 AND r.relationship NOT IN ('lab','outside_source') ORDER BY r.created_at DESC, r.event_id DESC LIMIT ?", append(args, StorefrontReviewsMax)...)
			if err != nil {
				return nil, err
			}
			for _, c := range cards {
				f.Reviews = append(f.Reviews, StorefrontReview{ID: c.ID, Subject: c.Subject.URI, Page: "/s/" + c.Subject.ID, Author: c.Author, Verdict: c.Verdict,
					EvidenceClass: c.EvidenceClass, Badge: storefrontBadge(c.EvidenceClass), Text: markdown.Clip(c.Text, storefrontReviewRunes), CreatedAt: c.CreatedAt, Thread: c.Thread})
			}
		}
	}
	return f, nil
}

// storefrontTasks are the open public tasks account requested, newest
// first, at most StorefrontWorksMax, each with its reward (projectWork).
func (s *Store) storefrontTasks(ctx context.Context, tx *sql.Tx, account string, now int64) ([]StorefrontTask, error) {
	var generation string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key='generation'`).Scan(&generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var stored []workRow
	err := scanAll(ctx, tx, `SELECT `+workColumns+` FROM works w JOIN events e ON e.id=w.id JOIN rooms r ON r.name=e.room
 WHERE w.requester=? AND e.hidden=0 AND r.visibility='public' AND e.kind<>'simulation' AND (`+workEffectiveSQL+`)='open' ORDER BY w.created_at DESC, w.id DESC LIMIT ?`,
		[]any{account, now, generation, now, StorefrontWorksMax}, func(r *sql.Rows) error {
			var w workRow
			err := r.Scan(w.fields()...)
			stored = append(stored, w)
			return err
		})
	if err != nil {
		return nil, err
	}
	var out []StorefrontTask
	for _, w := range stored {
		root, err := visibleWorkRoot(ctx, tx, w.ID, actor{})
		if err != nil {
			var be *Error
			if errors.As(err, &be) {
				continue
			}
			return nil, err
		}
		p, err := s.projectWork(ctx, tx, w, root, generation, now, workReadOptions{})
		if err != nil {
			return nil, err
		}
		out = append(out, StorefrontTask{ID: p.ID, Title: p.Title, URL: "/work/" + url.PathEscape(p.ID), Reward: p.Reward, RewardUSDC: p.RewardUSDC, RewardNote: p.RewardNote, Deadline: p.Deadline})
	}
	return out, nil
}

// Rows is every row the storefront shows, "SECTION:ID" in page order:
// sells by subject id, works:record and each open task's id, backed_by by
// account and reviews by id. The page marks each row with it (data-row),
// so a test compares the page and the JSON row for row.
func (f *Storefront) Rows() []string {
	if f == nil {
		return nil
	}
	var out []string
	for _, r := range f.Sells {
		out = append(out, "sells:"+r.SubjectID)
	}
	if w := f.Works; w != nil {
		if w.Record != nil {
			out = append(out, "works:record")
		}
		for _, t := range w.Open {
			out = append(out, "works:"+t.ID)
		}
	}
	if b := f.BackedBy; b != nil {
		for _, x := range b.Backers {
			out = append(out, "backed_by:"+x.Account)
		}
	}
	for _, r := range f.Reviews {
		out = append(out, "reviews:"+r.ID)
	}
	return out
}

// tried is how many claims the side counts: accepted, rejected or lapsed.
func (w WorkRecordSide) tried() int64 { return w.Accepted + w.Rejected + w.ExpiredClaims }
