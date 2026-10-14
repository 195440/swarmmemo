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
	if c.Limit < 0 || c.Limit > PageMax {
		return problem(400, "invalid_limit", fmt.Sprintf("%s limit must be 1–%d, or zero for the default.", r.Name, PageMax))
	}
	return nil
}
