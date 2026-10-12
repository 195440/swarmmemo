package find

// Sign-up doors (C228): who can sign up at a result, an agent alone or only
// a human. A door is a closed word, derived when the index is built from
// the sources that say it, each with its provenance: measured (SwarmMemo's
// own fact or lab), outside (an imported rater, such as the Agent Access
// Index), inferred (a paid sign-up path), or self (an agent's own claim).
// A higher source is never overwritten by a lower one; when they disagree,
// the door keeps the higher and its note says what the other said. A door
// changes rank only under a door filter, and a door older than
// DoorStaleAfter stays shown, marked stale and ranked lower.

import (
	"regexp"
	"slices"
	"strings"
	"time"
)

// Door values: the five an agent can use alone, then the five that need a
// human or are shut.
const (
	DoorWallet  = "wallet"  // pays to sign up (an x402 or MPP 402 is the door)
	DoorKey     = "key"     // registers a public key
	DoorPoW     = "pow"     // proof of work
	DoorOpen    = "open"    // no account needed
	DoorForm    = "form"    // a plain email or username form, no CAPTCHA: the agent needs its own inbox
	DoorHuman   = "human"   // a code to a human's email or phone, ID or KYC, an invite
	DoorOAuth   = "oauth"   // OAuth only: a human's account elsewhere
	DoorCaptcha = "captcha" // a visible CAPTCHA
	DoorBlocked = "blocked" // refuses headless browsers
	DoorClosed  = "closed"  // shut down
)

// Doors are every door value, agent doors first.
var Doors = []string{DoorWallet, DoorKey, DoorPoW, DoorOpen, DoorForm, DoorHuman, DoorOAuth, DoorCaptcha, DoorBlocked, DoorClosed}

// Door groups: a filter word that selects several doors. "human" as a
// filter is the group (the human door is in it).
const (
	DoorGroupAgent = "agent"
	DoorGroupHuman = "human"
)

// DoorGroups are the group words and the doors each selects.
var DoorGroups = map[string][]string{
	DoorGroupAgent: {DoorWallet, DoorKey, DoorPoW, DoorOpen, DoorForm},
	DoorGroupHuman: {DoorHuman, DoorOAuth, DoorCaptcha, DoorBlocked, DoorClosed},
}

// DoorFilters are the words the door filter takes.
var DoorFilters = append([]string{DoorGroupAgent}, Doors...)

// Door provenance, highest first.
const (
	DoorMeasured = "measured"
	DoorOutside  = "outside"
	DoorInferred = "inferred"
	DoorSelf     = "self"
)

// DoorBys are the provenances in precedence order.
var DoorBys = []string{DoorMeasured, DoorOutside, DoorInferred, DoorSelf}

// DoorStaleAfter is how old a dated door is before it is marked stale.
const DoorStaleAfter = 120 * 24 * time.Hour

// doorRank is a provenance's term in q under a door filter.
var doorRank = map[string]float64{DoorMeasured: 1, DoorOutside: 0.5, DoorInferred: 0, DoorSelf: -0.5}

// doorStalePenalty is what a stale door takes off that term.
const doorStalePenalty = 0.5

// DoorSource says who gave a door: its provenance, the source's name, its
// page (an attribution link for an outside rater), its terms (a licence),
// when (unix seconds, 0 when undated) and a note of ours.
type DoorSource struct {
	By    string
	Name  string
	URL   string
	Terms string
	At    int64
	Note  string
}

// DoorMark is a result's door, as the wire shows it.
type DoorMark struct {
	Value  string `json:"value"`
	By     string `json:"by"`              // measured, outside, inferred or self
	Source string `json:"source"`          // who said so
	URL    string `json:"url,omitempty"`   // the source's page
	Terms  string `json:"terms,omitempty"` // its licence, for an outside rater
	At     string `json:"at,omitempty"`    // YYYY-MM-DD
	Stale  bool   `json:"stale,omitempty"` // older than DoorStaleAfter
	Note   string `json:"note,omitempty"`  // ours: a caveat, or what a lower source said
}

// ValidDoor reports whether v is a door value.
func ValidDoor(v string) bool { return slices.Contains(Doors, v) }

// DoorAgent reports whether door is one an agent can use alone.
func DoorAgent(door string) bool { return slices.Contains(DoorGroups[DoorGroupAgent], door) }

// doorSet is the doors a filter word selects.
func doorSet(filter string) []string {
	if g, ok := DoorGroups[filter]; ok {
		return g
	}
	return []string{filter}
}

// SetDoor gives d the door from src unless a higher source already gave
// one: a lower source never overwrites a higher. When the sources
// disagree, the kept door's note says what the other said and when. A
// value outside Doors or a provenance outside DoorBys is ignored.
func SetDoor(d *Doc, door string, src DoorSource) {
	if !ValidDoor(door) || !slices.Contains(DoorBys, src.By) {
		return
	}
	said := func(s *DoorSource, v string) string {
		out := s.Name + " said " + v
		if s.At > 0 {
			out += " on " + doorDate(s.At)
		}
		return out
	}
	if d.DoorBy != nil {
		cur := slices.Index(DoorBys, d.DoorBy.By)
		next := slices.Index(DoorBys, src.By)
		if next >= cur {
			if door != d.Door && next > cur {
				d.DoorBy.Note = joinNote(d.DoorBy.Note, said(&src, door))
			}
			return
		}
		if door != d.Door {
			src.Note = joinNote(src.Note, said(d.DoorBy, d.Door))
		}
	}
	s := src
	d.Door, d.DoorBy = door, &s
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func doorDate(at int64) string { return time.Unix(at, 0).UTC().Format("2006-01-02") }

// doorStale reports whether d's door is older than DoorStaleAfter at now.
func doorStale(d *Doc, now time.Time) bool {
	return d.DoorBy != nil && d.DoorBy.At > 0 && now.Sub(time.Unix(d.DoorBy.At, 0)) > DoorStaleAfter
}

// doorMark is d's door for the wire; nil when it has none.
func doorMark(d *Doc, now time.Time) *DoorMark {
	if d.Door == "" || d.DoorBy == nil {
		return nil
	}
	m := &DoorMark{Value: d.Door, By: d.DoorBy.By, Source: d.DoorBy.Name, URL: d.DoorBy.URL, Terms: d.DoorBy.Terms, Note: d.DoorBy.Note, Stale: doorStale(d, now)}
	if d.DoorBy.At > 0 {
		m.At = doorDate(d.DoorBy.At)
	}
	return m
}

// doorWhy is the why line's door part: "door: wallet (inferred from a paid
// sign-up path)".
func doorWhy(d *Doc, now time.Time) string {
	if d.Door == "" || d.DoorBy == nil {
		return ""
	}
	var from string
	switch src := d.DoorBy; src.By {
	case DoorMeasured:
		from = "measured by " + src.Name
	case DoorOutside:
		from = src.Name
	case DoorInferred:
		from = "inferred from " + src.Name
	default:
		from = "self-reported"
	}
	if d.DoorBy.At > 0 {
		from += ", " + doorDate(d.DoorBy.At)
	}
	if doorStale(d, now) {
		from += ", stale"
	}
	return "door: " + d.Door + " (" + from + ")"
}

// doorTerm is d's provenance term in q under a door filter.
func doorTerm(d *Doc, now time.Time) (float64, bool) {
	if d.Door == "" || d.DoorBy == nil {
		return 0, false
	}
	v := doorRank[d.DoorBy.By]
	if doorStale(d, now) {
		v -= doorStalePenalty
	}
	return v, true
}

// Door cues in a query (parse.go): a door word counts only beside a sign-up
// word, except "no CAPTCHA".
var (
	signupRE     = regexp.MustCompile(`\b(?:sign[ -]?ups?|signing up|register(?:ing)?|registration|create (?:an )?account|onboard(?:ing)?)\b`)
	doorAgentRE  = regexp.MustCompile(`\b(?:without (?:a |any )?humans?(?: step| in the loop)?|no humans?(?: step| in the loop| needed)?|autonomous(?:ly)?|headless(?:ly)?)\b`)
	doorWalletRE = regexp.MustCompile(`\b(?:with (?:a |an |my |its )?(?:crypto )?wallet|pay(?:s|ing)? to sign[ -]?up)\b`)
	doorKeyRE    = regexp.MustCompile(`\b(?:with (?:a |my |its )?(?:public |signing )?key|key ?pairs?)\b`)
	noCaptchaRE  = regexp.MustCompile(`\b(?:no|without(?: a| any)?) captchas?\b`)
)

// doorCue reports whether q (any case) may name a door: a sign-up word or
// "no CAPTCHA". The model's door is kept only then.
func doorCue(q string) bool {
	q = strings.ToLower(q)
	return signupRE.MatchString(q) || noCaptchaRE.MatchString(q)
}
