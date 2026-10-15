package trust

import (
	"maps"
	"sort"
	"strconv"
	"strings"

	"swarmmemo/internal/judge"
)

// Judgements (RFC 0020 §1, kept by RFC 0021): every act that says "this is
// good or bad" is one judgement record (internal/judge): a vote, a vouch, a
// work.accept, a witness verdict and a community review. A run reads the
// window's judgements, keeps one current judgement per root and subject,
// and makes each a judgement edge to the subject's owners (unified.go) when
// x > 50. Each judgement with at least min_judges independent later
// judgements with weight is marked against the consensus it entered at
// (c0) and the later independent consensus (c_T), with judge.Gain; the
// gains set its author's accuracy, a multiplier on its edges' capacity.
//
// Choices where the RFCs leave room (each stated here, and recomputed by
// scripts/trust/recompute.py the same way):
//   - A judgement's weight (judge.Weight) is its evidence weight (a
//     review's class, an act's act_evidence class), v(s) of its author's
//     first-pass standing (accuracy 1), its relationship factor (reviews)
//     and its decay with the half-life H, at the time its consensus is
//     taken: c0 at its entry, c_T at its mark time.
//   - c0 is the consensus of the earlier judgements of other roots (with
//     the score's prior); c_T is judge.Outcome over the later judgements of
//     independent authors (another root, no transfer either way within
//     funded_days = H), up to the root's next judgement of the subject.
//   - A subject whose owner is under an active penalty has c_T = 0.
//   - A judgement of a subject an account of its own root owns is none.

// judgement is one judgement of the window with its routing.
type jEntry struct {
	j        judge.Judgement
	withdraw bool // closes the root's judgement without opening one
	closeIdx int  // index of the entry that closed it; −1 open
}

// judgementSet is the window's judgements by subject, in time order, with
// their subjects' owners.
type judgementSet struct {
	bySubject map[string][]jEntry
	subjects  []string
	owners    map[string]map[string]bool
}

// collectJudgements reads the snapshot's endorsements, acts and reviews in
// [ws, asOf) as judgements, drops those of an author's own root's subject,
// and orders each subject's judgements in time, closing a root's older
// judgement at its newer one.
func collectJudgements(p Params, snap Snapshot, asOf, ws int64, reset map[string]bool, rootOf func(string) string) judgementSet {
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
	evidence := func(act string) string { return p.ActEvidence[act] }
	var entries []jEntry
	add := func(j judge.Judgement, withdraw bool, owner string) {
		if j.Author == "" || j.Subject == "" || reset[j.Author] || j.At < ws || j.At >= asOf {
			return
		}
		own(j.Subject, owner)
		j.Root = rootOf(j.Author)
		entries = append(entries, jEntry{j: j, withdraw: withdraw, closeIdx: -1})
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
			add(judge.Judgement{ID: id, Act: judge.ActVote, Author: r.Voter, Subject: judge.PostSubject(r.MessageID), X: x, Coarse: true,
				Evidence: evidence(judge.ActVote), At: r.CreatedAt}, !ok, r.Target)
		case "vouch":
			x, coarse, ok := judge.FromVouch(r.Value, 0)
			add(judge.Judgement{ID: id, Act: judge.ActVouch, Author: r.Voter, Subject: judge.AgentSubject(r.Target), X: x, Coarse: coarse,
				Evidence: evidence(judge.ActVouch), At: r.CreatedAt}, !ok, r.Target)
		}
	}
	for _, r := range sortRecords(snap.Acts) {
		switch {
		case r.Kind == "work_accept":
			x, coarse := judge.FromWorkAccept(-1)
			add(judge.Judgement{ID: "work:" + r.ID, Act: judge.ActWorkAccept, Author: r.From, Subject: judge.AgentSubject(r.To), X: x, Coarse: coarse,
				Evidence: evidence(judge.ActWorkAccept), At: r.CreatedAt}, false, r.To)
		case r.Kind == "witness" && r.ID != "" && r.Value != 0:
			add(judge.Judgement{ID: "witness:" + r.From + ":" + r.ID, Act: judge.ActWitness, Author: r.From, Subject: judge.ClaimSubject(r.To, r.ID),
				X: judge.FromWitness(r.Value > 0), Coarse: true, Evidence: evidence(judge.ActWitness), At: r.CreatedAt}, false, r.To)
		}
	}
	for _, r := range sortRecords(snap.Reviews) {
		add(judge.Judgement{ID: "review:" + r.ID, Act: judge.ActReview, Author: r.Account, Subject: r.Target, X: min(judge.Max, max(judge.Min, r.Value)),
			Evidence: r.Kind, Relationship: r.LinkValue, At: r.CreatedAt}, false, "")
	}
	// An agent subject is its agent's, whoever judged it.
	for i := range entries {
		if s := entries[i].j.Subject; strings.HasPrefix(s, "agent:") {
			own(s, s[len("agent:"):])
		}
	}
	bySubject := map[string][]jEntry{}
	for _, e := range entries {
		if !judge.OwnSubject(e.j.Author, maps.Keys(owners[e.j.Subject]), rootOf) {
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
	return judgementSet{bySubject: bySubject, subjects: subjects, owners: owners}
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

// JudgementEvidencePPM is a judgement's evidence weight (RFC 0021 §11): a
// review's verified class's, any other act's act_evidence class's. Every
// weighing of a judgement reads it: the run's edges and marks, the
// reputation's backers.
func (p Params) JudgementEvidencePPM(act, class string) int64 {
	if act != judge.ActReview {
		class = p.ActEvidence[act]
	}
	return p.EvidenceWeightPPM[class]
}

// evidencePPM is a judgement's evidence weight under p.
func (p Params) evidencePPM(j judge.Judgement) int64 {
	return p.JudgementEvidencePPM(j.Act, j.Evidence)
}

// accuracyInput is what marking reads.
type accuracyInput struct {
	p        Params
	set      judgementSet
	asOf     int64
	maxWork  int64
	vOf      func(account string) int64 // v(s) of the author's first-pass standing
	penalty  func(account string) bool  // under an active penalty
	indep    func(a, b string) bool
	decayDay func(days int64) int64 // decay with the half-life H
}

// accuracyOutput is each author's marked judgements and Σ gain.
type accuracyOutput struct {
	gain      map[string]int64 // Σ gain (ppm) by author
	marked    map[string]int64 // marked judgements by author
	judged    int64
	open      int64
	truncated bool
	work      int64
}

// markAccuracy marks every judgement of the set with at least min_judges
// independent later judgements with weight (RFC 0021 §6).
func markAccuracy(in accuracyInput) accuracyOutput {
	out := accuracyOutput{gain: map[string]int64{}, marked: map[string]int64{}}
	weightAt := func(j judge.Judgement, t int64) int64 {
		return judge.Weight(in.p.evidencePPM(j), in.vOf(j.Author), judge.RelationshipPPM(j.Relationship), in.decayDay(dayOf(t)-dayOf(j.At)))
	}
	penalised := func(subject string) bool {
		for o := range in.set.owners[subject] {
			if in.penalty(o) {
				return true
			}
		}
		return false
	}
	// latest returns the newest entry per root among list[from:to] that pass
	// keep, as weighted points at time t, and how many have weight.
	latest := func(list []jEntry, from, to int, t int64, keep func(jEntry) bool) (pts []judge.Point, k int64) {
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
	m := in.p.MinJudges
	for _, s := range in.set.subjects {
		list := in.set.bySubject[s]
		pen := penalised(s)
		for i, e := range list {
			if e.withdraw {
				continue
			}
			out.judged++
			if out.truncated {
				out.open++
				continue
			}
			out.work += int64(len(list))
			if out.work > in.maxWork {
				out.truncated = true
				out.open++
				continue
			}
			T, end := in.asOf, len(list)
			if e.closeIdx >= 0 {
				T, end = list[e.closeIdx].j.At, e.closeIdx
			}
			before, _ := latest(list, 0, i, e.j.At, func(o jEntry) bool { return o.j.Root != e.j.Root })
			c0 := judge.Params{PriorWeight: m}.Consensus(before)
			after, k := latest(list, i+1, end, T, func(o jEntry) bool { return in.indep(e.j.Author, o.j.Author) })
			cT := judge.Outcome(after)
			if pen {
				cT = judge.Min
			} else if k < m {
				out.open++
				continue
			}
			out.gain[e.j.Author] += judge.Gain(e.j.X, c0, cT, e.j.Coarse)
			out.marked[e.j.Author]++
		}
	}
	return out
}

// AccuracyPPM is RFC 0021 §6's multiplier: clamp(1 + 2 × Σ gain / (n + m),
// 0.5, 2) in ppm, Σ gain in ppm over n marked judgements.
func AccuracyPPM(gainPPM, n, m int64) int64 {
	if n+m <= 0 {
		return 1e6
	}
	acc := 1e6 + 2*gainPPM/(n+m)
	return min(2e6, max(500000, acc))
}
