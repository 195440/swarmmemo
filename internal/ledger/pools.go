package ledger

// The daily share rule (RFC 0021 §12): OpenDay, the lever and budget
// adjustment, the claim's entitlement and the draws. The pure parts work on
// an in-memory dayState so a read (Balance) can show a prospective share
// without writing; loadDay and saveDay move it to and from allowance_days and
// allowance_pools.
//
// The day's budget B (less a pulled cut-budget lever) is cut into three
// slices: the grant slice (grant_share_ppm of B) funds operator grants; of
// the rest, the floor slice (floor_slice_ppm) gives every claimant
// min(floor, slice / expected claimants), and the standing slice (the
// remainder) is shared in proportion to Standing.ShareCents, each root once,
// under one per-claimant cap.
//
// Overbooking: a claim issues an entitlement and draws nothing. Water is
// drawn only when free units are actually spent (or held for a remote call):
// from the lot's own slice first, then from what the other slice may lend
// (lendable: unused water spills once), so the day's spend stays within the
// slices and the budget while the entitlements may add up to more.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"swarmmemo/internal/allowance"
)

// Slices of a day's budget, by allowance_pools row; row 3 is the anonymous
// account (not a slice: what anonymous lots drew of the floor and standing
// slices, and how many anonymous subjects claimed).
const (
	sliceGrant    = 0
	sliceFloor    = 1
	sliceStanding = 2
	rowAnonymous  = 3
)

// SliceNames are the slices' public names, by allowance_pools row.
var SliceNames = [3]string{"grant", "floor", "standing"}

// Lot origins (ledger_lots.origin_tier): the slice a free lot draws from
// first. Minted lots are origin 0 and are drawn from the grant slice when
// minted, never at spend. An anonymous subject's lot draws from the floor
// slice within the anonymous share (anonRoom). Origin 3, written before the
// share rule, draws from the floor slice.
const (
	originGrant     = 0
	originFloor     = 1
	originStanding  = 2
	originAnonymous = 4
)

// sliceOf is the slice a free lot of origin o draws from first.
func sliceOf(o int64) int {
	if o == originStanding {
		return sliceStanding
	}
	return sliceFloor
}

// other is the slice a spend may draw from when its own is dry.
func other(s int) int { return sliceFloor + sliceStanding - s }

// pool is one slice's water on one day. Claimed is what the slice's own lots
// drew of it (for the grant slice, what grants issued), Lent what it gave to
// the other slice's spends and Borrowed what its spends took from the other.
// Claimants counts today's claimants of the slice (floor: every claimant the
// levers give a share; standing: those given a standing part); Weight is the
// standing slice's Σ ShareCents of today's roots; Expected is the smoothed
// demand: expected claimants (floor) or expected Σ ShareCents (standing).
type pool struct {
	Size, Claimed, Lent, Borrowed int64
	Claimants, Weight, Expected   int64
}

// avail = size − claimed − lent.
func (p pool) avail() int64 { return p.Size - p.Claimed - p.Lent }

// dayState is allowance_days plus the slices of one (resource, day).
type dayState struct {
	Resource                             allowance.Resource
	Day                                  int64
	Budget, BudgetEffective, Unallocated int64
	SpentNonpaid, SpentPaid              int64
	OpenedAt, ParamsVersion              int64
	Slices                               [3]pool
	// Anon is allowance_pools row 3: Claimed is what anonymous lots drew,
	// Claimants how many anonymous subjects claimed.
	Anon pool
	// legacy marks a day opened before the share rule (five tier rows):
	// loadDay folds it and saveDay rewrites its rows.
	legacy bool
}

func (d *dayState) allocated() int64 {
	var n int64
	for _, p := range d.Slices {
		n += p.Size
	}
	return n
}

// effectiveBudget is the budget less a pulled cut-budget lever.
func effectiveBudget(rp *ResourceParams, r allowance.Resource, lv allowance.Levers) int64 {
	cut, ok := lv.BudgetCutPPM[r]
	if !ok {
		return rp.Budget
	}
	return rp.Budget - mulDiv(rp.Budget, clamp(cut, 0, ppm), ppm)
}

// sliceSizes cuts an effective budget into the grant, floor and standing
// slices.
func sliceSizes(be int64, rp *ResourceParams) [3]int64 {
	grant := min(be, mulDiv(be, rp.GrantSharePPM, ppm))
	rest := be - grant
	floor := mulDiv(rest, rp.FloorSlicePPM, ppm)
	return [3]int64{grant, floor, rest - floor}
}

// anonymousOff reports whether the levers give anonymous subjects nothing of
// resource r: signed-only and proven-only for every resource,
// signed-services for credit.
func anonymousOff(r allowance.Resource, lv allowance.Levers) bool {
	return lv.SignedOnly || lv.ProvenOnly || lv.SignedServices && r == allowance.Credit
}

// eligible reports whether the parameters and the levers give a subject of
// standing st any share: none to anonymous subjects under anonymousOff or
// where anonymous_share_ppm is 0, none below the proven band under
// proven-only.
func eligible(r allowance.Resource, rp *ResourceParams, lv allowance.Levers, st allowance.Standing) bool {
	if st.Band == allowance.BandAnonymous && (anonymousOff(r, lv) || rp.AnonymousSharePPM == 0) {
		return false
	}
	return !lv.ProvenOnly || st.Band >= allowance.BandProven
}

// anonRoom is what anonymous lots may still draw at now: the anonymous share
// of the effective budget (anonymous_share_ppm, lowered by the
// anonymous-shrink lever) as released so far today (anonReleased), less what
// they drew. The levers that give anonymous subjects nothing stop issued
// anonymous shares at once. hourly reports that the hourly release, not the
// day's limit, is what binds.
func anonRoom(d *dayState, rp *ResourceParams, lv allowance.Levers, now int64) (room int64, hourly bool) {
	if anonymousOff(d.Resource, lv) {
		return 0, false
	}
	share := rp.AnonymousSharePPM
	if lv.AnonymousSharePPM >= 0 {
		share = min(share, clamp(lv.AnonymousSharePPM, 0, ppm))
	}
	lim := mulDiv(d.BudgetEffective, share, ppm)
	released := anonReleased(d, rp, lim, now)
	return max(0, released-d.Anon.Claimed), released < lim
}

// anonReleased is how much of the anonymous day limit lim anonymous lots may
// have drawn by now. Where the parameters cap anonymous callers below the
// whole budget (anonymous_share_ppm < 100%, as for credit), the day is
// released over the day: a sixth at 00:00 UTC, then a 24th each hour,
// cumulatively, all of it from 20:00 (anonReleaseStart). One burst takes at
// most what has been released so far, never the whole day at once (security
// review 1.21, M3); what an hour leaves unused carries forward. At a full
// share (posting's default) nothing is held back.
func anonReleased(d *dayState, rp *ResourceParams, lim, now int64) int64 {
	if rp.AnonymousSharePPM >= ppm {
		return lim
	}
	hours := clamp((now-d.Day*86400)/3600, 0, 24)
	return min(lim, mulDiv(lim, anonReleaseStart+hours, 24))
}

// anonReleaseStart is the 24ths of a capped anonymous day released at 00:00
// UTC (anonReleased).
const anonReleaseStart = 4

// smooth is the expected demand from yesterday's: it rises at once and falls
// by half a day.
func smooth(expected, claimed int64) int64 { return max(claimed, (expected+claimed)/2) }

// computeOpen is OpenDay without writing: slice sizes from the effective
// budget and the smoothed demand of the previous opened day (prev may be nil).
func computeOpen(r allowance.Resource, day int64, rp *ResourceParams, lv allowance.Levers, prev *dayState, version, now int64) dayState {
	d := dayState{Resource: r, Day: day, Budget: rp.Budget, OpenedAt: now, ParamsVersion: version}
	d.BudgetEffective = effectiveBudget(rp, r, lv)
	if prev != nil {
		d.Slices[sliceFloor].Expected = smooth(prev.Slices[sliceFloor].Expected, prev.Slices[sliceFloor].Claimants)
		d.Slices[sliceStanding].Expected = smooth(prev.Slices[sliceStanding].Expected, prev.Slices[sliceStanding].Weight)
	}
	for s, size := range sliceSizes(d.BudgetEffective, rp) {
		d.Slices[s].Size = size
	}
	d.Unallocated = d.Budget - d.allocated()
	return d
}

// adjust applies a budget change, a cut-budget lever or new slice shares
// since the day opened: every slice is brought within its size and the total
// within the effective budget, shrinking standing, floor, then grant, and
// only from unspent water; growth refills the slices to their sizes. Issued
// lots are untouched, but a slice whose water shrank dries up for them too.
// It reports whether anything changed.
func adjust(d *dayState, rp *ResourceParams, lv allowance.Levers) bool {
	before := *d
	d.Budget = rp.Budget
	d.BudgetEffective = effectiveBudget(rp, d.Resource, lv)
	lim := sliceSizes(d.BudgetEffective, rp)
	for s := range d.Slices {
		if over := d.Slices[s].Size - lim[s]; over > 0 {
			d.Slices[s].Size -= min(over, max(0, d.Slices[s].avail()))
		}
	}
	if need := d.allocated() - d.BudgetEffective; need > 0 {
		for s := sliceStanding; s >= sliceGrant && need > 0; s-- {
			x := min(need, max(0, d.Slices[s].avail()))
			d.Slices[s].Size -= x
			need -= x
		}
	}
	if room := d.BudgetEffective - d.allocated(); room > 0 {
		for s := range d.Slices {
			x := min(room, max(0, lim[s]-d.Slices[s].Size))
			d.Slices[s].Size += x
			room -= x
		}
	}
	d.Unallocated = d.Budget - d.allocated()
	return *d != before
}

// spillPPM is the share of the day (ppm) elapsed at the latest spill
// boundary at or before now: 0 before spill_start, then a step every
// spill_interval; the day's last boundary after its end.
func spillPPM(day int64, rp *ResourceParams, now int64) int64 {
	if end := (day + 1) * 86400; now >= end {
		now = end - 1
	}
	start := day*86400 + rp.SpillStart
	if now < start {
		return 0
	}
	k := start + (now-start)/rp.SpillInterval*rp.SpillInterval
	return (k - day*86400) * ppm / 86400
}

// lendable is what slice s may lend the other slice's spends at now: unused
// water spills once. The slice keeps a shrinking share of its unspent size,
// ceil((size − claimed) × (1 − f)) with f the day elapsed at the latest spill
// boundary (spillPPM), and lends what is above that. At midnight it lends
// nothing, so a flood of one kind of claimant cannot drain the other slice
// before its claimants arrive; by the day's end nearly all of it.
func lendable(d *dayState, rp *ResourceParams, s int, now int64) int64 {
	p := d.Slices[s]
	keep := mulDivCeil(max(0, p.Size-p.Claimed), ppm-spillPPM(d.Day, rp, now), ppm)
	return max(0, p.avail()-keep)
}

// draw takes want units for a spend from a free lot of origin o: its own
// slice's water first, then what the other slice lends. An anonymous lot's
// draw also counts in the anonymous account.
func draw(d *dayState, rp *ResourceParams, o, want, now int64) int64 {
	if want <= 0 {
		return 0
	}
	s := sliceOf(o)
	got := min(want, max(0, d.Slices[s].avail()))
	d.Slices[s].Claimed += got
	if got < want {
		u := other(s)
		x := min(want-got, lendable(d, rp, u, now))
		d.Slices[u].Lent += x
		d.Slices[s].Borrowed += x
		got += x
	}
	if o == originAnonymous {
		d.Anon.Claimed += got
	}
	return got
}

// drawable is what a spend from a free lot of origin o could draw now: its
// slice's water plus what the other slice lends.
func drawable(d *dayState, rp *ResourceParams, o, now int64) int64 {
	s := sliceOf(o)
	return max(0, d.Slices[s].avail()) + lendable(d, rp, other(s), now)
}

// undraw returns x units a spend from a lot of origin o drew but did not use
// (a hold's refunded part): borrowed water first, then the slice's own. Each
// step is bounded, so the slices stay consistent.
func undraw(d *dayState, o, x int64) {
	s, u := sliceOf(o), other(sliceOf(o))
	back := min(x, d.Slices[s].Borrowed, d.Slices[u].Lent)
	d.Slices[u].Lent -= back
	d.Slices[s].Borrowed -= back
	own := min(x-back, d.Slices[s].Claimed)
	d.Slices[s].Claimed -= own
	if o == originAnonymous {
		d.Anon.Claimed -= min(back+own, d.Anon.Claimed)
	}
}

// rootDay is what today's claims of one root already hold: how many there
// are and the standing parts they were given.
type rootDay struct {
	Claims, Standing int64
}

// entitlement is a subject's share by the share rule, as its floor part and
// its standing part. The floor part is the floor while the floor slice has
// that much water left, else min(floor, floor slice / claimants),
// claimants being the expected count and never fewer than today's claimants
// plus this one. The standing part is the root's share of the standing slice,
// in proportion to ShareCents over the expected Σ ShareCents (never less
// than today's roots' plus this root's), less what the root's earlier claims
// today were given: each root once. The two together never exceed cap, and a
// new key under pause-new-keys gets at most new_key_floor. It draws nothing:
// the slices' water is drawn only as the share is spent, so entitlements may
// add up to more than the slices hold (overbooking).
func entitlement(d *dayState, rp *ResourceParams, lv allowance.Levers, st allowance.Standing, root rootDay) (floor, standing int64) {
	if !eligible(d.Resource, rp, lv, st) {
		return 0, 0
	}
	fs := d.Slices[sliceFloor]
	// While the floor slice holds water for a whole floor, every claimant
	// gets one (generous until constrained, C320c); only a slice that runs
	// dry thins it to its share per expected claimant.
	floor = min(rp.Floor, rp.Cap, max(max(0, fs.Size)/max(1, fs.Expected, fs.Claimants+1), fs.avail()))
	if st.ShareCents > 0 {
		ss := d.Slices[sliceStanding]
		w := ss.Weight
		if root.Claims == 0 {
			w += st.ShareCents
		}
		share := mulDiv(max(0, ss.Size), st.ShareCents, max(w, ss.Expected, st.ShareCents))
		standing = max(0, min(share-root.Standing, rp.Cap-floor))
	}
	if st.NewKey && lv.PauseNewKeys {
		total := min(floor+standing, rp.NewKeyFloor)
		floor = min(floor, total)
		standing = total - floor
	}
	return floor, standing
}

// TransitionFloorDays is how long the tier waterfall's shares floor the
// share rule (RFC 0021 §14, C320c: standing only adds at the cutover): a
// signed subject's share is at least the largest it got on a waterfall day
// within this many days, until the last such day is this old.
const TransitionFloorDays = 60

// legacyShare is the largest share subject was given on a day the tier
// waterfall opened (its allowance_pools still hold the tier-4 row) within
// TransitionFloorDays before day: what the waterfall gave it.
func legacyShare(ctx context.Context, q allowance.Querier, r allowance.Resource, day int64, subject string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT coalesce(max(c.entitlement),0) FROM allowance_claims c WHERE c.resource=? AND c.subject=? AND c.day>=? AND c.day<?
 AND EXISTS(SELECT 1 FROM allowance_pools p WHERE p.resource=c.resource AND p.day=c.day AND p.tier=4)`, string(r), subject, day-TransitionFloorDays, day).Scan(&n)
	return n, err
}

// Share is a subject's share of a resource for a day: the floor part and the
// standing part (the transition floor included in the floor part), the
// share rule's own total before it (Rule), and the legacy share it keeps
// (Legacy, 0 when none applies).
type Share struct {
	Floor, Standing, Rule, Legacy int64
}

// share is a subject's entitlement with the transition floor: a signed
// subject the levers give a share, and not a new key under pause-new-keys,
// gets at least its legacy share (within cap), the difference added to the
// floor part.
func share(ctx context.Context, q allowance.Querier, d *dayState, rp *ResourceParams, lv allowance.Levers, st allowance.Standing, rd rootDay, subject string) (Share, error) {
	var sh Share
	sh.Floor, sh.Standing = entitlement(d, rp, lv, st, rd)
	sh.Rule = sh.Floor + sh.Standing
	if st.Band == allowance.BandAnonymous || !eligible(d.Resource, rp, lv, st) || st.NewKey && lv.PauseNewKeys {
		return sh, nil
	}
	legacy, err := legacyShare(ctx, q, d.Resource, d.Day, subject)
	if err != nil {
		return Share{}, err
	}
	sh.Legacy = min(legacy, rp.Cap)
	if sh.Legacy > sh.Rule {
		sh.Floor = sh.Legacy - sh.Standing
	}
	return sh, nil
}

// LegacyShare is the share the tier waterfall gave subject of resource r
// within TransitionFloorDays before day (0 if none): what the transition
// floor keeps, and what "swarmmemo trust compare" shows as the old share.
func LegacyShare(ctx context.Context, q allowance.Querier, r allowance.Resource, day int64, subject string) (int64, error) {
	return legacyShare(ctx, q, r, day, subject)
}

// ProspectiveShare is what a claim by a subject of standing st would issue
// now (floor part, standing part, the transition floor included), without
// writing: Balance's prospective share for any standing, which the trust
// compare tool reads for a run's standing.
func (l *Ledger) ProspectiveShare(ctx context.Context, q allowance.Querier, r allowance.Resource, st allowance.Standing, subject string, now int64) (Share, error) {
	p, version, err := l.params(ctx, q, now)
	if err != nil {
		return Share{}, err
	}
	rp, err := l.resource(p, r)
	if err != nil {
		return Share{}, err
	}
	lv, err := l.levers(ctx, q, now)
	if err != nil {
		return Share{}, err
	}
	day := now / 86400
	d, opened, err := loadDay(ctx, q, r, day)
	if err != nil {
		return Share{}, err
	}
	if !opened {
		prev, err := previousDay(ctx, q, r, day)
		if err != nil {
			return Share{}, err
		}
		d = computeOpen(r, day, rp, lv, prev, version, now)
	}
	adjust(&d, rp, lv)
	rd, err := loadRootDay(ctx, q, r, day, st.Root)
	if err != nil {
		return Share{}, err
	}
	return share(ctx, q, &d, rp, lv, st, rd, subject)
}

// loadDay reads a day; ok is false when it has not opened. A day opened
// before the share rule (rows 0–4, one per tier of RFC 0012's waterfall) is
// folded: the grant slice keeps tier 0, the standing slice takes tiers 1–4's
// water and draws (so nothing already spent is handed out again), the
// anonymous account tier 4's draws; the floor slice starts empty and adjust
// sizes it from what is unspent.
func loadDay(ctx context.Context, q allowance.Querier, r allowance.Resource, day int64) (dayState, bool, error) {
	d := dayState{Resource: r, Day: day}
	err := q.QueryRowContext(ctx, "SELECT budget,budget_effective,unallocated,spent_nonpaid,spent_paid,opened_at,params_version FROM allowance_days WHERE resource=? AND day=?", string(r), day).
		Scan(&d.Budget, &d.BudgetEffective, &d.Unallocated, &d.SpentNonpaid, &d.SpentPaid, &d.OpenedAt, &d.ParamsVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	rows, err := q.QueryContext(ctx, "SELECT tier,size,spill_in,spill_out,claimed,lent,borrowed,expected_weight,claimed_weight,claimants FROM allowance_pools WHERE resource=? AND day=? ORDER BY tier LIMIT 5", string(r), day)
	if err != nil {
		return d, false, err
	}
	defer rows.Close()
	var raw [5]pool
	var seen [5]bool
	for rows.Next() {
		var row int
		var p pool
		var in, out int64
		if err = rows.Scan(&row, &p.Size, &in, &out, &p.Claimed, &p.Lent, &p.Borrowed, &p.Expected, &p.Weight, &p.Claimants); err != nil {
			return d, false, err
		}
		if row < 0 || row > 4 {
			return d, false, fmt.Errorf("ledger: pool row %d", row)
		}
		p.Size += in - out
		raw[row], seen[row] = p, true
	}
	if err = rows.Err(); err != nil {
		return d, false, err
	}
	if !seen[4] {
		d.Slices = [3]pool{raw[0], raw[1], raw[2]}
		d.Anon = raw[rowAnonymous]
		return d, true, nil
	}
	d.legacy = true
	d.Slices[sliceGrant] = pool{Size: raw[0].Size, Claimed: raw[0].Claimed}
	for _, p := range raw[1:] {
		d.Slices[sliceStanding].Size += p.Size
		d.Slices[sliceStanding].Claimed += p.Claimed + p.Lent
	}
	d.Anon.Claimed = raw[4].Claimed
	return d, true, nil
}

// previousDay is the most recent opened day before day, or nil.
func previousDay(ctx context.Context, q allowance.Querier, r allowance.Resource, day int64) (*dayState, error) {
	var prev int64
	err := q.QueryRowContext(ctx, "SELECT day FROM allowance_days WHERE resource=? AND day<? ORDER BY day DESC LIMIT 1", string(r), day).Scan(&prev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, ok, err := loadDay(ctx, q, r, prev)
	if err != nil || !ok {
		return nil, err
	}
	return &d, nil
}

// poolRows are the day's allowance_pools rows in order: the three slices
// and the anonymous account.
func (d *dayState) poolRows() [4]pool {
	return [4]pool{d.Slices[0], d.Slices[1], d.Slices[2], d.Anon}
}

func insertPool(ctx context.Context, q allowance.Querier, d *dayState, row int, p pool) error {
	_, err := q.ExecContext(ctx, "INSERT INTO allowance_pools(resource,day,tier,size,want,spill_in,spill_out,claimed,lent,borrowed,expected_units,expected_weight,claimed_weight,claimants) VALUES(?,?,?,?,0,0,0,?,?,?,0,?,?,?)",
		string(d.Resource), d.Day, row, p.Size, p.Claimed, p.Lent, p.Borrowed, p.Expected, p.Weight, p.Claimants)
	return err
}

func insertDay(ctx context.Context, q allowance.Querier, d *dayState) error {
	if _, err := q.ExecContext(ctx, "INSERT INTO allowance_days(resource,day,budget,budget_effective,unallocated,spent_nonpaid,spent_paid,spill_done_at,opened_at,params_version) VALUES(?,?,?,?,?,?,?,0,?,?)",
		string(d.Resource), d.Day, d.Budget, d.BudgetEffective, d.Unallocated, d.SpentNonpaid, d.SpentPaid, d.OpenedAt, d.ParamsVersion); err != nil {
		return err
	}
	for row, p := range d.poolRows() {
		if err := insertPool(ctx, q, d, row, p); err != nil {
			return err
		}
	}
	return nil
}

// saveDay writes back what changed since orig. A folded day opened before
// the share rule has its pool rows replaced by the share rule's.
func saveDay(ctx context.Context, q allowance.Querier, d, orig *dayState) error {
	if *d == *orig {
		return nil
	}
	if _, err := q.ExecContext(ctx, "UPDATE allowance_days SET budget=?,budget_effective=?,unallocated=?,spent_nonpaid=?,spent_paid=? WHERE resource=? AND day=?",
		d.Budget, d.BudgetEffective, d.Unallocated, d.SpentNonpaid, d.SpentPaid, string(d.Resource), d.Day); err != nil {
		return err
	}
	if orig.legacy {
		if _, err := q.ExecContext(ctx, "DELETE FROM allowance_pools WHERE resource=? AND day=?", string(d.Resource), d.Day); err != nil {
			return err
		}
		for row, p := range d.poolRows() {
			if err := insertPool(ctx, q, d, row, p); err != nil {
				return err
			}
		}
		d.legacy = false
		*orig = *d
		return nil
	}
	was := orig.poolRows()
	for row, p := range d.poolRows() {
		if p == was[row] {
			continue
		}
		if _, err := q.ExecContext(ctx, "UPDATE allowance_pools SET size=?,claimed=?,lent=?,borrowed=?,expected_weight=?,claimed_weight=?,claimants=? WHERE resource=? AND day=? AND tier=?",
			p.Size, p.Claimed, p.Lent, p.Borrowed, p.Expected, p.Weight, p.Claimants, string(d.Resource), d.Day, row); err != nil {
			return err
		}
	}
	*orig = *d
	return nil
}

// claimRow is allowance_claims: its tier column holds the band, its weight
// column the standing part of the entitlement.
type claimRow struct {
	Band        allowance.Band
	Standing    int64
	Root        string
	Entitlement int64
	Granted     int64
	Source      string
	LotID       int64
}

// parts splits what the claim granted into its floor and standing parts,
// after a cap lowered since: the standing part is cut first.
func (c claimRow) parts(rp *ResourceParams) (floor, standing int64) {
	total := min(c.Granted, rp.Cap)
	standing = clamp(c.Standing, 0, total)
	return total - standing, standing
}

func loadClaim(ctx context.Context, q allowance.Querier, r allowance.Resource, day int64, subject string) (claimRow, bool, error) {
	var c claimRow
	var band int64
	err := q.QueryRowContext(ctx, "SELECT tier,weight,root,entitlement,granted,source,lot_id FROM allowance_claims WHERE resource=? AND day=? AND subject=?", string(r), day, subject).
		Scan(&band, &c.Standing, &c.Root, &c.Entitlement, &c.Granted, &c.Source, &c.LotID)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	if band < int64(allowance.BandAnonymous) || band > int64(allowance.BandTrusted) {
		band = int64(allowance.BandAnonymous) // a claim made before the share rule
	}
	c.Band = allowance.Band(band)
	return c, err == nil, err
}

func loadRootDay(ctx context.Context, q allowance.Querier, r allowance.Resource, day int64, root string) (rootDay, error) {
	var rd rootDay
	err := q.QueryRowContext(ctx, "SELECT count(*),coalesce(sum(weight),0) FROM allowance_claims WHERE resource=? AND day=? AND root=?", string(r), day, root).Scan(&rd.Claims, &rd.Standing)
	return rd, err
}

// freeExpiry is when a claim lot expires: at the end of its UTC day plus
// claim_expiry_days, the same for every claimant.
func freeExpiry(p *AllowanceParams, day int64) (expiresAt, halfLife int64) {
	return (day + 1 + p.ClaimExpiryDays) * 86400, p.ClaimHalfLifeDays
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
