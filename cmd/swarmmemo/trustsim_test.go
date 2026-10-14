package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
