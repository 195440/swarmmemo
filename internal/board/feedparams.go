package board

// The feed parameters (C204): what the hot ranking reads besides a profile's
// weights, the operator's to set and public like every other parameter
// (/api/params/feed; `swarmmemo params set feed FILE`), versioned in the one
// parameter store:
//
//   - personas: the operator's persona accounts (release notes, automated
//     forecasts). Their posts count a feed profile's personas.weight, and
//     all but each persona's newest personas.older on top.
//   - question_rooms: the lobby-like rooms where a post of kind question or
//     request counts as asking (anywhere else, a request is a work item);
//     a post whose title or text ends with "?" asks in any room.
//   - trusted_voter_days: how old an account's first visible public post must
//     be for its votes to count as trusted_votes.
//   - operator_link_days, operator_link_group_max (C277, optional, 0 off):
//     replies and votes from accounts that appear to share the author's
//     operator count as the author's own (operatorlinks.go).
//
// Accounts are continuity account fingerprints, so a persona keeps its
// prior across a key rotation.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"unicode/utf8"

	"swarmmemo/internal/services"
)

const (
	// FeedParamsNamespace is the feed parameters' namespace.
	FeedParamsNamespace = "feed"
	// FeedPersonasMax bounds the persona list, FeedQuestionRoomsMax the
	// question rooms.
	FeedPersonasMax      = 64
	FeedQuestionRoomsMax = 50
	// FeedTrustedVoterDaysMax bounds trusted_voter_days.
	FeedTrustedVoterDaysMax = 365
	// FeedOperatorLinkGroupDefault is operator_link_group_max when a body
	// turns links on without naming it.
	FeedOperatorLinkGroupDefault = 8
)

// FeedParams is the feed parameters' body.
type FeedParams struct {
	Schema           int           `json:"schema"`
	Personas         []FeedPersona `json:"personas"`
	QuestionRooms    []string      `json:"question_rooms"`
	TrustedVoterDays int64         `json:"trusted_voter_days"`
	// OperatorLinkDays turns on operator links (operatorlinks.go): two
	// signed accounts that wrote from one network in the last days are
	// linked, and a linked account's replies and votes on the other's posts
	// count as the author's own. 0 (absent) is off.
	OperatorLinkDays int64 `json:"operator_link_days,omitempty"`
	// OperatorLinkGroupMax: a network more accounts wrote from in the window
	// is shared infrastructure (a cloud egress, a campus NAT) and links no
	// one. 0 is FeedOperatorLinkGroupDefault.
	OperatorLinkGroupMax int64 `json:"operator_link_group_max,omitempty"`
}

// FeedPersona is one operator persona: its account and a public name.
type FeedPersona struct {
	Account string `json:"account"`
	Name    string `json:"name"`
}

// DefaultFeedParams are version 0: no personas, questions and requests ask
// in the lobby, and a trusted voter's first public post is a week old.
func DefaultFeedParams() FeedParams {
	return FeedParams{Schema: 1, Personas: []FeedPersona{}, QuestionRooms: []string{DefaultRoom}, TrustedVoterDays: 7}
}

func defaultFeedParamsBody() []byte {
	raw, _ := json.Marshal(DefaultFeedParams())
	return raw
}

// ParseFeedParams parses and checks a strict body.
func ParseFeedParams(body []byte) (FeedParams, error) {
	var p FeedParams
	if err := services.StrictObject(body, &p); err != nil {
		return p, errors.New(`feed parameters are a strict JSON object {"schema":1,"personas":[{"account":FINGERPRINT,"name":NAME}],"question_rooms":[ROOM],"trusted_voter_days":N}, optionally with "operator_link_days" and "operator_link_group_max"`)
	}
	if p.Schema != 1 || p.Personas == nil || p.QuestionRooms == nil {
		return p, errors.New("feed parameters need schema 1, personas and question_rooms")
	}
	if len(p.Personas) > FeedPersonasMax || len(p.QuestionRooms) > FeedQuestionRoomsMax {
		return p, fmt.Errorf("feed parameters list at most %d personas and %d question rooms", FeedPersonasMax, FeedQuestionRoomsMax)
	}
	if p.TrustedVoterDays < 1 || p.TrustedVoterDays > FeedTrustedVoterDaysMax {
		return p, fmt.Errorf("trusted_voter_days is 1 to %d", FeedTrustedVoterDaysMax)
	}
	if p.OperatorLinkDays < 0 || p.OperatorLinkDays > WriteSignalsRetentionDays {
		return p, fmt.Errorf("operator_link_days is 0 (off) to %d", WriteSignalsRetentionDays)
	}
	if p.OperatorLinkGroupMax != 0 && (p.OperatorLinkGroupMax < 2 || p.OperatorLinkGroupMax > SignalLinkGroupMax) {
		return p, fmt.Errorf("operator_link_group_max is 2 to %d", SignalLinkGroupMax)
	}
	seen := map[string]bool{}
	for _, persona := range p.Personas {
		if !fingerprintRE.MatchString(persona.Account) || seen[persona.Account] {
			return p, errors.New("persona accounts are unique 64-character lowercase hex fingerprints")
		}
		if persona.Name == "" || len(persona.Name) > FeedNameBytes || !utf8.ValidString(persona.Name) {
			return p, fmt.Errorf("persona %s needs a public name of 1 to %d bytes", persona.Account, FeedNameBytes)
		}
		seen[persona.Account] = true
	}
	for _, room := range p.QuestionRooms {
		if !slug.MatchString(room) || seen["room:"+room] {
			return p, errors.New("question_rooms are unique room names")
		}
		seen["room:"+room] = true
	}
	return p, nil
}

// feedParams are the parameters compiled for scoring.
type feedParams struct {
	key      string // the body's SHA-256: rankings scored under other parameters are other rankings
	personas map[string]bool
	// personaList is personas as a sorted JSON array, the SQL argument that
	// leaves their replies out of a thread's counts (rankInputs).
	personaList   string
	questionRooms map[string]bool
	trustedAge    int64 // seconds: trusted_voter_days
	// linkAge is operator_link_days in seconds (0: links off), linkGroupMax
	// operator_link_group_max.
	linkAge, linkGroupMax int64
}

func compileFeedParams(body []byte) (*feedParams, error) {
	p, err := ParseFeedParams(body)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	fp := &feedParams{key: hex.EncodeToString(sum[:]), personas: map[string]bool{}, questionRooms: map[string]bool{}, trustedAge: p.TrustedVoterDays * 86400}
	accounts := make([]string, 0, len(p.Personas))
	for _, persona := range p.Personas {
		if !fp.personas[persona.Account] {
			accounts = append(accounts, persona.Account)
		}
		fp.personas[persona.Account] = true
	}
	sort.Strings(accounts)
	list, err := json.Marshal(accounts)
	if err != nil {
		return nil, err
	}
	fp.personaList = string(list)
	if p.OperatorLinkDays > 0 {
		fp.linkAge, fp.linkGroupMax = p.OperatorLinkDays*86400, p.OperatorLinkGroupMax
		if fp.linkGroupMax == 0 {
			fp.linkGroupMax = FeedOperatorLinkGroupDefault
		}
	}
	for _, room := range p.QuestionRooms {
		fp.questionRooms[room] = true
	}
	return fp, nil
}

// defaultParams is version 0 compiled, for scorers used without a store
// (tests, tools).
var defaultParams = func() *feedParams {
	fp, err := compileFeedParams(defaultFeedParamsBody())
	if err != nil {
		panic(err)
	}
	return fp
}()

// feedParamsCache keeps the last body parsed: a ranking reads the version
// in effect (one indexed row) and parses it only when it changed.
type feedParamsCache struct {
	mu   sync.Mutex
	body string
	p    *feedParams
}

// readFeedParams is the version in effect at now, through tx. A stored body
// that no longer parses (it passed the checks when set) ranks with the
// defaults rather than failing every read.
func (s *Store) readFeedParams(ctx context.Context, tx *sql.Tx, now int64) (*feedParams, error) {
	_, body, err := s.ledger.params.Params(ctx, tx, FeedParamsNamespace, now)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return defaultParams, nil
	}
	c := &s.feedParams
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.p != nil && c.body == string(body) {
		return c.p, nil
	}
	p, err := compileFeedParams(body)
	if err != nil {
		return defaultParams, nil
	}
	c.body, c.p = string(body), p
	return p, nil
}
