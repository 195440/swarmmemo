package safenet

import (
	"flag"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	reservedTS = "../../workers/runs-loader/src/reserved.gen.ts"
	reservedPy = "../../curation/publish_references.py"
	egressTS   = "../../workers/runs-loader/src/egress.ts"
)

var updateReservedTS = flag.Bool("update-reserved-ts", false, "rewrite "+reservedTS+" and the RESERVED line of "+reservedPy+" from ReservedRanges")

// reservedTSSource renders the runs loader's copy. The loader holds an IPv4
// address IPv4-mapped (::ffff:a.b.c.d), so the mapped prefix itself would
// match every IPv4 address there; its IPv4 ranges already do that job.
func reservedTSSource() string {
	var b strings.Builder
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n")
	b.WriteString("// Code generated from internal/safenet/reserved.go by go generate; DO NOT EDIT.\n")
	b.WriteString("// The one reserved list: no run may reach an address inside these ranges.\n\n")
	b.WriteString("export const RESERVED: readonly string[] = [\n")
	for _, r := range ReservedRanges {
		if r.CIDR == "::ffff:0:0/96" {
			continue
		}
		b.WriteString("  " + strconv.Quote(r.CIDR) + ", // " + r.Source + "\n")
	}
	b.WriteString("];\n")
	return b.String()
}

// reservedPyLine is curation/publish_references.py's RESERVED line.
func reservedPyLine() string {
	quoted := make([]string, len(ReservedRanges))
	for i, r := range ReservedRanges {
		quoted[i] = strconv.Quote(r.CIDR)
	}
	return "RESERVED = tuple(map(ipaddress.ip_network, (" + strings.Join(quoted, ", ") + ")))"
}

var reservedPyPattern = regexp.MustCompile(`(?m)^RESERVED = tuple\(map\(ipaddress\.ip_network, \(.*\)\)\)$`)

// TestReservedTSIsCurrent fails when a generated copy of the reserved list is
// stale: run go generate ./internal/safenet after changing ReservedRanges.
func TestReservedTSIsCurrent(t *testing.T) {
	py, err := os.ReadFile(reservedPy)
	if err != nil {
		t.Fatal(err)
	}
	if !reservedPyPattern.Match(py) {
		t.Fatalf("%s has no RESERVED line", reservedPy)
	}
	if *updateReservedTS {
		if err := os.WriteFile(reservedTS, []byte(reservedTSSource()), 0o644); err != nil {
			t.Fatal(err)
		}
		py = reservedPyPattern.ReplaceAllLiteral(py, []byte(reservedPyLine()))
		if err := os.WriteFile(reservedPy, py, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(reservedTS)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != reservedTSSource() {
		t.Fatalf("%s is stale: run go generate ./internal/safenet", reservedTS)
	}
	if line := reservedPyPattern.Find(py); string(line) != reservedPyLine() {
		t.Fatalf("%s RESERVED is stale: run go generate ./internal/safenet", reservedPy)
	}
	for _, name := range []string{"IPV4_DENY", "IPV6_DENY", "IPV6_EXCEPTIONS"} {
		if strings.Contains(string(py), name) {
			t.Fatalf("%s keeps its own %s list", reservedPy, name)
		}
	}
	src, err := os.ReadFile(egressTS)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "import { RESERVED } from \"./reserved.gen.ts\";") ||
		!strings.Contains(string(src), "cidrs(\"private_address\", RESERVED)") {
		t.Fatal("egress.ts must build its private ranges from reserved.gen.ts")
	}
}

// TestReservedRangesAreBlocked checks every range's first, last and a middle
// address against PublicIP, including the IPv4-mapped form of IPv4 ranges.
func TestReservedRangesAreBlocked(t *testing.T) {
	for _, a := range ReservedSamples() {
		if err := PublicIP(net.IP(a.AsSlice())); err != ErrBlocked {
			t.Errorf("PublicIP(%s) = %v, want ErrBlocked", a, err)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "::ffff:8.8.8.8", "2001:4860:4860::8888"} {
		if err := PublicIP(net.ParseIP(s)); err != nil {
			t.Errorf("PublicIP(%s) = %v, want nil", s, err)
		}
	}
	seen := map[string]bool{}
	for _, r := range ReservedRanges {
		if seen[r.CIDR] || r.Source == "" {
			t.Errorf("range %q duplicated or without a source", r.CIDR)
		}
		seen[r.CIDR] = true
		if p := netip.MustParsePrefix(r.CIDR); p != p.Masked() {
			t.Errorf("range %q has host bits set", r.CIDR)
		}
	}
}
