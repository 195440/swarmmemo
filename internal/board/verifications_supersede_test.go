package board

import (
	"strings"
	"testing"
)

// C371: a verifier corrects a record by publishing a new one that names it
// in supersedes. Only its own visible record, once; both stay readable and
// in the sitemap, each pointing at the other.
func TestVerificationSupersedes(t *testing.T) {
	s := openTest(t, Config{ArchiveDelaySeconds: 3600})
	atTime(s, testTime)
	key, other := keyFor(51), keyFor(52)
	register(t, s, key)
	register(t, s, other)
	old := verificationOf(t, run(t, s, publishVerify(key, verifyRecord(func(d map[string]any) { d["limits"] = "debug: stray line" }))))
	if old.Supersedes != "" || old.SupersededBy != "" {
		t.Fatalf("a fresh record %+v", old)
	}

	// Refusals: a malformed id, an unknown one, someone else's record.
	fails(t, s, publishVerify(key, verifyRecord(func(d map[string]any) { d["supersedes"] = "/v/" + old.ID })), "invalid_verification")
	fails(t, s, publishVerify(key, verifyRecord(func(d map[string]any) { d["supersedes"] = strings.Repeat("0", 16) })), "verification_not_found")
	fails(t, s, publishVerify(other, verifyRecord(func(d map[string]any) { d["supersedes"] = old.ID })), "supersede_forbidden")

	atTime(s, testTime+1)
	res := run(t, s, publishVerify(key, verifyRecord(func(d map[string]any) {
		d["limits"] = "Windows was not tested."
		d["supersedes"] = old.ID
	})))
	fresh := verificationOf(t, res)
	if fresh.Supersedes != old.ID || fresh.SupersededBy != "" || !strings.Contains(fresh.SignedPayload, `\"supersedes\":\"`+old.ID+`\"`) {
		t.Fatalf("the new record %+v", fresh)
	}
	if got := verificationOf(t, run(t, s, Command{Operation: "verify.get", Target: old.ID})); got.SupersededBy != fresh.ID || got.Supersedes != "" {
		t.Fatalf("the old record %+v", got)
	}
	list, _ := run(t, s, Command{Operation: "verify.list", Target: old.Repo}).Data["verifications"].([]Verification)
	if len(list) != 2 || list[0].ID != fresh.ID || list[0].Supersedes != old.ID || list[1].ID != old.ID || list[1].SupersededBy != fresh.ID {
		t.Fatalf("list %+v", list)
	}

	// Once: the old record is superseded; supersede the newest instead.
	fails(t, s, publishVerify(key, verifyRecord(func(d map[string]any) {
		d["limits"] = "again"
		d["supersedes"] = old.ID
	})), "already_superseded")

	// A hidden superseder no longer counts: the old record reads current
	// again and may be superseded anew; a hidden record cannot be.
	if err := s.moderateVerification(testContext, fresh.ID, "test", true); err != nil {
		t.Fatal(err)
	}
	if got := verificationOf(t, run(t, s, Command{Operation: "verify.get", Target: old.ID})); got.SupersededBy != "" {
		t.Fatalf("superseded by a hidden record: %+v", got)
	}
	fails(t, s, publishVerify(key, verifyRecord(func(d map[string]any) {
		d["limits"] = "of a hidden one"
		d["supersedes"] = fresh.ID
	})), "supersede_hidden")
	again := verificationOf(t, run(t, s, publishVerify(key, verifyRecord(func(d map[string]any) {
		d["limits"] = "third"
		d["supersedes"] = old.ID
	}))))
	if got := verificationOf(t, run(t, s, Command{Operation: "verify.get", Target: old.ID})); got.SupersededBy != again.ID {
		t.Fatalf("after the hide: %+v", got)
	}

	// The sitemap keeps a superseded record beside its successor.
	atTime(s, testTime+7200)
	page, err := s.PublicSitemapVerifications(testContext, 0, 10)
	if err != nil || len(page) != 2 || page[0].Name != again.ID || page[1].Name != old.ID {
		t.Fatalf("sitemap %+v %v", page, err)
	}
}
