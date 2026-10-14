package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"swarmmemo/internal/board"
	"swarmmemo/internal/leakscan"
)

// pageDataLimits is page-data.js's limits, as the scripts read them.
func pageDataLimits(t *testing.T) map[string]int64 {
	t.Helper()
	js, _ := pageData()
	raw := strings.TrimSuffix(strings.TrimPrefix(string(js), "window.swarmmemoPage="), ";\n")
	var data struct {
		Limits map[string]int64 `json:"limits"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	return data.Limits
}

// scriptConstants are the module constants a script states as a number that
// is also a server's: each is held here to the Go constant it mirrors, since
// the module also runs where no page data is loaded (Node tests, the embed).
var scriptConstants = map[string]int64{
	"seal.js|EPOCH_MESSAGES_MAX": board.SealEpochMessagesMax,
	"leak.js|MAX_FINDINGS":       leakscan.MaxFindings,
}

// The page scripts read every server limit from page-data.js (C295-3): each
// limits.KEY they read is there, no read asks for a page size equal to a
// maximum by literal, and a NAME_MAX constant is either page data or held to
// its Go constant above.
func TestScriptsReadLimitsFromPageData(t *testing.T) {
	limits := pageDataLimits(t)
	maxima := map[int64]string{}
	for k, v := range limits {
		if strings.HasSuffix(k, "_maximum") {
			maxima[v] = k
		}
	}
	scripts, _ := filepath.Glob("assets/*.js")
	if len(scripts) < 10 {
		t.Fatalf("found %d scripts; the glob is broken", len(scripts))
	}
	read := regexp.MustCompile(`\blimits\??\.([a-z_]+)`)
	literal := regexp.MustCompile(`\blimit(?:\s*:\s*|=)(\d+)\b`)
	constant := regexp.MustCompile(`\bconst\s+([A-Z_]*(?:MAX|LIMIT)[A-Z_]*)\s*=\s*([0-9][0-9 *]*)`)
	seen := map[string]bool{}
	for _, path := range scripts {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Base(path)
		for _, m := range read.FindAllStringSubmatch(string(raw), -1) {
			if _, ok := limits[m[1]]; !ok {
				t.Errorf("%s reads limits.%s, which page-data.js does not have", name, m[1])
			}
		}
		for _, m := range literal.FindAllStringSubmatch(string(raw), -1) {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			if key, ok := maxima[n]; ok {
				t.Errorf("%s asks for %s literally, the %s: read window.swarmmemoPage.limits.%s", name, m[0], key, key)
			}
		}
		for _, m := range constant.FindAllStringSubmatch(string(raw), -1) {
			key := name + "|" + m[1]
			want, ok := scriptConstants[key]
			if !ok {
				t.Errorf("%s states %s = %s: read it from page data, or hold it to its Go constant in scriptConstants", name, m[1], strings.TrimSpace(m[2]))
				continue
			}
			seen[key] = true
			if got := jsInt(t, m[2]); got != want {
				t.Errorf("%s says %s = %d; the server's is %d", name, m[1], got, want)
			}
		}
	}
	for key := range scriptConstants {
		if !seen[key] {
			t.Errorf("scriptConstants lists %s, which no script states", key)
		}
	}
}

// jsInt evaluates "256" or "2 ** 20".
func jsInt(t *testing.T, expr string) int64 {
	t.Helper()
	expr = strings.TrimSpace(expr)
	if base, exp, ok := strings.Cut(expr, "**"); ok {
		b, err1 := strconv.ParseInt(strings.TrimSpace(base), 10, 64)
		e, err2 := strconv.ParseInt(strings.TrimSpace(exp), 10, 64)
		if err1 != nil || err2 != nil {
			t.Fatalf("cannot read %q", expr)
		}
		v := int64(1)
		for range e {
			v *= b
		}
		return v
	}
	v, err := strconv.ParseInt(expr, 10, 64)
	if err != nil {
		t.Fatalf("cannot read %q", expr)
	}
	return v
}
