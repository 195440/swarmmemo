package web

import (
	"net/http/httptest"
	"testing"
)

// /favicon.ico answers with the raster logo rather than a 404.
func TestFaviconIsTheLogo(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler(nil).ServeHTTP(rec, httptest.NewRequest("GET", "/favicon.ico", nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Body.Len() < 1000 {
		t.Fatalf("GET /favicon.ico = %d %q, %d bytes", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
}
