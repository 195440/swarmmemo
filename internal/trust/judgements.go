package trust

import (
	"sort"
	"strconv"

	"swarmmemo/internal/judge"
)

// RFC 0020 in shadow (C303a): every rule 3 run also reads its acts and the
// window's community reviews as judgements (internal/judge), scores every
// judged subject with the standing-weighted median, and marks every
// judgement that has at least k_min independent later judgements with
// weight, settling the marks run-wide so nothing is minted. The result is
// the judgements_shadow section: published beside rule 3, read by nothing.
// It never changes standing, tiers, rankings or allowances, and it is not
// part of Output.Canonical, so rule 3's outputs (and recompute.py) are
// unchanged; it has its own canonical bytes and sha256.
//
// Rule 4 (parameter version 7, C303b, judgements_rule.go) makes the same
// judgements the rule: the section is then Output.Judgements, part of the
// canonical output, and standing moves only through the judgement edges and
// the final marks.
//
// Choices where the RFC leaves room (each stated in the section's params):
//   - An act that is not a review weighs evidence 1 (judge.ActEvidencePPM);
//     a review weighs its verified class.
//   - A judgement's weight is evaluated at the time its consensus is: c0 at
//     its entry (age t0 − at), cT at its mark time (age T − at); v(s) is the
//     author's standing in this run.
//   - Only later judgements with weight count towards KMin, so keys without
//     standing cannot force a settlement at the prior.
//   - cT is judge.Outcome: the later judgements' median without the prior
//     (C303s simulation, RFC 0020 §9; shadow version 2).
//   - The prior weighs PriorWeight × the subject's mean judgement weight
//     (C303b, shadow version 3), and a score below ThinBelow weighted
//     judgements says thin.
//   - A penalised owner forces cT = 0 and settles whatever KMin says.
//   - In shadow, marks are recomputed every run over the window (horizon =
//     window, as rule 3's positions are), so a judgement's mark changes
//     until it is closed (a newer judgement by its root) and counts while it
//     is in the window. Rule 4 settles each mark once, when its judgement
//     leaves the window.
//   - A judgement of a subject an account of its own root owns is no
//     judgement (rule 3 drops acts inside one root the same way).

// JudgementsShadow is RFC 0020's section of a run: judgements_shadow under
// rule 3 (published beside the output), Output.Judgements under rule 4 (the
// rule; its Final, Carry and CarryIn fields exist only there). Amounts are
// in the run's mass unit (a thousandth of a cent).
type JudgementsShadow struct {
	Version     int64            `json:"version"`
	Rule        string           `json:"rule"`
	Unit        string           `json:"unit"`
	Params      ShadowParams     `json:"params"`
	Judgements  int64            `json:"judgements"`
	Withdrawals int64            `json:"withdrawals"`
	ByAct       map[string]int64 `json:"by_act"`
	Subjects    int64            `json:"subjects"`
	Marked      int64            `json:"marked"`
	Open        int64            `json:"open"`
	Closed      int64            `json:"closed"`
	Penalised   int64            `json:"penalised"`
	// Truncated is true when the run stopped marking at max_work.
	Truncated bool `json:"truncated"`
	// Settlement is the shadow's run-wide settlement of its marks; under
	// rule 4 it is the settlement of the judgements that left the window
	// (Final), with the escrow carried in as one more loss.
	Settlement judge.Settlement `json:"settlement"`
	// StandingMass is the run's total standing under rule 3, and
	// StandingMassShadow the total with these marks in its settlement's
	// place: never more (nothing minted), up to the floors. Under rule 4
	// both are the run's total standing and SeedMass its seed mass, which
	// the total never exceeds.
	StandingMass       int64 `json:"standing_mass"`
	StandingMassShadow int64 `json:"standing_mass_shadow"`
	SeedMass           int64 `json:"seed_mass,omitempty"`
	// Bands compares each account's band under rule 3 with its band with
	// these marks in place of rule 3's settlement (first order: rule 3's
	// moved stakes kept, one step, not a fixed point).
	Bands    map[string]int64 `json:"bands"`
	Scores   []SubjectScore   `json:"scores"`
	Marks    []JudgementMark  `json:"marks"`
	Accounts []ShadowAccount  `json:"accounts"`
	// Rule 4 only (judgements_rule.go). Marks are then provisional (Mark 0:
	// recomputed every run, settled only at exit); Final lists the marks
	// settled in this run (the judgements that left the window, and unpaid
	// gains carried in); Edges counts the judgement edges; CarryIn is the
	// sha256 of the carry this run read ("" for none) and Carry what the
	// next run reads.
	Final   []JudgementMark `json:"final,omitempty"`
	Edges   *EdgeTotals     `json:"edges,omitempty"`
	CarryIn string          `json:"carry_in_sha256,omitempty"`
	Carry   *JudgementCarry `json:"carry,omitempty"`
}

// ShadowParams are the constants the section used: internal/judge's for
// the shadow, standing.judgements under rule 4.
type ShadowParams struct {
	KMin                  int64            `json:"k_min"`
	PriorWeight           int64            `json:"prior_weight"`
	PriorValue            int64            `json:"prior_value"`
	ThinBelow             int64            `json:"thin_below"`
	HalfLifeDays          int64            `json:"half_life_days"`
	DayFactorPPM          int64            `json:"day_factor_ppm"`
	HorizonDays           int64            `json:"horizon_days"`
	ReviewLoad            int64            `json:"review_load"`
	VouchDefault          int64            `json:"vouch_default"`
	WorkAcceptDefault     int64            `json:"work_accept_default"`
	VerdictX              map[string]int64 `json:"verdict_x"`
	EvidenceWeightPPM     map[string]int64 `json:"evidence_weight_ppm"`
	RelationshipFactorPPM map[string]int64 `json:"relationship_factor_ppm"`
	ActEvidencePPM        int64            `json:"act_evidence_ppm"`
}

func shadowParams(jp judge.Params, horizon int64) ShadowParams {
	return ShadowParams{KMin: jp.KMin, PriorWeight: jp.PriorWeight, PriorValue: judge.Neutral, ThinBelow: jp.ThinBelow, HalfLifeDays: jp.HalfLifeDays,
		DayFactorPPM: jp.DayFactorPPM, HorizonDays: horizon, ReviewLoad: jp.ReviewLoad, VouchDefault: judge.VouchDefault,
		WorkAcceptDefault: judge.WorkAcceptDefault, VerdictX: judge.VerdictX, EvidenceWeightPPM: judge.EvidenceWeightPPM,
		RelationshipFactorPPM: judge.RelationshipFactorPPM, ActEvidencePPM: judge.ActEvidencePPM}
}

// SubjectScore is one judged subject's score.
type SubjectScore struct {
	Subject    string `json:"subject"`
	Score      int64  `json:"score"`
	Judgements int64  `json:"judgements"` // current: one per root
	Roots      int64  `json:"roots"`
	Weighted   int64  `json:"weighted"` // current judgements with weight
	// Thin is true below thin_below weighted judgements: pages say so
	// instead of relying on the prior.
	Thin      bool `json:"thin,omitempty"`
	Penalised bool `json:"penalised,omitempty"`
}

// JudgementMark is one marked judgement.
type JudgementMark struct {
	ID        string `json:"id"`
	Act       string `json:"act"`
	Author    string `json:"author"`
	Subject   string `json:"subject"`
	X         int64  `json:"x"`
	Coarse    bool   `json:"coarse"`
	At        int64  `json:"at"`
	C0        int64  `json:"c0"`
	CT        int64  `json:"c_t"`
	K         int64  `json:"k"`
	GainPPM   int64  `json:"gain_ppm"`
	Stake     int64  `json:"stake"`
	Raw       int64  `json:"raw"`
	Mark      int64  `json:"mark"`
	Closed    bool   `json:"closed,omitempty"`
	Penalised bool   `json:"penalised,omitempty"`
}

// ShadowAccount is one account the section touches: its marks, rule 3's
// settled part, and its standing and band under each. Under rule 4 Mark is
// what was settled to it in this run, Balance its carried judgement balance
// after it, and Cents its standing (CentsShadow the same).
type ShadowAccount struct {
	Account     string `json:"account"`
	Judgements  int64  `json:"judgements"`
	Marked      int64  `json:"marked"`
	GainPPM     int64  `json:"gain_ppm"` // Σ gain of its marked judgements
	Mark        int64  `json:"mark"`
	Rule3Judged int64  `json:"rule3_judged"`
	Balance     int64  `json:"balance,omitempty"`
	Cents       int64  `json:"cents"`
	CentsShadow int64  `json:"cents_shadow"`
	Band        int64  `json:"band"`
	BandShadow  int64  `json:"band_shadow"`
}

// Canonical is the section's canonical JSON, what a recomputation of the
// same snapshot reproduces byte for byte.
func (s *JudgementsShadow) Canonical() []byte { return Canonical(s) }

// shadowInput is what computeStanding hands the shadow (rule 3 only).
type shadowInput struct {
	st        *StandingParams
	p         Params
	set       judgementSet
	asOf, D   int64
	penalty   map[string]int64
	nodes     []string
	index     map[string]int
	c, judged []int64 // rule 3's standing and its settled part (mass)
	seed, pen []int64
	indep     func(a, b string) bool
}

type shadowEntry struct {
	j        judge.Judgement
	withdraw bool // closes the root's judgement without opening one
	closeIdx int  // index of the entry that closed it; −1 open
}

// judgementSet is the window's judgements by subject, in time order, with
// their subjects' owners: what the shadow and rule 4 both read.
type judgementSet struct {
	bySubject map[string][]shadowEntry
	subjects  []string
	owners    map[string]map[string]bool
	rootOf    func(string) string
}

// collectJudgements reads the snapshot's acts and reviews in [ws, asOf) as
// judgements (RFC 0020 §1), drops those of an author's own root's subject,
// and orders each subject's judgements in time, closing a root's older
// judgement at its newer one (§3.4).
func collectJudgements(st *StandingParams, jp judge.Params, snap Snapshot, asOf, ws int64, service, reset map[string]bool, rootOf func(string) string) judgementSet {
	// Owners route a judgement to an account: an agent subject's agent, a
	// post's author, a claim's agent, a reviewed subject's owner records.
	owners := map[string]map[string]bool{}
	own := func(subject, account string) {
		if account == "" {
			return
		}
		if owners[subject] == nil {
			owners[subject] = map[string]bool{}
		}
		owners[subject][account] = true
	}
	for _, r := range sortRecords(snap.Owners) {
		own(r.Target, r.Account)
	}

	var entries []shadowEntry
	add := func(j judge.Judgement, withdraw bool, owner string) {
		if j.Author == "" || j.Subject == "" || service[j.Author] || reset[j.Author] || j.At < ws || j.At >= asOf {
			return
		}
		own(j.Subject, owner)
		j.Root = rootOf(j.Author)
		entries = append(entries, shadowEntry{j: j, withdraw: withdraw, closeIdx: -1})
	}
	ends := make([]*Record, 0, len(snap.Endorsements))
	for i := range snap.Endorsements {
		ends = append(ends, &snap.Endorsements[i])
	}
	sort.Slice(ends, func(i, j int) bool { return endorsementLess(ends[i], ends[j]) })
	for _, r := range ends {
		if r.Target == "" {
			continue
		}
		id := "e" + strconv.FormatInt(r.Seq, 10)
		switch r.Kind {
		case "vote":
			if r.MessageID == "" {
				continue
			}
			x, ok := judge.FromVote(r.Value)
			load := st.EdgeWeights["vote"]
			if r.Value < 0 {
				load = st.EdgeWeights["down_vote"]
			}
			add(judge.Judgement{ID: id, Act: judge.ActVote, Author: r.Voter, Subject: judge.PostSubject(r.MessageID), X: x, Coarse: true, Load: load, At: r.CreatedAt}, !ok, r.Target)
		case "vouch":
			x, coarse, ok := judge.FromVouch(r.Value, 0)
			add(judge.Judgement{ID: id, Act: judge.ActVouch, Author: r.Voter, Subject: judge.AgentSubject(r.Target), X: x, Coarse: coarse,
				Load: st.actWeight("vouch", r.Weight), At: r.CreatedAt}, !ok, r.Target)
		}
	}
	for _, r := range sortRecords(snap.Acts) {
		switch {
		case r.Kind == "work_accept":
			x, coarse := judge.FromWorkAccept(-1)
			add(judge.Judgement{ID: "work:" + r.ID, Act: judge.ActWorkAccept, Author: r.From, Subject: judge.AgentSubject(r.To), X: x, Coarse: coarse,
				Load: st.EdgeWeights["work_accept"], At: r.CreatedAt}, false, r.To)
		case r.Kind == "witness" && r.ID != "" && r.Value != 0:
			add(judge.Judgement{ID: "witness:" + r.From + ":" + r.ID, Act: judge.ActWitness, Author: r.From, Subject: judge.ClaimSubject(r.To, r.ID),
				X: judge.FromWitness(r.Value > 0), Coarse: true, Load: st.EdgeWeights["witness"], At: r.CreatedAt}, false, r.To)
		}
	}
	for _, r := range sortRecords(snap.Reviews) {
		add(judge.Judgement{ID: "review:" + r.ID, Act: judge.ActReview, Author: r.Account, Subject: r.Target, X: min(judge.Max, max(judge.Min, r.Value)),
			Evidence: r.Kind, Relationship: r.LinkValue, Load: jp.ReviewLoad, At: r.CreatedAt}, false, "")
	}
	// An agent subject is its agent's, whoever judged it.
	for i := range entries {
		if s := entries[i].j.Subject; len(s) > 6 && s[:6] == "agent:" {
			own(s, s[6:])
		}
	}

	// By subject, in time order; drop judgements of one's own root's subject.
	bySubject := map[string][]shadowEntry{}
	for _, e := range entries {
		selfOwned := false
		for o := range owners[e.j.Subject] {
			if o == e.j.Author || rootOf(o) == e.j.Root {
				selfOwned = true
				break
			}
		}
		if !selfOwned {
			bySubject[e.j.Subject] = append(bySubject[e.j.Subject], e)
		}
	}
	subjects := make([]string, 0, len(bySubject))
	for s, list := range bySubject {
		subjects = append(subjects, s)
		sort.SliceStable(list, func(a, b int) bool {
			x, y := list[a].j, list[b].j
			switch {
			case x.At != y.At:
				return x.At < y.At
			case x.ID != y.ID:
				return x.ID < y.ID
			}
			return x.Author < y.Author
		})
		last := map[string]int{}
		for i := range list {
			if prev, ok := last[list[i].j.Root]; ok {
				list[prev].closeIdx = i
			}
			last[list[i].j.Root] = i
		}
	}
	sort.Strings(subjects)
	return judgementSet{bySubject: bySubject, subjects: subjects, owners: owners, rootOf: rootOf}
}

// sortedOwners lists a subject's owners in order.
func (set judgementSet) sortedOwners(subject string) []string {
	out := make([]string, 0, len(set.owners[subject]))
	for o := range set.owners[subject] {
		out = append(out, o)
	}
	sort.Strings(out)
	return out
}

// markInput is what marking reads.
type markInput struct {
	st       *StandingParams
	jp       judge.Params
	set      judgementSet
	asOf, D  int64
	maxWork  int64
	standing func(string) int64 // an account's standing in this run (mass)
	penalty  map[string]int64
	indep    func(a, b string) bool
	// c0 (rule 4): the consensus at entry the previous run stored, by
	// judgement id; a judgement it does not name is first seen here and
	// its c0 is computed in this run.
	c0 map[string]int64
}

// markOutput is every judgement's mark (raw: before any settlement), the
// subjects' scores and the section's counts.
type markOutput struct {
	marks       []JudgementMark
	entries     []CarriedJudgement // every judgement in the window, with c0 and its raw mark (0 unmarked)
	scores      []SubjectScore
	byAuthor    map[string]int64 // judgements per author
	byAct       map[string]int64
	judgements  int64
	withdrawals int64
	open        int64
	closed      int64
	penalised   int64
	truncated   bool
}

// markJudgements marks every judgement of set that has at least k_min
// independent later judgements with weight (RFC 0020 §3), and scores every
// subject (§2).
func markJudgements(in markInput) markOutput {
	st, jp := in.st, in.jp
	out := markOutput{byAuthor: map[string]int64{}, byAct: map[string]int64{}}
	cv := curves{}
	v := map[string]int64{}
	vOf := func(a string) int64 {
		w, ok := v[a]
		if !ok {
			w = st.VoteWeight(in.standing(a) / massPerCent)
			v[a] = w
		}
		return w
	}
	weightAt := func(j judge.Judgement, t int64) int64 {
		return judge.Weight(j.EvidencePPM(), vOf(j.Author), judge.RelationshipPPM(j.Relationship), cv.decay(jp.DayFactorPPM, dayOf(t)-dayOf(j.At)))
	}
	load := in.set.loads(st, in.D)
	penalised := func(subject string) bool {
		for o := range in.set.owners[subject] {
			if in.penalty[o] > 0 {
				return true
			}
		}
		return false
	}
	// latest returns the newest entry per root among list[from:to] that
	// pass keep, as weighted points at time t.
	latest := func(list []shadowEntry, from, to int, t int64, keep func(shadowEntry) bool) (pts []judge.Point, k int64) {
		newest := map[string]int{}
		var roots []string
		for i := from; i < to; i++ {
			if !keep(list[i]) {
				continue
			}
			if _, ok := newest[list[i].j.Root]; !ok {
				roots = append(roots, list[i].j.Root)
			}
			newest[list[i].j.Root] = i
		}
		sort.Strings(roots)
		for _, r := range roots {
			e := list[newest[r]]
			if e.withdraw {
				continue
			}
			w := weightAt(e.j, t)
			pts = append(pts, judge.Point{X: e.j.MedianX(), W: w})
			if w > 0 {
				k++
			}
		}
		return pts, k
	}

	var work int64
	for _, s := range in.set.subjects {
		list := in.set.bySubject[s]
		pen := penalised(s)
		for i, e := range list {
			if e.withdraw {
				out.withdrawals++
				continue
			}
			out.judgements++
			out.byAct[e.j.Act]++
			out.byAuthor[e.j.Author]++
			if pen {
				out.penalised++
			}
			if e.closeIdx >= 0 {
				out.closed++
			}
			if out.truncated {
				out.open++
				continue
			}
			work += int64(len(list))
			if work > in.maxWork {
				out.truncated = true
				out.open++
				continue
			}
			T, end := in.asOf, len(list)
			if e.closeIdx >= 0 {
				T, end = list[e.closeIdx].j.At, e.closeIdx
			}
			c0, stored := in.c0[e.j.ID]
			if !stored {
				before, _ := latest(list, 0, i, e.j.At, func(o shadowEntry) bool { return o.j.Root != e.j.Root })
				c0 = jp.Consensus(before)
			}
			carried := CarriedJudgement{ID: e.j.ID, Author: e.j.Author, At: e.j.At, C0: c0}
			after, k := latest(list, i+1, end, T, func(o shadowEntry) bool { return in.indep(e.j.Author, o.j.Author) })
			cT := judge.Outcome(after)
			if pen {
				cT = judge.Min
			} else if k < jp.KMin {
				out.open++
				out.entries = append(out.entries, carried)
				continue
			}
			gain := judge.Gain(e.j.X, c0, cT, e.j.Coarse)
			stake := load.stake(e.j, in.standing(e.j.Author))
			m := JudgementMark{ID: e.j.ID, Act: e.j.Act, Author: e.j.Author, Subject: s, X: e.j.X, Coarse: e.j.Coarse, At: e.j.At,
				C0: c0, CT: cT, K: k, GainPPM: gain, Stake: stake, Raw: judge.Mark(stake, gain), Closed: e.closeIdx >= 0, Penalised: pen}
			carried.Raw = m.Raw
			out.marks = append(out.marks, m)
			out.entries = append(out.entries, carried)
		}
		// The subject's score: its current judgements, one per root.
		pts, k := latest(list, 0, len(list), in.asOf, func(shadowEntry) bool { return true })
		if len(pts) > 0 {
			roots := map[string]bool{}
			for _, e := range list {
				roots[e.j.Root] = true
			}
			out.scores = append(out.scores, SubjectScore{Subject: s, Score: jp.Score(pts), Judgements: int64(len(pts)), Roots: int64(len(roots)), Weighted: k,
				Thin: jp.Thin(k), Penalised: pen})
		}
	}
	return out
}

// judgementLoads is the stake of a judgement (RFC 0020 §3.3): its act's
// load decayed by standing's half-life, scaled by its author's stake budget
// over all of the author's judgements in the window.
type judgementLoads struct {
	st     *StandingParams
	cv     curves
	D      int64
	author map[string]int64
}

func (set judgementSet) loads(st *StandingParams, D int64) judgementLoads {
	l := judgementLoads{st: st, cv: curves{}, D: D, author: map[string]int64{}}
	for _, s := range set.subjects {
		for _, e := range set.bySubject[s] {
			if !e.withdraw {
				l.author[e.j.Author] += l.of(e.j)
			}
		}
	}
	return l
}

func (l judgementLoads) of(j judge.Judgement) int64 {
	return j.Load * l.cv.decay(l.st.DayFactorPPM, l.D-dayOf(j.At))
}

func (l judgementLoads) stake(j judge.Judgement, standing int64) int64 {
	return l.st.stake(standing, l.of(j), l.st.budgetScale(l.author[j.Author]))
}

func computeJudgementsShadow(in shadowInput) *JudgementsShadow {
	st, p := in.st, in.p
	out := &JudgementsShadow{Version: judge.ShadowVersion, Rule: "RFC 0020", Unit: "millicent", Params: shadowParams(judge.Default, p.WindowDays),
		ByAct: map[string]int64{}, Bands: map[string]int64{"raised": 0, "lowered": 0, "same": 0},
		Scores: []SubjectScore{}, Marks: []JudgementMark{}, Accounts: []ShadowAccount{}}
	standing := func(a string) int64 {
		if i, ok := in.index[a]; ok {
			return in.c[i]
		}
		return 0
	}
	mo := markJudgements(markInput{st: st, jp: judge.Default, set: in.set, asOf: in.asOf, D: in.D, maxWork: p.MaxWork, standing: standing,
		penalty: in.penalty, indep: in.indep})
	out.fillCounts(mo)

	// Run-wide settlement: gains paid from losses, never minted.
	raw := make([]int64, len(mo.marks))
	for i, m := range mo.marks {
		raw[i] = m.Raw
	}
	out.Settlement = judge.Settle(raw)
	marks, gains, marked := map[string]int64{}, map[string]int64{}, map[string]int64{}
	for i := range mo.marks {
		mo.marks[i].Mark = out.Settlement.Marks[i]
		marks[mo.marks[i].Author] += mo.marks[i].Mark
		gains[mo.marks[i].Author] += mo.marks[i].GainPPM
		marked[mo.marks[i].Author]++
		out.Marks = append(out.Marks, mo.marks[i])
	}
	out.Marked = int64(len(out.Marks))

	// Each account's standing with these marks in place of rule 3's
	// settled part. Rule 3 returned what its settlement took to the seeds
	// by seed (Σ judged = −that); this settlement returns its own leftover
	// the same way, so the seeds' share moves by R = Returned + Σ judged and
	// the total is conserved (up to floors).
	var S, R int64
	eff := make([]int64, len(in.nodes))
	for i := range in.nodes {
		eff[i] = mulDiv(in.seed[i], 1e6-in.pen[i], 1e6)
		S += eff[i]
		R += in.judged[i]
	}
	R += out.Settlement.Returned
	for i, a := range in.nodes {
		mark := marks[a]
		alt := in.c[i] - in.judged[i] + mark
		if S > 0 && eff[i] > 0 {
			if R >= 0 {
				alt += mulDiv(R, eff[i], S)
			} else {
				alt -= mulDiv(-R, eff[i], S) + 1 // rounded up: never minted
			}
		}
		alt = max(0, alt)
		out.StandingMass += in.c[i]
		out.StandingMassShadow += alt
		cents, centsShadow := in.c[i]/massPerCent, alt/massPerCent
		band, bandShadow := st.Band(cents), st.Band(centsShadow)
		switch {
		case bandShadow < band:
			out.Bands["raised"]++
		case bandShadow > band:
			out.Bands["lowered"]++
		default:
			out.Bands["same"]++
		}
		if mo.byAuthor[a] == 0 && mark == 0 && in.judged[i] == 0 && band == bandShadow {
			continue
		}
		out.Accounts = append(out.Accounts, ShadowAccount{Account: a, Judgements: mo.byAuthor[a], Marked: marked[a], GainPPM: gains[a], Mark: mark,
			Rule3Judged: in.judged[i], Cents: cents, CentsShadow: centsShadow, Band: band, BandShadow: bandShadow})
	}
	return out
}

// fillCounts copies marking's counts and scores into the section.
func (s *JudgementsShadow) fillCounts(mo markOutput) {
	s.Judgements, s.Withdrawals, s.Open, s.Closed, s.Penalised, s.Truncated = mo.judgements, mo.withdrawals, mo.open, mo.closed, mo.penalised, mo.truncated
	for k, v := range mo.byAct {
		s.ByAct[k] = v
	}
	s.Scores = append(s.Scores, mo.scores...)
	s.Subjects = int64(len(s.Scores))
}

// ShadowPublishedMax bounds each list of the judgements_shadow section a run
// records with its inputs; the full section is what `swarmmemo trust
// replay` recomputes from the published snapshot, and its sha256 is
// recorded.
const ShadowPublishedMax = 1000

// Published is the section as a run records it: the totals, its sha256 and
// size, and at most limit entries of each list (the most judged subjects,
// the largest marks, the accounts with the largest change), each list's
// full length beside it. Under rule 4 the carry is recorded whole beside
// it (Run: judgements_carry), and the final marks are listed like the marks.
func (s *JudgementsShadow) Published(limit int) map[string]any {
	full := s.Canonical()
	scores := append([]SubjectScore(nil), s.Scores...)
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].Judgements != scores[j].Judgements {
			return scores[i].Judgements > scores[j].Judgements
		}
		return scores[i].Subject < scores[j].Subject
	})
	largest := func(list []JudgementMark) []JudgementMark {
		marks := append([]JudgementMark(nil), list...)
		sort.SliceStable(marks, func(i, j int) bool {
			x, y := absInt(marks[i].Mark)+absInt(marks[i].Raw), absInt(marks[j].Mark)+absInt(marks[j].Raw)
			if x != y {
				return x > y
			}
			return marks[i].ID < marks[j].ID
		})
		return marks[:min(limit, len(marks))]
	}
	accounts := append([]ShadowAccount(nil), s.Accounts...)
	sort.SliceStable(accounts, func(i, j int) bool {
		x, y := absInt(accounts[i].Mark-accounts[i].Rule3Judged), absInt(accounts[j].Mark-accounts[j].Rule3Judged)
		if x != y {
			return x > y
		}
		return accounts[i].Account < accounts[j].Account
	})
	out := map[string]any{"version": s.Version, "rule": s.Rule, "unit": s.Unit, "params": s.Params, "sha256": sha256Hex(full), "bytes": len(full),
		"judgements": s.Judgements, "withdrawals": s.Withdrawals, "by_act": s.ByAct, "subjects": s.Subjects, "marked": s.Marked, "open": s.Open,
		"closed": s.Closed, "penalised": s.Penalised, "truncated": s.Truncated, "settlement": s.Settlement, "standing_mass": s.StandingMass, "standing_mass_shadow": s.StandingMassShadow, "bands": s.Bands,
		"scores": scores[:min(limit, len(scores))], "scores_total": len(scores),
		"marks": largest(s.Marks), "marks_total": len(s.Marks),
		"accounts": accounts[:min(limit, len(accounts))], "accounts_total": len(accounts),
		"recompute": "swarmmemo trust replay --snapshot SNAPSHOT.jsonl --json recomputes the full section from the run's snapshot"}
	if s.Carry != nil {
		out["seed_mass"], out["edges"], out["carry_in_sha256"], out["carry_sha256"] = s.SeedMass, s.Edges, s.CarryIn, sha256Hex(Canonical(s.Carry))
		out["final"], out["final_total"] = largest(s.Final), len(s.Final)
	}
	return out
}

func absInt(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
