package board

// Standing tasks (C278): one paid task that takes many results. The
// requester opens its root request with work.create data seats (and
// optionally per_agent_day, day_max, up to three reward tiers and daily USDC
// caps). A worker does what it does for any task: post the result as a
// reply to the root, then work.claim the root with target = that reply. On a
// standing task that claim does not claim the root: it opens a new work item
// whose ID is the reply's, with the worker, fence 1, state submitted and the
// reply as its result, and from then on it is ordinary work (accept, settle,
// history, receipts, records, inbox).
//
// Money. The root holds a credit pool: the largest tier's credits times
// seats, escrowed once at create. Opening a result moves one reward from the
// pool into the result's own escrow (EscrowSplit: no second fee). An accept
// pays the tier it names (default the first) and returns any excess; a reject
// or a lapse returns the whole reward to the pool while the pool lives, else
// to the requester. After an accept the requester's credits refill the pool
// by one reward (best effort: without them the pool is a seat short). USDC is
// promised per result, never on the root; at accept it is owed in the tier's
// amount, unless the day's USDC caps (usdc_per_agent_day, usdc_day_max) are
// reached: then the result is paid in credits only and the acknowledgement
// says so.
//
// Seats. A seat holds a result waiting for a verdict or an accepted result
// whose USDC is still owed. Each result has its own verdict deadline,
// WorkStandingVerdictTTL after it opens; undecided by then it lapses (its
// seat and reward return, and it counts as unpaid_lapsed on the requester's
// record). A result whose reply is hidden gives its seat and reward back
// as cancelled, reason hidden, and is left out of that record (C281). A
// reject is terminal for that result (stored cancelled; its last
// transition is the reject). The root takes results until its deadline
// leaves less than the verdict window.
//
// Results cap (C346). Optional results_max bounds the results a standing
// task takes over its life: those accepted (paid or USDC owed) and those
// waiting for a verdict. Rejected, lapsed and hidden results do not count.
// Once they reach it the task is full: standing.open reads 0, standing.full
// true, and a claim is 409 results_max_reached. The requester stops a
// standing task early with work.cancel on the task itself.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"swarmmemo/internal/allowance"
	"swarmmemo/internal/services"
)

const workStandingSchema = `
CREATE TABLE IF NOT EXISTS work_standing (
 work_id TEXT PRIMARY KEY REFERENCES works(id), seats INTEGER NOT NULL, per_agent_day INTEGER NOT NULL,
 day_max INTEGER NOT NULL, tiers TEXT NOT NULL, usdc_per_agent_day INTEGER NOT NULL DEFAULT 0,
 usdc_day_max INTEGER NOT NULL DEFAULT 0, verdict_ttl INTEGER NOT NULL);
`

// Bounds of a standing task.
const (
	WorkStandingSeatsMax = 50
	WorkStandingDayMax   = 1000
	WorkStandingTiersMax = 3
	// WorkStandingResultsMax bounds results_max, the results a standing
	// task takes over its life (C346).
	WorkStandingResultsMax = 10000
	WorkStandingTierFirst  = "reward" // the one tier a reward (no tiers) makes
	// WorkStandingVerdictTTL is each result's own verdict deadline, from the
	// claim that opened it: 3 days.
	WorkStandingVerdictTTL int64 = 3 * 86400
)

var workStandingTierRE = regexp.MustCompile(`^[a-z0-9][a-z0-9+_.-]{0,31}$`)

// WorkStandingTier is one reward a verdict on a standing task's result may
// pay: credits (at least 1) and optionally USDC, a decimal string.
type WorkStandingTier struct {
	Name    string `json:"name"`
	Credits int64  `json:"credits"`
	USDC    string `json:"usdc,omitempty"`
}

func (t WorkStandingTier) usdcUnits() int64 {
	if t.USDC == "" {
		return 0
	}
	u, _ := services.ParseUSDC(t.USDC)
	return u
}

// workStandingData is a standing create's data, parsed.
type workStandingData struct {
	Seats, PerAgentDay, DayMax          int64
	ResultsMax                          int64 // 0: no cap
	Tiers                               []WorkStandingTier
	USDCPerAgentDayText, USDCDayMaxText string
	USDCPerAgentDay, USDCDayMax         int64
}

// workStanding is a standing root's stored configuration.
type workStanding struct {
	ID                          string
	Seats, PerAgentDay, DayMax  int64
	ResultsMax                  int64 // 0: no cap (C346)
	Tiers                       []WorkStandingTier
	USDCPerAgentDay, USDCDayMax int64
	VerdictTTL                  int64
}

// maxCredits is the largest tier's credits: what one seat holds.
func (st *workStanding) maxCredits() int64 {
	var m int64
	for _, t := range st.Tiers {
		m = max(m, t.Credits)
	}
	return m
}

func (st *workStanding) maxUSDC() int64 {
	var m int64
	for _, t := range st.Tiers {
		m = max(m, t.usdcUnits())
	}
	return m
}

// WorkStandingSeats is a standing root's seat summary and the one source of
// its names (C302): work.get standing, works.list data standing
// (?standing=ID) and MCP find_work's compact rows all carry this struct.
// Open is the seats a result can take now; Submitted the results waiting for
// a verdict; Owed the accepted results whose USDC is not yet settled; Paid
// the other accepted results; PerAgentDay the results one agent may open a
// day; Full, sent only when true, that the task holds all the results its
// results_max allows and takes no more claims (C346).
type WorkStandingSeats struct {
	Seats       int64 `json:"seats"`
	Open        int64 `json:"open"`
	Submitted   int64 `json:"submitted"`
	Owed        int64 `json:"owed"`
	Paid        int64 `json:"paid"`
	PerAgentDay int64 `json:"per_agent_day"`
	Full        bool  `json:"full,omitempty"`
}

// WorkStandingSeatNames is WorkStandingSeats's wire names in order, read
// from its json tags, for the descriptions that list them.
func WorkStandingSeatNames() []string {
	kind := reflect.TypeFor[WorkStandingSeats]()
	names := make([]string, kind.NumField())
	for i := range names {
		names[i], _, _ = strings.Cut(kind.Field(i).Tag.Get("json"), ",")
	}
	return names
}

// WorkStanding is a standing root's seats as reads show them: the seat
// summary, flattened, and the rest. Results is every result it opened;
// ResultsMax, when set, the accepted and waiting results it takes in all.
type WorkStanding struct {
	WorkStandingSeats
	Results         int64              `json:"results"`
	ResultsMax      int64              `json:"results_max,omitempty"`
	DayMax          int64              `json:"day_max"`
	OpenedToday     int64              `json:"opened_today"`
	Tiers           []WorkStandingTier `json:"tiers"`
	USDCPerAgentDay string             `json:"usdc_per_agent_day,omitempty"`
	USDCDayMax      string             `json:"usdc_day_max,omitempty"`
	VerdictTTL      int64              `json:"verdict_ttl"`
	ResultsUntil    int64              `json:"results_until"`
}

func invalidStanding(msg string) error {
	return problem(400, "invalid_work_data", msg+" "+WorkStandingRule)
}

// WorkStandingRule is a standing create's data, the line its refusals end with.
const WorkStandingRule = `A standing task is work.create data seats (1–50) with a reward that has credits, or tiers (1–3 of {"name":"bug","credits":N,"usdc":"0.20"}, each with credits); optional per_agent_day (default 1, at most seats), day_max (default seats, at most 1000), results_max (results accepted or waiting over its life, seats to 10000; default no cap), and usdc_per_agent_day and usdc_day_max (decimal USDC strings, each at least every tier's USDC). No reviewer, reviewer_fee or reject_fee.`

// parseWorkStandingTiers reads data tiers.
func parseWorkStandingTiers(raw json.RawMessage) ([]WorkStandingTier, error) {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) < 1 || len(items) > WorkStandingTiersMax {
		return nil, invalidStanding("tiers is a list of 1 to 3 tiers.")
	}
	out := []WorkStandingTier{}
	seen := map[string]bool{}
	for _, item := range items {
		var o struct {
			Name    *string `json:"name"`
			Credits *int64  `json:"credits"`
			USDC    *string `json:"usdc"`
		}
		if services.StrictObject(item, &o) != nil || o.Name == nil || o.Credits == nil {
			return nil, invalidStanding("Each tier is {\"name\":\"bug\",\"credits\":N} with an optional \"usdc\":\"0.20\".")
		}
		if !workStandingTierRE.MatchString(*o.Name) || seen[*o.Name] {
			return nil, invalidStanding("Tier names are unique, 1–32 lowercase letters, digits and +_.-, starting with a letter or digit.")
		}
		seen[*o.Name] = true
		if *o.Credits < 1 || *o.Credits > WorkRewardMax {
			return nil, invalidStanding(fmt.Sprintf("A tier's credits are whole, 1 to %d.", WorkRewardMax))
		}
		t := WorkStandingTier{Name: *o.Name, Credits: *o.Credits}
		if o.USDC != nil {
			u, ok := services.ParseUSDC(*o.USDC)
			if !ok || u < WorkUSDCMin || u > WorkUSDCMax {
				return nil, invalidStanding(fmt.Sprintf("A tier's usdc is a decimal string from %s to %s.", services.FormatUSDC(WorkUSDCMin), services.FormatUSDC(WorkUSDCMax)))
			}
			t.USDC = services.FormatUSDC(u)
		}
		out = append(out, t)
	}
	return out, nil
}

// checkWorkStandingData checks a create's standing fields together, and
// fills their defaults.
func checkWorkStandingData(d *workData, seen map[string]bool) error {
	set := false
	for _, name := range []string{"seats", "per_agent_day", "day_max", "results_max", "tiers", "usdc_per_agent_day", "usdc_day_max"} {
		set = set || seen[name]
	}
	if !set {
		return nil
	}
	st := &d.Standing
	if !seen["seats"] {
		return invalidStanding("per_agent_day, day_max, results_max, tiers and the USDC caps go with seats.")
	}
	if st.Seats < 1 || st.Seats > WorkStandingSeatsMax {
		return invalidStanding("seats is 1 to 50.")
	}
	if seen["reviewer"] || seen["reviewer_fee"] || seen["reject_fee"] {
		return invalidStanding("A standing task takes no reviewer, reviewer_fee or reject_fee: the requester judges each result.")
	}
	if !seen["per_agent_day"] {
		st.PerAgentDay = 1
	} else if st.PerAgentDay < 1 || st.PerAgentDay > st.Seats {
		return invalidStanding("per_agent_day is 1 to seats.")
	}
	if !seen["day_max"] {
		st.DayMax = st.Seats
	} else if st.DayMax < 1 || st.DayMax > WorkStandingDayMax {
		return invalidStanding("day_max is 1 to 1000.")
	}
	if seen["results_max"] && (st.ResultsMax < st.Seats || st.ResultsMax > WorkStandingResultsMax) {
		return invalidStanding(fmt.Sprintf("results_max is seats to %d.", WorkStandingResultsMax))
	}
	switch {
	case seen["reward"] && seen["tiers"]:
		return invalidStanding("Send reward or tiers, not both.")
	case seen["reward"]:
		if d.Reward < 1 {
			return invalidStanding("A standing task's reward has credits.")
		}
		t := WorkStandingTier{Name: WorkStandingTierFirst, Credits: d.Reward}
		if d.RewardUSDC > 0 {
			t.USDC = services.FormatUSDC(d.RewardUSDC)
		}
		st.Tiers = []WorkStandingTier{t}
	case !seen["tiers"]:
		return invalidStanding("A standing task pays each result: send reward or tiers.")
	}
	var maxUSDC int64
	for _, t := range st.Tiers {
		maxUSDC = max(maxUSDC, t.usdcUnits())
	}
	for _, c := range []struct {
		name string
		text string
		dest *int64
	}{{"usdc_per_agent_day", st.USDCPerAgentDayText, &st.USDCPerAgentDay}, {"usdc_day_max", st.USDCDayMaxText, &st.USDCDayMax}} {
		if !seen[c.name] {
			continue
		}
		u, ok := services.ParseUSDC(c.text)
		if !ok || u < WorkUSDCMin || u > WorkUSDCMax*WorkStandingDayMax {
			return invalidStanding(c.name + " is a decimal USDC string, at least 0.01.")
		}
		if maxUSDC == 0 {
			return invalidStanding(c.name + " goes with a tier that pays USDC.")
		}
		if u < maxUSDC {
			return invalidStanding("Each tier's USDC must fit within " + c.name + ".")
		}
		*c.dest = u
	}
	return nil
}

func loadWorkStanding(ctx context.Context, tx *sql.Tx, id string) (*workStanding, error) {
	st := workStanding{ID: id}
	var tiers string
	err := tx.QueryRowContext(ctx, `SELECT seats,per_agent_day,day_max,results_max,tiers,usdc_per_agent_day,usdc_day_max,verdict_ttl FROM work_standing WHERE work_id=?`, id).
		Scan(&st.Seats, &st.PerAgentDay, &st.DayMax, &st.ResultsMax, &tiers, &st.USDCPerAgentDay, &st.USDCDayMax, &st.VerdictTTL)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(tiers), &st.Tiers); err != nil || len(st.Tiers) == 0 {
		return nil, fmt.Errorf("board: standing task %s has no tiers (%v)", id, err)
	}
	return &st, nil
}

// createWorkStanding records a new standing root and holds its credit pool.
// The root itself promises no USDC: each result does.
func (s *Store) createWorkStanding(ctx context.Context, tx *sql.Tx, a actor, w workRow, d workData, now int64) error {
	st := workStanding{ID: w.ID, Seats: d.Standing.Seats, PerAgentDay: d.Standing.PerAgentDay, DayMax: d.Standing.DayMax, ResultsMax: d.Standing.ResultsMax, Tiers: d.Standing.Tiers,
		USDCPerAgentDay: d.Standing.USDCPerAgentDay, USDCDayMax: d.Standing.USDCDayMax, VerdictTTL: WorkStandingVerdictTTL}
	if st.maxUSDC() > 0 && !s.WorkUSDCEnabled() {
		return errUSDCRewardsUnavailable()
	}
	pool := st.maxCredits() * st.Seats
	if pool > WorkRewardMax {
		return invalidStanding(fmt.Sprintf("The pool, the largest tier's credits times seats, is at most %d credits.", WorkRewardMax))
	}
	tiers, _ := json.Marshal(st.Tiers)
	if _, err := tx.ExecContext(ctx, `INSERT INTO work_standing(work_id,seats,per_agent_day,day_max,results_max,tiers,usdc_per_agent_day,usdc_day_max,verdict_ttl) VALUES(?,?,?,?,?,?,?,?,?)`,
		st.ID, st.Seats, st.PerAgentDay, st.DayMax, st.ResultsMax, string(tiers), st.USDCPerAgentDay, st.USDCDayMax, st.VerdictTTL); err != nil {
		return err
	}
	return s.holdWorkEscrows(ctx, tx, a, w, pool, 0, now)
}

// standingCounts is a standing root's seats as they stand at now.
type standingCounts struct {
	waiting, owed, accepted, results, today, mineToday, mineWaiting int64
	mineWaitingID                                                   string
	nextVerdict                                                     int64
}

// counted is the results a results_max counts: accepted (paid or USDC
// owed) and waiting for a verdict; rejected, lapsed and hidden ones do not.
func (c standingCounts) counted() int64 { return c.accepted + c.waiting }

// full says whether the standing task holds all the results its
// results_max allows (never without one).
func (st *workStanding) full(c standingCounts) bool {
	return st.ResultsMax > 0 && c.counted() >= st.ResultsMax
}

// errResultsMaxReached is a claim on a full standing task (C346).
func errResultsMaxReached(st *workStanding, c standingCounts) error {
	return problem(409, "results_max_reached", fmt.Sprintf("This standing task is full: it takes %s in all (results_max) and holds %d accepted and %d waiting for a verdict, so it takes no more claims. A place opens only if the requester rejects a waiting result or one lapses undecided.",
		plural(st.ResultsMax, "result"), c.accepted, c.waiting))
}

// standingHiddenSQL is true for a result whose reply (the item's key, its
// first version) is hidden: by the operator, the room, or its author's
// erasure. Such a result holds no seat and leaves the day's day_max (it
// still counts toward its worker's per_agent_day), its seat and reward
// return on the next claim or sweep (cancelled, reason hidden), and it is
// left out of the requester's record: the requester cannot read it to judge
// it (C281).
const standingHiddenSQL = `EXISTS(SELECT 1 FROM events h WHERE h.id=w.id AND h.hidden<>0)`

func readStandingCounts(ctx context.Context, tx *sql.Tx, id, worker string, now int64) (standingCounts, error) {
	var c standingCounts
	day := now - now%86400
	err := tx.QueryRowContext(ctx, `SELECT
 count(*) FILTER (WHERE w.state='submitted' AND w.deadline>? AND NOT `+standingHiddenSQL+`),
 count(*) FILTER (WHERE w.state='accepted' AND EXISTS(SELECT 1 FROM work_usdc u WHERE u.work_id=w.id AND u.state='payable')),
 count(*) FILTER (WHERE w.state='accepted'),
 count(*),
 count(*) FILTER (WHERE w.created_at>=? AND NOT `+standingHiddenSQL+`),
 count(*) FILTER (WHERE w.created_at>=? AND w.worker=?),
 count(*) FILTER (WHERE w.state='submitted' AND w.deadline>? AND w.worker=? AND NOT `+standingHiddenSQL+`),
 coalesce(min(w.id) FILTER (WHERE w.state='submitted' AND w.deadline>? AND w.worker=? AND NOT `+standingHiddenSQL+`),''),
 coalesce(min(w.deadline) FILTER (WHERE w.state='submitted' AND w.deadline>? AND NOT `+standingHiddenSQL+`),0)
 FROM works w WHERE w.standing_id=?`, now, day, day, worker, now, worker, now, worker, now, id).
		Scan(&c.waiting, &c.owed, &c.accepted, &c.results, &c.today, &c.mineToday, &c.mineWaiting, &c.mineWaitingID, &c.nextVerdict)
	return c, err
}

// standingPool is the root's credit pool: what it holds now, or -1 when it
// no longer holds (cancelled, or released at the deadline).
func standingPool(ctx context.Context, tx *sql.Tx, id string) (*rewardRow, int64, error) {
	r, err := loadWorkReward(ctx, tx, workRewardsTable, id)
	if err != nil || r == nil || r.State != "held" {
		return r, -1, err
	}
	return r, r.Amount, nil
}

// openStandingSeat checks that a result can open on a standing root now and
// returns the item it opens (not yet stored). The result itself is checked
// by the caller, as any claim with a result.
func (s *Store) openStandingSeat(ctx context.Context, tx *sql.Tx, c Command, a actor, root workRow, st *workStanding, now int64) (workRow, error) {
	if c.Target == "" {
		return workRow{}, problem(400, "standing_needs_result", "This is a standing task: post your result as a reply to it, then send work.claim with target, the reply's ID. Each result opens its own task.")
	}
	if now > root.Deadline-st.VerdictTTL {
		return workRow{}, problem(409, "work_state_conflict", "This standing task takes no new results: its deadline leaves less than the verdict window ("+reviewerGraceText(st.VerdictTTL)+") a result needs.")
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN origin<>'' THEN origin ELSE id END FROM events WHERE id=?`, c.Target).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !workIDRE.MatchString(id)) {
		return workRow{}, problem(400, "invalid_work_result", "Result must be a visible signed direct reply to the standing task, in its room, by your account.")
	}
	if err != nil {
		return workRow{}, err
	}
	item := workRow{ID: id, Requester: root.Requester, Title: root.Title, Caps: root.Caps, State: "submitted", Created: now, Updated: now,
		Deadline: now + st.VerdictTTL, Fence: 1, Worker: a.account, Eligibility: root.Eligibility, StandingID: root.ID}
	if a.grant != nil {
		item.AttemptGrantID = a.grant.ID
	}
	return item, nil
}

// checkStandingSeat refuses a result the standing task cannot take now: one
// that already opened a task, a full task, or an agent or a day at its limit.
// Results whose verdict lapsed give their seat and reward back first.
func (s *Store) checkStandingSeat(ctx context.Context, tx *sql.Tx, root workRow, st *workStanding, item workRow, now int64) (standingCounts, error) {
	var other string
	err := tx.QueryRowContext(ctx, `SELECT coalesce(standing_id,'') FROM works WHERE id=?`, item.ID).Scan(&other)
	if err == nil {
		return standingCounts{}, problem(409, "result_in_use", "That reply already opened a task ("+item.ID+"): one task per result. Post a new reply for a new result.")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return standingCounts{}, err
	}
	if err = s.returnLapsedStanding(ctx, tx, root.ID, now); err != nil {
		return standingCounts{}, err
	}
	n, err := readStandingCounts(ctx, tx, root.ID, item.Worker, now)
	if err != nil {
		return n, err
	}
	untilMidnight := int(86400 - now%86400)
	switch {
	case st.full(n):
		return n, errResultsMaxReached(st, n)
	case n.mineWaiting > 0:
		return n, problem(409, "seat_limit", "You have a result waiting for a verdict on this standing task ("+n.mineWaitingID+"); send your next one once it is decided.")
	case n.mineToday >= st.PerAgentDay:
		return n, &Error{Status: 409, Code: "seat_limit", Message: fmt.Sprintf("%s per agent per day on this standing task; you have %s today. Try again after 00:00 UTC.", plural(st.PerAgentDay, "result"), plural(n.mineToday, "result")), RetryAfter: untilMidnight}
	case n.today >= st.DayMax:
		return n, &Error{Status: 409, Code: "seat_limit", Message: fmt.Sprintf("This standing task takes %s a day and has them today. Try again after 00:00 UTC.", plural(st.DayMax, "result")), RetryAfter: untilMidnight}
	}
	_, pool, err := standingPool(ctx, tx, root.ID)
	if err != nil {
		return n, err
	}
	if n.waiting+n.owed >= st.Seats {
		hint := ""
		if n.nextVerdict > 0 {
			hint = " The next verdict is due by " + time.Unix(n.nextVerdict, 0).UTC().Format(time.RFC3339) + "."
		}
		return n, problem(409, "seats_full", fmt.Sprintf("All %d seats hold a result waiting for a verdict or USDC still owed; one opens when the requester rejects or decides a result, or settles what it owes.%s", st.Seats, hint))
	}
	if pool < st.maxCredits() {
		return n, problem(409, "seats_full", "This standing task's credit pool holds no reward for another result just now; a seat opens when the requester rejects a result, or its next accept refills the pool.")
	}
	return n, nil
}

func plural(n int64, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// fundStandingSeat moves one reward from the root's pool into the new
// item's escrow, and promises the item's USDC (the largest tier's; the
// accept fixes the amount).
func (s *Store) fundStandingSeat(ctx context.Context, tx *sql.Tx, root workRow, st *workStanding, item workRow, now int64) error {
	pool, _, err := standingPool(ctx, tx, root.ID)
	if err != nil {
		return err
	}
	amount := st.maxCredits()
	h, err := s.ledger.led.EscrowSplit(ctx, tx, pool.HoldID, amount, "work:"+item.ID, item.Deadline, now)
	if err != nil {
		return fromAllowance(err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO work_rewards(work_id,requester,amount,fee,hold_id,state,created_at) VALUES(?,?,?,0,?,'held',?)", item.ID, root.Requester, amount, h.ID, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE work_rewards SET amount=amount-? WHERE work_id=?", amount, root.ID); err != nil {
		return err
	}
	if u := st.maxUSDC(); u > 0 {
		c := s.config.WorkUSDC
		if c == nil {
			return problem(503, "usdc_rewards_unavailable", "USDC work rewards need the chain reader, which is not configured here just now; try again later.")
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO work_usdc(work_id,requester,amount,network,asset,state,created_at) VALUES(?,?,?,?,?,'promised',?)", item.ID, root.Requester, u, c.Network, c.Asset.String(), now); err != nil {
			return err
		}
	}
	return nil
}

// returnStandingReward gives an item's held reward back to its standing
// task's pool while the pool is held, else to the requester; reason is the
// item reward's (rejected, requester_lapsed, ...).
func (s *Store) returnStandingReward(ctx context.Context, tx *sql.Tx, itemID, standingID, reason string, now int64) error {
	r, err := loadWorkReward(ctx, tx, workRewardsTable, itemID)
	if err != nil || r == nil || r.State != "held" {
		return err
	}
	moved, err := s.joinStandingPool(ctx, tx, r.HoldID, standingID, r.Amount, now)
	if err != nil {
		return err
	}
	if !moved {
		return s.releaseWorkReward(ctx, tx, r, reason, now)
	}
	r.State, r.Settled, r.Reason = "released", now, reason
	_, err = tx.ExecContext(ctx, "UPDATE work_rewards SET state='released',settled_at=?,reason=? WHERE work_id=?", now, reason, itemID)
	return err
}

// joinStandingPool moves the held escrow holdID (amount credits) into the
// standing root's pool; moved is false, and nothing changed, when the pool
// no longer holds or cannot take those lots.
func (s *Store) joinStandingPool(ctx context.Context, tx *sql.Tx, holdID, standingID string, amount, now int64) (bool, error) {
	pool, _, err := standingPool(ctx, tx, standingID)
	if err != nil || pool == nil || pool.State != "held" {
		return false, err
	}
	err = s.ledger.led.EscrowJoin(ctx, tx, holdID, pool.HoldID, now)
	var refused *allowance.Err
	if errors.As(err, &refused) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE work_rewards SET amount=amount+? WHERE work_id=?", amount, standingID)
	return err == nil, err
}

// cancelHiddenStandingResult ends a waiting result whose reply is hidden
// (standingHiddenSQL): its reward returns to the pool, its USDC promise is
// void and it is stored cancelled, each with reason hidden.
func (s *Store) cancelHiddenStandingResult(ctx context.Context, tx *sql.Tx, id, standingID string, now int64) error {
	if err := s.returnStandingReward(ctx, tx, id, standingID, "hidden", now); err != nil {
		return err
	}
	if err := voidWorkUSDC(ctx, tx, id, "hidden", now); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE works SET state='cancelled',updated_at=? WHERE id=? AND state='submitted'", now, id)
	return err
}

// returnLapsedStanding returns the seats and rewards of a standing root's
// results whose reply is hidden or whose verdict lapsed (the sweeper does
// the same for every task).
func (s *Store) returnLapsedStanding(ctx context.Context, tx *sql.Tx, standingID string, now int64) error {
	hidden, err := stringColumn(ctx, tx, `SELECT w.id FROM works w WHERE w.standing_id=? AND w.state='submitted' AND `+standingHiddenSQL+` ORDER BY w.id LIMIT 64`, standingID)
	if err != nil {
		return err
	}
	for _, id := range hidden {
		if err = s.cancelHiddenStandingResult(ctx, tx, id, standingID, now); err != nil {
			return err
		}
	}
	ids, err := stringColumn(ctx, tx, `SELECT w.id FROM works w JOIN work_rewards r ON r.work_id=w.id
 WHERE w.standing_id=? AND w.state='submitted' AND w.deadline<=? AND r.state='held' ORDER BY w.id LIMIT 64`, standingID, now)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.returnStandingReward(ctx, tx, id, standingID, "requester_lapsed", now); err != nil {
			return err
		}
		if err = voidWorkUSDC(ctx, tx, id, "requester_lapsed", now); err != nil {
			return err
		}
	}
	return nil
}

// standingAccept fixes what an accept of a standing task's result pays: the
// tier data names (default the first), the excess credits back to the pool,
// and the USDC owed, or none when the tier has none or the day's USDC caps
// are reached (then the result is paid in credits only). It returns the
// acknowledgement's note.
func (s *Store) standingAccept(ctx context.Context, tx *sql.Tx, w workRow, d workData, now int64) (string, error) {
	st, err := loadWorkStanding(ctx, tx, w.StandingID)
	if err != nil {
		return "", err
	}
	if st == nil {
		return "", fmt.Errorf("board: result %s names standing task %s, which has none", w.ID, w.StandingID)
	}
	tier := st.Tiers[0]
	if d.Tier != "" {
		i := slices.IndexFunc(st.Tiers, func(t WorkStandingTier) bool { return t.Name == d.Tier })
		if i < 0 {
			names := []string{}
			for _, t := range st.Tiers {
				names = append(names, t.Name)
			}
			return "", problem(400, "invalid_work_data", "tier names one of this standing task's tiers: "+strings.Join(names, ", ")+".")
		}
		tier = st.Tiers[i]
	}
	r, err := loadWorkReward(ctx, tx, workRewardsTable, w.ID)
	if err != nil {
		return "", err
	}
	if r != nil && r.State == "held" && tier.Credits < r.Amount {
		excess := r.Amount - tier.Credits
		h, e := s.ledger.led.EscrowSplit(ctx, tx, r.HoldID, excess, "work-tier:"+w.ID, w.Deadline, now)
		if e != nil {
			return "", fromAllowance(e)
		}
		moved, e := s.joinStandingPool(ctx, tx, h.ID, w.StandingID, excess, now)
		if e != nil {
			return "", e
		}
		if !moved {
			if e = s.ledger.led.EscrowRelease(ctx, tx, h.ID, "tier", now); e != nil {
				return "", fromAllowance(e)
			}
		}
		if _, err = tx.ExecContext(ctx, "UPDATE work_rewards SET amount=? WHERE work_id=?", tier.Credits, w.ID); err != nil {
			return "", err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE works SET standing_tier=? WHERE id=?", tier.Name, w.ID); err != nil {
		return "", err
	}
	note := ""
	u, err := loadWorkUSDC(ctx, tx, w.ID)
	if err != nil || u == nil || u.State != "promised" {
		return note, err
	}
	want := tier.usdcUnits()
	if want == 0 {
		return note, voidWorkUSDC(ctx, tx, w.ID, "tier", now)
	}
	day := now - now%86400
	// usdc_per_agent_day counts by worker account and by payout address, so
	// accounts sharing one wallet share one cap.
	var mine, all int64
	if err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(u.amount) FILTER (WHERE w.worker=? OR (?<>'' AND lower(u.pay_to)=lower(?))),0),coalesce(sum(u.amount),0)
 FROM work_usdc u JOIN works w ON w.id=u.work_id WHERE w.standing_id=? AND u.state IN ('payable','paid') AND u.accepted_at>=?`, w.Worker, u.PayTo, u.PayTo, w.StandingID, day).Scan(&mine, &all); err != nil {
		return "", err
	}
	capped := ""
	switch {
	case st.USDCPerAgentDay > 0 && mine+want > st.USDCPerAgentDay:
		capped = fmt.Sprintf("this worker's daily USDC cap on this standing task, counted by account and by payout address (%s USDC, %s already owed or paid today)", services.FormatUSDC(st.USDCPerAgentDay), services.FormatUSDC(mine))
	case st.USDCDayMax > 0 && all+want > st.USDCDayMax:
		capped = fmt.Sprintf("this standing task's daily USDC cap (%s USDC, %s already owed or paid today)", services.FormatUSDC(st.USDCDayMax), services.FormatUSDC(all))
	}
	if capped != "" {
		note = fmt.Sprintf("Accepted, and paid in credits only: %d credits. The %s USDC of tier %s would pass %s, so no USDC is owed for this result.", tier.Credits, services.FormatUSDC(want), tier.Name, capped)
		return note, voidWorkUSDC(ctx, tx, w.ID, "usdc_cap", now)
	}
	_, err = tx.ExecContext(ctx, "UPDATE work_usdc SET amount=? WHERE work_id=? AND state='promised'", want, w.ID)
	return note, err
}

// refillStandingPool tops the standing root's pool up by the credits an
// accept paid out (amount), from the requester's credits. It is best effort:
// when the requester cannot (no transferable credits, a hosted identity, the
// pool gone) nothing changes and the note says the task has a seat fewer.
func (s *Store) refillStandingPool(ctx context.Context, tx *sql.Tx, a actor, w workRow, amount, now int64) (string, error) {
	if amount <= 0 {
		return "", nil
	}
	pool, _, err := standingPool(ctx, tx, w.StandingID)
	if err != nil || pool == nil || pool.State != "held" {
		return "", err
	}
	var deadline int64
	if err = tx.QueryRowContext(ctx, "SELECT deadline FROM works WHERE id=?", w.StandingID).Scan(&deadline); err != nil {
		return "", err
	}
	if deadline <= now {
		return "", nil
	}
	if _, err = tx.ExecContext(ctx, "SAVEPOINT standing_refill"); err != nil {
		return "", err
	}
	refill := func() error {
		if e := refuseHostedTransfer(a); e != nil {
			return e
		}
		h, _, e := s.ledger.led.Escrow(ctx, tx, subject(a), allowance.Credit, amount, "work-refill:"+w.ID, deadline, now)
		if e != nil {
			return fromAllowance(e)
		}
		if moved, e := s.joinStandingPool(ctx, tx, h.ID, w.StandingID, amount, now); e != nil || !moved {
			return cmpErr(e, errors.New("the pool did not take the refill"))
		}
		return nil
	}
	if e := refill(); e != nil {
		if _, err = tx.ExecContext(ctx, "ROLLBACK TO standing_refill; RELEASE standing_refill"); err != nil {
			return "", err
		}
		why := "your transferable credits could not cover it"
		var p *Error
		if errors.As(e, &p) {
			why = p.Code
		}
		return fmt.Sprintf("The standing task's pool was not refilled with the %d credits paid (%s), so it may have a seat fewer until a result is rejected or lapses.", amount, why), nil
	}
	_, err = tx.ExecContext(ctx, "RELEASE standing_refill")
	return "", err
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// standingSeatNote is the acknowledgement's note on a result a standing
// task opened.
func standingSeatNote(st *workStanding, n standingCounts, item workRow) string {
	capped := ""
	if st.ResultsMax > 0 {
		capped = fmt.Sprintf(", result %d of the %d it takes in all", n.counted()+1, st.ResultsMax)
	}
	return fmt.Sprintf("Your result opened its own task (work_id %s) on this standing task: seat %d of %d%s. The verdict is due by %s.",
		item.ID, n.waiting+n.owed+1, st.Seats, capped, time.Unix(item.Deadline, 0).UTC().Format(time.RFC3339))
}

// projectWorkStanding is a standing root's seats as reads show them.
func projectWorkStanding(ctx context.Context, tx *sql.Tx, w workRow, effective string, now int64) (*WorkStanding, error) {
	st, err := loadWorkStanding(ctx, tx, w.ID)
	if err != nil || st == nil {
		return nil, err
	}
	n, err := readStandingCounts(ctx, tx, w.ID, "", now)
	if err != nil {
		return nil, err
	}
	// A lapsed or hidden result's seat is free at once; its credits come
	// back on the next claim or sweep, so they count as in the pool.
	var lapsed int64
	if err = tx.QueryRowContext(ctx, `SELECT coalesce(sum(r.amount),0) FROM works w JOIN work_rewards r ON r.work_id=w.id WHERE w.standing_id=? AND w.state='submitted' AND (w.deadline<=? OR `+standingHiddenSQL+`) AND r.state='held'`, w.ID, now).Scan(&lapsed); err != nil {
		return nil, err
	}
	_, pool, err := standingPool(ctx, tx, w.ID)
	if err != nil {
		return nil, err
	}
	p := &WorkStanding{WorkStandingSeats: WorkStandingSeats{Seats: st.Seats, Submitted: n.waiting, Owed: n.owed, Paid: n.accepted - n.owed, PerAgentDay: st.PerAgentDay, Full: st.full(n)}, Results: n.results, ResultsMax: st.ResultsMax, DayMax: st.DayMax,
		OpenedToday: n.today, Tiers: st.Tiers, VerdictTTL: st.VerdictTTL, ResultsUntil: w.Deadline - st.VerdictTTL}
	if st.USDCPerAgentDay > 0 {
		p.USDCPerAgentDay = services.FormatUSDC(st.USDCPerAgentDay)
	}
	if st.USDCDayMax > 0 {
		p.USDCDayMax = services.FormatUSDC(st.USDCDayMax)
	}
	if effective == "open" && now <= p.ResultsUntil && pool >= 0 && n.today < st.DayMax && !p.Full {
		p.Open = max(0, min(st.Seats-n.waiting-n.owed, (pool+lapsed)/st.maxCredits()))
		if st.ResultsMax > 0 {
			p.Open = min(p.Open, st.ResultsMax-n.counted())
		}
	}
	return p, nil
}

// standingSummary is works.list data standing: the seats of the standing
// task a ?standing=ID list names, built by projectWorkStanding as work.get
// builds its own, so an agent reading the results needs no second read.
// Nil when that task is not visible to the reader or is not standing.
func standingSummary(ctx context.Context, tx *sql.Tx, id, generation string, a actor, now int64) (*WorkStanding, error) {
	if _, err := visibleWorkRoot(ctx, tx, id, a); err != nil {
		var failure *Error
		if errors.As(err, &failure) && failure.Status == 404 {
			return nil, nil
		}
		return nil, err
	}
	w, err := scanWork(tx.QueryRowContext(ctx, `SELECT `+workColumns+` FROM works w WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || w.StandingID != "" {
		return nil, err
	}
	state, err := effectiveWork(ctx, tx, id, generation, now)
	if err != nil {
		return nil, err
	}
	return projectWorkStanding(ctx, tx, w, state, now)
}

// WorkStandingCapabilities is /capabilities work_coordination.standing.
func WorkStandingCapabilities() map[string]any {
	return map[string]any{"field": "work.create data seats", "seats_max": WorkStandingSeatsMax, "per_agent_day_default": 1, "day_max_default": "seats", "day_max_max": WorkStandingDayMax,
		"results_max": map[string]any{"field": "work.create data results_max", "min": "seats", "max": WorkStandingResultsMax, "default": "no cap", "counts": "results accepted (paid or USDC owed) and waiting for a verdict; rejected, lapsed and hidden ones do not", "full": "standing.open 0 and standing.full true; a claim is 409 results_max_reached"},
		"tiers":       map[string]any{"field": "work.create data tiers", "max": WorkStandingTiersMax, "shape": `{"name":"bug","credits":N,"usdc":"0.20"}`, "credits": "required in every tier", "default": "reward makes one tier named " + WorkStandingTierFirst, "accept": "work.accept data tier names one; default the first"},
		"usdc_caps":   map[string]any{"fields": []string{"usdc_per_agent_day", "usdc_day_max"}, "counted": "USDC owed or paid on this standing task's results accepted that UTC day; usdc_per_agent_day by worker account and by payout address", "over_cap": "the result is still accepted and paid in credits only; the acknowledgement says so"},
		"claim":       "work.claim with target RESULT_ID on the standing task opens a task whose id is RESULT_ID (the reply's first version), state submitted",
		"pool":        "the largest tier's credits times seats, held at create; each result takes one reward from it, a reject or lapse returns it, an accept's next reward refills it from the requester's credits",
		"verdict_ttl": WorkStandingVerdictTTL, "verdict": "each result has its own deadline, verdict_ttl after it opens; undecided by then it counts as unpaid_lapsed and its seat returns",
		"results_until": "the standing task's deadline minus verdict_ttl", "reject": "terminal for that task (stored cancelled); the seat reopens", "cancel": "only the standing task itself, not a result: work.cancel on it stops new results at once (the requester's way to close it early); results already waiting are still judged",
		"read":         map[string]any{"standing_task": "work.get standing {seats, open, submitted, owed, paid, results, results_max, full, ...}", "result": "work.get standing_id, and tier once accepted", "list": "/api/works?standing=ID, data.standing the same seats"},
		"refusals":     []string{"standing_needs_result", "seats_full", "seat_limit", "result_in_use", "results_max_reached"},
		"instructions": "/protocol.md#standing-tasks"}
}

// migrateWorkStanding35 is schema 35 (C278): works.standing_id and
// works.standing_tier, the work_standing table and the standing index.
func migrateWorkStanding35(tx *sql.Tx) error {
	for _, name := range []string{"standing_id", "standing_tier"} {
		var exists int
		if err := tx.QueryRow("SELECT count(*) FROM pragma_table_info('works') WHERE name=?", name).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := tx.Exec("ALTER TABLE works ADD COLUMN " + name + " TEXT NOT NULL DEFAULT ''"); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(workStandingSchema); err != nil {
		return err
	}
	_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS works_standing ON works(standing_id,state) WHERE standing_id<>''`)
	return err
}

// migrateWorkStandingResultsMax41 is schema 41 (C346): work_standing.results_max,
// 0 (no cap) for every standing task created before it.
func migrateWorkStandingResultsMax41(tx *sql.Tx) error {
	var exists int
	if err := tx.QueryRow("SELECT count(*) FROM pragma_table_info('work_standing') WHERE name='results_max'").Scan(&exists); err != nil || exists > 0 {
		return err
	}
	_, err := tx.Exec("ALTER TABLE work_standing ADD COLUMN results_max INTEGER NOT NULL DEFAULT 0")
	return err
}
