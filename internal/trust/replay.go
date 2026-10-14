package trust

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"swarmmemo/internal/judge"
)

// ReplayReport compares rule 3 with RFC 0020 on one snapshot (RFC 0020 §6
// step 2): the report that goes to the owner before the cutover. It is
// computed by Compute alone, so it reads nothing but the snapshot.
type ReplayReport struct {
	AsOf          int64            `json:"as_of"`
	ParamsVersion int64            `json:"params_version"`
	WindowDays    int64            `json:"window_days"`
	OutputSHA256  string           `json:"output_sha256"`
	ShadowSHA256  string           `json:"judgements_shadow_sha256"`
	ShadowVersion int64            `json:"judgements_shadow_version"`
	Judgements    int64            `json:"judgements"`
	ByAct         map[string]int64 `json:"by_act"`
	Subjects      int64            `json:"subjects"`
	Marked        int64            `json:"marked"`
	Open          int64            `json:"open"`
	Truncated     bool             `json:"truncated"`
	// Conservation: the settlement's totals (millicents) and the run's
	// standing under each rule. Minted is true only if RFC 0020 paid out
	// more than it took or raised the total standing.
	Conservation ReplayConservation `json:"conservation"`
	// Bands counts accounts by band move; TierMoves lists every move with
	// its cause (rule 3's settled part replaced by the marks).
	Bands     map[string]int64 `json:"bands"`
	TierMoves []ShadowAccount  `json:"tier_moves"`
	// Top and Bottom are the ten accounts with the largest and smallest
	// total mark.
	Top    []ShadowAccount `json:"top"`
	Bottom []ShadowAccount `json:"bottom"`
	// MostJudged are the scores of the twenty most judged subjects.
	MostJudged []SubjectScore `json:"most_judged"`
	// Detected are the marks of this run's ring and funnel members.
	Detected []ReplayDetected `json:"detected"`
}

// ReplayConservation is the replay's conservation check.
type ReplayConservation struct {
	judge.Settlement
	StandingMass       int64 `json:"standing_mass"`
	StandingMassShadow int64 `json:"standing_mass_shadow"`
	Minted             bool  `json:"minted"`
}

// ReplayDetected is one detector finding's member and its marks.
type ReplayDetected struct {
	Evidence string `json:"evidence"`
	Kind     string `json:"kind"`
	ShadowAccount
}

// Replay computes the run on snap (its window set to days when days > 0)
// and reports rule 3 against RFC 0020. It needs a rule 3 or rule 4
// parameter version.
func Replay(ctx context.Context, snap Snapshot, days int64) (ReplayReport, error) {
	if days > 0 {
		snap.Params.WindowDays = days
	}
	if snap.Params.Standing == nil || snap.Params.Standing.Rule < 3 {
		return ReplayReport{}, fmt.Errorf("trust replay: parameter version %d has no rule 3 (standing.rule 3, version %d and later)", snap.Params.Version, StandingStakesVersion)
	}
	out, err := Compute(ctx, snap)
	if err != nil {
		return ReplayReport{}, err
	}
	// Under rule 4 (version 7) the section is the rule: there is no rule 3
	// to compare with, and the report shows the run itself (its final
	// marks as the marks, its standing on both sides).
	s := out.JudgementsShadow
	minted := func() bool {
		return s.Settlement.Paid > s.Settlement.Forfeited || s.StandingMassShadow > s.StandingMass
	}
	if s == nil {
		s = out.Judgements
		minted = func() bool { return s.Settlement.Paid > s.Settlement.Forfeited || s.StandingMass > s.SeedMass }
	}
	r := ReplayReport{AsOf: out.AsOf, ParamsVersion: out.ParamsVersion, WindowDays: snap.Params.WindowDays, OutputSHA256: sha256Hex(out.Canonical()),
		ShadowSHA256: sha256Hex(s.Canonical()), ShadowVersion: s.Version, Judgements: s.Judgements, ByAct: s.ByAct, Subjects: s.Subjects,
		Marked: s.Marked, Open: s.Open, Truncated: s.Truncated, Bands: s.Bands,
		Conservation: ReplayConservation{Settlement: s.Settlement, StandingMass: s.StandingMass, StandingMassShadow: s.StandingMassShadow,
			Minted: minted()},
		TierMoves: []ShadowAccount{}, Top: []ShadowAccount{}, Bottom: []ShadowAccount{}, MostJudged: []SubjectScore{}, Detected: []ReplayDetected{}}
	byAccount := map[string]ShadowAccount{}
	for _, a := range s.Accounts {
		byAccount[a.Account] = a
		if a.Band != a.BandShadow {
			r.TierMoves = append(r.TierMoves, a)
		}
	}
	ranked := append([]ShadowAccount(nil), s.Accounts...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Mark != ranked[j].Mark {
			return ranked[i].Mark > ranked[j].Mark
		}
		return ranked[i].Account < ranked[j].Account
	})
	for _, a := range ranked {
		if len(r.Top) == 10 || a.Mark <= 0 {
			break
		}
		r.Top = append(r.Top, a)
	}
	for i := len(ranked) - 1; i >= 0 && len(r.Bottom) < 10 && ranked[i].Mark < 0; i-- {
		r.Bottom = append(r.Bottom, ranked[i])
	}
	scores := append([]SubjectScore(nil), s.Scores...)
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].Judgements != scores[j].Judgements {
			return scores[i].Judgements > scores[j].Judgements
		}
		return scores[i].Subject < scores[j].Subject
	})
	r.MostJudged = append(r.MostJudged, scores[:min(20, len(scores))]...)
	for _, ev := range out.Evidence {
		for _, m := range ev.Members {
			a, ok := byAccount[m]
			if !ok {
				a = ShadowAccount{Account: m}
			}
			r.Detected = append(r.Detected, ReplayDetected{Evidence: ev.ID, Kind: ev.Kind, ShadowAccount: a})
		}
	}
	return r, nil
}

// Markdown is the report as a page for the owner.
func (r ReplayReport) Markdown() string {
	var b strings.Builder
	short := func(a string) string {
		if len(a) > 22 {
			return a[:22] + "…"
		}
		return a
	}
	cents := func(mc int64) string { return fmt.Sprintf("%.3f", float64(mc)/massPerCent) }
	gain := func(ppm int64) string { return fmt.Sprintf("%+.2f", float64(ppm)/1e6) }
	fmt.Fprintf(&b, "# Trust replay: rule 3 against RFC 0020\n\nAs of %d, parameter version %d, window %d days. Output sha256 `%s`; judgements_shadow v%d sha256 `%s`.\n\n",
		r.AsOf, r.ParamsVersion, r.WindowDays, r.OutputSHA256, r.ShadowVersion, r.ShadowSHA256)
	acts := make([]string, 0, len(r.ByAct))
	for k, v := range r.ByAct {
		acts = append(acts, fmt.Sprintf("%s %d", k, v))
	}
	sort.Strings(acts)
	fmt.Fprintf(&b, "%d judgements (%s) on %d subjects; %d marked, %d open (fewer than %d independent later judgements with weight).", r.Judgements, strings.Join(acts, ", "), r.Subjects, r.Marked, r.Open, judge.KMin)
	if r.Truncated {
		b.WriteString(" **Truncated at max_work.**")
	}
	c := r.Conservation
	fmt.Fprintf(&b, "\n\n## Conservation\n\n| | cents |\n|---|---|\n| forfeited | %s |\n| owed to gains | %s |\n| paid (scale %.4f) | %s |\n| returned to seeds | %s |\n| standing, rule 3 | %s |\n| standing, RFC 0020 | %s |\n\nMinted: **%v**.\n",
		cents(c.Forfeited), cents(c.Owed), float64(c.ScalePPM)/1e6, cents(c.Paid), cents(c.Returned), cents(c.StandingMass), cents(c.StandingMassShadow), c.Minted)
	row := func(a ShadowAccount) string {
		return fmt.Sprintf("| `%s` | %d | %d | %s | %s | %s | %d | %d | %d | %d |\n", short(a.Account), a.Judgements, a.Marked, gain(a.GainPPM), cents(a.Mark), cents(a.Rule3Judged), a.Cents, a.CentsShadow, a.Band, a.BandShadow)
	}
	head := "| account | judgements | marked | Σ gain | mark (¢) | rule 3 settled (¢) | cents | cents RFC 0020 | band | band RFC 0020 |\n|---|---|---|---|---|---|---|---|---|---|\n"
	fmt.Fprintf(&b, "\n## Tier moves\n\nRaised %d, lowered %d, same %d. Each move's cause is its row: rule 3's settled part replaced by the marks (first order).\n\n", r.Bands["raised"], r.Bands["lowered"], r.Bands["same"])
	table := func(list []ShadowAccount) {
		if len(list) == 0 {
			b.WriteString("None.\n")
			return
		}
		b.WriteString(head)
		for _, a := range list {
			b.WriteString(row(a))
		}
	}
	table(r.TierMoves)
	b.WriteString("\n## Top 10 by mark\n\n")
	table(r.Top)
	b.WriteString("\n## Bottom 10 by mark\n\n")
	table(r.Bottom)
	b.WriteString("\n## The 20 most judged subjects\n\n")
	if len(r.MostJudged) == 0 {
		b.WriteString("None.\n")
	} else {
		b.WriteString("| subject | score | judgements | roots | with weight | penalised |\n|---|---|---|---|---|---|\n")
		for _, s := range r.MostJudged {
			fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %d | %v |\n", short(s.Subject), s.Score, s.Judgements, s.Roots, s.Weighted, s.Penalised)
		}
	}
	b.WriteString("\n## Ring and funnel members\n\n")
	if len(r.Detected) == 0 {
		b.WriteString("None in this run.\n")
	} else {
		b.WriteString("| evidence | kind " + strings.Replace(head, "\n|", "\n|---|---|", 1))
		for _, d := range r.Detected {
			fmt.Fprintf(&b, "| `%s` | %s %s", short(d.Evidence), d.Kind, row(d.ShadowAccount))
		}
	}
	return b.String()
}
