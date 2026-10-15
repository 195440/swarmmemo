// Package judge is the single source of RFC 0020 §1–§2, as RFC 0021 keeps
// them: one judgement record, one weight, one aggregate (the
// standing-weighted median), and the gain formula a trust run turns into
// each judge's accuracy (RFC 0021 §6).
//
// Everything here is a pure integer function (ppm fixed point, like
// internal/trust), so a trust run that calls it recomputes byte for byte from
// its published snapshot. Nothing here reads a clock, a float or a database.
package judge

import (
	"iter"
	"math/bits"
	"sort"
)

// ScoreField is the wire name of the score every reputation surface shows
// (C304: agent.get and subject.get reputation, Find's agent hits): the
// reviews aggregate's score_shadow while RFC 0020's score is in shadow.
// When the score goes live, this constant is the switch.
const ScoreField = "score_shadow"

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

// HalfLifeDays is H (RFC 0021 §11): the decay of a judgement's weight in
// the aggregate and of a judgement edge (RFC 0015 §5.4: services change).
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

// OwnSubject reports whether a judgement by author of a subject its owners
// own is no judgement (RFC 0020 §3.3): an owner is the author or shares its
// root. rootOf maps an account to its root (itself when it has none). It is
// the one exclusion every score reads: the trust run's judgement set, the
// subject aggregate (subject.get, /s/ID, Find quality) and the reputation.
func OwnSubject(author string, owners iter.Seq[string], rootOf func(string) string) bool {
	root := rootOf(author)
	for o := range owners {
		if o == author || rootOf(o) == root {
			return true
		}
	}
	return false
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

// EdgePPM is s(x), the strength of the judgement's edge to its subject's
// owners (RFC 0021 §2): (x − 50) / 50 for x > 50, a coarse judgement
// counting as coarseX; 0 otherwise (a down judgement makes no edge).
func (j Judgement) EdgePPM(coarseX int64) int64 {
	x := j.X
	if j.Coarse && x > Neutral {
		x = coarseX
	}
	if x <= Neutral {
		return 0
	}
	return (min(x, Max) - Neutral) * 1_000_000 / Neutral
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

// Params are the score's numbers: m = PriorWeight (RFC 0021 §11's m, also
// the trust run's min_judges) and the thin label's bound. The defaults are
// the constants of this package.
type Params struct {
	PriorWeight int64 `json:"prior_weight"`
	ThinBelow   int64 `json:"thin_below"`
}

// Default is the constants as Params.
var Default = Params{PriorWeight: PriorWeight, ThinBelow: ThinBelow}

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
