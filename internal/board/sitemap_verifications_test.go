package board

import (
	"strings"
	"testing"
)

// C353b: the sitemap offers verification records (/v/ID) once past the
// archive delay, newest first, never a hidden (withdrawn) one, and never
// past SitemapVerificationsMax.
func TestSitemapVerifications(t *testing.T) {
	s := openTest(t, Config{ArchiveDelaySeconds: 3600})
	atTime(s, testTime)
	key := keyFor(42)
	register(t, s, key)
	var ids []string
	for i, hash := range []string{"aa", "bb", "cc"} {
		atTime(s, testTime+int64(i))
		res := run(t, s, publishVerify(key, verifyRecord(func(d map[string]any) { d["output_sha256"] = strings.Repeat(hash, 32) })))
		ids = append(ids, verificationOf(t, res).ID)
	}
	listed := func() []SitemapRoom {
		t.Helper()
		n, err := s.PublicSitemapVerificationCount(testContext)
		if err != nil {
			t.Fatal(err)
		}
		list, err := s.PublicSitemapVerifications(testContext, 0, 50000)
		if err != nil {
			t.Fatal(err)
		}
		if n != len(list) {
			t.Fatalf("count %d, listed %d", n, len(list))
		}
		return list
	}
	if got := listed(); len(got) != 0 {
		t.Fatalf("listed inside the archive delay: %+v", got)
	}
	atTime(s, testTime+7200)
	if _, err := s.db.Exec("UPDATE verifications SET hidden_at=?,hidden_reason='spam' WHERE id=?", testTime+10, ids[1]); err != nil {
		t.Fatal(err)
	}
	got := listed()
	if len(got) != 2 || got[0].Name != ids[2] || got[1].Name != ids[0] || got[0].Modified != testTime+2 || got[1].Modified != testTime {
		t.Fatalf("listed %+v, want %s then %s", got, ids[2], ids[0])
	}
	if page, err := s.PublicSitemapVerifications(testContext, 1, 1); err != nil || len(page) != 1 || page[0].Name != ids[0] {
		t.Fatalf("offset 1: %+v %v", page, err)
	}
	// Never past the cap.
	if page, err := s.PublicSitemapVerifications(testContext, SitemapVerificationsMax, 10); err != nil || len(page) != 0 {
		t.Fatalf("past the cap: %+v %v", page, err)
	}
	if _, err := s.PublicSitemapVerifications(testContext, -1, 10); err == nil {
		t.Fatal("a negative offset was taken")
	}
}
