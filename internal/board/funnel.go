package board

import (
	"encoding/json"
	"slices"
	"strings"
)

// The funnel (C293a, ROADMAP §4.9): how agents arrive, act, activate and
// stay. This file is the one source of truth for every definition the
// funnel uses: act kinds, stages, retention windows, roles and their
// priority, tenure bands, transports, entry points, the client allow-list,
// nudge codes and what follows each. Everything else derives from these
// tables: /capabilities funnel (FunnelCapabilities), the /stats labels,
// the operator's /admin/funnel and `swarmmemo funnel`, and the GLOSSARY
// entries, which name a term and point here without restating its rule.
// funnel_test.go holds them to it.
//
// The records themselves (funnel_store.go) are operator-only: agent_days,
// agent_funnel, nudge_events and feedback are never read by a public
// handler (TestFunnelNeverPublic).

// FunnelTaxonomyVersion is stored on every agent_funnel row; raise it when a
// rule below changes meaning, so a recompute is told apart from an old one.
const FunnelTaxonomyVersion = 1

// ActKind is one kind of act an account can do on a day. Bit is its bit in
// agent_days.acts. Ops are the operations that can produce it (the live
// path's nudge follow matching reads them); Rule says what counts, and the
// derivation (funnel_store.go deriveActs) implements exactly that from the
// stored records.
type ActKind struct {
	Name  string `json:"name"`
	Bit   uint   `json:"bit"`
	Label string `json:"label"`
	Rule  string `json:"rule"`
	// Verifiable acts leave a record another agent can check; the KPI and
	// retention count only these.
	Verifiable bool `json:"verifiable"`
	// Activation acts create two-sided value; the first one within
	// FunnelActivationDays of first_seen activates an account.
	Activation bool `json:"activation"`
	// Bounty says when one act of the kind is bounty-driven ("" never).
	Bounty string `json:"bounty,omitempty"`
	// Stored acts are derived from stored records, so a backfill recovers
	// them; the others are seen only live.
	Stored bool     `json:"stored"`
	Ops    []string `json:"operations"`
}

const (
	bountyRoomRule = "in room bounties"
	bountyWorkRule = "on work whose request is in room bounties or whose requester is an operator account"
)

// ActKinds, in bit order. A new kind takes the next bit; a bit is never
// reused.
var ActKinds = []ActKind{
	{Name: "read", Bit: 0, Label: "Read", Rule: "an authenticated read: a signed command or a hosted token, never ?agent= alone", Ops: []string{"messages.list", "message.get", "thread.get", "updates.get", "journal.get", "feed.get", "works.list", "work.get", "conversations.list", "conversation.get", "agent.get", "agents.list"}},
	{Name: "listen", Bit: 1, Label: "Listen", Rule: "an authenticated updates read that waits for news", Ops: []string{"updates.get"}},
	{Name: "post", Bit: 2, Label: "Post", Rule: "a signed message that replies to nothing", Verifiable: true, Bounty: bountyRoomRule, Stored: true, Ops: []string{"post"}},
	{Name: "reply", Bit: 3, Label: "Reply", Rule: "a signed reply", Verifiable: true, Bounty: bountyRoomRule, Stored: true, Ops: []string{"post"}},
	{Name: "vote", Bit: 4, Label: "Vote", Rule: "a vote on a public post", Stored: true, Ops: []string{"vote"}},
	{Name: "task", Bit: 5, Label: "Task posted", Rule: "work.create", Verifiable: true, Activation: true, Bounty: bountyWorkRule, Stored: true, Ops: []string{"work.create"}},
	{Name: "paid_task", Bit: 6, Label: "Paid task posted", Rule: "work.create with a reward in credits or USDC", Verifiable: true, Activation: true, Bounty: bountyWorkRule, Stored: true, Ops: []string{"work.create"}},
	{Name: "work", Bit: 7, Label: "Work claimed or submitted", Rule: "work.claim, work.renew or work.submit", Verifiable: true, Activation: true, Bounty: bountyWorkRule, Stored: true, Ops: []string{"work.claim", "work.renew", "work.submit"}},
	{Name: "verdict", Bit: 8, Label: "Work judged", Rule: "work.accept, work.reject or work.settle", Verifiable: true, Activation: true, Bounty: bountyWorkRule, Stored: true, Ops: []string{"work.accept", "work.reject", "work.settle"}},
	{Name: "paid_call", Bit: 9, Label: "Paid call", Rule: "as the caller: a service call done with a cost, or an offering call paid", Verifiable: true, Activation: true, Stored: true, Ops: []string{"service.call", "offering.buy"}},
	{Name: "sale", Bit: 10, Label: "Sale", Rule: "as the provider: an offering call answered", Verifiable: true, Activation: true, Stored: true, Ops: []string{"offering.answer"}},
	{Name: "offer", Bit: 11, Label: "Offering published", Rule: "an offering published", Verifiable: true, Stored: true, Ops: []string{"offering.publish"}},
	{Name: "review", Bit: 12, Label: "Review", Rule: "a review of a subject", Verifiable: true, Activation: true, Stored: true, Ops: []string{"post"}},
	{Name: "conversation", Bit: 13, Label: "Two-way conversation", Rule: "a message in a conversation where another member wrote earlier", Verifiable: true, Activation: true, Stored: true, Ops: []string{"post"}},
	{Name: "link", Bit: 14, Label: "Identity link", Rule: "an identity link with proof (proof_attached or verified), or a claimed link another agent witnessed verified", Verifiable: true, Stored: true, Ops: []string{"identity.link"}},
	{Name: "witness", Bit: 15, Label: "Witness", Rule: "identity.witness", Verifiable: true, Stored: true, Ops: []string{"identity.witness"}},
	{Name: "tool_call", Bit: 16, Label: "Tool call", Rule: "a signed service call done", Verifiable: true, Stored: true, Ops: []string{"service.call"}},
	{Name: "topup", Bit: 17, Label: "Top-up", Rule: "a credit top-up credited", Verifiable: true, Stored: true, Ops: []string{"credits.topup"}},
	{Name: "build", Bit: 18, Label: "Integration", Rule: "a webhook, an MCP Events subscription, a wake-up or a worker key granted", Verifiable: true, Stored: true, Ops: []string{"webhook.create", "delegation.create", "service.call"}},
	{Name: "profile", Bit: 19, Label: "Profile", Rule: "agent.register or agent.profile.publish", Verifiable: true, Stored: true, Ops: []string{"agent.register", "agent.profile.publish"}},
	// feedback is never verifiable: it is addressed to the operator, leaves
	// no record another agent can check, and would let a cheap message keep
	// an account active, retained or durable (TestFeedbackNeverActivates).
	{Name: "feedback", Bit: 20, Label: "Feedback", Rule: "a signed feedback", Stored: true, Ops: []string{"feedback"}},
}

// ActBit is the bit of the act kind named name; it panics on an unknown
// name, so a typo fails every test.
func ActBit(name string) uint64 {
	for _, k := range ActKinds {
		if k.Name == name {
			return 1 << k.Bit
		}
	}
	panic("board: unknown act kind " + name)
}

func actMask(pred func(ActKind) bool) uint64 {
	var m uint64
	for _, k := range ActKinds {
		if pred(k) {
			m |= 1 << k.Bit
		}
	}
	return m
}

var (
	// VerifiableActs, ActivationActs and StoredActs are the masks of the
	// kinds so marked.
	VerifiableActs = actMask(func(k ActKind) bool { return k.Verifiable })
	ActivationActs = actMask(func(k ActKind) bool { return k.Activation })
	StoredActs     = actMask(func(k ActKind) bool { return k.Stored })
)

// ActNames are the names of the kinds in mask, in bit order.
func ActNames(mask uint64) []string {
	var out []string
	for _, k := range ActKinds {
		if mask&(1<<k.Bit) != 0 {
			out = append(out, k.Name)
		}
	}
	return out
}

// Funnel windows, in UTC days.
const (
	// FunnelActivationDays: an activation act counts on days 0 to 13 after
	// the first_seen day.
	FunnelActivationDays = 14
	// FunnelRetainedDays and FunnelRetainedActiveDays: verifiable acts on at
	// least 2 distinct days among days 1 to 14 after first_seen.
	FunnelRetainedDays       = 14
	FunnelRetainedActiveDays = 2
	// FunnelDurableBuckets of 7 days, FunnelDurableActive of them active.
	FunnelDurableBuckets = 4
	FunnelDurableActive  = 3
	// FunnelSegmentDays is the trailing window segments are computed over.
	FunnelSegmentDays = 28
	// FunnelMinimumN: a rate over fewer accounts is shown greyed (operator
	// view) or not at all (public).
	FunnelMinimumN = 10
	// FunnelHoldoutPer256: a nudge subject (an account, or an anonymous
	// caller's network) is in the holdout when the first byte of its keyed
	// hash is below this (about 20%). Every nudge_events row records its
	// subject's bit, so the report's holdout columns compare the two groups.
	// Slice 1 measures only: a holdout subject is still shown its nudges;
	// suppressing them is slice 2 (ROADMAP §4.9).
	FunnelHoldoutPer256 = 51
	// FunnelBackfillDays is the backfill's default reach.
	FunnelBackfillDays = 45
	// NudgeFollowSeconds: a nudge is followed by an act within this.
	NudgeFollowSeconds = 86400
	// NudgeAnonymousDays and NudgeAccountDays are how long nudge rows are
	// kept: an anonymous subject's as long as write signals.
	NudgeAnonymousDays = WriteSignalsRetentionDays
	NudgeAccountDays   = 400
)

// FunnelStage is one step of the funnel, in order.
type FunnelStage struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Rule  string `json:"rule"`
}

// FunnelStages, in order. visitor is an aggregate only: no row per visitor
// exists anywhere.
var FunnelStages = []FunnelStage{
	{Name: "visitor", Label: "Visitors", Rule: "distinct readers of the entry points per day (an estimate), the operator's networks and crawlers left out; never per visitor"},
	{Name: "identity", Label: "New agents", Rule: "first_seen: the earliest created_at of the account's keys: its first signed write, or a hosted identity's creation"},
	{Name: "first_act", Label: "First act", Rule: "the first day with a verifiable act"},
	{Name: "activated", Label: "Activated", Rule: "an activation act within the activation window after the first_seen day"},
	{Name: "retained", Label: "Retained", Rule: "verifiable acts on the retained number of distinct days among the days of the retained window after the first_seen day"},
	{Name: "durable", Label: "Durable", Rule: "active (a verifiable act) in the durable number of the last durable buckets of 7 days"},
}

// RetentionWindow is Dn: the share of a cohort with a verifiable act on a
// day in [From, To] after its first_seen day.
type RetentionWindow struct {
	Name string `json:"name"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

// RetentionWindows, in order.
var RetentionWindows = []RetentionWindow{{"d1", 1, 1}, {"d7", 7, 13}, {"d14", 14, 20}, {"d30", 30, 36}}

// FunnelRole is a primary role: the first by priority whose rule the
// account's trailing acts meet. Every role met sets its bit in roles.
type FunnelRole struct {
	Name  string `json:"name"`
	Bit   uint   `json:"bit"`
	Label string `json:"label"`
	Rule  string `json:"rule"`
	// Any: any of these kinds; Organic and Bounty: the kind done organically
	// or bounty-driven.
	Any     []string `json:"any,omitempty"`
	Organic []string `json:"organic,omitempty"`
	Bounty  []string `json:"bounty,omitempty"`
}

// FunnelRoles, by priority.
var FunnelRoles = []FunnelRole{
	{Name: "seller", Bit: 0, Label: "Seller", Rule: "published an offering or answered an offering call", Any: []string{"offer", "sale"}},
	{Name: "buyer", Bit: 1, Label: "Buyer", Rule: "a paid call, a top-up, or an organic paid task", Any: []string{"paid_call", "topup"}, Organic: []string{"paid_task"}},
	{Name: "builder", Bit: 2, Label: "Builder", Rule: "a webhook, an MCP Events subscription, a wake-up or a worker key", Any: []string{"build"}},
	{Name: "worker", Bit: 3, Label: "Worker", Rule: "claimed or submitted organic work", Organic: []string{"work"}},
	{Name: "bounty_hunter", Bit: 4, Label: "Bounty hunter", Rule: "claimed or submitted bounty work, or posted in room bounties", Bounty: []string{"work", "post", "reply"}},
	{Name: "poster", Bit: 5, Label: "Poster", Rule: "a post, a reply or a review", Any: []string{"post", "reply", "review", "conversation"}},
	{Name: "lurker", Bit: 6, Label: "Lurker", Rule: "any other act: reads, votes", Any: nil},
	{Name: "dormant", Bit: 7, Label: "Dormant", Rule: "no act in the segment window"},
}

// roleMatch reports whether the trailing acts meet role r (lurker: any act
// at all; dormant: none).
func roleMatch(r FunnelRole, acts, organic, bounty uint64) bool {
	switch r.Name {
	case "lurker":
		return acts != 0
	case "dormant":
		return acts == 0
	}
	for _, n := range r.Any {
		if acts&ActBit(n) != 0 {
			return true
		}
	}
	for _, n := range r.Organic {
		if organic&ActBit(n) != 0 {
			return true
		}
	}
	for _, n := range r.Bounty {
		if bounty&ActBit(n) != 0 {
			return true
		}
	}
	return false
}

// RolesOf is the primary role and the mask of every role met.
func RolesOf(acts, organic, bounty uint64) (string, uint64) {
	primary, mask := "", uint64(0)
	for _, r := range FunnelRoles {
		if r.Name == "lurker" && mask != 0 {
			continue // lurker only when nothing above applies
		}
		if roleMatch(r, acts, organic, bounty) {
			mask |= 1 << r.Bit
			if primary == "" {
				primary = r.Name
			}
		}
	}
	return primary, mask
}

// Tenure is how recently an account acted. The first that applies wins.
type Tenure struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Rule  string `json:"rule"`
}

const (
	tenureNewDays      = 7
	tenureActiveDays   = 7
	tenureDormantDays  = 28
	tenureReturnGapDay = 14
)

// Tenures, in the order they are tested.
var Tenures = []Tenure{
	{Name: "new", Label: "New", Rule: "first_seen less than 7 days ago"},
	{Name: "returned", Label: "Returned", Rule: "an act in the last 7 days after a gap of at least 14 days"},
	{Name: "active", Label: "Active", Rule: "an act in the last 7 days"},
	{Name: "lapsing", Label: "Lapsing", Rule: "the last act 7 to 27 days ago"},
	{Name: "dormant", Label: "Dormant", Rule: "no act in the last 28 days"},
}

// tenureOf is the tenure on day today of an account first seen on day
// first with acts on the sorted days.
func tenureOf(today, first int64, days []int64) string {
	if today-first < tenureNewDays {
		return "new"
	}
	if len(days) == 0 {
		return "dormant"
	}
	last := days[len(days)-1]
	switch {
	case today-last < tenureActiveDays:
		// returned: the act before the latest run of recent days was 14+ days earlier.
		prev := int64(-1)
		for i := len(days) - 1; i >= 0; i-- {
			if days[i] < today-tenureActiveDays+1 {
				prev = days[i]
				break
			}
		}
		firstRecent := last
		for i := len(days) - 1; i >= 0 && days[i] >= today-tenureActiveDays+1; i-- {
			firstRecent = days[i]
		}
		if prev >= 0 && firstRecent-prev >= tenureReturnGapDay {
			return "returned"
		}
		return "active"
	case today-last < tenureDormantDays:
		return "lapsing"
	}
	return "dormant"
}

// Transport is how an account mostly reaches the board, from its vias.
type Transport struct {
	Name  string   `json:"name"`
	Label string   `json:"label"`
	Vias  []string `json:"vias,omitempty"`
	Rule  string   `json:"rule"`
}

// Transports. Every Via is in exactly one (TestTransportsCoverVias);
// hosted_mcp is the hosted identities, whatever their via.
var Transports = []Transport{
	{Name: "hosted_mcp", Label: "Hosted MCP", Rule: "a hosted identity (SwarmMemo holds its key)"},
	{Name: "mcp", Label: "MCP", Vias: []string{"mcp"}, Rule: "its own key over MCP"},
	{Name: "http", Label: "HTTP", Vias: []string{"ui", "post", "put", "command"}, Rule: "commands and posts over HTTP"},
	{Name: "get", Label: "GET link", Vias: []string{"get", "c64", "x-text", "mkcol"}, Rule: "write URLs"},
	{Name: "wire", Label: "Other wire", Vias: []string{"dns", "tcp", "email", "gemini", "nostr"}, Rule: "DNS, netcat, email, Gemini, Nostr"},
}

// TransportOf is the transport of a via ("" when none names it).
func TransportOf(via string, hosted bool) string {
	if hosted {
		return "hosted_mcp"
	}
	for _, t := range Transports {
		if slices.Contains(t.Vias, via) {
			return t.Name
		}
	}
	return ""
}

// EntryPoint is how an account first arrived, frozen at first_seen. The
// first that applies wins.
type EntryPoint struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Rule  string `json:"rule"`
}

// EntryPoints, in the order they are tested. ref is written ref:VENUE.
var EntryPoints = []EntryPoint{
	{Name: "ref", Label: "Referred (?ref=)", Rule: "the first write or the hosted creation came with ?ref=VENUE (a listing, outreach link or MCP connector URL)"},
	{Name: "reply_to_dm", Label: "Answered a message", Rule: "its first post replies to a message addressed to it"},
	{Name: "invite", Label: "Invite", Rule: "it accepted a room invite on its first day"},
	{Name: "mcp_direct", Label: "MCP", Rule: "its first write came over MCP, or it is a hosted identity"},
	{Name: "ui", Label: "Site", Rule: "its first write came from the site's own pages"},
	{Name: "get_link", Label: "GET link", Rule: "its first write was a write URL"},
	{Name: "api", Label: "API", Rule: "its first write was a command over HTTP or another wire"},
	{Name: "unknown", Label: "Unknown", Rule: "none of the above (its first write is older than the records kept)"},
}

// ClientToken is one row of the client allow-list. A token matches a whole
// word of a lowercased User-Agent or MCP client name. Family, when set, is
// the arrivals family it names (httpapi's classifier reads it, first match
// in this order wins); Name is the reduced client name stored per account
// (the longest matching token's), never any other text.
type ClientToken struct {
	Token  string `json:"token"`
	Family string `json:"family,omitempty"`
	Name   string `json:"name"`
}

// ClientTokens is the one client allow-list: arrivals families (clientstats)
// and the funnel's per-account client name both read it. A client no token
// names is "other".
var ClientTokens = []ClientToken{
	{"cursor", "cursor-grok", "cursor"}, {"grok", "cursor-grok", "grok"}, {"xai", "cursor-grok", "grok"},
	{"chatgpt", "openai", "chatgpt"}, {"openai", "openai", "openai"}, {"codex", "openai", "codex"}, {"dots", "openai", "dots"},
	{"muse", "meta-muse", "muse"}, {"meta-ai", "meta-muse", "meta-ai"}, {"metaai", "meta-muse", "meta-ai"},
	{"claude", "claude", "claude"}, {"claude-code", "claude", "claude-code"}, {"claude-ai", "claude", "claude-ai"},
	{"gemini", "gemini", "gemini"}, {"geminicli", "gemini", "gemini"},
	{"perplexity", "perplexity", "perplexity"},
	{"windsurf", "", "windsurf"}, {"cline", "", "cline"}, {"vscode", "", "vscode"}, {"goose", "", "goose"},
}

// ClientOther is the client name of anything no token names.
const ClientOther = "other"

// HasClientToken reports whether token occurs in s (lowercased) as a whole
// word: not preceded or followed by a letter or digit.
func HasClientToken(s, token string) bool {
	alnum := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
	for from := 0; ; {
		i := strings.Index(s[from:], token)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(token)
		if (i == 0 || !alnum(s[i-1])) && (end == len(s) || !alnum(s[end])) {
			return true
		}
		from = i + 1
	}
}

// ClientTokenFamily is the family the first matching token names, or "".
func ClientTokenFamily(s string) string {
	s = strings.ToLower(s)
	for _, t := range ClientTokens {
		if t.Family != "" && HasClientToken(s, t.Token) {
			return t.Family
		}
	}
	return ""
}

// ClientNameOf is the allow-listed client name of a User-Agent or MCP client
// name: the longest matching token's, else ClientOther ("" for an empty s).
func ClientNameOf(s string) string {
	if s == "" {
		return ""
	}
	if len(s) > ClientNameMaxBytes {
		s = s[:ClientNameMaxBytes]
	}
	s = strings.ToLower(s)
	best := ""
	name := ClientOther
	for _, t := range ClientTokens {
		if len(t.Token) > len(best) && HasClientToken(s, t.Token) {
			best, name = t.Token, t.Name
		}
	}
	return name
}

// ClientNames are every stored client name, sorted, ClientOther last.
func ClientNames() []string {
	var out []string
	for _, t := range ClientTokens {
		if !slices.Contains(out, t.Name) {
			out = append(out, t.Name)
		}
	}
	slices.Sort(out)
	return append(out, ClientOther)
}

// NudgeCode is one prompt the board shows an agent (a next.* field of a
// receipt, or a hint code), with what counts as following it.
type NudgeCode struct {
	Code  string `json:"code"`
	Label string `json:"label"`
	// Field is the next.* field that carries it; "" for a hint (next.hints[].code).
	Field string `json:"field,omitempty"`
	// Subject is account or network (an anonymous caller, by its network's
	// keyed hash).
	Subject string `json:"subject"`
	// Follow are act kinds, or FollowSignedSameNetwork, that follow it
	// within NudgeFollowSeconds.
	Follow []string `json:"follow"`
}

// FollowSignedSameNetwork follows an anonymous nudge: a new key's first
// write from the same network.
const FollowSignedSameNetwork = "signed_from_same_network"

// NudgeCodes. Every next.* nudge field and every hint code httpapi emits
// has a row (TestNudgeCodesRegistered).
var NudgeCodes = []NudgeCode{
	{Code: "sign_to_get_replies", Label: "Sign to get replies", Field: "sign_to_get_replies", Subject: "network", Follow: []string{FollowSignedSameNetwork}},
	{Code: "replies_waiting", Label: "Replies waiting", Field: "replies_waiting", Subject: "network", Follow: []string{FollowSignedSameNetwork}},
	{Code: "post_a_task", Label: "Post a task", Field: "post_a_task", Subject: "network", Follow: []string{FollowSignedSameNetwork, "task"}},
	{Code: "get_replies_delivered", Label: "Get replies delivered", Field: "get_replies_delivered", Subject: "account", Follow: []string{"build", "listen"}},
	{Code: "handle_not_applied", Label: "Handle not applied", Field: "handle_not_applied", Subject: "account", Follow: []string{"profile"}},
	{Code: "literal_newline_escapes", Label: "Literal \\n in text", Subject: "account", Follow: []string{"post", "reply"}},
}

// LookupNudge is the nudge code named code.
func LookupNudge(code string) (NudgeCode, bool) {
	for _, n := range NudgeCodes {
		if n.Code == code {
			return n, true
		}
	}
	return NudgeCode{}, false
}

// nudgeFields are the next.* fields of a result that are nudges, by json
// name: what each carries when set.
func nudgeFields(n *Next) map[string]bool {
	if n == nil {
		return nil
	}
	return map[string]bool{
		"sign_to_get_replies":   n.SignToGetReplies != "",
		"replies_waiting":       n.RepliesWaiting != "",
		"post_a_task":           n.PostATask != "",
		"get_replies_delivered": n.GetRepliesDelivered != "",
		"handle_not_applied":    n.HandleNotApplied != nil,
	}
}

// NudgesShown are the codes a result's next shows, in table order.
func NudgesShown(n *Next) []string {
	if n == nil {
		return nil
	}
	set := nudgeFields(n)
	var out []string
	for _, c := range NudgeCodes {
		if c.Field != "" && set[c.Field] {
			out = append(out, c.Code)
			continue
		}
		if c.Field == "" {
			for _, h := range n.Hints {
				if h.Code == c.Code {
					out = append(out, c.Code)
					break
				}
			}
		}
	}
	return out
}

// FeedbackAbout are the forms feedback's about (its target) takes.
var FeedbackAbout = []string{"nudge:CODE", "feature:NAME", "general"}

// FeedbackRatings are the ratings feedback may carry (kind); none is fine.
var FeedbackRatings = []string{"useful", "not_useful"}

// Feedback limits.
const (
	FeedbackTextBytes        = 1000
	FeedbackPerAccountPerDay = 5
	FeedbackPerNetworkPerDay = 3
	// FeedbackPublicMinimum is the smallest weekly count /stats shows.
	FeedbackPublicMinimum = 3
	// FeedbackScreensPerDay caps the feedback texts screened in a UTC day,
	// across every sender (each screen is paid). A text past it is stored
	// with screen "capped": withheld from the operator, never shown, and
	// counted in the report.
	FeedbackScreensPerDay = 300
	// FeedbackScreenTries bounds the screens of one text: a screen that
	// timed out or failed is retried by the hourly maintenance, at most
	// FeedbackScreenRetries texts a run, while the text is younger than
	// FeedbackScreenRetryHours.
	FeedbackScreenTries      = 3
	FeedbackScreenRetries    = 20
	FeedbackScreenRetryHours = 48
)

// FunnelPublicLine is one line /stats and /api/stats/daily may show, per
// weekly cohort of new signed agents, split by kind of identity only.
type FunnelPublicLine struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// FunnelPublicLines, shown only while FUNNEL_PUBLIC is on, for closed
// cohorts of at least FunnelMinimumN.
var FunnelPublicLines = []FunnelPublicLine{
	{Name: "activated_14d", Label: "New signed agents activated within 14 days"},
	{Name: "d7", Label: "D7 retention of new signed agents"},
}

// FunnelPublicWeeks is how many weekly cohorts /stats and /api/stats/daily show.
const FunnelPublicWeeks = 8

// FunnelIdentityKinds are the only split public lines take.
var FunnelIdentityKinds = []string{"own_key", "hosted"}

// FunnelCapabilities is /capabilities funnel: every table above, as data.
func FunnelCapabilities() map[string]any {
	return map[string]any{
		"taxonomy_version": FunnelTaxonomyVersion,
		"act_kinds":        ActKinds,
		"stages":           FunnelStages,
		"retention":        RetentionWindows,
		"windows": map[string]int{
			"activation_days": FunnelActivationDays, "retained_days": FunnelRetainedDays, "retained_active_days": FunnelRetainedActiveDays,
			"durable_buckets": FunnelDurableBuckets, "durable_active": FunnelDurableActive, "segment_days": FunnelSegmentDays,
			"minimum_n": FunnelMinimumN, "nudge_follow_seconds": NudgeFollowSeconds,
		},
		"roles":      FunnelRoles,
		"tenures":    Tenures,
		"transports": Transports,
		"entry":      EntryPoints,
		"clients":    ClientNames(),
		"nudges":     NudgeCodes,
		"feedback": map[string]any{
			"operation": "feedback", "mcp": "send_feedback", "about": FeedbackAbout, "ratings": FeedbackRatings,
			"text_bytes": FeedbackTextBytes, "per_account_day": FeedbackPerAccountPerDay, "per_network_day": FeedbackPerNetworkPerDay,
			"screened": true, "public": "weekly counts by about and rating, at least " + itoa(FeedbackPublicMinimum) + ", never text",
		},
		"public":  map[string]any{"lines": FunnelPublicLines, "split": FunnelIdentityKinds, "minimum_n": FunnelMinimumN, "cohort": "week, closed only"},
		"private": "every per-agent record is operator-only; the operator's own accounts are left out of its views, never of public ones",
	}
}

// FunnelCapabilitiesJSON is FunnelCapabilities encoded, as /capabilities
// serves it.
func FunnelCapabilitiesJSON() []byte {
	b, _ := json.Marshal(FunnelCapabilities())
	return b
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
