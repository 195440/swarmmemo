package web

import (
	"strings"
	"testing"
)

// The footer is generated from the one table of pages (sitenav.go): six
// labelled columns that hold every public page once (C349); the tools are
// one link to their index, not a column of tool pages. The operator's
// backlink comes from FOOTER_CREDIT_URL and is absent from a deployment that
// sets none.
func TestFooterColumnsAndCredit(t *testing.T) {
	_, _, body := getPage(t, "/")
	_, footer, _ := strings.Cut(body, "<footer")
	for _, heading := range []string{">Use</span>", ">Connect</span>", ">Learn</span>", ">Trust and record</span>", ">For sites</span>", ">About</span>"} {
		if !strings.Contains(footer, heading) {
			t.Errorf("footer lacks column %s", heading)
		}
	}
	if n := strings.Count(footer, `class="footer-col"`); n != 6 {
		t.Errorf("footer has %d columns, want 6", n)
	}
	for _, want := range []string{`href="/agents/network"`, `href="/gate"`, `href="/glossary"`, `href="/trust-model"`, `href="/trust"`, `href="/terms"`, `href="/find"`, `href="/me"`} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer lacks %s", want)
		}
	}
	for _, dropped := range []string{`href="/llms.txt"`, `href="/feed.atom"`, `href="/limits"`, `href="/policy"`, `href="/tools/memory"`, `href="/admin`} {
		if strings.Contains(footer, dropped) {
			t.Errorf("footer still links %s", dropped)
		}
	}
	if strings.Contains(footer, "footer-credit") {
		t.Error("credit shown without FOOTER_CREDIT_URL")
	}
	if err := SetFooterCredit("http://example.org"); err == nil {
		t.Error("a non-https credit URL was accepted")
	}
	if err := SetFooterCredit("https://www.example.org/"); err != nil {
		t.Fatal(err)
	}
	defer SetFooterCredit("")
	_, _, body = getPage(t, "/")
	if !strings.Contains(body, `<a class="footer-credit" href="https://www.example.org/" rel="noopener">made with love by example.org</a>`) {
		t.Error("credit not rendered")
	}
}
