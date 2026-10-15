package trust

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
)

// testAsOf is 2026-09-28 00:00 UTC.
const testAsOf = 1790553600

func acct(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// builder assembles snapshots for tests (parameter version 8).
type builder struct {
	snap Snapshot
	seq  int64
	post int
}

func newBuilder() *builder {
	return &builder{snap: Snapshot{Params: DefaultParams(), Meta: Meta{Schema: SnapshotSchema, AsOf: testAsOf}}}
}

func ago(days int64) int64 { return testAsOf - days*day }

// account registers name, first seen daysAgo.
func (b *builder) account(name string, daysAgo int64) *builder {
	b.snap.Accounts = append(b.snap.Accounts, Record{Type: "account", Account: acct(name), FirstSeen: ago(daysAgo)})
	return b
}

// postBy adds a post by name daysAgo and returns its id.
func (b *builder) postBy(name string, daysAgo int64) string {
	b.post++
	id := "p" + strconv.Itoa(b.post)
	b.snap.Posts = append(b.snap.Posts, Record{Type: "post", ID: id, Account: acct(name), CreatedAt: ago(daysAgo) + 3600})
	return id
}

// reply is from's reply to a post by to, daysAgo.
func (b *builder) reply(from, to string, daysAgo int64) *builder {
	parent := b.postBy(to, daysAgo+1)
	b.post++
	b.snap.Posts = append(b.snap.Posts, Record{Type: "post", ID: "p" + strconv.Itoa(b.post), Account: acct(from), CreatedAt: ago(daysAgo) + 7200,
		ReplyTo: parent, ReplyToAccount: acct(to)})
	return b
}

func (b *builder) endorse(kind, from, to string, value, daysAgo int64) *builder {
	b.seq++
	r := Record{Type: "endorsement", Seq: b.seq, Kind: kind, Voter: acct(from), Target: acct(to), Value: value, CreatedAt: ago(daysAgo) + 60}
	if kind == "vote" {
		r.MessageID = b.postBy(to, daysAgo+1)
	}
	b.snap.Endorsements = append(b.snap.Endorsements, r)
	return b
}

func (b *builder) vote(from, to string, daysAgo int64) *builder {
	return b.endorse("vote", from, to, 1, daysAgo)
}
func (b *builder) vouch(from, to string, daysAgo int64) *builder {
	return b.endorse("vouch", from, to, 1, daysAgo)
}

// accept is a work.accept by from of to's work, daysAgo.
func (b *builder) accept(from, to string, daysAgo int64) *builder {
	b.seq++
	b.snap.Acts = append(b.snap.Acts, Record{Type: "edge", Kind: "work_accept", ID: "w" + strconv.FormatInt(b.seq, 10), From: acct(from), To: acct(to), CreatedAt: ago(daysAgo) + 120})
	return b
}

// review is from's review of agent to at x with evidence class, daysAgo.
func (b *builder) review(from, to string, x int64, class string, daysAgo int64) *builder {
	b.seq++
	b.snap.Reviews = append(b.snap.Reviews, Record{Type: "review", ID: "r" + strconv.FormatInt(b.seq, 10), Account: acct(from), Target: "agent:" + acct(to),
		Value: x, Kind: class, State: "good", CreatedAt: ago(daysAgo) + 180})
	return b
}

// wallet links a verified wallet to name, assessed at cents.
func (b *builder) wallet(name, address string, cents int64) *builder {
	b.snap.Proofs = append(b.snap.Proofs, Record{Type: "proof", Account: acct(name), Kind: "wallet", LinkValue: address, State: "verified",
		CreatedAt: ago(100), CheckedAt: ago(1), Root: "wallet:eip155:1:" + address, Assessed: cents})
	return b
}

// github links a verified GitHub account to name, assessed at cents.
func (b *builder) github(name, id string, cents int64) *builder {
	b.snap.Proofs = append(b.snap.Proofs, Record{Type: "proof", Account: acct(name), Kind: "github", LinkValue: id, State: "verified",
		CreatedAt: ago(100), CheckedAt: ago(1), Root: "github:" + id, Assessed: cents})
	return b
}

// domain links a verified domain, registered regDaysAgo (0: unknown) and
// ranked rank (0: unlisted).
func (b *builder) domain(name, domain string, regDaysAgo, rank int64) *builder {
	r := Record{Type: "proof", Account: acct(name), Kind: "domain", LinkValue: domain, State: "verified", CreatedAt: ago(200), CheckedAt: ago(1), Rank: rank}
	if regDaysAgo > 0 {
		r.RegisteredAt = ago(regDaysAgo)
	}
	b.snap.Proofs = append(b.snap.Proofs, r)
	return b
}

// link attaches name's key to other's account (one root).
func (b *builder) link(name, other string) *builder {
	b.snap.Proofs = append(b.snap.Proofs, Record{Type: "proof", Account: acct(name), Kind: "ed25519", LinkValue: "k" + other, State: "proof_attached",
		CreatedAt: ago(10), LinkAccount: acct(other)})
	return b
}

// pay is name paying SwarmMemo cents, daysAgo.
func (b *builder) pay(name string, cents, daysAgo int64) *builder {
	b.seq++
	b.snap.Payments = append(b.snap.Payments, Record{Type: "payment", ID: "pay" + strconv.FormatInt(b.seq, 10), Kind: "topup", Account: acct(name), Amount: cents, CreatedAt: ago(daysAgo) + 300})
	return b
}

// payTo is from paying to cents, daysAgo, to payout address (or none).
func (b *builder) payTo(from, to string, cents, daysAgo int64, payout string) *builder {
	b.seq++
	r := Record{Type: "payment", ID: "pay" + strconv.FormatInt(b.seq, 10), Kind: "usdc", Account: acct(from), To: acct(to), Amount: cents, CreatedAt: ago(daysAgo) + 300}
	if payout != "" {
		r.LinkValue = "wallet:eip155:1:" + payout
	}
	b.snap.Payments = append(b.snap.Payments, r)
	return b
}

func (b *builder) transfer(from, to string, amount, daysAgo int64) *builder {
	b.snap.Transfers = append(b.snap.Transfers, Record{Type: "transfer", From: acct(from), To: acct(to), Amount: amount, CreatedAt: ago(daysAgo) + 400})
	return b
}

// carryWorld adds a carry world record for name.
func (b *builder) carryWorld(name string, c CarryAccount) *builder {
	if len(b.snap.Carry) == 0 {
		b.snap.Carry = append(b.snap.Carry, Record{Type: "carry", Kind: "run", AsOf: testAsOf - day})
	}
	c.Account = acct(name)
	if c.FadePPM == 0 {
		c.FadePPM = 1e6
	}
	b.snap.Carry = append(b.snap.Carry, Carry{Accounts: []CarryAccount{c}}.Records()[1:]...)
	return b
}

func compute(t testing.TB, b *builder) Output {
	t.Helper()
	out, err := Compute(context.Background(), b.snap)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func scoreOf(out Output, name string) ScoreOut {
	a := acct(name)
	for _, s := range out.Scores {
		if s.Account == a {
			return s
		}
	}
	return ScoreOut{Account: a}
}

func centsOf(out Output, names ...string) int64 {
	var n int64
	for _, name := range names {
		n += scoreOf(out, name).Cents
	}
	return n
}
