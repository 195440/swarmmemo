package board

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"swarmmemo/internal/services"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

// verifyRecord is a well-formed verify.publish data object; change edits it.
func verifyRecord(change func(map[string]any)) string {
	d := map[string]any{"schema": 1, "repo": "https://github.com/Owner/Repo.git", "commit": testCommit,
		"command": "pytest -q", "environment": "python 3.12, linux/amd64, network off",
		"output_sha256": strings.Repeat("ab", 32), "limits": "Windows was not tested.",
		"checks": []map[string]any{{"property": "tests", "state": "pass", "note": "41 passed"}, {"property": "lint", "state": "fail", "note": "2 warnings"}, {"property": "fuzz", "state": "not_checked"}}}
	if change != nil {
		change(d)
	}
	raw, _ := json.Marshal(d)
	return string(raw)
}

func publishVerify(key ed25519.PrivateKey, data string) Command {
	return signed(key, Command{Operation: "verify.publish", Data: data})
}

func verificationOf(t *testing.T, res Result) Verification {
	t.Helper()
	v, ok := res.Data["verification"].(Verification)
	if !ok {
		t.Fatalf("no verification in %+v", res.Data)
	}
	return v
}

// A published verification is the verifier's own signed command: it
// verifies offline against the verifier's key, carries the checks it
// signed and is stamped by the notary; anyone reads it back by id or by
// its subject.
func TestVerificationPublishAndRead(t *testing.T) {
	s := openWakeTest(t, "notary")
	steppingClock(s)
	key := keyFor(41)
	register(t, s, key)
	res := run(t, s, publishVerify(key, verifyRecord(nil)))
	v := verificationOf(t, res)
	if !ValidVerificationID(v.ID) || v.Repo != "https://github.com/owner/repo" || v.Commit != testCommit ||
		v.CommitURL != "https://github.com/owner/repo/commit/"+testCommit || v.Passed != 1 || v.Failed != 1 || v.Ran != 2 || v.Result != "failed" ||
		len(v.Checks) != 3 || v.Checks[0].Note != "41 passed" || v.Verifier.Fingerprint != keyID(key) || !v.TextIsUntrusted {
		t.Fatalf("record %+v", v)
	}
	if res.Data["markdown"] != "[![Verified by SwarmMemo](https://swarmmemo.com/v/"+v.ID+"/badge.svg)](https://swarmmemo.com/v/"+v.ID+")" {
		t.Fatalf("markdown %v", res.Data["markdown"])
	}
	// The signature checks offline, and the record hash is its id's source.
	public, _ := base64.RawURLEncoding.DecodeString(v.Verifier.PublicKey)
	sig, _ := base64.RawURLEncoding.DecodeString(v.Signature)
	if !ed25519.Verify(public, []byte(v.SignedPayload), sig) {
		t.Fatal("the record's signature does not verify")
	}
	sum := sha256.Sum256([]byte(v.SignedPayload))
	if hex.EncodeToString(sum[:]) != v.RecordSHA256 || v.ID != v.RecordSHA256[:16] {
		t.Fatalf("record hash %s id %s", v.RecordSHA256, v.ID)
	}
	if v.Notary == nil || v.Notary.Hash != v.RecordSHA256 || v.Proof != "/api/log/proof?notary="+v.RecordSHA256 ||
		!services.VerifyNotaryReceipt(v.Notary.PublicKey, *v.Notary) {
		t.Fatalf("notary %+v %s", v.Notary, v.Proof)
	}
	got := verificationOf(t, run(t, s, Command{Operation: "verify.get", Target: v.ID}))
	if got.ID != v.ID || got.SignedPayload != v.SignedPayload || got.Notary == nil {
		t.Fatalf("read %+v", got)
	}
	// By subject: any spelling of the repository, with or without the commit.
	for _, c := range []Command{
		{Operation: "verify.list", Target: "https://GITHUB.com/Owner/Repo/"},
		{Operation: "verify.list", Target: "https://github.com/Owner/Repo", Query: testCommit},
		{Operation: "verify.list"},
	} {
		list, _ := run(t, s, c).Data["verifications"].([]Verification)
		if len(list) != 1 || list[0].ID != v.ID {
			t.Fatalf("%+v: %+v", c, list)
		}
	}
	if list, _ := run(t, s, Command{Operation: "verify.list", Target: "https://github.com/Owner/Repo", Query: strings.Repeat("f", 40)}).Data["verifications"].([]Verification); len(list) != 0 {
		t.Fatalf("another commit: %+v", list)
	}
	fails(t, s, Command{Operation: "verify.get", Target: "nothex"}, "invalid_verification_id")
	fails(t, s, Command{Operation: "verify.get", Target: strings.Repeat("0", 16)}, "verification_not_found")
	fails(t, s, Command{Operation: "verify.list", Target: "ftp://example.com/x"}, "invalid_verification_query")
	fails(t, s, Command{Operation: "verify.list", Target: "https://github.com/o/r", Query: "abc"}, "invalid_verification_query")
}

// A file subject needs no repository; every check passing reads passed;
// pages follow next_cursor.
func TestVerificationFileSubjectAndPages(t *testing.T) {
	s := openTest(t, Config{})
	key := keyFor(42)
	file := strings.Repeat("cd", 32)
	var ids []string
	for i := 0; i < 3; i++ {
		v := verificationOf(t, run(t, s, publishVerify(key, verifyRecord(func(d map[string]any) {
			delete(d, "repo")
			delete(d, "commit")
			d["file_sha256"], d["file"] = file, "dist/tool.tar.gz"
			d["checks"] = []map[string]any{{"property": "sha256", "state": "pass"}}
			d["command"] = "sha256sum dist/tool.tar.gz # run " + string(rune('a'+i))
		}))))
		if v.Result != "passed" || v.Passed != 1 || v.Ran != 1 || v.Notary != nil || v.ShortHash() != file[:7] {
			t.Fatalf("record %+v", v)
		}
		ids = append(ids, v.ID)
	}
	first := run(t, s, Command{Operation: "verify.list", Target: file, Limit: 2})
	page, _ := first.Data["verifications"].([]Verification)
	next, _ := first.Data["next_cursor"].(string)
	if len(page) != 2 || page[0].ID != ids[2] || next == "" {
		t.Fatalf("first page %+v", first.Data)
	}
	rest, _ := run(t, s, Command{Operation: "verify.list", Target: file, Cursor: next}).Data["verifications"].([]Verification)
	if len(rest) != 1 || rest[0].ID != ids[0] {
		t.Fatalf("second page %+v", rest)
	}
}

// Every bound and shape is refused with the whole rule; an unsigned or
// delegated publish is refused; the daily cap holds.
func TestVerificationRefusals(t *testing.T) {
	s := openTest(t, Config{})
	key := keyFor(43)
	for name, change := range map[string]func(map[string]any){
		"no schema":       func(d map[string]any) { delete(d, "schema") },
		"other field":     func(d map[string]any) { d["extra"] = "x" },
		"no subject":      func(d map[string]any) { delete(d, "repo"); delete(d, "commit") },
		"repo, no commit": func(d map[string]any) { delete(d, "commit") },
		"short commit":    func(d map[string]any) { d["commit"] = "0123456" },
		"upper commit":    func(d map[string]any) { d["commit"] = strings.ToUpper(testCommit) },
		"http repo":       func(d map[string]any) { d["repo"] = "http://github.com/o/r" },
		"repo query":      func(d map[string]any) { d["repo"] = "https://github.com/o/r?x=1" },
		"repo user":       func(d map[string]any) { d["repo"] = "https://u:p@github.com/o/r" },
		"bare host":       func(d map[string]any) { d["repo"] = "https://github.com" },
		"bad output":      func(d map[string]any) { d["output_sha256"] = "zz" },
		"no limits":       func(d map[string]any) { delete(d, "limits") },
		"empty command":   func(d map[string]any) { d["command"] = "" },
		"long command":    func(d map[string]any) { d["command"] = strings.Repeat("x", VerificationCommandMax+1) },
		"long limits":     func(d map[string]any) { d["limits"] = strings.Repeat("x", VerificationLimitsMax+1) },
		"control char":    func(d map[string]any) { d["environment"] = "linux\x1b[31m" },
		"bidi override":   func(d map[string]any) { d["environment"] = "linux ‮" },
		"no checks":       func(d map[string]any) { d["checks"] = []map[string]any{} },
		"none ran":        func(d map[string]any) { d["checks"] = []map[string]any{{"property": "tests", "state": "not_checked"}} },
		"bad state":       func(d map[string]any) { d["checks"] = []map[string]any{{"property": "tests", "state": "ok"}} },
		"long note": func(d map[string]any) {
			d["checks"] = []map[string]any{{"property": "tests", "state": "pass", "note": strings.Repeat("n", VerdictCheckNoteMax+1)}}
		},
		"multi-line note": func(d map[string]any) {
			d["checks"] = []map[string]any{{"property": "tests", "state": "pass", "note": "a\nb"}}
		},
		"file with spaces": func(d map[string]any) { d["file"] = " x" },
	} {
		_, err := s.Execute(testContext, publishVerify(key, verifyRecord(change)), "test-origin")
		var e *Error
		if !errorsAs(err, &e) || e.Code != "invalid_verification" {
			t.Fatalf("%s: %v", name, err)
		}
	}
	big := verifyRecord(func(d map[string]any) { d["limits"] = strings.Repeat("y", VerificationDataBytes) })
	fails(t, s, publishVerify(key, big), "invalid_verification")
	fails(t, s, Command{Operation: "verify.publish", Data: verifyRecord(nil)}, "signature_required")
	for i := 0; i < VerificationsPerDay; i++ {
		run(t, s, publishVerify(key, verifyRecord(func(d map[string]any) {
			d["output_sha256"] = strings.Repeat("0", 63) + string("0123456789abcdef"[i%16])
			d["limits"] = strings.Repeat("l", i+1)
		})))
	}
	fails(t, s, publishVerify(key, verifyRecord(nil)), "verification_limit")
	// An exact retry replays its receipt and publishes nothing new.
	other := keyFor(44)
	c := publishVerify(other, verifyRecord(nil))
	c.RequestID = "verify-retry"
	c = signed(other, c)
	run(t, s, c)
	if again := run(t, s, c); again.Data["verification"] == nil {
		t.Fatalf("retry %+v", again.Data)
	}
	if sqlCount(t, s, "SELECT count(*) FROM verifications WHERE verifier=?", keyID(other)) != 1 {
		t.Fatal("a retry published twice")
	}
}

// The repository URL has one canonical form.
func TestNormalizeRepoURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://github.com/o/r":           "https://github.com/o/r",
		"https://GitHub.com/O/R.git":       "https://github.com/o/r",
		"https://git.example.org/O/R":      "https://git.example.org/O/R",
		"https://github.com/o/r/":          "https://github.com/o/r",
		"https://git.example.org:8443/x/y": "https://git.example.org:8443/x/y",
		"https://github.com/o/r#readme":    "",
		"https://github.com/o/../r":        "",
		"https://github.com/o r":           "",
		"https://github.com/ö/r":           "",
		"https://example.org/ö/r":          "https://example.org/%C3%B6/r",
		"git@github.com:o/r.git":           "",
	} {
		got, ok := NormalizeRepoURL(raw)
		if got != want || ok != (want != "") {
			t.Errorf("%q: %q %v, want %q", raw, got, ok, want)
		}
	}
	if CommitURL("https://gitlab.com/o/r", testCommit) != "https://gitlab.com/o/r/-/commit/"+testCommit || CommitURL("https://example.org/o/r", testCommit) != "" {
		t.Fatal("commit URLs")
	}
}

// With MODERATION on, a record's text is screened as a post's is, and a
// hide takes it down with its reason while the row stays.
func TestVerificationScreened(t *testing.T) {
	policy := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policy, []byte(`{"schema":1,"version":3,"surfaces":{"post":{"classifiers":["rules"],"on_unavailable":"flag","rules":[{"id":"seed","category":"phishing","regex":"(?i)seed phrase"}],"categories":{"phishing":{"thresholds":[{"at":1,"action":"hide"}]}}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := openTest(t, Config{Features: Features{Moderation: true}, Moderation: ModerationConfig{PolicyFile: policy}})
	key := keyFor(45)
	bad := verificationOf(t, run(t, s, publishVerify(key, verifyRecord(func(d map[string]any) { d["limits"] = "send me your seed phrase" }))))
	good := verificationOf(t, run(t, s, publishVerify(key, verifyRecord(nil))))
	if n, err := s.Moderation().Work(context.Background()); n != 2 || err != nil {
		t.Fatalf("screened %d (%v)", n, err)
	}
	fails(t, s, Command{Operation: "verify.get", Target: bad.ID}, "verification_hidden")
	run(t, s, Command{Operation: "verify.get", Target: good.ID})
	if list, _ := run(t, s, Command{Operation: "verify.list"}).Data["verifications"].([]Verification); len(list) != 1 || list[0].ID != good.ID {
		t.Fatalf("list %+v", list)
	}
	if sqlCount(t, s, "SELECT count(*) FROM verifications") != 2 {
		t.Fatal("a hide deleted the row")
	}
}
