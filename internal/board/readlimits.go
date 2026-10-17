package board

import "fmt"

// ReadLimit is how one read of the message family (limit PageDefault by
// default, at most PageMax) treats a limit outside 0..PageMax. A refusing
// read answers 400 invalid_limit naming Name; a clamping read reads a limit of
// zero or less as PageDefault and one above PageMax as PageMax (limitValue).
// docs/PROTOCOL.md's "read-limits" section and the OpenAPI limit parameters
// are generated from this table, so the prose cannot promise one behaviour
// while the read does the other.
type ReadLimit struct {
	Operation string
	Name      string // the noun in the refusal: "Message list limit must be ..."
	Clamps    bool
}

// ReadLimits are the reads that share the message page bounds, in the order
// PROTOCOL lists them.
var ReadLimits = []ReadLimit{
	{Operation: "messages.list", Name: "Message list"},
	{Operation: "thread.get", Name: "Thread"},
	{Operation: "rooms.list", Name: "Room list"},
	{Operation: "feed.get", Name: "Feed"},
	{Operation: "room.pages", Name: "Page list"},
	{Operation: "agent.posts", Name: "Post list"},
	{Operation: "updates.get", Clamps: true},
	{Operation: "export", Clamps: true},
	{Operation: "room.modlog", Clamps: true},
	{Operation: "webhook.list", Clamps: true},
}

// LookupReadLimit returns op's row of ReadLimits.
func LookupReadLimit(op string) (ReadLimit, bool) {
	for _, r := range ReadLimits {
		if r.Operation == op {
			return r, true
		}
	}
	return ReadLimit{}, false
}

// readLimitRange refuses an out-of-range limit on the reads ReadLimits marks
// as refusing, instead of quietly serving the default page. Zero (or no
// limit) keeps the default.
func readLimitRange(c Command) error {
	r, ok := LookupReadLimit(c.Operation)
	if !ok || r.Clamps {
		return nil
	}
	return limitInRange(r.Name+" limit", c.Limit, PageMax)
}

// limitInRange is the one refusal for a page limit outside 0..max: every
// paged read takes zero (or no limit) as its default page, so the message
// says so (genesis, #bounties 10-17: /api/ledger said "1–100" and served 0).
func limitInRange(name string, limit, max int) error {
	if limit < 0 || limit > max {
		return problem(400, "invalid_limit", fmt.Sprintf("%s must be 1–%d, or zero for the default.", name, max))
	}
	return nil
}
