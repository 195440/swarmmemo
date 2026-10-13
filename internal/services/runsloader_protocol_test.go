package services

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

const runsLimitsTS = "../../workers/runs-loader/src/limits.gen.ts"

var updateRunsLimits = flag.Bool("update-runs-limits", false, "rewrite "+runsLimitsTS+" from the Go constants")

// runsLimitsSource renders the loader's LIMITS from the Go constants, the one
// definition of them (Inner Ozma 9aa9904d: the two hand copies had drifted,
// clockSkewSec 60 against 120 here).
func runsLimitsSource() string {
	rows := []struct {
		name    string
		value   int64
		comment string
	}{
		{"requestBytes", RunsRequestBytes, ""},
		{"codeBytes", RunsCodeBytes, ""},
		{"inputBytes", RunsInputBytes, ""},
		{"cpuMsMax", RunsCPUMsMax, ""},
		{"wallMsMin", RunsWallMsMin, ""},
		{"wallMsMax", RunsWallMsMax, ""},
		{"subrequestsMax", RunsSubrequestsMax, ""},
		{"outputBytesMax", RunsOutputBytesMax, "each of stdout, stderr and the result"},
		{"errorBytes", RunsErrorBytes, ""},
		{"egressRequestsMax", RunsEgressRequestsMax, ""},
		{"egressBytesOutMax", RunsEgressBytesOutMax, ""},
		{"egressBytesInMax", RunsEgressBytesInMax, ""},
		{"egressLogMax", RunsEgressLogMax, ""},
		{"denyEntriesMax", RunsDenyEntriesMax, ""},
		{"hostBytesMax", RunsHostBytesMax, ""},
		{"responseBytesMax", RunsResponseBytes, ""},
		{"clockSkewSec", RunsClockSkewSec, "both ways: requests here, answers on the server"},
		{"tailWaitMs", RunsTailWaitMs, ""},
	}
	var b strings.Builder
	b.WriteString("// SPDX-License-Identifier: Apache-2.0\n")
	b.WriteString("// Code generated from internal/services/runsloader.go by go generate; DO NOT EDIT.\n")
	b.WriteString("// The loader's hard bounds. The server's own configured limits sit inside\n")
	b.WriteString("// these; the loader refuses anything outside them whatever the server asks.\n\n")
	b.WriteString("export const LIMITS = {\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %s: %d,", r.name, r.value)
		if r.comment != "" {
			b.WriteString(" // " + r.comment)
		}
		b.WriteString("\n")
	}
	b.WriteString("} as const;\n")
	return b.String()
}

// TestRunsLimitsTSIsCurrent fails when limits.gen.ts is not what the Go
// constants generate: run go generate ./internal/services after changing one.
func TestRunsLimitsTSIsCurrent(t *testing.T) {
	want := runsLimitsSource()
	if *updateRunsLimits {
		if err := os.WriteFile(runsLimitsTS, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(runsLimitsTS)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s is stale: run go generate ./internal/services", runsLimitsTS)
	}
}

// TestRunsProtocolTSHasNoOwnLimits keeps the generated file the only
// definition: protocol.ts imports and re-exports it rather than defining its
// own, and its schema version is the one the Go side checks.
func TestRunsProtocolTSHasNoOwnLimits(t *testing.T) {
	src, err := os.ReadFile("../../workers/runs-loader/src/protocol.ts")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if !strings.Contains(text, "import { LIMITS } from \"./limits.gen.ts\";\nexport { LIMITS };\n") {
		t.Fatal("protocol.ts must import and re-export LIMITS from limits.gen.ts")
	}
	if regexp.MustCompile(`(?m)^\s*export const LIMITS\b`).MatchString(text) {
		t.Fatal("protocol.ts defines its own LIMITS")
	}
	if m := regexp.MustCompile(`(?m)^export const SCHEMA = (\d+);`).FindStringSubmatch(text); m == nil || m[1] != "1" {
		t.Fatalf("protocol.ts SCHEMA %v; the Go side checks schema 1", m)
	}
	if runsHTTPGrace.Milliseconds() <= RunsTailWaitMs {
		t.Fatalf("runsHTTPGrace %s does not cover the tail wait", runsHTTPGrace)
	}
}
