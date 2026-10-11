package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

// C176: "enabled" in TOPUP_CONFIG governs credit top-ups only; agent
// offerings load the shared fields whatever it says, and a malformed shared
// field keeps both off.
func TestTopupConfigEnabledGovernsTopupsOnly(t *testing.T) {
	const base = `{"schema":1,"enabled":true,"network":"eip155:8453","asset":"0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913","asset_name":"USD Coin","asset_version":"2","pay_to":"0x1111111111111111111111111111111111111111","facilitator_url":"https://facilitator.example.com"}`
	ledger := board.Features{Ledger: board.LedgerOn}
	load := func(t *testing.T, config string) {
		path := filepath.Join(t.TempDir(), "topup.json")
		if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TOPUP_CONFIG", path)
		t.Setenv("OFFERINGS", "on")
	}
	disabled := strings.Replace(base, `"enabled":true`, `"enabled":false`, 1)
	for _, c := range []struct {
		name              string
		config            string
		topups, offerings bool
	}{
		{"enabled", base, true, true},
		{"disabled", disabled, false, true},
		{"disabled without pay_to", strings.Replace(disabled, `"pay_to":"0x1111111111111111111111111111111111111111",`, "", 1), false, true},
		{"enabled, bad network", strings.Replace(base, "eip155:8453", "base", 1), false, false},
		{"disabled, bad network", strings.Replace(disabled, "eip155:8453", "base", 1), false, false},
		{"disabled, http facilitator", strings.Replace(disabled, "https://", "http://", 1), false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			load(t, c.config)
			if got := topupFromEnvironment(ledger) != nil; got != c.topups {
				t.Errorf("top-ups on = %v, want %v", got, c.topups)
			}
			if got := offeringsFromEnvironment() != nil; got != c.offerings {
				t.Errorf("offerings on = %v, want %v", got, c.offerings)
			}
		})
	}
	// OFFERINGS unset leaves offerings off whatever the config says.
	load(t, disabled)
	t.Setenv("OFFERINGS", "")
	if offeringsFromEnvironment() != nil {
		t.Error("offerings on without OFFERINGS=on")
	}
}
