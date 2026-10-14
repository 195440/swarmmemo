package trust

import (
	"sort"
	"strconv"

	"swarmmemo/internal/judge"
)

// RFC 0020 in shadow (C303a): every rule 3 run also reads its acts and the
// window's community reviews as judgements (internal/judge), scores every
// judged subject with the standing-weighted median, and marks every
// judgement that has at least judge.KMin independent later judgements with
// weight, settling the marks run-wide so nothing is minted. The result is
// the judgements_shadow section: published beside rule 3, read by nothing.
// It never changes standing, tiers, rankings or allowances, and it is not
// part of Output.Canonical, so rule 3's outputs (and recompute.py) are
// unchanged; it has its own canonical bytes and sha256.
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
//   - A penalised owner forces cT = 0 and settles whatever KMin says.
//   - Marks are recomputed every run over the window (horizon = window, as
//     rule 3's positions are), so a judgement's mark changes until it is
//     closed (a newer judgement by its root) and counts while it is in the
//     window.
//   - A judgement of a subject an account of its own root owns is no
//     judgement (rule 3 drops acts inside one root the same way).

// JudgementsShadow is the judgements_shadow section of a run (RFC 0020).
// Amounts are in the run's mass unit (a thousandth of a cent).
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
	Truncated  bool             `json:"truncated"`
	Settlement judge.Settlement `json:"settlement"`
	// StandingMass is the run's total standing under rule 3, and
	// StandingMassShadow the total with these marks in its settlement's
	// place: never more (nothing minted), up to the floors.
	StandingMass       int64 `json:"standing_mass"`
	StandingMassShadow int64 `json:"standing_mass_shadow"`
	// Bands compares each account's band under rule 3 with its band with
	// these marks in place of rule 3's settlement (first order: rule 3's
	// moved stakes kept, one step, not a fixed point).
	Bands    map[string]int64 `json:"bands"`
	Scores   []SubjectScore   `json:"scores"`
	Marks    []JudgementMark  `json:"marks"`
	Accounts []ShadowAccount  `json:"accounts"`
}

// ShadowParams are the constants the section used (internal/judge).
type ShadowParams struct {
	KMin                  int64            `json:"k_min"`
	PriorWeight           int64            `json:"prior_weight"`
	PriorValue            int64            `json:"prior_value"`
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

// SubjectScore is one judged subject's score.
type SubjectScore struct {
	Subject    string `json:"subject"`
	Score      int64  `json:"score"`
	Judgements int64  `json:"judgements"` // current: one per root
	Roots      int64  `json:"roots"`
	Weighted   int64  `json:"weighted"` // current judgements with weight
	Penalised  bool   `json:"penalised,omitempty"`
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
// settled part, and its standing and band under each.
type ShadowAccount struct {
	Account     string `json:"account"`
	Judgements  int64  `json:"judgements"`
	Marked      int64  `json:"marked"`
	GainPPM     int64  `json:"gain_ppm"` // Σ gain of its marked judgements
	Mark        int64  `json:"mark"`
	Rule3Judged int64  `json:"rule3_judged"`
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
	snap      Snapshot
	asOf, D   int64
	ws        int64
	service   map[string]bool
	reset     map[string]bool
	rootOf    func(string) string
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

func computeJudgementsShadow(in shadowInput) *JudgementsShadow {
	st, p := in.st, in.p
	out := &JudgementsShadow{Version: judge.ShadowVersion, Rule: "RFC 0020", Unit: "millicent",
		Params: ShadowParams{KMin: judge.KMin, PriorWeight: judge.PriorWeight, PriorValue: judge.Neutral, HalfLifeDays: judge.HalfLifeDays,
			DayFactorPPM: judge.DayFactorPPM, HorizonDays: p.WindowDays, ReviewLoad: judge.ReviewLoad, VouchDefault: judge.VouchDefault,
			WorkAcceptDefault: judge.WorkAcceptDefault, VerdictX: judge.VerdictX, EvidenceWeightPPM: judge.EvidenceWeightPPM,
			RelationshipFactorPPM: judge.RelationshipFactorPPM, ActEvidencePPM: judge.ActEvidencePPM},
		ByAct: map[string]int64{}, Bands: map[string]int64{"raised": 0, "lowered": 0, "same": 0},
		Scores: []SubjectScore{}, Marks: []JudgementMark{}, Accounts: []ShadowAccount{}}
	cv := curves{}

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
	for _, r := range sortRecords(in.snap.Owners) {
		own(r.Target, r.Account)
	}

	var entries []shadowEntry
	add := func(j judge.Judgement, withdraw bool, owner string) {
		if j.Author == "" || j.Subject == "" || in.service[j.Author] || in.reset[j.Author] || j.At < in.ws || j.At >= in.asOf {
			return
		}
		own(j.Subject, owner)
		j.Root = in.rootOf(j.Author)
		entries = append(entries, shadowEntry{j: j, withdraw: withdraw, closeIdx: -1})
	}
	ends := make([]*Record, 0, len(in.snap.Endorsements))
	for i := range in.snap.Endorsements {
		ends = append(ends, &in.snap.Endorsements[i])
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
	for _, r := range sortRecords(in.snap.Acts) {
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
	for _, r := range sortRecords(in.snap.Reviews) {
		add(judge.Judgement{ID: "review:" + r.ID, Act: judge.ActReview, Author: r.Account, Subject: r.Target, X: min(judge.Max, max(judge.Min, r.Value)),
			Evidence: r.Kind, Relationship: r.LinkValue, Load: judge.ReviewLoad, At: r.CreatedAt}, false, "")
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
			if o == e.j.Author || in.rootOf(o) == e.j.Root {
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

	v := make(map[string]int64, len(in.nodes))
	standing := func(a string) int64 {
		if i, ok := in.index[a]; ok {
			return in.c[i]
		}
		return 0
	}
	vOf := func(a string) int64 {
		w, ok := v[a]
		if !ok {
			w = st.VoteWeight(standing(a) / massPerCent)
			v[a] = w
		}
		return w
	}
	weightAt := func(j judge.Judgement, t int64) int64 {
		return judge.Weight(j.EvidencePPM(), vOf(j.Author), judge.RelationshipPPM(j.Relationship), cv.decay(judge.DayFactorPPM, dayOf(t)-dayOf(j.At)))
	}
	// The stake budget: an author's judgements are scaled down together.
	loadOf := func(j judge.Judgement) int64 { return j.Load * cv.decay(st.DayFactorPPM, in.D-dayOf(j.At)) }
	authorLoad := map[string]int64{}
	for _, s := range subjects {
		for _, e := range bySubject[s] {
			if !e.withdraw {
				authorLoad[e.j.Author] += loadOf(e.j)
			}
		}
	}
	penalised := func(subject string) bool {
		for o := range owners[subject] {
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

	type rawMark struct {
		m      JudgementMark
		author string
	}
	var raws []rawMark
	judgements := map[string]int64{}
	var work int64
	for _, s := range subjects {
		list := bySubject[s]
		pen := penalised(s)
		for i, e := range list {
			if e.withdraw {
				out.Withdrawals++
				continue
			}
			out.Judgements++
			out.ByAct[e.j.Act]++
			judgements[e.j.Author]++
			if pen {
				out.Penalised++
			}
			if e.closeIdx >= 0 {
				out.Closed++
			}
			if out.Truncated {
				out.Open++
				continue
			}
			work += int64(len(list))
			if work > p.MaxWork {
				out.Truncated = true
				out.Open++
				continue
			}
			T, end := in.asOf, len(list)
			if e.closeIdx >= 0 {
				T, end = list[e.closeIdx].j.At, e.closeIdx
			}
			before, _ := latest(list, 0, i, e.j.At, func(o shadowEntry) bool { return o.j.Root != e.j.Root })
			c0 := judge.Consensus(before)
			after, k := latest(list, i+1, end, T, func(o shadowEntry) bool { return in.indep(e.j.Author, o.j.Author) })
			cT := judge.Outcome(after)
			if pen {
				cT = judge.Min
			} else if k < judge.KMin {
				out.Open++
				continue
			}
			gain := judge.Gain(e.j.X, c0, cT, e.j.Coarse)
			stake := st.stake(standing(e.j.Author), loadOf(e.j), st.budgetScale(authorLoad[e.j.Author]))
			raws = append(raws, rawMark{JudgementMark{ID: e.j.ID, Act: e.j.Act, Author: e.j.Author, Subject: s, X: e.j.X, Coarse: e.j.Coarse, At: e.j.At,
				C0: c0, CT: cT, K: k, GainPPM: gain, Stake: stake, Raw: judge.Mark(stake, gain), Closed: e.closeIdx >= 0, Penalised: pen}, e.j.Author})
		}
		// The subject's score: its current judgements, one per root.
		pts, k := latest(list, 0, len(list), in.asOf, func(shadowEntry) bool { return true })
		if len(pts) > 0 {
			roots := map[string]bool{}
			for _, e := range list {
				roots[e.j.Root] = true
			}
			out.Scores = append(out.Scores, SubjectScore{Subject: s, Score: judge.Score(pts), Judgements: int64(len(pts)), Roots: int64(len(roots)), Weighted: k, Penalised: pen})
		}
	}
	out.Subjects = int64(len(out.Scores))

	// Run-wide settlement: gains paid from losses, never minted.
	raw := make([]int64, len(raws))
	for i, r := range raws {
		raw[i] = r.m.Raw
	}
	out.Settlement = judge.Settle(raw)
	marks, gains, marked := map[string]int64{}, map[string]int64{}, map[string]int64{}
	for i := range raws {
		raws[i].m.Mark = out.Settlement.Marks[i]
		marks[raws[i].author] += raws[i].m.Mark
		gains[raws[i].author] += raws[i].m.GainPPM
		marked[raws[i].author]++
		out.Marks = append(out.Marks, raws[i].m)
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
		if judgements[a] == 0 && mark == 0 && in.judged[i] == 0 && band == bandShadow {
			continue
		}
		out.Accounts = append(out.Accounts, ShadowAccount{Account: a, Judgements: judgements[a], Marked: marked[a], GainPPM: gains[a], Mark: mark,
			Rule3Judged: in.judged[i], Cents: cents, CentsShadow: centsShadow, Band: band, BandShadow: bandShadow})
	}
	return out
}

// ShadowPublishedMax bounds each list of the judgements_shadow section a run
// records with its inputs; the full section is what `swarmmemo trust
// replay` recomputes from the published snapshot, and its sha256 is
// recorded.
const ShadowPublishedMax = 1000

// Published is the section as a run records it: the totals, its sha256 and
// size, and at most limit entries of each list (the most judged subjects,
// the largest marks, the accounts with the largest change), each list's
// full length beside it.
func (s *JudgementsShadow) Published(limit int) map[string]any {
	full := s.Canonical()
	scores := append([]SubjectScore(nil), s.Scores...)
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].Judgements != scores[j].Judgements {
			return scores[i].Judgements > scores[j].Judgements
		}
		return scores[i].Subject < scores[j].Subject
	})
	marks := append([]JudgementMark(nil), s.Marks...)
	sort.SliceStable(marks, func(i, j int) bool {
		x, y := absInt(marks[i].Mark), absInt(marks[j].Mark)
		if x != y {
			return x > y
		}
		return marks[i].ID < marks[j].ID
	})
	accounts := append([]ShadowAccount(nil), s.Accounts...)
	sort.SliceStable(accounts, func(i, j int) bool {
		x, y := absInt(accounts[i].Mark-accounts[i].Rule3Judged), absInt(accounts[j].Mark-accounts[j].Rule3Judged)
		if x != y {
			return x > y
		}
		return accounts[i].Account < accounts[j].Account
	})
	return map[string]any{"version": s.Version, "rule": s.Rule, "unit": s.Unit, "params": s.Params, "sha256": sha256Hex(full), "bytes": len(full),
		"judgements": s.Judgements, "withdrawals": s.Withdrawals, "by_act": s.ByAct, "subjects": s.Subjects, "marked": s.Marked, "open": s.Open,
		"closed": s.Closed, "penalised": s.Penalised, "truncated": s.Truncated, "settlement": s.Settlement, "standing_mass": s.StandingMass, "standing_mass_shadow": s.StandingMassShadow, "bands": s.Bands,
		"scores": scores[:min(limit, len(scores))], "scores_total": len(scores),
		"marks": marks[:min(limit, len(marks))], "marks_total": len(marks),
		"accounts": accounts[:min(limit, len(accounts))], "accounts_total": len(accounts),
		"recompute": "swarmmemo trust replay --snapshot SNAPSHOT.jsonl --json recomputes the full section from the run's snapshot"}
}

func absInt(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
