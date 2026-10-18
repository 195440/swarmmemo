package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"swarmmemo/internal/useragent"
)

// The summarizer names itself: a library-default User-Agent is refused by
// Cloudflare's Browser Integrity Check.
func TestOpenRouterUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	o := &OpenRouter{Key: "k", Client: srv.Client(), BaseURL: srv.URL}
	_, _, _, _, _ = o.Summarize(context.Background(), "m", "s", "u", 10)
	if got != useragent.For("graph-summary") {
		t.Fatalf("User-Agent %q", got)
	}
}
