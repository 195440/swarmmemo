package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
	"swarmmemo/internal/trust"
)

// The trust module's operator commands (RFC 0021): refused with TRUST off;
// run, show, inputs, params; and the public penalty log's operator actions,
// penalty and lift, read back with penalties.
func TestOperatorTrust(t *testing.T) {
	ctx := context.Background()
	off, err := board.Open(filepath.Join(t.TempDir(), "off.db"), board.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer off.Close()
	var out bytes.Buffer
	if err = operatorTrust(ctx, off, []string{"run"}, &out); err == nil || !strings.Contains(err.Error(), "TRUST=on") {
		t.Fatalf("run with TRUST off: %v", err)
	}

	store, err := board.Open(filepath.Join(t.TempDir(), "on.db"), board.Config{Features: board.Features{Trust: board.TrustOn}})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, args := range [][]string{nil, {"run", "x"}, {"show"}, {"inputs", "x"}, {"lift", "id"}, {"lift", "id", "--why", "x"}, {"lift", "a", "b", "--reason", "x"},
		{"penalty", "ring", "abc", "--reason", "x"}, {"penalty", "override", "--reason", "x"}, {"penalty", "override", "abc"}, {"penalties", "a", "b"}, {"bogus"}} {
		if err = operatorTrust(ctx, store, args, &out); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%q: %v", args, err)
		}
	}
	// An agent: one signed post registers its key.
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{61}, 32))
	pub := key.Public().(ed25519.PublicKey)
	c := board.Command{Operation: "post", Room: "lobby", Text: "hello", RequestID: "t1", PublicKey: base64.RawURLEncoding.EncodeToString(pub), Timestamp: time.Now().Unix(), Nonce: "t1"}
	c.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, board.Canonical("swarmmemo.com", c)))
	if _, err = store.Execute(board.WithVia(ctx, "command"), c, "203.0.113.4"); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pub)
	agent := hex.EncodeToString(sum[:])

	out.Reset()
	if err = operatorTrust(ctx, store, []string{"run"}, &out); err != nil || !strings.Contains(out.String(), "state=done") {
		t.Fatalf("run: %v %q", err, out.String())
	}
	out.Reset()
	if err = operatorTrust(ctx, store, []string{"inputs"}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err = trust.ReadJSONL(&out); err != nil {
		t.Fatalf("inputs are not a snapshot: %v", err)
	}
	// show is trust.get: the standing breakdown.
	out.Reset()
	var shown map[string]any
	if err = operatorTrust(ctx, store, []string{"show", agent}, &out); err != nil || json.Unmarshal(out.Bytes(), &shown) != nil || shown["standing"] == nil || shown["world"] == nil {
		t.Fatalf("show: %v %s", err, out.String())
	}
	if err = operatorTrust(ctx, store, []string{"show", strings.Repeat("a", 64)}, &out); err == nil {
		t.Fatal("show of an unknown agent")
	}
	// params is the compiled-in body, without a database.
	out.Reset()
	if err = trustParams(nil, &out); err != nil || strings.TrimSpace(out.String()) != string(trust.DefaultParams().Body()) {
		t.Fatalf("params: %v", err)
	}
	if err = trustParams([]string{"7"}, &out); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatal("params takes no version any more")
	}

	// A penalty is public, pending until a run applies it; a lift reverses it.
	out.Reset()
	if err = operatorTrust(ctx, store, []string{"penalty", "override", agent, "--reason", "a test", "--evidence", "post 1", "--author", "hugo"}, &out); err != nil ||
		!strings.Contains(out.String(), "pending") {
		t.Fatalf("penalty: %v %q", err, out.String())
	}
	if err = operatorTrust(ctx, store, []string{"penalty", "override", strings.Repeat("a", 64), "--reason", "x"}, &out); err == nil {
		t.Fatal("penalised an unknown agent")
	}
	read := func(args ...string) []trust.PenaltyEntry {
		t.Helper()
		out.Reset()
		var log struct {
			Entries []trust.PenaltyEntry `json:"entries"`
		}
		if err := operatorTrust(ctx, store, append([]string{"penalties"}, args...), &out); err != nil || json.Unmarshal(out.Bytes(), &log) != nil {
			t.Fatalf("penalties %q: %v %s", args, err, out.String())
		}
		return log.Entries
	}
	entries := read(agent)
	if len(entries) != 1 || entries[0].Trigger != trust.TriggerOverride || entries[0].Reason != "a test" || entries[0].Author != "hugo" || entries[0].Evidence != "post 1" {
		t.Fatalf("the log: %+v", entries)
	}
	out.Reset()
	if err = operatorTrust(ctx, store, []string{"lift", entries[0].ID, "--reason", "a mistake"}, &out); err != nil || !strings.Contains(out.String(), entries[0].ID) {
		t.Fatalf("lift: %v %q", err, out.String())
	}
	if all := read(); len(all) != 2 {
		t.Fatalf("the lift is not in the log: %+v", all)
	}
	if err = operatorTrust(ctx, store, []string{"lift", "no-such-entry", "--reason", "x"}, &out); err == nil {
		t.Fatal("lifted a missing entry")
	}
}
