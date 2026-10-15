package trust

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"swarmmemo/internal/allowance"
)

// The public penalty log (RFC 0021 §8): every penalty with its trigger and
// evidence, every moderation decision and every operator override is an
// entry. A run opens an entry for each new detector finding and applies a
// pending one (moderation, override): its level, end, burned roots and
// backers' liabilities. A lift is an entry too: it reverses another, with
// its author and reason, from the next run on.

// Penalty triggers.
const (
	TriggerRing         = "ring"
	TriggerFunnel       = "funnel"
	TriggerSharedWallet = "shared_wallet"
	TriggerModeration   = "moderation"
	TriggerOverride     = "override"
	TriggerLift         = "lift"
)

// PenaltyEntry is one log entry as /api/trust/penalties shows it.
type PenaltyEntry struct {
	Seq             int64       `json:"seq"`
	ID              string      `json:"id"`
	Trigger         string      `json:"trigger"`
	State           string      `json:"state"` // pending (waiting for the next run) or applied
	Members         []string    `json:"members"`
	Evidence        string      `json:"evidence"`
	Reason          string      `json:"reason"`
	Author          string      `json:"author"`
	DetectorVersion int64       `json:"detector_version"`
	Run             int64       `json:"run"`
	Level           int64       `json:"level"`
	StartsAt        int64       `json:"starts_at"`
	EndsAt          int64       `json:"ends_at"`
	DurationDays    int64       `json:"duration_days"`
	Burned          []string    `json:"burned"`
	BurnedCents     int64       `json:"burned_cents"`
	Backers         []BackerOut `json:"backers"`
	BackersLost     int64       `json:"backers_lost_cents"`
	Lifts           string      `json:"lifts,omitempty"`     // a lift: the entry it reverses
	LiftedBy        string      `json:"lifted_by,omitempty"` // the lift that reversed this entry
	Active          bool        `json:"active"`
	CreatedAt       int64       `json:"created_at"`
}

// PenaltyLog lists entries newest first, before the given seq (0: from the
// newest), at most limit; account, when set, keeps the entries that name it
// as a member or a backer. next is the before cursor of the following page
// (0 at the end).
func PenaltyLog(ctx context.Context, q allowance.Querier, before int64, limit int, account string, now int64) ([]PenaltyEntry, int64, error) {
	if before <= 0 {
		before = math.MaxInt64
	}
	where, args := "seq<?", []any{before}
	if account != "" {
		where += " AND (instr(members,?)>0 OR instr(backers,?)>0)"
		args = append(args, `"`+account+`"`, `"`+account+`"`)
	}
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, `SELECT seq,id,trigger,state,members,evidence,reason,author,detector_version,run_id,level,starts_at,ends_at,burned,backers,burned_cents,lifts,created_at,
 coalesce((SELECT l.id FROM trust_penalty_log l WHERE l.lifts=trust_penalty_log.id ORDER BY l.seq LIMIT 1),'')
 FROM trust_penalty_log WHERE `+where+` ORDER BY seq DESC LIMIT ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []PenaltyEntry{}
	var last int64
	for rows.Next() {
		var e PenaltyEntry
		var members, burned, backers string
		if err = rows.Scan(&e.Seq, &e.ID, &e.Trigger, &e.State, &members, &e.Evidence, &e.Reason, &e.Author, &e.DetectorVersion, &e.Run, &e.Level,
			&e.StartsAt, &e.EndsAt, &burned, &backers, &e.BurnedCents, &e.Lifts, &e.CreatedAt, &e.LiftedBy); err != nil {
			return nil, 0, err
		}
		e.Members, e.Burned, e.Backers = []string{}, []string{}, []BackerOut{}
		_ = json.Unmarshal([]byte(members), &e.Members)
		_ = json.Unmarshal([]byte(burned), &e.Burned)
		_ = json.Unmarshal([]byte(backers), &e.Backers)
		for _, b := range e.Backers {
			e.BackersLost += b.LostCents
		}
		if e.EndsAt > e.StartsAt {
			e.DurationDays = (e.EndsAt - e.StartsAt) / day
		}
		e.Active = e.Trigger != TriggerLift && e.State == "applied" && e.LiftedBy == "" && e.StartsAt <= now && now < e.EndsAt
		last = e.Seq
		out = append(out, e)
	}
	if len(out) < limit {
		last = 0
	}
	return out, last, rows.Err()
}

// PenaltyRecords are the log's entries as snapshot records (penalty, burn,
// liability, lift), what a run as of asOf reads: every entry created before
// asOf. Entries are bounded (detectors and the operator write them), so the
// whole log is read in one pass of pages.
func PenaltyRecords(ctx context.Context, q allowance.Querier, asOf int64, after int64, limit int) ([]Record, int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT seq,id,trigger,state,members,evidence,level,starts_at,ends_at,burned,backers,lifts,created_at FROM trust_penalty_log
 WHERE seq>? AND created_at<? ORDER BY seq LIMIT ?`, after, asOf, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []Record
	n := 0
	for rows.Next() {
		n++
		var seq, level, starts, ends, created int64
		var id, trigger, state, members, evidence, burned, backers, lifts string
		if err = rows.Scan(&seq, &id, &trigger, &state, &members, &evidence, &level, &starts, &ends, &burned, &backers, &lifts, &created); err != nil {
			return nil, after, err
		}
		after = seq
		if trigger == TriggerLift {
			out = append(out, Record{Type: "lift", ID: lifts, CreatedAt: created})
			continue
		}
		var ms, bs []string
		var bk []BackerOut
		_ = json.Unmarshal([]byte(members), &ms)
		_ = json.Unmarshal([]byte(burned), &bs)
		_ = json.Unmarshal([]byte(backers), &bk)
		for _, m := range ms {
			out = append(out, Record{Type: "penalty", ID: id, Kind: trigger, Account: m, CreatedAt: starts, EndsAt: ends, Value: level, State: state, Evidence: evidence})
		}
		for _, b := range bs {
			out = append(out, Record{Type: "burn", ID: id, Root: b})
		}
		for _, b := range bk {
			out = append(out, Record{Type: "liability", ID: id, Account: b.Account, FractionPPM: b.FractionPPM, CreatedAt: starts, EndsAt: ends, Value: b.Level})
		}
	}
	if n < limit {
		after = -1
	}
	return out, after, rows.Err()
}

// ReasonBytesMax bounds an entry's reason; EvidenceBytesMax its evidence.
const (
	ReasonBytesMax   = 500
	EvidenceBytesMax = 2000
	MembersMax       = 64
)

var memberRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func checkText(name, s string, maxBytes int, required bool) error {
	s = strings.TrimSpace(s)
	if required && s == "" || len(s) > maxBytes {
		return fmt.Errorf("a public %s of 1-%d bytes is required", name, maxBytes)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("the %s must be one line without control characters", name)
		}
	}
	return nil
}

// NewPenalty records an operator's penalty (trigger moderation, only in the
// moderation standard's categories, or override) on members, pending until
// the next run applies it (level, end, burned roots, backers). It returns
// the entry's id.
func NewPenalty(ctx context.Context, q allowance.Querier, trigger string, members []string, evidence, reason, author string, now int64) (string, error) {
	if trigger != TriggerModeration && trigger != TriggerOverride {
		return "", fmt.Errorf("an operator penalty's trigger is moderation or override")
	}
	if len(members) == 0 || len(members) > MembersMax {
		return "", fmt.Errorf("name 1-%d member accounts", MembersMax)
	}
	set := map[string]bool{}
	for _, m := range members {
		if !memberRE.MatchString(m) {
			return "", fmt.Errorf("member %q is not an account fingerprint", m)
		}
		set[m] = true
	}
	list := make([]string, 0, len(set))
	for m := range set {
		list = append(list, m)
	}
	sort.Strings(list)
	if err := checkText("reason", reason, ReasonBytesMax, true); err != nil {
		return "", err
	}
	if err := checkText("evidence", evidence, EvidenceBytesMax, trigger == TriggerModeration); err != nil {
		return "", err
	}
	if err := checkText("author", author, 100, true); err != nil {
		return "", err
	}
	id := trigger + "-" + randomID()
	_, err := q.ExecContext(ctx, `INSERT INTO trust_penalty_log(id,trigger,state,members,evidence,reason,author,starts_at,created_at) VALUES(?,?,'pending',?,?,?,?,?,?)`,
		id, trigger, string(Canonical(list)), strings.TrimSpace(evidence), strings.TrimSpace(reason), strings.TrimSpace(author), now, now)
	return id, err
}

// LiftPenalty records an operator override reversing entry id (pending or
// applied), with its author and reason: from the next run the entry's
// members, burns and liabilities no longer apply. It returns the lift's id.
func LiftPenalty(ctx context.Context, q allowance.Querier, id, reason, author string, now int64) (string, error) {
	if err := checkText("reason", reason, ReasonBytesMax, true); err != nil {
		return "", err
	}
	if err := checkText("author", author, 100, true); err != nil {
		return "", err
	}
	var trigger string
	err := q.QueryRowContext(ctx, "SELECT trigger FROM trust_penalty_log WHERE id=?", id).Scan(&trigger)
	if errors.Is(err, sql.ErrNoRows) || err == nil && trigger == TriggerLift {
		return "", fmt.Errorf("no penalty entry %q", id)
	}
	if err != nil {
		return "", err
	}
	var lifted int
	if err = q.QueryRowContext(ctx, "SELECT count(*) FROM trust_penalty_log WHERE lifts=?", id).Scan(&lifted); err != nil {
		return "", err
	}
	if lifted > 0 {
		return "", fmt.Errorf("entry %q is already lifted", id)
	}
	lift := TriggerLift + "-" + randomID()
	_, err = q.ExecContext(ctx, `INSERT INTO trust_penalty_log(id,trigger,state,reason,author,starts_at,lifts,created_at) VALUES(?,?,'applied',?,?,?,?,?)`,
		lift, TriggerLift, strings.TrimSpace(reason), strings.TrimSpace(author), now, id, now)
	return lift, err
}

func randomID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
