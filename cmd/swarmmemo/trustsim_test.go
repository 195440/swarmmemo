package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"swarmmemo/internal/trust"
)

func TestTrustSimulate(t *testing.T) {
	var md bytes.Buffer
	if err := trustSimulate([]string{"--scenario", "thin", "--seed", "3", "--seeds", "1", "--md"}, &md); err != nil {
		t.Fatal(err)
	}
	if s := md.String(); !strings.Contains(s, "## thin") || !strings.Contains(s, "| honest-informed |") || !strings.Contains(s, "Conserved in every run: **true**") {
		t.Fatalf("page:\n%s", s)
	}
	var js bytes.Buffer
	if err := trustSimulate([]string{"--scenario", "thin", "--seed", "3", "--seeds", "1", "--json"}, &js); err != nil {
		t.Fatal(err)
	}
	var reports []map[string]any
	if err := json.Unmarshal(js.Bytes(), &reports); err != nil || len(reports) != 1 {
		t.Fatalf("json: %v %s", err, js.String())
	}
	for _, bad := range [][]string{{"--scenario", "nope"}, {"--seeds", "0"}, {"--cadence", "99"}, {"--frobnicate"}} {
		if err := trustSimulate(bad, &js); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}

// "trust params 7" prints the body `params set trust` takes to make RFC 0020
// the rule; 6 is the compiled-in default; nothing else is printed.
func TestTrustParamsBody(t *testing.T) {
	var out bytes.Buffer
	if err := trustParams([]string{"7"}, &out); err != nil {
		t.Fatal(err)
	}
	p, err := trust.ParseParams(trust.JudgementsVersion, bytes.TrimSpace(out.Bytes()))
	if err != nil || p.Standing.Rule != 4 || p.Standing.Judgements == nil {
		t.Fatalf("version 7: %v %s", err, out.String())
	}
	out.Reset()
	if err = trustParams([]string{"6"}, &out); err != nil || !bytes.Equal(bytes.TrimSpace(out.Bytes()), trust.DefaultParams().Body()) {
		t.Fatalf("version 6: %v", err)
	}
	for _, bad := range [][]string{{"5"}, {}, {"7", "8"}} {
		if trustParams(bad, &out) == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}
