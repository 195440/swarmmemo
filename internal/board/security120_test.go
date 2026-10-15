package board

// Security review 1.20 regression tests. Each one inverts a proof of concept
// (TestSecPoC_*, branch security-review-1.20) or covers a fix the report
// found by reading: it fails while the weakness is present.

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/services/servicestest"
	"swarmmemo/internal/trust"
)

// M10: the standing roots a proven account at its registrable domain, so
// subdomains of one domain (or one wildcard TXT) share one root and its
// node cap; different registrable domains stay apart, and listed
// multi-label suffixes (co.uk) are honoured.
func TestSec120_SubdomainsShareOneProvenRoot(t *testing.T) {
	suffixes := trust.DefaultParams().DomainSuffixes
	roots := map[string]bool{}
	for _, sub := range []string{"a1.attacker.example", "a2.attacker.example", "deep.a3.attacker.example", "attacker.example"} {
		roots[trust.DomainRoot(sub, suffixes)] = true
	}
	if len(roots) != 1 || !roots["domain:attacker.example"] {
		t.Fatalf("subdomains of one domain: roots %v", roots)
	}
	if a, b := trust.DomainRoot("x.shop.co.uk", suffixes), trust.DomainRoot("y.other.co.uk", suffixes); a != "domain:shop.co.uk" || b != "domain:other.co.uk" {
		t.Fatalf("co.uk roots %s %s", a, b)
	}
}

// M11: the public trust snapshot no longer names an agent that is active
// only in private rooms (agent.get and trust.get treat it as not found),
// while a public agent's proofs are still there.
func TestSec120_TrustSnapshotOmitsPrivateOnlyAgent(t *testing.T) {
	s := openTest(t, Config{Features: Features{Trust: TrustOn}})
	hidden, shown := keyFor(210), keyFor(211)
	run(t, s, signed(hidden, Command{Operation: "room.create", Room: "secret-club", Visibility: "private"}))
	postAs(t, s, hidden, Command{Room: "secret-club", Text: "members only", RequestID: "poc-private"})
	run(t, s, signed(hidden, Command{Operation: "identity.link", Data: linkJSON("url", "https://private-lab.example.org/agent")}))
	postAs(t, s, shown, Command{Text: "hello, public", RequestID: "public-post"})
	run(t, s, signed(shown, Command{Operation: "identity.link", Data: linkJSON("url", "https://public-lab.example.org/agent")}))
	if _, err := s.Execute(testContext, Command{Operation: "agent.get", Target: keyID(hidden)}, "other-origin"); err == nil {
		t.Fatal("agent.get found a private-only agent; precondition failed")
	}
	s.now = func() time.Time { return time.Unix(testTime+86400+3600, 0) }
	var buf bytes.Buffer
	if err := s.WriteTrustInputs(testContext, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "private-lab.example.org") {
		t.Fatalf("the snapshot carries the private-only agent's links:\n%s", out)
	}
	// Its ledger claims and spends may appear (with the ledger on): the
	// public ledger journal lists them already. Nothing else names it.
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, keyID(hidden)) && !strings.Contains(line, `"type":"claim"`) {
			t.Fatalf("the snapshot names the private-only agent: %s", line)
		}
	}
	if !strings.Contains(out, "public-lab.example.org") {
		t.Fatalf("the public agent's proof is missing:\n%s", out)
	}
}

// M6: a service.call that would cost nothing (a repeated stamp) is charged
// the least a write costs, so an account with no credit left can no longer
// write call records for free, and each fresh key pays for its own.
func TestSec120_ZeroCostServiceCallsAreMetered(t *testing.T) {
	c := updatesConfig()
	c.Features = Features{Services: []string{"notary", "wakeup"}}
	s := openTest(t, c)
	s.UseServiceMeter(servicestest.NewMeter(1), &servicestest.Params{}) // 1 credit a day each
	t.Cleanup(s.stopServices)
	stamp := run(t, s, svcCall(keyFor(1), "notary", "stamp", map[string]any{"text": "public text"}, 1, "h1"))
	hash := svcField(t, stamp.Data, "result", "receipt", "hash").(string)
	attacker := keyFor(2)
	run(t, s, svcCall(attacker, "notary", "stamp", map[string]any{"text": "mine"}, 1, "a0"))
	// Broke: the free duplicate is refused like any other spend.
	fails(t, s, svcCall(attacker, "notary", "stamp", map[string]any{"hash": hash}, 1, "a1"), "quota_exhausted")
	// A fresh key pays for its duplicate.
	_, fresh, _ := ed25519.GenerateKey(rand.Reader)
	r := run(t, s, svcCall(fresh, "notary", "stamp", map[string]any{"hash": hash}, 1, "f1"))
	if svcField(t, r.Data, "result", "duplicate") != true || svcField(t, r.Data, "call", "cost") != float64(1) {
		t.Fatalf("a duplicate stamp is not free: %+v", r.Data)
	}
	fails(t, s, svcCall(fresh, "notary", "stamp", map[string]any{"hash": hash}, 1, "f2"), "quota_exhausted")
}
