package board

import (
	"bytes"
	"testing"
	"time"

	"swarmmemo/internal/trust"
)

// C320d: links are rechecked daily, so a run late in the day finds most
// verified wallets checked after its as-of midnight. Such a link held at
// as-of too and counts (checked at as_of − 1); before the fix it read
// checked_at 0, priced 0, and on the production copy every standing was 0.
// A link that lapsed on its recheck, or a claimed domain, still counts 0.
func TestTrustInputsRecheckedLinksCount(t *testing.T) {
	s := openTest(t, Config{Features: Features{Trust: TrustOn}})
	created := testTime + 3600
	s.now = func() time.Time { return time.Unix(created, 0) }
	type link struct {
		name, kind, value, state string
		checked                  int64 // seconds after the as-of midnight (negative: before)
		cents                    int64 // standing the run must give
	}
	asOf := testTime + 2*86400
	links := []link{
		{"rechecked", "wallet", "0x1111111111111111111111111111111111111111", "verified", 3600, trust.WalletFallbackCents},
		{"checked-before", "wallet", "0x2222222222222222222222222222222222222222", "verified", -3600, trust.WalletFallbackCents},
		{"lapsed-on-recheck", "wallet", "0x3333333333333333333333333333333333333333", "lapsed", 3600, 0},
		{"claimed-domain", "domain", "example.com", "claimed", 0, 0},
	}
	keys := map[string]string{}
	for i, l := range links {
		key := keyFor(byte(90 + i))
		postAs(t, s, key, Command{Room: "lobby", Text: "hello from " + l.name, RequestID: "rc-" + l.name})
		var agent string
		if err := s.db.QueryRow("SELECT id FROM identities WHERE account=?", keyID(key)).Scan(&agent); err != nil {
			t.Fatal(err)
		}
		checked := int64(0)
		if l.state != "claimed" {
			checked = asOf + l.checked
		}
		if _, err := s.db.Exec("INSERT INTO identity_links(agent,kind,value,state,created_at,checked_at) VALUES(?,?,?,?,?,?)", agent, l.kind, l.value, l.state, created, checked); err != nil {
			t.Fatal(err)
		}
		keys[l.name] = keyID(key)
	}
	s.now = func() time.Time { return time.Unix(asOf+23*3600, 0) } // the run, late in the day
	var buf bytes.Buffer
	if err := s.WriteTrustInputs(testContext, &buf); err != nil {
		t.Fatal(err)
	}
	snap, err := trust.ReadJSONL(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.AsOf != asOf {
		t.Fatalf("as_of %d, want %d", snap.Meta.AsOf, asOf)
	}
	checkedOf := map[string]int64{}
	for _, p := range snap.Proofs {
		checkedOf[p.Account] = p.CheckedAt
	}
	if got := checkedOf[keys["rechecked"]]; got != asOf-1 {
		t.Errorf("a verified link rechecked after as_of reads checked_at %d, want %d", got, asOf-1)
	}
	if got := checkedOf[keys["checked-before"]]; got != asOf-3600 {
		t.Errorf("a link checked before as_of reads checked_at %d, want %d", got, asOf-3600)
	}
	if got := checkedOf[keys["lapsed-on-recheck"]]; got != 0 {
		t.Errorf("a lapsed link checked after as_of reads checked_at %d, want 0", got)
	}
	out, err := trust.Compute(testContext, snap)
	if err != nil {
		t.Fatal(err)
	}
	cents := map[string]int64{}
	for _, sc := range out.Scores {
		cents[sc.Account] = sc.Cents
	}
	for _, l := range links {
		if got := cents[keys[l.name]]; got != l.cents {
			t.Errorf("%s: standing %d¢, want %d¢", l.name, got, l.cents)
		}
	}
}
