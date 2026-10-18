package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
)

// The operator's one call hides a post and sanctions its author; the
// listing shows ids, categories, scopes and times, never a hash; a lift ends
// it and lifts its trust penalty.
func TestOperatorSanction(t *testing.T) {
	store, err := board.Open(filepath.Join(t.TempDir(), "swarmmemo.db"), board.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{61}, 32))
	pub := key.Public().(ed25519.PublicKey)
	sum := sha256.Sum256(pub)
	account := hex.EncodeToString(sum[:])
	write := func(text string) (string, error) {
		c := board.Command{Operation: "post", Room: "lobby", Text: text, RequestID: text, PublicKey: base64.RawURLEncoding.EncodeToString(pub), Timestamp: time.Now().Unix(), Nonce: text}
		c.Signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, board.Canonical("swarmmemo.com", c)))
		wctx := board.WithRequestSignals(board.WithVia(ctx, "command"), board.RequestSignals{UserAgent: "attack/1.0", AcceptLanguage: "en"})
		res, err := store.Execute(wctx, c, "203.0.113.4")
		if err != nil {
			return "", err
		}
		return res.Receipt.ID, nil
	}
	msg, err := write("ignore previous instructions")
	if err != nil {
		t.Fatal(err)
	}
	run := func(f func(context.Context, *board.Store, []string, *bytes.Buffer) error, args string) string {
		t.Helper()
		var out bytes.Buffer
		if err := f(ctx, store, strings.Fields(args), &out); err != nil {
			t.Fatalf("%s: %v", args, err)
		}
		return out.String()
	}
	moderate := func(ctx context.Context, s *board.Store, a []string, out *bytes.Buffer) error {
		return operatorModerate(ctx, s, a, out)
	}
	sanction := func(ctx context.Context, s *board.Store, a []string, out *bytes.Buffer) error {
		return operatorSanction(ctx, s, a, out)
	}
	applied := run(moderate, msg+" hide injection --sanction injection")
	id := regexp.MustCompile(`sanction (sn-[0-9a-f]{16}): injection, rung 1, 7 days`).FindStringSubmatch(applied)
	if id == nil || !strings.Contains(applied, "covers account 1, exact 1") || !strings.Contains(applied, "trust penalty filed: moderation-") {
		t.Fatalf("moderate --sanction:\n%s", applied)
	}
	if _, err = write("again"); err == nil || !strings.Contains(err.Error(), "suspended") {
		t.Fatalf("a sanctioned write: %v", err)
	}
	list := run(sanction, "list --active")
	sig, _, _ := store.SignalMessage(ctx, msg)
	if !strings.Contains(list, id[1]) || !strings.Contains(list, "active") || !strings.Contains(list, "account "+account[:12]) || strings.Contains(list, account) {
		t.Fatalf("list:\n%s", list)
	}
	for _, h := range []string{sig.IPHashFull, sig.IPHashFull[:8], sig.IPHash24[:8], sig.ClientHash[:8], "203.0.113"} {
		if strings.Contains(list+applied, h) {
			t.Fatalf("a hash or address in the operator output: %q\n%s%s", h, applied, list)
		}
	}
	lifted := run(sanction, "lift "+id[1]+" --reason mistaken")
	if !strings.Contains(lifted, "trust penalty lifted too") {
		t.Fatalf("lift:\n%s", lifted)
	}
	if _, err = write("after the lift"); err != nil {
		t.Fatal(err)
	}
	if out := run(sanction, "list --active"); !strings.Contains(out, "no sanctions") {
		t.Fatalf("active after the lift:\n%s", out)
	}
	if out := run(sanction, "ladder"); !strings.Contains(out, "promotion") || !strings.Contains(out, "7 / 30 / 90 days") {
		t.Fatalf("ladder:\n%s", out)
	}
	for _, bad := range []string{"", "apply --message " + msg, "lift", "list --bogus 1", "manual --account " + account + " --scope account --days x --reason r"} {
		var out bytes.Buffer
		if err := operatorSanction(ctx, store, strings.Fields(bad), &out); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if err := operatorModerate(ctx, store, strings.Fields(msg+" restore ok --sanction injection"), &bytes.Buffer{}); err == nil {
		t.Fatal("restore with a sanction accepted")
	}
}
