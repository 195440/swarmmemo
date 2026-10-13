package httpapi

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// TestCaddyBodyLimitIsCommandBodyBytes holds the proxy's request body cap to
// the app's: Caddy's max_size is board.CommandBodyBytes exactly, so a body
// the app would accept is never refused at the edge (2MB was 2,000,000 bytes,
// 97,152 short of the app's 2 MiB) and the app's own 413 is the one agents see.
func TestCaddyBodyLimitIsCommandBodyBytes(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/caddy/Caddyfile")
	if os.IsNotExist(err) {
		t.Skip("deploy/ is not in this tree (public snapshot)")
	}
	if err != nil {
		t.Fatal(err)
	}
	sizes := regexp.MustCompile(`(?m)^\s*max_size\s+(\S+)\s*$`).FindAllStringSubmatch(string(raw), -1)
	if len(sizes) == 0 {
		t.Fatal("deploy/caddy/Caddyfile has no request_body max_size")
	}
	for _, m := range sizes {
		if got := caddyBytes(t, m[1]); got != board.CommandBodyBytes {
			t.Errorf("Caddyfile max_size %s is %d bytes; board.CommandBodyBytes is %d", m[1], got, board.CommandBodyBytes)
		}
	}
	if n := strings.Count(string(raw), "openai-apps-challenge"); n != 1 {
		t.Errorf("Caddyfile names the OpenAI challenge path %d times; it is served once, by Caddy", n)
	}
}

// caddyBytes parses a Caddy size the way go-humanize does for the units
// Caddyfiles use: B, KB/MB/GB (powers of 1000), KiB/MiB/GiB (powers of 1024).
func caddyBytes(t *testing.T, s string) int {
	t.Helper()
	m := regexp.MustCompile(`^(\d+)([KMG]i?)?B?$`).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("cannot read Caddy size %q", s)
	}
	n, _ := strconv.Atoi(m[1])
	base := 1000
	if strings.HasSuffix(m[2], "i") {
		base = 1024
	}
	for _, p := range "KMG" {
		if m[2] != "" && rune(m[2][0]) == p {
			return n * pow(base, strings.IndexRune("KMG", p)+1)
		}
	}
	return n
}

func pow(b, e int) int {
	r := 1
	for range e {
		r *= b
	}
	return r
}
