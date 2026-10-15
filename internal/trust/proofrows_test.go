package trust

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// C326: version 9 adds price rows and nothing else. The golden fixture's
// inputs (version 8: domain, wallet, github and pow proofs, money, time,
// payments, penalties) computed under version 9 give every account the same
// standing, world items and parts, byte for byte.
func TestProofRowsKeepExistingStanding(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "golden_unified_inputs.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	v8, err := ReadJSONL(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if v8.Params.Version != UnifiedVersion {
		t.Fatalf("the golden fixture is version %d", v8.Params.Version)
	}
	if !bytes.Equal(v8.Params.Body(), UnifiedParams().Body()) {
		t.Fatal("UnifiedParams is not the golden fixture's version 8 body")
	}
	v9, err := ReadJSONL(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	v9.Params = ProofsParams()
	if v9.Params.Version != ProofsVersion {
		t.Fatalf("default version %d", v9.Params.Version)
	}
	a, err := Compute(context.Background(), v8)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Compute(context.Background(), v9)
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a.Scores)
	jb, _ := json.Marshal(b.Scores)
	if !bytes.Equal(ja, jb) {
		t.Fatalf("version 9 changed standing on version 8's inputs:\n v8 %.400s\n v9 %.400s", ja, jb)
	}
}

// Every row validates, the old rows are untouched and each new kind is
// assessed and capped.
func TestProofRowsV9(t *testing.T) {
	p := ProofsParams()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseParams(ProofsVersion, p.Body()); err != nil {
		t.Fatal(err)
	}
	// Version 10 (C336) is version 9 plus pass_share_ppm, refused before it.
	d := PassParams()
	if _, err := ParseParams(PassVersion, d.Body()); err != nil || d.Version != PassVersion || d.PassSharePPM != DefaultPassSharePPM {
		t.Fatalf("version 10: %v, %+v", err, d.Version)
	}
	if _, err := ParseParams(ProofsVersion, d.Body()); err == nil {
		t.Fatal("version 9 accepted pass_share_ppm")
	}
	if d.PassSharePPM, d.Version = 0, ProofsVersion; !bytes.Equal(d.Body(), p.Body()) {
		t.Fatal("version 10 differs from version 9 in more than pass_share_ppm")
	}
	u := UnifiedParams()
	for kind, row := range u.Proofs {
		if kind == "wallet" { // C320c: version 9 adds the wallet's fallback, nothing else
			row.FallbackCents = WalletFallbackCents
		}
		if p.Proofs[kind] != row {
			t.Errorf("%s changed: %+v → %+v", kind, row, p.Proofs[kind])
		}
	}
	for kind, row := range proofRowsV9() {
		if _, old := u.Proofs[kind]; old && kind != "wallet" {
			t.Errorf("%s already had a row in version 8", kind)
		}
		if !slices.Contains(AssessedKinds(), kind) || assessedKinds[kind] != row.Assess {
			t.Errorf("%s is not an assessed kind", kind)
		}
		if got := AssessedContribution(row, 1e9); got != min(row.Forge, row.Rent) {
			t.Errorf("%s caps at %d", kind, got)
		}
	}
	if len(AssessedKinds()) != len(assessedKinds) {
		t.Errorf("AssessedKinds %v, map %d", AssessedKinds(), len(assessedKinds))
	}
}

// AccountCents saturates in both parts and matches GitHubCents' shape.
func TestAccountCents(t *testing.T) {
	for _, c := range []struct{ age, repos, followers int64 }{{0, 0, 0}, {365, 3, 4}, {4000, 1e6, 1e6}, {-5, -1, 7}, {90, 0, 1000}} {
		want := GitHubCents(c.age, c.repos, c.followers)
		got := AccountCents(c.age, GitHubAgeCents, githubAgeHalfLife, min(max(0, c.repos), 1e6)+min(max(0, c.followers), 1e6), GitHubActivityCents, GitHubActivityHalf)
		if got != want {
			t.Errorf("%+v: AccountCents %d, GitHubCents %d", c, got, want)
		}
	}
	if got := AccountCents(1e6, 100, 365, 1e15, 50, 10); got > 150 || got < 147 {
		t.Errorf("saturation: %d", got)
	}
	if AccountCents(-1, 100, 365, -1, 50, 10) != 0 {
		t.Error("negative inputs count")
	}
}
