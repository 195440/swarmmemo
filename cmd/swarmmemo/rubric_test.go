package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRubricCommand(t *testing.T) {
	const dir = "../../internal/rubric/testdata/"
	var out bytes.Buffer
	if err := rubricCommand([]string{"score", dir + "x402-endpoint-1.json", dir + "obs-healthy.json"}, &out); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(dir + "golden/healthy.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("CLI output differs from the golden result")
	}
	out.Reset()
	if err := rubricCommand([]string{"validate", dir + "x402-endpoint-1.json"}, &out); err != nil || !strings.HasPrefix(out.String(), "x402-endpoint@") {
		t.Fatalf("validate: %v %q", err, out.String())
	}
	for _, args := range [][]string{{}, {"score", dir + "x402-endpoint-1.json"}, {"score", dir + "obs-healthy.json", dir + "obs-healthy.json"}, {"validate", "/nonexistent"}} {
		if err := rubricCommand(args, &out); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}
