package trust

import (
	"errors"
	"fmt"
	"math/bits"
	"sort"

	"swarmmemo/internal/judge"
)

// Rule 4 (parameter version 7, C303b): RFC 0020 is the rule. It replaces
// rule 3's stakes (stakes.go, still recomputable for its runs); seeds,
// the stake budget, the independence test and the penalties are as in
// rule 3. Standing moves only two ways:
//
//   - Judgement edges (RFC 0020 §1). The current judgement of each root on
//     each subject an account owns is an edge from its author to the owners:
//     ENDORSE above 50, OPPOSE below, weighing |x − 50| / 50. Each run it
//     moves that share of the judgement's stake (stake_ppm × its load ×
//     decay × the author's budget scale × the author's standing) to the
//     owners, split equally; an OPPOSE share is spent instead, and the
//     owners lose it too: both return to the seeds by seed. Standing is the
//     fixed point of seed + balance + moved in − moved out in `iterations`
//     steps, as rule 3's.
//   - Final marks (§3.3). Every run marks every judgement in its window
//     (provisional, recomputed each run, never paid) against the consensus
//     at entry c0, which the run that first sees the judgement computes and
//     the carry keeps from then on. When a judgement leaves the window its
//     last provisional mark (the previous run's) is FINAL: each run settles
//     the judgements that left the window since the previous run, plus the
//     gains still owed, with judge.Settle against the escrow (what earlier
//     settlements forfeited and did not pay), so Σ paid ≤ Σ forfeited over
//     all runs. A loss is capped at its author's standing in the run. What
//     is settled adds to the author's judgement balance, which counts in
//     standing and decays with standing's half-life, as do the escrow and
//     the gains owed (balances below zero decay rounded away from zero, so
//     the carried total never rises).
//
// A run is still a pure function of its snapshot: the previous run's carry
// (its as-of time, escrow, every judgement of its window with c0 and the
// provisional mark, the balances and the gains owed) is in the snapshot as
// "carry" records, the run publishes the carry it read by sha256 and the
// carry it hands on in full, inside its canonical output.

// JudgementsVersion is the parameter version in which RFC 0020 is the rule
// (standing.rule 4). It is never the compiled-in default: the operator
// activates it with `swarmmemo params set trust` (RUNBOOK).
const JudgementsVersion = 7

// JudgementCarry is what a rule 4 run hands the next: the published inputs
// of finality.
type JudgementCarry struct {
	AsOf       int64              `json:"as_of"`
	Escrow     int64              `json:"escrow"`
	Judgements []CarriedJudgement `json:"judgements"`
	Balances   []CarriedBalance   `json:"balances"`
	Owed       []CarriedOwed      `json:"owed"`
}

// CarriedJudgement is one judgement of the run's window: its consensus at
// entry and its provisional raw mark in this run (0 when unmarked).
type CarriedJudgement struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	At     int64  `json:"at"`
	C0     int64  `json:"c0"`
	Raw    int64  `json:"raw"`
}

// CarriedBalance is an account's judgement balance: Σ of its settled final
// marks, decayed.
type CarriedBalance struct {
	Account string `json:"account"`
	Amount  int64  `json:"amount"`
}

// CarriedOwed is a final gain not yet paid in full: a later settlement
// pays it when losses allow.
type CarriedOwed struct {
	ID     string `json:"id"`
	Author string `json:"author"`
	Amount int64  `json:"amount"`
}

// EdgeTotals counts a rule 4 run's judgement edges and what they moved.
type EdgeTotals struct {
	Endorse     int64 `json:"endorse"`
	Oppose      int64 `json:"oppose"`
	EndorseMass int64 `json:"endorse_mass"`
	OpposeMass  int64 `json:"oppose_mass"`
}

// Carry record kinds (Record type "carry").
const (
	carryRun       = "run"       // as_of, amount: the escrow
	carryJudgement = "judgement" // id, account: its author, created_at, value: c0, amount: the raw mark
	carryBalance   = "balance"   // account, amount
	carryOwed      = "owed"      // id, account: its author, amount
)

// Records is the carry as snapshot records, what the next run reads.
func (c *JudgementCarry) Records() []Record {
	if c == nil {
		return nil
	}
	out := []Record{{Type: "carry", Kind: carryRun, AsOf: c.AsOf, Amount: c.Escrow}}
	for _, j := range c.Judgements {
		out = append(out, Record{Type: "carry", Kind: carryJudgement, ID: j.ID, Account: j.Author, CreatedAt: j.At, Value: j.C0, Amount: j.Raw})
	}
	for _, b := range c.Balances {
		out = append(out, Record{Type: "carry", Kind: carryBalance, Account: b.Account, Amount: b.Amount})
	}
	for _, o := range c.Owed {
		out = append(out, Record{Type: "carry", Kind: carryOwed, ID: o.ID, Account: o.Author, Amount: o.Amount})
	}
	return out
}

// carryFromRecords reads a snapshot's carry records (nil for none).
func carryFromRecords(list []Record) (*JudgementCarry, error) {
	if len(list) == 0 {
		return nil, nil
	}
	c := &JudgementCarry{Judgements: []CarriedJudgement{}, Balances: []CarriedBalance{}, Owed: []CarriedOwed{}}
	runs := 0
	ids, accounts, owed := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, r := range sortRecords(list) {
		// Amounts are millicents: far below 2^50 (11 trillion dollars).
		if r.Amount > 1<<50 || r.Amount < -(1<<50) {
			return nil, fmt.Errorf("trust input: carry amount %d out of range", r.Amount)
		}
		switch r.Kind {
		case carryRun:
			runs++
			c.AsOf, c.Escrow = r.AsOf, r.Amount
			if r.Amount < 0 {
				return nil, errors.New("trust input: a negative escrow")
			}
		case carryJudgement:
			if r.ID == "" || ids[r.ID] || r.Value < judge.Min || r.Value > judge.Max {
				return nil, fmt.Errorf("trust input: carry judgement %q is empty, repeated or has c0 outside 0-100", r.ID)
			}
			ids[r.ID] = true
			c.Judgements = append(c.Judgements, CarriedJudgement{ID: r.ID, Author: r.Account, At: r.CreatedAt, C0: r.Value, Raw: r.Amount})
		case carryBalance:
			if r.Account == "" || accounts[r.Account] {
				return nil, fmt.Errorf("trust input: carry balance %q is empty or repeated", r.Account)
			}
			accounts[r.Account] = true
			c.Balances = append(c.Balances, CarriedBalance{Account: r.Account, Amount: r.Amount})
		case carryOwed:
			if r.ID == "" || owed[r.ID] || r.Amount <= 0 {
				return nil, fmt.Errorf("trust input: carry owed %q is empty, repeated or not positive", r.ID)
			}
			owed[r.ID] = true
			c.Owed = append(c.Owed, CarriedOwed{ID: r.ID, Author: r.Account, Amount: r.Amount})
		default:
			return nil, fmt.Errorf("trust input: unknown carry kind %q", r.Kind)
		}
	}
	if runs != 1 {
		return nil, fmt.Errorf("trust input: the carry needs one run record, not %d", runs)
	}
	// Every run keeps Σ balances + escrow ≤ 0 (judge.Settle with the escrow
	// as a loss, decay rounding debts away from zero): a carry above it
	// would mint.
	total := c.Escrow
	for _, b := range c.Balances {
		total += b.Amount
	}
	if total > 0 {
		return nil, fmt.Errorf("trust input: the carry's balances and escrow sum to %d > 0: it would mint standing", total)
	}
	sort.Slice(c.Judgements, func(i, j int) bool { return c.Judgements[i].ID < c.Judgements[j].ID })
	sort.Slice(c.Balances, func(i, j int) bool { return c.Balances[i].Account < c.Balances[j].Account })
	sort.Slice(c.Owed, func(i, j int) bool { return c.Owed[i].ID < c.Owed[j].ID })
	return c, nil
}

// carryAccounts are the accounts a carry names: they are nodes of the run,
// whose balances count in standing and whose final marks settle.
func (c *JudgementCarry) accounts() []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, j := range c.Judgements {
		out = append(out, j.Author)
	}
	for _, b := range c.Balances {
		out = append(out, b.Account)
	}
	for _, o := range c.Owed {
		out = append(out, o.Author)
	}
	return out
}

// ruleInput is what computeStanding hands rule 4.
type ruleInput struct {
	st          *StandingParams
	p           Params
	set         judgementSet
	carry       *JudgementCarry
	asOf, D, ws int64
	nodes       []string
	index       map[string]int
	seed, pen   []int64
	penalty     map[string]int64
	indep       func(a, b string) bool
}

type jEdge struct {
	src    int
	dsts   []int
	w      int64 // ppm of the stake
	oppose bool
	j      judge.Judgement
}

// runJudgementRule computes rule 4's standing and section.
func runJudgementRule(in ruleInput) (stakeResult, *JudgementsShadow) {
	st, jp := in.st, *in.st.Judgements
	n := len(in.nodes)
	cv := curves{}
	out := &JudgementsShadow{Version: judge.ShadowVersion, Rule: "RFC 0020", Unit: "millicent", Params: shadowParams(jp, in.p.WindowDays),
		ByAct: map[string]int64{}, Bands: map[string]int64{"raised": 0, "lowered": 0, "same": 0},
		Scores: []SubjectScore{}, Marks: []JudgementMark{}, Accounts: []ShadowAccount{}, Final: []JudgementMark{}, Edges: &EdgeTotals{}}

	// The carry, decayed to this run.
	carry := in.carry
	if carry == nil {
		carry = &JudgementCarry{AsOf: in.asOf}
	} else {
		out.CarryIn = sha256Hex(Canonical(carry))
	}
	f := cv.decay(st.DayFactorPPM, max(0, dayOf(in.asOf)-dayOf(carry.AsOf)))
	decayed := func(x int64) int64 {
		if x >= 0 {
			return mulDiv(x, f, 1e6)
		}
		return -ceilMulDiv(-x, f, 1e6) // a debt decays rounded away from zero
	}
	balPrev := make([]int64, n)
	for _, b := range carry.Balances {
		if i, ok := in.index[b.Account]; ok {
			balPrev[i] = decayed(b.Amount)
		}
	}
	escrow := decayed(carry.Escrow)

	// Judgement edges: each root's current judgement of an owned subject.
	loads := in.set.loads(st, in.D)
	var edges []jEdge
	for _, s := range in.set.subjects {
		owners := in.set.sortedOwners(s)
		var dsts []int
		for _, o := range owners {
			if i, ok := in.index[o]; ok {
				dsts = append(dsts, i)
			}
		}
		if len(dsts) == 0 {
			continue
		}
		for _, e := range in.set.bySubject[s] {
			if e.withdraw || e.closeIdx >= 0 {
				continue
			}
			src, ok := in.index[e.j.Author]
			w, oppose := e.j.EdgeWeightPPM()
			if !ok || w <= 0 {
				continue
			}
			edges = append(edges, jEdge{src: src, dsts: dsts, w: w, oppose: oppose, j: e.j})
			if oppose {
				out.Edges.Oppose++
			} else {
				out.Edges.Endorse++
			}
		}
	}

	// What returns to the seeds goes to them by seed, less any penalty.
	eff := make([]int64, n)
	var S int64
	for i, s := range in.seed {
		eff[i] = mulDiv(s, 1e6-in.pen[i], 1e6)
		S += eff[i]
		out.SeedMass += s
	}
	fixedPoint := func(bal []int64) (c, raw []int64, moved, spent int64) {
		c = make([]int64, n)
		for i := range c {
			c[i] = max(0, in.seed[i]+bal[i])
		}
		raw = make([]int64, n)
		for it := int64(0); it < st.Iterations; it++ {
			next := make([]int64, n)
			for i := range next {
				next[i] = in.seed[i] + bal[i]
			}
			var back int64
			moved, spent = 0, 0
			for _, e := range edges {
				amt := mulDiv(loads.stake(e.j, max(0, c[e.src])), e.w, 1e6)
				if amt <= 0 {
					continue
				}
				next[e.src] -= amt
				share := amt / int64(len(e.dsts))
				if e.oppose {
					back += amt
					spent += amt
					for _, d := range e.dsts {
						next[d] -= share
						back += share
					}
					continue
				}
				moved += amt
				for _, d := range e.dsts {
					next[d] += share
				}
				back += amt - share*int64(len(e.dsts))
			}
			var deficit int64
			removed := make([]int64, n)
			for i := range next {
				removed[i] = mulDiv(max(0, next[i]), in.pen[i], 1e6)
				next[i] -= removed[i]
				back += removed[i]
				if next[i] < 0 {
					deficit -= next[i]
					next[i] = 0
				}
			}
			// A debt (a negative balance) its author's standing no longer
			// covers is not forgiven: it is taken from what returns to the
			// seeds and, past that, from the seeds by seed (rounded up), so
			// the total never exceeds the seed mass.
			back -= deficit
			for i := range next {
				if S > 0 && eff[i] > 0 {
					if back >= 0 {
						next[i] += mulDiv(back, eff[i], S)
					} else {
						next[i] = max(0, next[i]-ceilMulDiv(-back, eff[i], S))
					}
				}
				raw[i] = next[i] + removed[i]
			}
			c = next
		}
		return c, raw, moved, spent
	}

	// Phase A: standing before this run's settlement; it prices the stakes
	// and weighs the judgements.
	cA, _, _, _ := fixedPoint(balPrev)
	standingA := func(a string) int64 {
		if i, ok := in.index[a]; ok {
			return cA[i]
		}
		return 0
	}
	c0 := make(map[string]int64, len(carry.Judgements))
	for _, j := range carry.Judgements {
		c0[j.ID] = j.C0
	}
	mo := markJudgements(markInput{st: st, jp: jp, set: in.set, asOf: in.asOf, D: in.D, maxWork: in.p.MaxWork, standing: standingA,
		penalty: in.penalty, indep: in.indep, c0: c0})
	out.fillCounts(mo)
	gains, marked := map[string]int64{}, map[string]int64{}
	for _, m := range mo.marks {
		gains[m.Author] += m.GainPPM
		marked[m.Author]++
	}
	out.Marks = append(out.Marks, mo.marks...)
	out.Marked = int64(len(out.Marks))

	// Final: the judgements that left the window since the previous run,
	// then the gains still owed, settled against the escrow.
	type item struct {
		m    JudgementMark
		owed bool
	}
	var items []item
	for _, j := range carry.Judgements {
		if j.At < in.ws && j.Raw != 0 {
			items = append(items, item{m: JudgementMark{ID: j.ID, Author: j.Author, At: j.At, C0: j.C0, Raw: j.Raw}})
		}
	}
	for _, o := range carry.Owed {
		if a := decayed(o.Amount); a > 0 {
			items = append(items, item{m: JudgementMark{ID: o.ID, Author: o.Author, Raw: a}, owed: true})
		}
	}
	// A loss is capped at its author's standing in the run.
	loss := map[string]int64{}
	for _, it := range items {
		if it.m.Raw < 0 {
			loss[it.m.Author] += -it.m.Raw
		}
	}
	raw := make([]int64, 0, len(items)+1)
	for i := range items {
		r := items[i].m.Raw
		if l, have := loss[items[i].m.Author], standingA(items[i].m.Author); r < 0 && l > have {
			r = -mulDiv(-r, have, l)
			items[i].m.Raw = r
		}
		raw = append(raw, r)
	}
	out.Settlement = judge.Settle(append(raw, -escrow))
	settled := map[string]int64{}
	newOwed := []CarriedOwed{}
	for i := range items {
		m := &items[i].m
		m.Mark = out.Settlement.Marks[i]
		settled[m.Author] += m.Mark
		if m.Raw > 0 && m.Mark < m.Raw {
			newOwed = append(newOwed, CarriedOwed{ID: m.ID, Author: m.Author, Amount: m.Raw - m.Mark})
		}
		out.Final = append(out.Final, *m)
	}
	sort.Slice(out.Final, func(i, j int) bool { return out.Final[i].ID < out.Final[j].ID })
	sort.Slice(newOwed, func(i, j int) bool { return newOwed[i].ID < newOwed[j].ID })

	// Phase B: standing with the new balances.
	bal := make([]int64, n)
	balances := []CarriedBalance{}
	for i, a := range in.nodes {
		bal[i] = balPrev[i] + settled[a]
		if bal[i] != 0 {
			balances = append(balances, CarriedBalance{Account: a, Amount: bal[i]})
		}
	}
	c, rawC, moved, spent := fixedPoint(bal)
	out.Edges.EndorseMass, out.Edges.OpposeMass = moved, spent
	for i, a := range in.nodes {
		out.StandingMass += c[i]
		cents, before := c[i]/massPerCent, cA[i]/massPerCent
		band, bandBefore := st.Band(cents), st.Band(before)
		switch {
		case band < bandBefore:
			out.Bands["raised"]++
		case band > bandBefore:
			out.Bands["lowered"]++
		default:
			out.Bands["same"]++
		}
		if mo.byAuthor[a] == 0 && settled[a] == 0 && bal[i] == 0 {
			continue
		}
		out.Accounts = append(out.Accounts, ShadowAccount{Account: a, Judgements: mo.byAuthor[a], Marked: marked[a], GainPPM: gains[a], Mark: settled[a],
			Balance: bal[i], Cents: cents, CentsShadow: cents, Band: band, BandShadow: band})
	}
	out.StandingMassShadow = out.StandingMass

	entries := append([]CarriedJudgement{}, mo.entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	out.Carry = &JudgementCarry{AsOf: in.asOf, Escrow: out.Settlement.Returned, Judgements: entries, Balances: balances, Owed: newOwed}
	return stakeResult{c: c, raw: rawC, judged: bal}, out
}

// ceilMulDiv is ⌈a × b / c⌉ for non-negative a, b and positive c.
// A quotient past int64 saturates, as mulDiv's does.
func ceilMulDiv(a, b, c int64) int64 {
	if a <= 0 || b <= 0 || c <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	if hi >= uint64(c) {
		return 1<<63 - 1
	}
	q, r := bits.Div64(hi, lo, uint64(c))
	if r > 0 {
		q++
	}
	return int64(min(q, 1<<63-1))
}
