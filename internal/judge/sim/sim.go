// Package sim is an agent-based simulation of RFC 0020 (C303s): a seeded
// world of subjects with a hidden true quality q, judged over 90 days by
// honest, herding, contrarian and attacking agents, scored and marked by the
// REAL code. Every judgement is a community review in a synthetic
// trust.Snapshot; every number comes from trust.Compute under trust
// parameter version 7 (rule 4, C303b: standing from the seed list and priced
// spend, moved by the judgement edges and the final marks), each run reading
// the previous run's carry as the board does. Nothing here reimplements a
// formula.
//
// Runs happen every Cadence days from day 6 to day 181 (window 90 days), so
// every judgement (days 1-89) is marked while in the window and then leaves
// it, which settles its mark. Run R90 (day 90) is the one run whose window
// holds every judgement: its scores and provisional marks.
//
// Deterministic: one PCG stream per (scenario, seed); floats are used only to
// draw noise and to report.
package sim

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"

	"swarmmemo/internal/judge"
	"swarmmemo/internal/trust"
)

const day = 86400

// Epoch is day 0 of every world (2026-06-01 00:00 UTC, day aligned).
const Epoch = 1780272000

// Kind is an agent type.
type Kind string

const (
	Informed   Kind = "honest-informed"
	Late       Kind = "honest-late"
	Herder     Kind = "herder"
	Contrarian Kind = "contrarian"
	Sybil      Kind = "sybil-ring"
	Funded     Kind = "funded-ring"
	Griefer    Kind = "griefer"
	Collusive  Kind = "collusive-pair"
)

// Kinds lists the agent types in report order.
var Kinds = []Kind{Informed, Late, Herder, Contrarian, Sybil, Funded, Griefer, Collusive}

// Noise is the honest judges' σ on q.
const Noise = 10

// FundedCents is what a funded ring member (or a griefer, a collusive root)
// buys through the cheapest priced root: spent credit, which seeds standing
// at cost up to standing.spend_cap_cents (200).
const FundedCents = 200

// Scenario is one world's shape.
type Scenario struct {
	Name  string `json:"name"`
	About string `json:"about"`
	// Subjects judged by the honest pool, and the pool by type.
	Subjects                             int `json:"subjects"`
	Informed, Late, Herders, Contrarians int
	PerSubject                           [2]int   `json:"per_subject"`    // judges per subject, min and max
	StandingCents                        [2]int64 `json:"standing_cents"` // an honest agent's spend standing, min and max
	SeedEvery                            int      `json:"seed_every"`     // every n-th honest agent is an arbiter seed (500 cents); 0 none
	SybilK, FundedK                      int
	Griefer, Collusive                   bool
}

// Scenarios are the seven worlds of C303s.
func Scenarios() []Scenario {
	base := Scenario{Subjects: 30, Informed: 20, Late: 15, Herders: 10, Contrarians: 5, PerSubject: [2]int{6, 10},
		StandingCents: [2]int64{80, 200}, SeedEvery: 10}
	with := func(name, about string, f func(*Scenario)) Scenario {
		s := base
		s.Name, s.About = name, about
		f(&s)
		return s
	}
	return []Scenario{
		with("baseline", "honest market: 6-10 judges per subject, mixed types", func(*Scenario) {}),
		with("herding", "herding-heavy: most judges copy the consensus", func(s *Scenario) { s.Informed, s.Late, s.Herders, s.Contrarians = 6, 6, 30, 3 }),
		with("sybil", "8 fresh keys with no standing judge their operator's q=20 subject 100, funded by transfers", func(s *Scenario) { s.SybilK = 8 }),
		with("funded", "5 members buy 200¢ of standing each (spend) and judge their operator's q=20 subjects 100 with paid use; one subject has no honest judges", func(s *Scenario) { s.FundedK = 5 }),
		with("griefing", "a seller with 200¢ judges the three best competitors 0 with paid use, early", func(s *Scenario) { s.Griefer = true }),
		with("thin", "2-3 judges per subject (like today), plus a collusive pair rating each other 100", func(s *Scenario) { s.PerSubject = [2]int{2, 3}; s.Collusive = true }),
		with("coldstart", "most agents just above the vote floor (50-70¢), few seeds, 3-5 judges per subject, a 3-member funded ring", func(s *Scenario) {
			s.StandingCents, s.SeedEvery, s.PerSubject, s.FundedK = [2]int64{50, 70}, 50, [2]int{3, 5}, 3
		}),
	}
}

// ScenarioByName finds a scenario.
func ScenarioByName(name string) (Scenario, bool) {
	for _, s := range Scenarios() {
		if s.Name == name {
			return s, true
		}
	}
	return Scenario{}, false
}

type agent struct {
	name, acct string
	kind       Kind
	cents      int64 // spend standing target (0: none, or a seed)
	seed       bool
	cost       int64 // cents spent during the 90 days
}

type subject struct {
	id, owner string
	q         int64
	pool      bool // the honest pool judges it
	griefed   bool // the griefer judges it 0
}

type planned struct {
	a        *agent
	s        *subject
	at       int64
	x        int64
	evidence string
}

// Config tunes a run (zero values are the defaults).
type Config struct {
	Cadence int64 // days between trust runs (default 7)
}

// Result is one (scenario, seed) run's metrics, by name (Metric*).
type Result struct {
	Scenario string             `json:"scenario"`
	Seed     uint64             `json:"seed"`
	M        map[string]float64 `json:"metrics"`
	// Conserved is true when every run paid at most what it forfeited and
	// its total standing never exceeded its seed mass, and all runs together
	// paid at most what they forfeited.
	Conserved bool `json:"conserved"`
	Runs      int  `json:"runs"`
}

func acct(name string) string {
	sum := sha256.Sum256([]byte("sim:" + name))
	return hex.EncodeToString(sum[:])
}

// Run simulates one scenario with one seed.
func Run(ctx context.Context, sc Scenario, seed uint64, cfg Config) (Result, error) {
	if cfg.Cadence <= 0 {
		cfg.Cadence = 7
	}
	h := sha256.Sum256([]byte(sc.Name))
	var hs uint64
	for _, b := range h[:8] {
		hs = hs<<8 | uint64(b)
	}
	rng := rand.New(rand.NewPCG(seed, hs))
	at := func(d0, d1 int64) int64 { // a time in days [d0, d1]
		return Epoch + d0*day + rng.Int64N((d1-d0+1)*day)
	}
	params := trust.JudgementsParams() // rule 4: RFC 0020 is the rule (C303b)
	params.Seeds, params.ServiceAccounts, params.WindowDays = nil, nil, 90
	operators := map[Kind]string{} // a ring's operator: its members' judgements route standing to it
	st := params.Standing
	snap := trust.Snapshot{Params: params}
	merchant := acct("merchant")
	topUp := func(a int64) int64 { return a * 206 / 1000 } // keeps decayed spend near a (half-life 90 days, every 30)
	addAccount := func(a *agent, firstSeen int64) {
		snap.Accounts = append(snap.Accounts, trust.Record{Type: "account", Account: a.acct, FirstSeen: firstSeen})
		if a.seed {
			snap.Params.Seeds = append(snap.Params.Seeds, a.acct)
		}
		if a.cents <= 0 {
			return
		}
		for d := int64(-1); d < 181; d += 30 {
			amount := a.cents
			if d > 0 {
				amount = topUp(a.cents)
			}
			snap.Spends = append(snap.Spends, trust.Record{Type: "spend", Account: a.acct, Day: Epoch/day + d, Amount: amount * st.CreditsPerCent, To: merchant})
			if d < 90 {
				a.cost += amount
			}
		}
	}
	var agents []*agent
	newAgent := func(kind Kind, i int, cents int64, seed bool, firstSeen int64) *agent {
		name := string(kind) + "-" + strconv.Itoa(i)
		a := &agent{name: name, acct: acct(sc.Name + ":" + name), kind: kind, cents: cents, seed: seed}
		agents = append(agents, a)
		addAccount(a, firstSeen)
		return a
	}
	// The honest pool.
	var pool []*agent
	n := 0
	for _, k := range []struct {
		kind Kind
		n    int
	}{{Informed, sc.Informed}, {Late, sc.Late}, {Herder, sc.Herders}, {Contrarian, sc.Contrarians}} {
		for i := 0; i < k.n; i++ {
			n++
			seeded := sc.SeedEvery > 0 && n%sc.SeedEvery == 0
			cents := sc.StandingCents[0] + rng.Int64N(sc.StandingCents[1]-sc.StandingCents[0]+1)
			if seeded {
				cents = 0
			}
			pool = append(pool, newAgent(k.kind, i, cents, seeded, Epoch-200*day))
		}
	}
	// Subjects, each owned by a seller with no standing.
	var subjects []*subject
	addSubject := func(id, owner string, q int64, inPool bool) *subject {
		s := &subject{id: id, owner: owner, q: q, pool: inPool}
		subjects = append(subjects, s)
		snap.Owners = append(snap.Owners, trust.Record{Type: "owner", Target: "subject:" + id, Account: owner})
		return s
	}
	for i := 0; i < sc.Subjects; i++ {
		addSubject("s"+strconv.Itoa(i), acct(sc.Name+":seller-"+strconv.Itoa(i)), rng.Int64N(101), true)
	}
	var plan []planned
	evidence := func() string {
		switch r := rng.IntN(10); {
		case r < 5:
			return "paid_use"
		case r < 8:
			return "free_use"
		}
		return "opinion"
	}
	// Attackers and their subjects.
	if sc.SybilK > 0 {
		op := acct(sc.Name + ":sybil-op")
		operators[Sybil] = op
		s := addSubject("sybil", op, 20, true)
		for i := 0; i < sc.SybilK; i++ {
			a := newAgent(Sybil, i, 0, false, Epoch+4*day)
			snap.Transfers = append(snap.Transfers, trust.Record{Type: "transfer", From: op, To: a.acct, Amount: 1000, CreatedAt: Epoch + 4*day + 60})
			plan = append(plan, planned{a: a, s: s, at: at(5, 15), x: 100, evidence: "opinion"})
		}
	}
	if sc.FundedK > 0 {
		op := acct(sc.Name + ":funded-op")
		operators[Funded] = op
		s := addSubject("funded", op, 20, true)
		solo := addSubject("funded-solo", op, 20, false)
		for i := 0; i < sc.FundedK; i++ {
			a := newAgent(Funded, i, FundedCents, false, Epoch-2*day)
			plan = append(plan, planned{a: a, s: s, at: at(2, 20), x: 100, evidence: "paid_use"}, planned{a: a, s: solo, at: at(2, 20), x: 100, evidence: "paid_use"})
		}
	}
	if sc.Griefer {
		a := newAgent(Griefer, 0, FundedCents, false, Epoch-200*day)
		addSubject("griefer", a.acct, 55, true)
		best := append([]*subject(nil), subjects[:sc.Subjects]...)
		sort.SliceStable(best, func(i, j int) bool { return best[i].q > best[j].q })
		for _, s := range best[:3] {
			s.griefed = true
			plan = append(plan, planned{a: a, s: s, at: at(2, 8), x: 0, evidence: "paid_use"})
		}
	}
	if sc.Collusive {
		a, b := newAgent(Collusive, 0, FundedCents, false, Epoch-200*day), newAgent(Collusive, 1, FundedCents, false, Epoch-200*day)
		sa, sb := addSubject("pair-a", a.acct, 35, true), addSubject("pair-b", b.acct, 35, true)
		plan = append(plan, planned{a: a, s: sb, at: at(2, 8), x: 100, evidence: "paid_use"}, planned{a: b, s: sa, at: at(2, 8), x: 100, evidence: "paid_use"})
	}
	// The honest pool's visits: n judges per pool subject, drawn without
	// replacement, each at its type's time.
	when := map[Kind][2]int64{Informed: {1, 30}, Late: {50, 85}, Herder: {10, 85}, Contrarian: {10, 85}}
	for _, s := range subjects {
		if !s.pool {
			continue
		}
		k := sc.PerSubject[0] + rng.IntN(sc.PerSubject[1]-sc.PerSubject[0]+1)
		perm := rng.Perm(len(pool))
		for _, i := range perm[:min(k, len(pool))] {
			w := when[pool[i].kind]
			plan = append(plan, planned{a: pool[i], s: s, at: at(w[0], w[1]), evidence: evidence()})
		}
	}

	// Standing as the first run sees it: what observers weigh with.
	compute := func(asOf int64) (trust.Output, error) {
		s := snap
		s.Meta = trust.Meta{Schema: 1, AsOf: asOf, PriorRuns: 30}
		return trust.Compute(ctx, s)
	}
	first, err := compute(Epoch + day)
	if err != nil {
		return Result{}, err
	}
	cents := map[string]int64{}
	for _, s := range first.Scores {
		if s.Parts.Standing != nil {
			cents[s.Account] = s.Parts.Standing.Cents
		}
	}
	decay := trust.NewDecay()
	// Values, subject by subject in time order: herders and contrarians read
	// the consensus they see (judge.Consensus over the latest judgement per
	// root, weighted as a run weighs them).
	sort.SliceStable(plan, func(i, j int) bool { return plan[i].at < plan[j].at })
	bySubject := map[string][]int{}
	for i := range plan {
		p := &plan[i]
		var pts []judge.Point
		latest := map[string]int{}
		for _, j := range bySubject[p.s.id] {
			latest[plan[j].a.acct] = j
		}
		for _, j := range latest {
			o := plan[j]
			if o.a == p.a {
				continue
			}
			w := judge.Weight(judge.EvidencePPM(o.evidence), st.VoteWeight(cents[o.a.acct]), 1_000_000, decay(judge.DayFactorPPM, (p.at-o.at)/day))
			pts = append(pts, judge.Point{X: o.x, W: w})
		}
		sort.Slice(pts, func(a, b int) bool { return pts[a].X < pts[b].X || pts[a].X == pts[b].X && pts[a].W < pts[b].W })
		c0 := judge.Consensus(pts)
		est := p.s.q + int64(math.Round(Noise*rng.NormFloat64()))
		switch p.a.kind {
		case Informed, Late:
			p.x = est
		case Herder:
			p.x = c0
		case Contrarian:
			if est >= c0 {
				p.x = c0 - 25
			} else {
				p.x = c0 + 25
			}
		}
		p.x = min(judge.Max, max(judge.Min, p.x))
		bySubject[p.s.id] = append(bySubject[p.s.id], i)
		snap.Reviews = append(snap.Reviews, trust.Record{Type: "review", ID: "r" + strconv.Itoa(i), Account: p.a.acct, Target: "subject:" + p.s.id,
			Value: p.x, Kind: p.evidence, State: "good", CreatedAt: p.at})
	}

	// The runs: rule 4, each reading the previous run's carry, as the board
	// does (trust.LatestCarry).
	var runDays []int64
	for d := int64(6); d <= 181; d += cfg.Cadence {
		runDays = append(runDays, d)
	}
	if !containsDay(runDays, 90) {
		runDays = append(runDays, 90)
		sort.Slice(runDays, func(i, j int) bool { return runDays[i] < runDays[j] })
	}
	type runOut struct {
		day   int64
		marks map[string]trust.JudgementMark // provisional
		sec   *trust.JudgementsShadow
		out   trust.Output
	}
	runs := make([]runOut, 0, len(runDays))
	conserved := true
	var carry *trust.JudgementCarry
	for _, d := range runDays {
		snap.Carry = carry.Records()
		out, err := compute(Epoch + d*day)
		if err != nil {
			return Result{}, err
		}
		sec := out.Judgements
		if sec == nil || sec.Carry == nil {
			return Result{}, fmt.Errorf("sim: run on day %d has no judgements section", d)
		}
		if sec.Settlement.Paid > sec.Settlement.Forfeited || sec.StandingMass > sec.SeedMass {
			conserved = false
		}
		m := make(map[string]trust.JudgementMark, len(sec.Marks))
		for _, mk := range sec.Marks {
			m[mk.ID] = mk
		}
		runs = append(runs, runOut{d, m, sec, out})
		carry = sec.Carry
	}
	snap.Carry = nil

	res := Result{Scenario: sc.Name, Seed: seed, M: map[string]float64{}, Runs: len(runs)}
	kindOf := map[string]Kind{}
	count := map[Kind]int{}
	cost := map[Kind]int64{}
	for _, a := range agents {
		kindOf[a.acct] = a.kind
		count[a.kind]++
		cost[a.kind] += a.cost
	}
	planOf := map[string]*planned{}
	for i := range plan {
		planOf["review:r"+strconv.Itoa(i)] = &plan[i]
	}
	perAgent := func(prefix string, sums map[Kind]int64) {
		for _, k := range Kinds {
			if count[k] > 0 {
				res.M[prefix+string(k)] = float64(sums[k]) / 1000 / float64(count[k])
			}
		}
	}

	// R90: the one run whose window holds every judgement.
	var r90 runOut
	for _, r := range runs {
		if r.day == 90 {
			r90 = r
		}
	}
	scoreOf := map[string]trust.SubjectScore{}
	for _, s := range r90.sec.Scores {
		scoreOf[s.Subject] = s
	}
	var errSum float64
	var nPool, thin int
	for _, s := range subjects {
		ss, ok := scoreOf["subject:"+s.id]
		if !ok {
			ss = trust.SubjectScore{Score: judge.Neutral, Thin: true}
		}
		if s.pool {
			errSum += math.Abs(float64(ss.Score - s.q))
			nPool++
			if ss.Thin {
				thin++
			}
		}
		switch s.id {
		case "sybil", "funded", "funded-solo", "pair-a":
			res.M["lift."+s.id] = float64(ss.Score - s.q)
		}
		if s.griefed {
			res.M["lift.griefed"] += float64(ss.Score-s.q) / 3
		}
	}
	res.M[MetricAccuracy] = errSum / float64(nPool)
	res.M[MetricThin] = float64(thin) / float64(nPool)
	sums := map[Kind]int64{}
	for _, m := range r90.marks {
		sums[kindOf[m.Author]] += m.Raw
	}
	perAgent(MetricMarkR90, sums)
	res.M[MetricMarked] = float64(r90.sec.Marked)
	res.M[MetricOpen] = float64(r90.sec.Open)

	// Final: what every run settled (the judgements leaving its window and
	// the gains still owed), the rule's only effect of a mark on standing.
	final := map[Kind]int64{}
	var paid, forfeited int64
	for _, r := range runs {
		for _, m := range r.sec.Final {
			final[kindOf[m.Author]] += m.Mark
			if m.Mark > 0 {
				paid += m.Mark
			} else {
				forfeited -= m.Mark
			}
		}
	}
	if paid > forfeited {
		conserved = false
	}
	perAgent(MetricMarkFinal, final)
	res.M[MetricPaid] = float64(paid) / 1000
	res.M[MetricForfeited] = float64(forfeited) / 1000
	res.M[MetricEscrow] = float64(runs[len(runs)-1].sec.Carry.Escrow) / 1000
	var owed int64
	for _, o := range runs[len(runs)-1].sec.Carry.Owed {
		owed += o.Amount
	}
	res.M[MetricOwed] = float64(owed) / 1000

	// Provisional marks that change sign across runs.
	flips, everMarked := 0, 0
	for id := range planOf {
		signs := map[int64]bool{}
		for _, r := range runs {
			if m, ok := r.marks[id]; ok {
				signs[sign(m.Raw)] = true
			}
		}
		if len(signs) > 0 {
			everMarked++
			if signs[1] && signs[-1] {
				flips++
			}
		}
	}
	if everMarked > 0 {
		res.M[MetricFlips] = float64(flips) / float64(everMarked)
	}

	// Time to first mark: the first run with a mark on a pool subject, from
	// its first judgement.
	var waits []float64
	marked := 0
	for _, s := range subjects {
		if !s.pool {
			continue
		}
		var firstAt int64 = -1
		for _, j := range bySubject[s.id] {
			if firstAt < 0 || plan[j].at < firstAt {
				firstAt = plan[j].at
			}
		}
		for _, r := range runs {
			if r.day > 90 {
				break
			}
			hit := false
			for _, m := range r.marks {
				if m.Subject == "subject:"+s.id {
					hit = true
					break
				}
			}
			if hit {
				waits = append(waits, float64(Epoch+r.day*day-firstAt)/day)
				marked++
				break
			}
		}
	}
	res.M[MetricMarkedSubjects] = float64(marked) / float64(nPool)
	if len(waits) > 0 {
		sort.Float64s(waits)
		res.M[MetricFirstMarkDays] = waits[len(waits)/2]
	}

	// Rings: the most standing the ring (members and operator) held above
	// its own seed in any run, against what it spent; and its final marks.
	for _, k := range []Kind{Sybil, Funded, Griefer, Collusive} {
		if count[k] == 0 {
			continue
		}
		members := map[string]bool{}
		for _, a := range agents {
			if a.kind == k {
				members[a.acct] = true
			}
		}
		if op := operators[k]; op != "" {
			members[op] = true
		}
		var best int64
		for _, r := range runs {
			var above int64
			for _, s := range r.out.Scores {
				if st := s.Parts.Standing; members[s.Account] && st != nil {
					above += st.Cents - st.SeedCents
				}
			}
			best = max(best, above)
		}
		res.M[MetricCapture+string(k)] = float64(best)
		res.M[MetricCost+string(k)] = float64(cost[k])
	}

	// Cost to fake: funded keys (200¢, paid use) needed to hold the median
	// of a pool subject against its R90 judges and the prior (PriorWeight ×
	// their mean weight), and their spend; the median subject.
	vFunded := judge.Weight(judge.EvidencePPM("paid_use"), st.VoteWeight(FundedCents), 1_000_000, 1_000_000)
	var fake []float64
	for _, s := range subjects {
		if !s.pool {
			continue
		}
		latest := map[string]int{}
		for _, j := range bySubject[s.id] {
			latest[plan[j].a.acct] = j
		}
		var pts []judge.Point
		for _, j := range latest {
			p := plan[j]
			pts = append(pts, judge.Point{X: p.x, W: judge.Weight(judge.EvidencePPM(p.evidence), st.VoteWeight(cents[p.a.acct]), 1_000_000, decay(judge.DayFactorPPM, (Epoch+90*day-p.at)/day))})
		}
		w := judge.Default.PriorPPM(pts)
		for _, p := range pts {
			w += p.W
		}
		keys := w/vFunded + 1
		fake = append(fake, float64(keys*(FundedCents+3*topUp(FundedCents))))
	}
	sort.Float64s(fake)
	res.M[MetricFakeCents] = fake[len(fake)/2]
	res.Conserved = conserved
	return res, nil
}

func containsDay(days []int64, d int64) bool {
	for _, x := range days {
		if x == d {
			return true
		}
	}
	return false
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

// Metric names. Per-type metrics append the Kind; marks are cents per agent.
const (
	MetricAccuracy       = "accuracy"        // mean |score − q| over pool subjects, run R90
	MetricThin           = "thin"            // share of pool subjects labelled thin at R90
	MetricMarkR90        = "mark.r90."       // Σ provisional raw mark per agent, run R90 (never paid: recomputed every run)
	MetricMarkFinal      = "mark.final."     // Σ settled final marks per agent over all runs: the rule
	MetricPaid           = "final.paid"      // Σ final gains paid, all runs
	MetricForfeited      = "final.forfeited" // Σ final losses taken, all runs
	MetricEscrow         = "final.escrow"    // forfeited and not yet paid, after the last run
	MetricOwed           = "final.owed"      // gains still owed after the last run
	MetricMarked         = "r90.marked"
	MetricOpen           = "r90.open"
	MetricFlips          = "sign_flips" // share of marked judgements whose provisional mark changes sign across runs
	MetricMarkedSubjects = "subjects_marked_by_r90"
	MetricFirstMarkDays  = "first_mark_days" // median days from a subject's first judgement to the first run that marks it
	MetricCapture        = "capture."        // cents: the most a ring (members and operator) held above its seed in any run
	MetricCost           = "cost."           // cents spent on standing during the 90 days
	MetricFakeCents      = "fake_cents"      // cents of funded keys to hold the median subject's median
)

// Stat is a metric over seeds.
type Stat struct {
	Mean float64 `json:"mean"`
	SD   float64 `json:"sd"`
	N    int     `json:"n"`
}

// Report aggregates one scenario over seeds.
type Report struct {
	Scenario  Scenario        `json:"scenario"`
	Seeds     []uint64        `json:"seeds"`
	Conserved bool            `json:"conserved"`
	Stats     map[string]Stat `json:"stats"`
	Results   []Result        `json:"results"`
}

// Simulate runs a scenario for seeds seed, seed+1, …, seed+n−1.
func Simulate(ctx context.Context, sc Scenario, seed uint64, n int, cfg Config) (Report, error) {
	rep := Report{Scenario: sc, Conserved: true, Stats: map[string]Stat{}}
	vals := map[string][]float64{}
	for i := 0; i < n; i++ {
		r, err := Run(ctx, sc, seed+uint64(i), cfg)
		if err != nil {
			return rep, err
		}
		rep.Seeds = append(rep.Seeds, r.Seed)
		rep.Results = append(rep.Results, r)
		rep.Conserved = rep.Conserved && r.Conserved
		for k, v := range r.M {
			vals[k] = append(vals[k], v)
		}
	}
	for k, xs := range vals {
		var s Stat
		s.N = len(xs)
		for _, x := range xs {
			s.Mean += x
		}
		s.Mean /= float64(s.N)
		for _, x := range xs {
			s.SD += (x - s.Mean) * (x - s.Mean)
		}
		if s.N > 1 {
			s.SD = math.Sqrt(s.SD / float64(s.N-1))
		}
		rep.Stats[k] = s
	}
	return rep, nil
}

// Markdown is the report as a page.
func (r Report) Markdown() string {
	var b strings.Builder
	f := func(k string) string {
		s, ok := r.Stats[k]
		if !ok {
			return "–"
		}
		return fmt.Sprintf("%.2f ± %.2f", s.Mean, s.SD)
	}
	fmt.Fprintf(&b, "## %s\n\n%s. %d subjects, seeds %v. Conserved in every run: **%v**.\n\n", r.Scenario.Name, r.Scenario.About, r.Scenario.Subjects, r.Seeds, r.Conserved)
	fmt.Fprintf(&b, "| metric | mean ± sd |\n|---|---|\n")
	for _, row := range [][2]string{
		{"mean \\|score − q\\| (R90)", MetricAccuracy},
		{"subjects labelled thin (R90, share)", MetricThin},
		{"subjects marked by day 90 (share)", MetricMarkedSubjects},
		{"days to first mark (median)", MetricFirstMarkDays},
		{"marked / open judgements (R90)", MetricMarked},
		{"sign flips of provisional marks across runs (share)", MetricFlips},
		{"final paid / forfeited ¢ (all runs)", MetricPaid},
		{"escrow / owed ¢ after the last run", MetricEscrow},
		{"cost to fake the median subject (¢)", MetricFakeCents},
	} {
		v := f(row[1])
		switch row[1] {
		case MetricMarked:
			v += " / " + f(MetricOpen)
		case MetricPaid:
			v += " / " + f(MetricForfeited)
		case MetricEscrow:
			v += " / " + f(MetricOwed)
		}
		fmt.Fprintf(&b, "| %s | %s |\n", row[0], v)
	}
	b.WriteString("\nCents per agent by type: the provisional marks at R90 (recomputed every run, never paid) and the final marks every run settled (the rule).\n\n")
	b.WriteString("| type | provisional R90 | final (settled) |\n|---|---|---|\n")
	for _, k := range Kinds {
		if _, ok := r.Stats[MetricMarkR90+string(k)]; !ok {
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", k, f(MetricMarkR90+string(k)), f(MetricMarkFinal+string(k)))
	}
	var attack []string
	for _, k := range []Kind{Sybil, Funded, Griefer, Collusive} {
		if _, ok := r.Stats[MetricCapture+string(k)]; ok {
			attack = append(attack, fmt.Sprintf("| %s | %s | %s |", k, f(MetricCapture+string(k)), f(MetricCost+string(k))))
		}
	}
	if len(attack) > 0 {
		b.WriteString("\n| attacker (members and operator) | most standing above seed in any run ¢ | spent ¢ |\n|---|---|---|\n")
		b.WriteString(strings.Join(attack, "\n") + "\n")
	}
	var lifts []string
	for _, s := range []string{"sybil", "funded", "funded-solo", "pair-a", "griefed"} {
		if _, ok := r.Stats["lift."+s]; ok {
			lifts = append(lifts, fmt.Sprintf("%s %s", s, f("lift."+s)))
		}
	}
	if len(lifts) > 0 {
		fmt.Fprintf(&b, "\nScore − q of attacked subjects (R90): %s.\n", strings.Join(lifts, "; "))
	}
	return b.String()
}
