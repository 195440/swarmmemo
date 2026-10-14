// Package judge is the single source of RFC 0020 §1–§3: one judgement
// record, one weight, one aggregate (the standing-weighted median), one gain
// formula and the run-wide settlement that never mints standing.
//
// Everything here is a pure integer function (ppm fixed point, like
// internal/trust), so a trust run that calls it recomputes byte for byte from
// its published snapshot. Nothing here reads a clock, a float or a database.
//
// Built in C303a (shadow): the trust run computes scores and marks with it
// beside rule 3 and changes no gate. C303b adds the cutover: trust parameter
// version 7 (standing.rule 4) makes it the rule and publishes Params in its
// standing.judgements section; the constants below are that section's
// defaults and the shadow's values.
package judge

import (
	"math/bits"
	"sort"
)

// ShadowVersion is the version of the judgements_shadow section a trust run
// publishes; it changes whenever any constant or rule below changes.
// Version 3 (C303b): the prior weighs PriorWeight × the subject's mean
// judgement weight, and scores say when they are thin.
const ShadowVersion = 3

// The scale: x ∈ [0, 100], 50 neutral.
const (
	Min     = 0
	Max     = 100
	Neutral = 50
)

// Score parameters (RFC 0020 §2): m = PriorWeight pseudo-judgements at
// Neutral, each weighing the subject's mean judgement weight (C303b; until
// then each weighed a full judgement, which pinned real subjects near 50).
// A subject with fewer than ThinBelow weighted judgements is labelled thin:
// below it the prior holds at least half of the weight.
const (
	PriorWeight = 2
	ThinBelow   = PriorWeight + 1
)

// Settlement parameters (RFC 0020 §3.3).
const (
	// KMin is how many independent later judgements with weight a subject
	// needs before a judgement of it is marked.
	KMin = 2
	// ReviewLoad is a review's act weight (a vote's is 1, a vouch's 1-50,
	// the trust parameters' edge_weights).
	ReviewLoad = 10
)

// HalfLifeDays is the decay of a judgement's weight in the aggregate
// (RFC 0015 §5.4: services change). The stake's decay is standing's.
const HalfLifeDays = 180

// DayFactorPPM is round(1e6 × 2^(−1/HalfLifeDays)), the published day
// factor of that decay (trust.DayFactor; a test checks they agree), so a run
// applies it one whole day at a time without a float.
const DayFactorPPM = 996157

// Acts that make judgements (RFC 0020 §1).
const (
	ActReview     = "review"
	ActVouch      = "vouch"
	ActVote       = "vote"
	ActWorkAccept = "work_accept"
	ActWitness    = "witness"
)

// Default values of the mapping (RFC 0020 §1).
const (
	// VouchDefault is a vouch's x when it names no score (coarse).
	VouchDefault = 80
	// VouchScoreMin is the lowest score a vouch may name: a vouch is up.
	VouchScoreMin = 51
	// WorkAcceptDefault is a work.accept's x when it carries no rating
	// (coarse). C300 (work.accept rating) has not landed: when it does,
	// board.WorkAcceptDefaultScore must be this constant, not a copy.
	WorkAcceptDefault = 75
	// Up and Down are a vote's sides; a witness verdict is Up (verified) or
	// Down (failed).
	Up   = Max
	Down = Min
)

// VerdictX maps a review verdict to x when the review names neither a score
// nor the good dimension. unverified is absent: it is no judgement.
var VerdictX = map[string]int64{"good": 80, "mixed": 50, "bad": 20, "unusable": 0}

// EvidenceWeightPPM is RFC 0015 §5.4's evidence weight by class.
var EvidenceWeightPPM = map[string]int64{"paid_use": 1_000_000, "work": 1_000_000, "observed": 400_000, "free_use": 300_000, "opinion": 200_000}

// RelationshipFactorPPM is RFC 0015 §5.4's relationship factor; a
// relationship not listed weighs 1e6. lab and outside_source are never
// community judgements.
var RelationshipFactorPPM = map[string]int64{"owner": 0, "affiliate": 0, "paid": 0, "competitor": 500_000, "outside_source": 0, "lab": 0}

// ActEvidencePPM is the evidence weight of an act that is not a review (a
// vote, a vouch, a work.accept, a witness verdict): it carries no evidence
// class, and v(s) already prices its author, so it weighs in full. Only a
// review's weight reads its verified evidence class.
const ActEvidencePPM = 1_000_000

// Rule is one row of the act-to-judgement table (RFC 0020 §1), as data.
type Rule struct {
	Act     string `json:"act"`
	Input   string `json:"input"`
	Subject string `json:"subject"`
	X       string `json:"x"`
	Coarse  string `json:"coarse"`
}

// Mapping is RFC 0020 §1's table, generated from the constants above (docs
// and /capabilities may print it; the From functions implement it).
var Mapping = []Rule{
	{ActReview, "data.review", "its subject", "score if given, else the good dimension, else verdict: good 80, mixed 50, bad 20, unusable 0 (unverified: no judgement)", "no"},
	{ActVouch, "value 1", "agent:TARGET", "data.score 51-100, default 80", "yes unless score given"},
	{ActVouch, "value 0", "agent:TARGET", "withdraws the author's judgement", ""},
	{ActVote, "up / down", "post:ID (its author owns it)", "100 / 0 (above / below the consensus)", "yes"},
	{ActWorkAccept, "work.accept", "agent:WORKER", "rating 0-100 if given, else 75", "yes unless rated"},
	{ActWitness, "verified / failed", "the claim", "100 / 0", "yes"},
}

// Judgement is one act that says "this is good or bad" (RFC 0020 §1).
type Judgement struct {
	ID      string `json:"id"`      // the act: an event, an endorsement seq, a work item
	Act     string `json:"act"`     // one of the Act constants
	Author  string `json:"author"`  // the account
	Root    string `json:"root"`    // the author's root: one current judgement per root and subject
	Subject string `json:"subject"` // AgentSubject, PostSubject, ClaimSubject or a review subject
	X       int64  `json:"x"`       // 0-100
	Coarse  bool   `json:"coarse"`  // states only a side
	// Evidence is a review's verified evidence class (empty for other
	// acts); Relationship a review's (effective) relationship.
	Evidence     string `json:"evidence,omitempty"`
	Relationship string `json:"relationship,omitempty"`
	Load         int64  `json:"load"` // the act's weight (stake), before decay
	At           int64  `json:"at"`
}

// Subject names. A review of agent:FINGERPRINT and a vouch for it judge the
// same subject.
func AgentSubject(account string) string { return "agent:" + account }
func PostSubject(id string) string       { return "post:" + id }
func ClaimSubject(agent, claim string) string {
	return "claim:" + agent + ":" + claim
}

// ReviewSubject names a review's subject: an agent subject as the agent,
// any other by its subject id.
func ReviewSubject(typ, key, id string) string {
	if typ == "agent" {
		return AgentSubject(key)
	}
	return "subject:" + id
}

// Side is a coarse judgement's end point: Up above Neutral, Down below.
func Side(x int64) int64 {
	switch {
	case x > Neutral:
		return Up
	case x < Neutral:
		return Down
	}
	return Neutral
}

// MedianX is the value the judgement counts as in a median: x, or its side
// when coarse.
func (j Judgement) MedianX() int64 {
	if j.Coarse {
		return Side(j.X)
	}
	return j.X
}

// EdgeWeightPPM is the judgement's edge weight to its subject's owner
// (RFC 0020 §1, from C303b): |x − 50| / 50, ENDORSE above 50, OPPOSE below.
func (j Judgement) EdgeWeightPPM() (w int64, oppose bool) {
	d := j.X - Neutral
	if d < 0 {
		return -d * 1_000_000 / Neutral, true
	}
	return d * 1_000_000 / Neutral, false
}

func clamp(x int64) int64 { return min(Max, max(Min, x)) }

// FromReview maps a review: its score when given (≥ 0), else its good
// dimension (≥ 0), else its verdict. ok is false for unverified (or an
// unknown verdict): no judgement.
func FromReview(verdict string, score, good int64) (x int64, ok bool) {
	switch {
	case verdict == "unverified":
		return 0, false
	case score >= 0:
		return clamp(score), true
	case good >= 0:
		return clamp(good), true
	}
	x, ok = VerdictX[verdict]
	return x, ok
}

// FromVouch maps a vouch: value 1 is a judgement (score 51-100 when given,
// else VouchDefault, coarse); any other value withdraws (ok false).
func FromVouch(value, score int64) (x int64, coarse, ok bool) {
	if value != 1 {
		return 0, false, false
	}
	if score > 0 {
		return min(Max, max(VouchScoreMin, score)), false, true
	}
	return VouchDefault, true, true
}

// FromVote maps a vote: up Up, down Down, both coarse; 0 (unvote) withdraws.
func FromVote(value int64) (x int64, ok bool) {
	switch value {
	case 1:
		return Up, true
	case -1:
		return Down, true
	}
	return 0, false
}

// FromWorkAccept maps a work.accept: its rating 0-100 when given (≥ 0),
// else WorkAcceptDefault, coarse.
func FromWorkAccept(rating int64) (x int64, coarse bool) {
	if rating >= 0 {
		return clamp(rating), false
	}
	return WorkAcceptDefault, true
}

// FromWitness maps a witness verdict: verified Up, failed Down, coarse.
func FromWitness(verified bool) int64 {
	if verified {
		return Up
	}
	return Down
}

// EvidencePPM is a class's evidence weight (0 for an unknown class).
func EvidencePPM(class string) int64 { return EvidenceWeightPPM[class] }

// EvidencePPM is the judgement's evidence weight: a review's class's, any
// other act's ActEvidencePPM.
func (j Judgement) EvidencePPM() int64 {
	if j.Act == ActReview {
		return EvidencePPM(j.Evidence)
	}
	return ActEvidencePPM
}

// RelationshipPPM is a relationship's factor (1e6 when not listed).
func RelationshipPPM(rel string) int64 {
	if f, ok := RelationshipFactorPPM[rel]; ok {
		return f
	}
	return 1_000_000
}

// Weight is RFC 0015 §5.4's w = evidence_weight × v(s_author) ×
// relationship_factor × decay(age), every factor and the result in ppm,
// rounded down at each step. Callers compute v (trust.VoteWeightPPM or
// today's seasoned rule) and the decay; this is the one product.
func Weight(evidencePPM, vPPM, relationshipPPM, decayPPM int64) int64 {
	w := MulDiv(max(0, evidencePPM), max(0, vPPM), 1_000_000)
	w = MulDiv(w, max(0, relationshipPPM), 1_000_000)
	return MulDiv(w, max(0, decayPPM), 1_000_000)
}

// Point is one judgement in a median: its value (MedianX) and weight (ppm).
type Point struct {
	X int64 `json:"x"`
	W int64 `json:"w"`
}

// Params are RFC 0020's tunable numbers. The defaults are the constants of
// this package; trust parameter version 7 publishes them as
// standing.judgements and the run reads them from there.
type Params struct {
	KMin         int64 `json:"k_min"`
	PriorWeight  int64 `json:"prior_weight"`
	ThinBelow    int64 `json:"thin_below"`
	HalfLifeDays int64 `json:"half_life_days"`
	DayFactorPPM int64 `json:"day_factor_ppm"`
	ReviewLoad   int64 `json:"review_load"`
}

// Default is the constants as Params: the shadow's values and version 7's.
var Default = Params{KMin: KMin, PriorWeight: PriorWeight, ThinBelow: ThinBelow, HalfLifeDays: HalfLifeDays, DayFactorPPM: DayFactorPPM, ReviewLoad: ReviewLoad}

// PriorPPM is the weight of the prior over points (RFC 0020 §2):
// PriorWeight × their mean weight (Σ w / n over the n points with weight),
// rounded down; 0 when no point has weight.
func (p Params) PriorPPM(points []Point) int64 {
	var w, n int64
	for _, pt := range points {
		if pt.W > 0 {
			w = satAdd(w, pt.W)
			n++
		}
	}
	return p.priorOf(w, n)
}

func (p Params) priorOf(w, n int64) int64 {
	if n <= 0 {
		return 0
	}
	return MulDiv(w, max(0, p.PriorWeight), n)
}

// Consensus is the standing-weighted median of points with PriorWeight
// pseudo-judgements at Neutral, each weighing the points' mean weight
// (RFC 0020 §2): the smallest x at which the cumulative weight reaches half
// the total; when it reaches exactly half, the midpoint of that x and the
// next, rounded down. Points with no weight do not count; with none it is
// Neutral. points is not modified.
func (p Params) Consensus(points []Point) int64 { return median(points, p.PriorPPM(points)) }

// Score is the consensus of a subject's current judgements under p.
func (p Params) Score(current []Point) int64 { return p.Consensus(current) }

// ScoreGrouped is Score when the caller merged judgements of equal value
// into points: n is how many weighted judgements they stand for, so the
// prior's mean weight is the judgements', not the groups'.
func (p Params) ScoreGrouped(groups []Point, n int64) int64 {
	var w int64
	for _, pt := range groups {
		if pt.W > 0 {
			w = satAdd(w, pt.W)
		}
	}
	return median(groups, p.priorOf(w, n))
}

// Thin reports whether a subject with this many weighted current judgements
// is labelled thin: pages say so instead of relying on the prior.
func (p Params) Thin(weighted int64) bool { return weighted < p.ThinBelow }

// Consensus is Default.Consensus.
func Consensus(points []Point) int64 { return Default.Consensus(points) }

// Thin is Default.Thin.
func Thin(weighted int64) bool { return Default.Thin(weighted) }

// ScoreGrouped is Default.ScoreGrouped.
func ScoreGrouped(groups []Point, n int64) int64 { return Default.ScoreGrouped(groups, n) }

// Outcome is c_T, where a judgement is marked against (RFC 0020 §3, as
// amended by the C303s simulation): the same weighted median over the later
// independent judgements, without the prior. The prior stands for "nobody
// knows yet"; k_min already requires later evidence before a mark, and a
// prior inside c_T pulled the outcome towards neutral, so the judges who
// named the truth early lost to the ones who said 50. With no weighted
// point it is Neutral.
func Outcome(points []Point) int64 { return median(points, 0) }

// median is the weighted median of points plus priorPPM at Neutral.
func median(points []Point, priorPPM int64) int64 {
	pts := make([]Point, 0, len(points)+1)
	for _, p := range points {
		if p.W > 0 {
			pts = append(pts, Point{clamp(p.X), p.W})
		}
	}
	if priorPPM > 0 || len(pts) == 0 {
		pts = append(pts, Point{Neutral, max(1, priorPPM)})
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].X < pts[j].X })
	// Merge equal values so "the next x" is a different one.
	merged := pts[:1]
	for _, p := range pts[1:] {
		if last := &merged[len(merged)-1]; last.X == p.X {
			last.W = satAdd(last.W, p.W)
		} else {
			merged = append(merged, p)
		}
	}
	var total int64
	for _, p := range merged {
		total = satAdd(total, p.W)
	}
	var cum int64
	for i, p := range merged {
		cum = satAdd(cum, p.W)
		switch twice := satAdd(cum, cum); {
		case twice > total || (twice == total && i == len(merged)-1):
			return p.X
		case twice == total:
			return (p.X + merged[i+1].X) / 2
		}
	}
	return Neutral // unreachable: some point always has weight
}

// Score is a subject's score (RFC 0020 §2): the consensus of its current
// judgements, one per root. Every surface calls it.
func Score(current []Point) int64 { return Default.Score(current) }

// Gain is a judgement's accuracy (RFC 0020 §3) in ppm of [−1, 1]: entered
// at consensus c0, the later independent consensus is cT. Numeric:
// (|c0 − cT| − |x − cT|) / 100. Coarse (x read as its Side): sign(x − c0) ×
// (cT − c0) / 100.
func Gain(x, c0, cT int64, coarse bool) int64 {
	x, c0, cT = clamp(x), clamp(c0), clamp(cT)
	if coarse {
		x = Side(x)
		return sign(x-c0) * (cT - c0) * 1_000_000 / (Max - Min)
	}
	return (abs(c0-cT) - abs(x-cT)) * 1_000_000 / (Max - Min)
}

// Mark is stake × gain (gain in ppm), rounded towards zero: a judgement's
// raw mark before the run-wide settlement.
func Mark(stake, gainPPM int64) int64 {
	m := MulDiv(max(0, stake), abs(gainPPM), 1_000_000)
	if gainPPM < 0 {
		return -m
	}
	return m
}

// Settlement is the run-wide settlement of raw marks (RFC 0020 §3.3).
type Settlement struct {
	Marks     []int64 `json:"-"`         // each mark after scaling: losses whole, gains scaled
	Forfeited int64   `json:"forfeited"` // Σ |negative marks|
	Owed      int64   `json:"owed"`      // Σ positive raw marks
	Paid      int64   `json:"paid"`      // Σ positive marks after scaling, ≤ Forfeited
	Returned  int64   `json:"returned"`  // Forfeited − Paid: back to the seeds, by seed
	ScalePPM  int64   `json:"scale_ppm"` // what share of each gain is paid
}

// Settle pays positive marks from negative ones across the whole run:
// losses are taken whole, gains are scaled by min(1, forfeited / owed) and
// rounded down, so Σ paid ≤ Σ forfeited and nothing is minted. What is left
// over (Returned) goes back to the seeds by seed, as today.
func Settle(raw []int64) Settlement {
	s := Settlement{Marks: make([]int64, len(raw)), ScalePPM: 1_000_000}
	for _, m := range raw {
		if m < 0 {
			s.Forfeited = satAdd(s.Forfeited, -max(m, -(1<<62)))
		} else {
			s.Owed = satAdd(s.Owed, m)
		}
	}
	if s.Owed > s.Forfeited {
		s.ScalePPM = MulDiv(s.Forfeited, 1_000_000, s.Owed)
	}
	for i, m := range raw {
		switch {
		case m < 0:
			s.Marks[i] = max(m, -(1 << 62))
		case s.Owed > s.Forfeited:
			s.Marks[i] = MulDiv(m, s.Forfeited, s.Owed)
		default:
			s.Marks[i] = m
		}
		if s.Marks[i] > 0 {
			s.Paid += s.Marks[i]
		}
	}
	s.Returned = s.Forfeited - s.Paid
	return s
}

// MulDiv is ⌊a × b / c⌋ for non-negative a, b and positive c, exact in 128
// bits; a quotient past int64 saturates; any other input gives 0. It is the
// trust run's one fixed-point product (internal/trust calls it too).
func MulDiv(a, b, c int64) int64 {
	if a <= 0 || b <= 0 || c <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(c) {
		return 1<<63 - 1
	}
	q, _ := bits.Div64(hi, lo, uint64(c))
	if q > 1<<63-1 {
		return 1<<63 - 1
	}
	return int64(q)
}

func satAdd(a, b int64) int64 {
	if s := a + b; s >= a {
		return s
	}
	return 1<<63 - 1
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func sign(x int64) int64 {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	}
	return 0
}
