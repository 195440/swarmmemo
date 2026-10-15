package trust

import (
	"math"
	"math/bits"
	"strconv"
	"strings"

	"swarmmemo/internal/allowance"
)

// The readings of standing every consumer shares (RFC 0021 §12): the band,
// v(s) and the displayed score. Each is one function of the cents and the
// parameters, the one copy.

// Band is the band of a standing: signed below theta2 (= U), proven from
// theta2, trusted from node_cap. Anonymous subjects have no standing and are
// banded by the caller.
func (p Params) Band(cents int64) allowance.Band {
	switch {
	case cents >= p.NodeCapCents:
		return allowance.BandTrusted
	case cents >= p.Theta2Cents:
		return allowance.BandProven
	}
	return allowance.BandSigned
}

// BandFloorCents is the least standing of a band (anonymous and signed: 0).
func (p Params) BandFloorCents(b allowance.Band) int64 {
	switch b {
	case allowance.BandTrusted:
		return p.NodeCapCents
	case allowance.BandProven:
		return p.Theta2Cents
	}
	return 0
}

// VoteWeightPPM is v(s), the one weight function (RFC0015 §0.3), in ppm:
// 0 for C = 0 or C below the floor, else v0 + (1 − v0) × √min(1, C / C_ref).
// Votes, reviews, judgements and Find read it; nobody's weighs more than
// 1e6. The floor gates v0, so splitting standing into many 1-cent keys does
// not multiply vote weight.
func VoteWeightPPM(cents, v0PPM, cRefCents, floorCents int64) int64 {
	if cents <= 0 || cRefCents <= 0 || cents < floorCents {
		return 0
	}
	frac := int64(1e6)
	if cents < cRefCents {
		frac = mulDiv(cents, 1e6, cRefCents)
	}
	root := isqrt(frac * 1e6) // √frac in ppm
	return v0PPM + mulDiv(1e6-v0PPM, root, 1e6)
}

// VoteWeight is v(s) under these parameters: what a vote, a review and a
// judgement by an account of this standing weigh.
func (p Params) VoteWeight(cents int64) int64 {
	return VoteWeightPPM(cents, p.V0PPM, p.CRefCents, p.VFloorCents)
}

// FlooredVoteWeight is v(s) with today's rule as its floor (RFC 0021 §14:
// every gate reads max(today's rule, standing), so nobody loses vote weight
// at the cutover): a whole vote (1e6) for an account seasoned when it acted
// (a visible public post at least a day old, the board's VoterMinAge), else
// v(s). v(s) never exceeds 1e6, so the floor is the whole of it.
func (p Params) FlooredVoteWeight(seasoned bool, cents int64) int64 {
	if seasoned {
		return 1e6
	}
	return p.VoteWeight(cents)
}

// PublicIndependent is the independence test (RFC 0021 §7) over its public
// facts: two accounts are independent when they differ, their roots differ
// (keys, verified domains and shared wallet or payout addresses merge
// roots), and neither funded the other within funded_days (funded holds
// both orders).
func PublicIndependent(a, b string, rootOf func(string) string, funded map[[2]string]bool) bool {
	return a != b && rootOf(a) != rootOf(b) && !funded[[2]string{a, b}]
}

// PassedSharePPM is the share (ppm) of a run's flow passed on that the
// roots of accounts pass, each root once: the operator dashboard reads it
// with the funnel's exclude list (the operator's own accounts), the RFC
// 0021 §4 measure of how much trust still comes from the operator.
func PassedSharePPM(scores []ScoreOut, accounts map[string]bool) int64 {
	passed, mine := map[string]int64{}, map[string]bool{}
	for _, s := range scores {
		passed[s.Root] = s.Parts.PassedCents
		if accounts[s.Account] {
			mine[s.Root] = true
		}
	}
	var total, part int64
	for root, x := range passed {
		total += x
		if mine[root] {
			part += x
		}
	}
	if total == 0 {
		return 0
	}
	return mulDiv(part, 1e6, total)
}

// isqrt is ⌊√n⌋ for n ≥ 0.
func isqrt(n int64) int64 {
	if n <= 0 {
		return 0
	}
	x := int64(1) << ((bits.Len64(uint64(n)) + 1) / 2)
	for {
		y := (x + n/x) / 2
		if y >= x {
			return x
		}
		x = y
	}
}

// StandingScore is log₁₀(1 + C) rounded to two decimals, the displayed
// standing (RFC0015 §3.1); C in cents.
func StandingScore(cents int64) float64 {
	if cents <= 0 {
		return 0
	}
	return math.Round(math.Log10(float64(cents)+1)*100) / 100
}

// FakeCostText is the dollar figure shown beside the score: "about $2.50
// to fake".
func FakeCostText(cents int64) string { return "about " + DollarText(cents) + " to fake" }

// CentsText is an amount of US cents as every surface writes it: "1,250¢".
func CentsText(cents int64) string {
	if cents < 0 {
		return "-" + groupThousands(-cents) + "¢"
	}
	return groupThousands(cents) + "¢"
}

// DollarText is cents as dollars: "$1,234.50".
func DollarText(cents int64) string {
	d := cents / 100
	c := cents % 100
	s := strconv.FormatInt(c, 10)
	if c < 10 {
		s = "0" + s
	}
	return "$" + groupThousands(d) + "." + s
}

func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}
