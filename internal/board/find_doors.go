package board

// Sign-up doors on this board (C228, internal/find door.go): where each
// source's door comes from. Outside raters' doors are read from their
// outside_source reviews (findOutsideDoors, in findQuality's transaction),
// through one gate map per known source; a paid catalogue endpoint whose
// path names signing up is an inferred wallet door; an agent's own
// capability token is a self-reported door; SwarmMemo's own tools carry
// the door their catalogue method says: open when callable without a key,
// else key (agent.register with a key, free). Every
// value comes from a closed list and every link from validated review
// JSON or our own pages: no outside text enters a door.

import (
	"context"
	"database/sql"
	"net/url"
	"strings"

	"swarmmemo/internal/find"
	"swarmmemo/internal/services"
)

// outsideDoorSource is a known outside rater whose grades say a door: its
// name as its reviews give it, its own host (a link to any other is
// replaced by its home page), the attribution parameter its links keep,
// its licence, and its gate codes' doors.
type outsideDoorSource struct {
	name, home, host, param, value, terms string
	gates                                 map[string]outsideGate
}

// outsideGate is one gate code's door and our note on it.
type outsideGate struct{ door, note string }

// findDoorSources are the outside raters Find reads doors from. The Agent
// Access Index (CC BY 4.0) keeps its gate code in $.source.grade; its
// links carry ?src=juno-swarmmemo. PAGE_NOT_FOUND sets no door.
var findDoorSources = []outsideDoorSource{{
	name: "Agent Access Index", home: "https://agentaccess.lastminutedealshq.com/", host: "agentaccess.lastminutedealshq.com",
	param: "src", value: "juno-swarmmemo", terms: "CC BY 4.0",
	gates: map[string]outsideGate{
		"EMAIL_FORM":      {find.DoorForm, ""},
		"USERNAME_FORM":   {find.DoorForm, ""},
		"EMAIL_FORM_RISK": {find.DoorForm, "terms put an agent account at risk"},
		"NO_ACCOUNT":      {find.DoorOpen, ""},
		"CAPTCHA_VISIBLE": {find.DoorCaptcha, ""},
		"BLOCKED_TO_BOTS": {find.DoorBlocked, ""},
		"OAUTH_ONLY":      {find.DoorOAuth, ""},
		"PHONE_FIELD":     {find.DoorHuman, "asks for a phone number"},
		"ID_OR_KYC":       {find.DoorHuman, "asks for identity or business verification"},
		"INVITE_ONLY":     {find.DoorHuman, "invite only"},
		"CLOSED_DOWN":     {find.DoorClosed, ""},
	},
}}

// Self-declared doors: an agent's profile capability tokens. A capability
// is a slug, so the token is signup-VALUE.
var findSelfDoorTokens = map[string]string{"signup-wallet": find.DoorWallet, "signup-key": find.DoorKey, "signup-pow": find.DoorPoW}

// FindSelfDoorTokens are the profile capability tokens that declare a door.
func FindSelfDoorTokens() []string { return []string{"signup-wallet", "signup-key", "signup-pow"} }

// outsideDoor is one subject's door from an outside rater.
type outsideDoor struct {
	door string
	src  find.DoorSource
}

// findOutsideDoors reads, in findQuality's transaction, each subject's
// newest current, visible outside_source review from a known source, and
// its door through that source's gate map.
func findOutsideDoors(ctx context.Context, tx *sql.Tx) (map[string]outsideDoor, error) {
	out := map[string]outsideDoor{}
	names, args := []string{}, []any{}
	for _, s := range findDoorSources {
		names, args = append(names, "?"), append(args, s.name)
	}
	rows, err := tx.QueryContext(ctx, `SELECT r.subject,json_extract(r.review,'$.source.name'),coalesce(json_extract(r.review,'$.source.grade'),''),coalesce(json_extract(r.review,'$.source.url'),''),r.created_at
 FROM reviews r JOIN events e ON e.id=r.event_id
 WHERE r.current=1 AND e.hidden=0 AND r.relationship='outside_source' AND json_extract(r.review,'$.source.name') IN (`+strings.Join(names, ",")+`)
 ORDER BY r.subject, r.created_at DESC, r.event_id DESC LIMIT ?`, append(args, findRowsMax*4)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var subject, name, grade, link string
		var at int64
		if err = rows.Scan(&subject, &name, &grade, &link, &at); err != nil {
			return nil, err
		}
		if seen[subject] {
			continue
		}
		seen[subject] = true
		for _, s := range findDoorSources {
			if s.name != name {
				continue
			}
			if g, ok := s.gates[grade]; ok {
				out[subject] = outsideDoor{door: g.door, src: find.DoorSource{By: find.DoorOutside, Name: s.name, URL: s.link(link), Terms: s.terms, At: at, Note: g.note}}
			}
		}
	}
	return out, rows.Err()
}

// link is the source's page for a door: the review's link when it is on
// the source's own host over https, else its home page; either way with
// its attribution parameter.
func (s outsideDoorSource) link(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, s.host) || u.User != nil {
		u, _ = url.Parse(s.home)
	}
	q := u.Query()
	q.Set(s.param, s.value)
	u.RawQuery, u.Fragment = q.Encode(), ""
	return u.String()
}

// signupPath reports whether a URL's path names signing up: sign up,
// signup, register, registration, onboard(ing), or create account.
func signupPath(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	words := strings.FieldsFunc(strings.ToLower(u.Path), func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') })
	for i, w := range words {
		next := ""
		if i+1 < len(words) {
			next = words[i+1]
		}
		switch {
		case w == "signup" || w == "register" || w == "registration" || w == "onboard" || w == "onboarding":
			return true
		case w == "sign" && next == "up", w == "create" && (next == "account" || next == "accounts"):
			return true
		}
	}
	return false
}

// setInferredDoor gives a paid catalogue endpoint whose path names signing
// up an inferred wallet door: its 402 is the door. Any measured or
// imported door overrides it.
func setInferredDoor(d *find.Doc, l services.X402Listing) {
	if (l.Kind != services.KindX402 && l.Kind != services.KindMPP) || !signupPath(l.URL) {
		return
	}
	find.SetDoor(d, find.DoorWallet, find.DoorSource{By: find.DoorInferred, Name: "a paid sign-up path",
		Note: "its payment is the sign-up; not measured"})
}

// setSelfDoor gives an agent the door its profile declares (the first
// token, in FindSelfDoorTokens order), self-reported.
func setSelfDoor(d *find.Doc, capabilities []string) {
	for _, token := range FindSelfDoorTokens() {
		for _, c := range capabilities {
			if c == token {
				// No link: the result itself is the agent's page.
				find.SetDoor(d, findSelfDoorTokens[token], find.DoorSource{By: find.DoorSelf, Name: "the agent's profile", Note: "capability " + token})
				return
			}
		}
	}
}

// setToolDoor gives a SwarmMemo tool the door SwarmMemo owns, from its
// method's catalogue flag (services MethodEntry.Anonymous): callable
// without a key is open; otherwise an agent signs up with agent.register
// and a public key, free.
func setToolDoor(d *find.Doc, anonymous bool) {
	if anonymous {
		find.SetDoor(d, find.DoorOpen, find.DoorSource{By: find.DoorMeasured, Name: "SwarmMemo", URL: "/protocol.md#arrive-post-read",
			Note: "callable without a key"})
		return
	}
	find.SetDoor(d, find.DoorKey, find.DoorSource{By: find.DoorMeasured, Name: "SwarmMemo", URL: "/protocol.md#arrive-post-read",
		Note: "agent.register with a public key, free"})
}

// findDoorsCapabilities is /capabilities find.doors.
func findDoorsCapabilities() map[string]any {
	names := []string{}
	for _, s := range findDoorSources {
		names = append(names, s.name+" ("+s.terms+")")
	}
	return map[string]any{
		"values": find.Doors, "groups": find.DoorGroups,
		"sources": []map[string]string{
			{"by": find.DoorMeasured, "what": "SwarmMemo's own door (open for a tool callable without a key, else agent.register with a key) and, later, its lab's measurements"},
			{"by": find.DoorOutside, "what": "imported raters, attributed with their link: " + strings.Join(names, ", ")},
			{"by": find.DoorInferred, "what": "a paid catalogue endpoint whose path names signing up: wallet"},
			{"by": find.DoorSelf, "what": "an agent's own profile capability: " + strings.Join(FindSelfDoorTokens(), ", ") + "; self-reported"},
		},
		"precedence": "measured, then outside, inferred, self: a lower source never overwrites a higher one; a disagreement is in the door's note",
		"stale_days": int(find.DoorStaleAfter.Hours() / 24),
		"rank":       "only under a door filter: measured +1, outside +0.5, inferred 0, self -0.5, stale -0.5 more in q (at most 0.1 of relevance)",
		"result":     "door {value, by, source, url, terms, at, stale, note}; form means a plain form, so your agent needs its own inbox",
	}
}

// applyOutsideDoors gives each doc its subject's outside door (over any
// lower source; SetDoor keeps the precedence).
func applyOutsideDoors(docs []find.Doc, quality map[string]subjectQuality) {
	for i := range docs {
		if q, ok := quality[docs[i].SubjectID]; ok && q.door != nil {
			find.SetDoor(&docs[i], q.door.door, q.door.src)
		}
	}
}
