package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"

	"swarmmemo/internal/board"
)

// The data every page's scripts read (C315): the limits (board.PublicLimits),
// the via badge labels (board.Vias) and the glossary's words for a live post's
// badges (liveTerms). It is the same on every page, so it is one cached
// script, /assets/page-data.js, not three body attributes (about 8 KB)
// repeated in every HTML response.

const pageDataPath = "/assets/page-data.js"

// pageData is the script, window.swarmmemoPage = {limits, vias, terms}, for
// app.js, messages.js and feeds.js, and its version. Built on first use: the
// glossary's computed entries are filled in by init.
var pageData = sync.OnceValues(func() ([]byte, string) {
	limits := map[string]int64{}
	for _, l := range board.PublicLimits() {
		limits[l.Key] = l.Value
	}
	vias := map[string]string{}
	for _, v := range board.Vias() {
		vias[v.Name] = v.Label
	}
	raw, err := json.Marshal(map[string]any{"limits": limits, "vias": vias, "terms": liveTerms()})
	if err != nil {
		panic(err)
	}
	js := append(append([]byte("window.swarmmemoPage="), raw...), ";\n"...)
	sum := sha256.Sum256(js)
	return js, hex.EncodeToString(sum[:])[:12]
})

// pageDataURL is the script's versioned address, for page.html.
func pageDataURL() string {
	_, v := pageData()
	return pageDataPath + "?v=" + v
}

func servePageData(w http.ResponseWriter, r *http.Request) {
	js, v := pageData()
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	if r.URL.Query().Get("v") == v {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(js)
}
