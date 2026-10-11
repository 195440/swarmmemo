package httpapi

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
	"swarmmemo/internal/reviews"
	"swarmmemo/internal/web"
)

// Reviews on every surface (C200) over HTTP: an offering's page and JSON
// carry its reviews, its fingerprint address resolves to its page's, its
// badge is served, and listed subjects are in the sitemap with a lastmod
// while unlisted ones are not.

func TestOfferingReviewsOverHTTP(t *testing.T) {
	s, _, _, key := offeringServerWith(t, board.Features{Reviews: true})
	const uri = "x402:POST https://swarmmemo.com/@pythia/oracle"
	sub, err := reviews.Resolve(uri, reviews.Options{OwnHosts: []string{"swarmmemo.com"}})
	if err != nil {
		t.Fatal(err)
	}
	page := makeRequestWithAccept(s, "https://swarmmemo.com/@pythia/oracle", "text/html")
	body := page.Body.String()
	for _, want := range []string{`id="reviews"`, "No reviews of this offering yet.", "Review this offering", `{"subject":"` + uri + `"`, "/s/" + sub.ID + "/badge.svg"} {
		if page.Code != 200 || !strings.Contains(body, want) {
			t.Fatalf("offering page lacks %q (%d)", want, page.Code)
		}
	}
	j := makeRequestWithAccept(s, "https://swarmmemo.com/@pythia/oracle", "application/json")
	got := decodeResult(t, j.Body.Bytes())
	if dig(got, "data", "offering", "reviews", "subject") != uri || dig(got, "data", "offering", "reviews", "url") != "/s?q="+url.QueryEscape(uri) {
		t.Fatalf("offering JSON reviews %v", dig(got, "data", "offering", "reviews"))
	}
	res := makeRequest(s, "GET", "/api/subjects/resolve?uri="+url.QueryEscape("POST https://swarmmemo.com/@"+fingerprintOf(key)+"/oracle"), "", "")
	if dig(decodeResult(t, res.Body.Bytes()), "data", "subject", "uri") != uri {
		t.Fatalf("resolve by fingerprint: %s", res.Body.String())
	}
	badge := makeRequest(s, "GET", "/s/"+sub.ID+"/badge.svg", "", "")
	if badge.Code != 200 || !strings.HasPrefix(badge.Header().Get("Content-Type"), "image/svg+xml") || !strings.Contains(badge.Body.String(), "no reviews yet") {
		t.Fatalf("badge %d %v", badge.Code, badge.Header())
	}
}

func TestSitemapListsListedSubjects(t *testing.T) {
	store, err := board.Open(filepath.Join(t.TempDir(), "sitemap.sqlite"), board.Config{ArchiveDelaySeconds: -1, ServiceID: "swarmmemo.com", Features: board.Features{Reviews: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	n := 0
	review := func(seed byte, uri string) {
		t.Helper()
		n++
		key := ed25519.NewKeyFromSeed(append(make([]byte, 31), seed))
		sub, err := reviews.Resolve(uri, reviews.Options{})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"schema": 1, "review": map[string]any{"subject": sub.URI(), "verdict": "good"}})
		c := board.Command{Operation: "post", Room: board.ReviewsRoom, Page: sub.ID, Kind: "review", Text: "Fine.", Data: string(raw)}
		c.PublicKey = base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
		c.Timestamp, c.Nonce = time.Now().Unix(), "n"+strconv.Itoa(n)
		c.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, board.Canonical("swarmmemo.com", c)))
		if _, err := store.Execute(context.Background(), c, "test"); err != nil {
			t.Fatal(err)
		}
	}
	review(1, "domain:listed-subject.com")
	review(2, "domain:listed-subject.com")
	review(3, "domain:unlisted-subject.com")
	s := New(store, web.Handler(store), Config{PublicURL: "https://example.test", ServiceID: "swarmmemo.com", Features: board.Features{Reviews: true}})
	locs := map[string]string{}
	for _, u := range readSitemap(t, s, "/sitemap.xml").URLs {
		locs[u.Loc] = u.LastMod
	}
	if lastmod, ok := locs["https://example.test/s/"+reviews.SubjectID("domain", "listed-subject.com")]; !ok || lastmod == "" {
		t.Fatalf("listed subject missing or without lastmod: %v", locs)
	}
	if _, ok := locs["https://example.test/s/"+reviews.SubjectID("domain", "unlisted-subject.com")]; ok {
		t.Fatal("an unlisted subject is in the sitemap")
	}
}
