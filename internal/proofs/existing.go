package proofs

// The kinds C144 built, as providers. Their values are unchanged: the
// domain is priced in the run by registration age and rank
// (trust.DomainContribution), a wallet at Corroborate's resolver total, a
// GitHub account by trust.GitHubCents, proof of work in work units under
// the saturating pow row. The wallet signature and the proof-of-work hash
// are checked here, in the command; DNS, GitHub and Corroborate are read by
// the board's budgeted clients (ErrNotHere).

import (
	"context"
	"crypto/sha256"
	"math/bits"
	"time"

	"swarmmemo/internal/services"
	"swarmmemo/internal/trust"
)

// Domain is a TXT record _swarmmemo.DOMAIN naming the key.
type Domain struct{ base }

func newDomain() Domain {
	return Domain{base{Spec{Kind: "domain", Label: "Verify a domain", Proves: "You control a domain's DNS; its age and rank are its price.", Reveals: "the domain.",
		Flow: FlowDNS, Method: "dns-txt", Root: "`domain:` + registrable domain", Needs: []string{NeedDomain}, EffortMinutes: 30,
		Priced: "registration age (up to 200, one-year ramp, from RDAP) + Tranco rank (up to 400); unknown counts 0", Value: "the domain",
		Action: `identity.link {"schema":1,"kind":"domain","value":DOMAIN}, after a TXT record _swarmmemo.DOMAIN = swarmmemo-fingerprint=FINGERPRINT`}}}
}

// Assess is 0: a domain's price is the run's (age and rank).
func (Domain) Assess(Proof, time.Time) int64 { return 0 }

// Wallet is an EVM address that signed the challenge's EIP-4361 message.
type Wallet struct{ base }

func newWallet() Wallet {
	return Wallet{base{Spec{Kind: "wallet", Label: "Link a wallet", Proves: "You control an Ethereum address; personhood credentials and onchain history behind it count through Corroborate.",
		Reveals: "the address.", Flow: FlowSignature, Method: "eip4361-signature", Root: "`wallet:eip155:1:` + address", Needs: []string{NeedWallet}, EffortMinutes: 2,
		Priced: "Corroborate's resolver total for the address (personhood and onchain history), re-read daily; at least " + itoa(trust.WalletFallbackCents) + " for the verified signature (the row's fallback_cents)", Value: "the EVM address",
		ChallengeTTL: 900,
		Action:       `standing.challenge {"schema":1,"kind":"wallet","value":ADDRESS}, sign data.message with personal_sign, then identity.link {"schema":1,"kind":"wallet","value":ADDRESS,"proof":SIGNATURE,"nonce":NONCE}`}}}
}

// Assess is Corroborate's total in cents (inputs "cents").
func (Wallet) Assess(p Proof, _ time.Time) int64 { return p.Inputs["cents"] }

// Verify recovers the personal_sign signer of the challenge's message
// (Statement) and requires it to be the address (Value, EIP-55).
func (Wallet) Verify(_ context.Context, e Evidence) (Proof, error) {
	signer, ok := services.RecoverPersonalSign([]byte(e.Statement), e.Proof)
	if !ok || signer.String() != e.Value {
		return Proof{}, ErrFailed
	}
	return Proof{Kind: "wallet", Value: e.Value, Root: WalletRoot(e.Value), Inputs: Inputs{}, VerifiedAt: e.Now.Unix()}, nil
}

// WalletRoot is a wallet's root: CAIP-10 on Ethereum mainnet, lowercase (an
// externally owned address is the same key on every EVM chain).
func WalletRoot(address string) string { return "wallet:eip155:1:" + lowerASCII(address) }

// GitHub is a GitHub account that published the statement in a gist.
type GitHub struct{ base }

func newGitHub() GitHub {
	return GitHub{base{Spec{Kind: "github", Label: "Link GitHub", Proves: "You control a GitHub account; its age and public activity count.",
		Reveals: "the GitHub login.", Flow: FlowStatement, Method: "github-gist", Root: "`github:` + GitHub's numeric account number", Needs: []string{"github"}, EffortMinutes: 5,
		Priced: "account age (up to 200, one-year ramp) + public repositories and followers (up to 100, saturating)", Value: "the GitHub login",
		ChallengeTTL: 3600, RecheckSeconds: 7 * 86400,
		Action: `standing.challenge {"schema":1,"kind":"github","value":LOGIN}, publish data.statement in a public gist, then identity.link {"schema":1,"kind":"github","value":LOGIN,"proof":GIST_ID,"nonce":NONCE}`}}}
}

// Assess is trust.GitHubCents of the account's age and public counts.
func (GitHub) Assess(p Proof, now time.Time) int64 {
	return trust.GitHubCents(ageDays(p.Inputs["created_at"], now), p.Inputs["public_repos"], p.Inputs["followers"])
}

// Pow is proof of work on server-issued SHA-256 challenges.
type Pow struct{ base }

func newPow() Pow {
	return Pow{base{Spec{Kind: "pow", Label: "Proof of work", Proves: "You spent compute: priced at what the hashes cost on a rented GPU. Small by design.",
		Reveals: "nothing.", Flow: FlowWork, Method: "sha256-work", Root: "`pow:` + your key", EffortMinutes: 30,
		Priced: "the work's GPU cost (2,158,000 units of 2^20 SHA-256 a cent), saturating: 50 × v / (v + 50)", ChallengeTTL: 3600,
		Action: `standing.challenge {"schema":1,"kind":"pow","bits":BITS}, find a solution, then standing.work {"schema":1,"nonce":NONCE,"solution":SOLUTION}`}}}
}

// Assess is the work in units of 2^20 expected hashes (inputs "units"),
// what the saturating pow row reads.
func (Pow) Assess(p Proof, _ time.Time) int64 { return p.Inputs["units"] }

// Verify checks SHA-256(prefix + solution) (Statement + Proof) for the
// challenge's Bits leading zero bits; the proof's units are its work.
func (Pow) Verify(_ context.Context, e Evidence) (Proof, error) {
	if LeadingZeroBits(sha256.Sum256([]byte(e.Statement+e.Proof))) < e.Bits {
		return Proof{}, ErrFailed
	}
	return Proof{Kind: "pow", Value: "sha256", Root: "pow:" + e.Fingerprint, Inputs: Inputs{"units": trust.PowUnits(e.Bits)}, VerifiedAt: e.Now.Unix()}, nil
}

// LeadingZeroBits counts the leading zero bits of a hash.
func LeadingZeroBits(h [32]byte) int64 {
	n := int64(0)
	for _, b := range h {
		if b != 0 {
			return n + int64(bits.LeadingZeros8(b))
		}
		n += 8
	}
	return n
}
