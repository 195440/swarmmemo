package trust

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// walletCents is what an account's wallet proofs count this run, and how
// many of them carry the once-per-root note.
func walletCents(out Output, name string) (cents int64, once int) {
	for _, it := range scoreOf(out, name).Parts.World {
		if it.Kind == "proof" && it.Source == "wallet" {
			cents += it.Cents
			if strings.HasPrefix(it.Note, "fallback counted once per root: on ") {
				once++
			}
		}
	}
	return cents, once
}

// C492 (version 12): the wallet row's fallback prices control of a key,
// which is free to make again, so it counts once per (kind, node root):
// seven fresh empty wallets on one root count 50¢, not 350¢, whether on one
// key or spread over keys of one root. Version 11 still counts 350¢.
func TestWalletFallbackOncePerRoot(t *testing.T) {
	b := newBuilder()
	for i := range 7 {
		b.wallet("w", "0xe"+strconv.Itoa(i), 0)
	}
	out := compute(t, b)
	if got, once := walletCents(out, "w"); got != WalletFallbackCents || once != 6 {
		t.Errorf("seven empty wallets on one key count %d¢ (%d noted), want %d¢ (6 noted)", got, once, WalletFallbackCents)
	}
	if got := scoreOf(out, "w").Parts.DirectCents; got != WalletFallbackCents {
		t.Errorf("direct %d¢, want %d", got, WalletFallbackCents)
	}
	b.snap.Params = BreakerParams()
	out = compute(t, b)
	if got, once := walletCents(out, "w"); got != 7*WalletFallbackCents || once != 0 {
		t.Errorf("version 11: seven empty wallets count %d¢ (%d noted), want %d¢ as before", got, once, 7*WalletFallbackCents)
	}

	b = newBuilder().link("w2", "w")
	for i := range 4 {
		b.wallet("w", "0xe"+strconv.Itoa(i), 0).wallet("w2", "0xf"+strconv.Itoa(i), 0)
	}
	out = compute(t, b)
	a, _ := walletCents(out, "w")
	c, _ := walletCents(out, "w2")
	if a+c != WalletFallbackCents {
		t.Errorf("eight empty wallets over two keys of one root count %d¢, want %d", a+c, WalletFallbackCents)
	}

	// Two node roots, one empty wallet each: each keeps its floor.
	out = compute(t, newBuilder().wallet("x", "0xa", 0).wallet("y", "0xb", 0))
	for _, who := range []string{"x", "y"} {
		if got, _ := walletCents(out, who); got != WalletFallbackCents {
			t.Errorf("%s's one empty wallet counts %d¢, want %d", who, got, WalletFallbackCents)
		}
	}
}

// Value a wallet proves beyond the floor still stacks across distinct
// wallets (real balances cost real money); only the floor is counted once.
func TestWalletAssessedValueStacks(t *testing.T) {
	for _, c := range []struct {
		assessed []int64
		want     int64
	}{
		{[]int64{300, 200}, 500},  // both above the floor: both count in full
		{[]int64{300, 0, 0}, 300}, // the best wallet absorbs the floor; empty ones add 0
		{[]int64{30, 20}, 70},     // 30 lifts to the floor (50), 20 counts its own 20
		{[]int64{0, 20}, 50},      // 20 lifts to 50; the empty one adds 0
		{[]int64{20, 0}, 50},      // in either order
		{[]int64{1e9, 1e9}, 1200}, // two capped wallets: 600 each
	} {
		b := newBuilder()
		for i, v := range c.assessed {
			b.wallet("w", "0xv"+strconv.Itoa(i), v)
		}
		if got, _ := walletCents(compute(t, b), "w"); got != c.want {
			t.Errorf("wallets assessed %v count %d¢, want %d", c.assessed, got, c.want)
		}
	}
	row := DefaultParams().Proofs["wallet"]
	for _, c := range []struct{ assessed, value, price int64 }{{0, 0, 50}, {20, 20, 50}, {300, 300, 300}, {1e9, 600, 600}} {
		if v, p := AssessedValue(row, c.assessed), AssessedContribution(row, c.assessed); v != c.value || p != c.price {
			t.Errorf("assessed %d: value %d price %d, want %d %d", c.assessed, v, p, c.value, c.price)
		}
	}
	pow := DefaultParams().Proofs["pow"]
	if AssessedValue(pow, 1e6) != AssessedContribution(pow, 1e6) {
		t.Error("a saturating row has no floor to drop")
	}
}

// Version 12's body is version 11's: the rule is the only change.
func TestFloorOnceVersion(t *testing.T) {
	if !bytes.Equal(DefaultParams().Body(), BreakerParams().Body()) || DefaultVersion != FloorOnceVersion {
		t.Fatal("version 12 changes the parameter body")
	}
	for _, v := range []int64{BreakerVersion, FloorOnceVersion} {
		if _, err := ParseParams(v, DefaultParams().Body()); err != nil {
			t.Errorf("version %d: %v", v, err)
		}
	}
}

// walletFloorSnapshot is the golden_wallet_floor fixtures' snapshot under
// params: one root with seven empty wallets over two keys, one with real
// and empty wallets, two roots with one empty wallet each, and one empty
// wallet claimed by two accounts (which it joins into one root).
// scripts/trust/test_recompute.py recomputes both versions byte for byte.
func walletFloorSnapshot(params Params) Snapshot {
	b := newBuilder()
	b.snap.Params = params
	for _, name := range []string{"sybil", "sybil2", "rich", "x", "y", "p", "q"} {
		b.account(name, 60)
	}
	b.link("sybil2", "sybil")
	for i := range 4 {
		b.wallet("sybil", "0xs"+strconv.Itoa(i), 0)
	}
	for i := range 3 {
		b.wallet("sybil2", "0xt"+strconv.Itoa(i), 0)
	}
	b.wallet("rich", "0xr1", 300).wallet("rich", "0xr2", 200).wallet("rich", "0xr3", 0)
	b.wallet("x", "0xx", 0).wallet("y", "0xy", 0)
	b.wallet("p", "0xpq", 0).wallet("q", "0xpq", 0).wallet("q", "0xq2", 0)
	return b.snap
}

// Version 11 keeps each wallet's floor (its published runs recompute as
// they were: seven empty wallets on one root count 350¢); version 12 counts
// it once per root (50¢). Both fixtures recompute in recompute.py.
func TestGoldenWalletFloor(t *testing.T) {
	for _, c := range []struct {
		name   string
		params Params
		want   [6]int64 // sybil+sybil2, rich, x, y, p, q
	}{
		{"v11", BreakerParams(), [6]int64{350, 550, 50, 50, 50, 50}},
		// 0xpq, claimed by p and q, joins them into one root: the floor
		// counts once there (on p, the lower account at equal price); q's
		// claims of 0xpq and of its own empty 0xq2 add nothing.
		{"v12", DefaultParams(), [6]int64{50, 500, 50, 50, 50, 0}},
	} {
		t.Run(c.name, func(t *testing.T) {
			inPath := filepath.Join("testdata", "golden_wallet_floor_"+c.name+"_inputs.jsonl")
			outPath := filepath.Join("testdata", "golden_wallet_floor_"+c.name+"_output.json")
			snap := walletFloorSnapshot(c.params)
			var buf bytes.Buffer
			if err := snap.WriteJSONL(&buf); err != nil {
				t.Fatal(err)
			}
			out, err := Compute(context.Background(), snap)
			if err != nil {
				t.Fatal(err)
			}
			if *updateGolden {
				if err = os.WriteFile(inPath, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(outPath, out.Canonical(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(inPath)
			if err != nil || !bytes.Equal(raw, buf.Bytes()) {
				t.Fatalf("fixture inputs differ from walletFloorSnapshot (%v); rerun with -update", err)
			}
			want, err := os.ReadFile(outPath)
			if err != nil || !bytes.Equal(out.Canonical(), want) {
				t.Fatalf("golden wallet floor output changed (%v); if intended, rerun with -update", err)
			}
			a, _ := walletCents(out, "sybil")
			b, _ := walletCents(out, "sybil2")
			got := [6]int64{a + b, first(walletCents(out, "rich")), first(walletCents(out, "x")), first(walletCents(out, "y")),
				first(walletCents(out, "p")), first(walletCents(out, "q"))}
			if got != c.want || out.ParamsVersion != c.params.Version {
				t.Errorf("version %d: wallets count %v, want %v", out.ParamsVersion, got, c.want)
			}
			testRecomputePy(t, raw, want)
		})
	}
}

func first(a int64, _ int) int64 { return a }
