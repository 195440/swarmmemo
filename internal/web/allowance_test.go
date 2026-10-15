package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
)

// The page and API parity tests are in internal/httpapi
// (stats_allowance_test.go), where both surfaces are served by one server.
// These cover the normalising functions and the flags-off guarantees.

func decode(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAllowanceAnswerReadsListsKeyedMapsAndStanding(t *testing.T) {
	listed, err := AllowanceAnswerFrom(decode(t, `{"band":"proven","standing":{"band":"proven","cents":250},"resources":[{"resource":"credit","remaining":3},{"resource":"post_bytes","remaining":7,"floor_part":5,"standing_part":2}]}`))
	if err != nil || listed.Band != "proven" || listed.StandingCents != 250 || listed.Resources[0].Resource != "post_bytes" || listed.Resources[0].StandingPart != 2 || listed.Resources[1].Remaining != 3 {
		t.Fatalf("listed: %+v %v", listed, err)
	}
	keyed, err := AllowanceAnswerFrom(decode(t, `{"standing":{"band":"anonymous","reason":"one share per network"},"resources":{"memory_bytes":{"remaining":1},"post_bytes":{"remaining":2}}}`))
	if err != nil || keyed.Band != "anonymous" || keyed.Reason != "one share per network" || keyed.Resources[0].Resource != "post_bytes" || keyed.Resources[1].Resource != "memory_bytes" {
		t.Fatalf("keyed: %+v %v", keyed, err)
	}
	if _, err := AllowanceAnswerFrom(map[string]any{}); err == nil {
		t.Fatal("an answer without a band was accepted")
	}
}

func TestShareRuleNormalisesAndDerives(t *testing.T) {
	w := &ShareRuleStats{}
	if err := remarshal(decode(t, `{"resources":[{"resource":"credit","day":"2026-09-28"},{"resource":"post_bytes","day":20359,
	  "slices":[{"slice":"standing","size":120,"drawn":30,"lent":6},{"slice":"floor","size":50,"drawn":50},{"slice":"grant","size":0}],"anonymous":{"drawn":9,"claimants":2}}]}`), w); err != nil {
		t.Fatal(err)
	}
	normalizeShareRule(w, board.Features{Ledger: board.LedgerShadow})
	r := w.Resources[0]
	if r.Resource != "post_bytes" || r.Day != "2025-09-28" || w.Resources[1].Day != "2026-09-28" {
		t.Fatalf("resource order or day: %+v", r)
	}
	if r.Slices[0].Slice != "grant" || r.Slices[0].FillPPM != 0 || r.Slices[1].FillPPM != 1_000_000 || r.Slices[2].FillPPM != 300000 || r.Issued != 86 || r.Unit != "byte" || r.Anonymous.Claimants != 2 {
		t.Fatalf("derived numbers: %+v", r)
	}
	if w.Resources[1].Slices == nil || w.Ledger != "shadow" || w.Rule != ShareRuleSentence || w.History == nil || w.Services == nil || w.Levers == nil || w.Transfers.Resource != "post_bytes" {
		t.Fatalf("defaults: %+v", w)
	}
}

func TestTrustDistributionBins(t *testing.T) {
	ts, err := trustStatsFrom(decode(t, `{"cents_log10_bins":[4,0,2],"bands":{"proven":6},"above_vote_floor":5,"vote_floor_cents":50}`), board.Features{Trust: board.TrustOn})
	if err != nil || ts.Mode != "on" || ts.Accounts != 6 || len(ts.CentsLog10) != 3 || len(ts.Bands) != 3 || ts.Bands[1].Band != "proven" || ts.Bands[1].Accounts != 6 || ts.AboveVoteFloor != 5 || ts.VoteFloorCents != 50 {
		t.Fatalf("%+v %v", ts, err)
	}
	if binLabel(ts.CentsLog10[0]) != "under 10¢" || binLabel(ts.CentsLog10[2]) != "100¢ to 1,000¢" {
		t.Fatal("bin labels")
	}
	if units("credit", 1) != "1 credit" || units("credit", 2500) != "2,500 credits" || units("post_bytes", 4<<20) != "4 MB" || ppmPercent(2500) != "0%" || ppmPercent(52083) != "5%" {
		t.Fatal("formatting")
	}
}

func TestStandingGlance(t *testing.T) {
	answer, err := AllowanceAnswerFrom(decode(t, `{"band":"signed","resources":{"post_bytes":{"entitlement":4194304,"remaining":4063232}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := templates.ExecuteTemplate(&b, "standing-glance", &standingView{Agent: "abc", Allowance: agentAllowanceFrom(answer), TrustOn: true}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{">Allowance today</span>: signed band", `data-key="resources.0.remaining" data-value="4063232">3.9 MB</span> left`, `href="/agent/abc#allowance">Allowance and trust →`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("glance lacks %q: %s", want, b.String())
		}
	}
}

// With every flag off nothing is read and nothing renders.
func TestStandingOffReadsNothing(t *testing.T) {
	calls := 0
	execute := func(board.Command) (board.Result, error) { calls++; return board.Result{}, nil }
	if v := loadStanding(execute, board.Features{}, "abc", true); v != nil || calls != 0 {
		t.Fatalf("flags off: %v after %d reads", v, calls)
	}
	if s, err := ReadAllowanceStats(context.Background(), &testService{}, board.Features{}, 7, time.Now()); s != nil || err != nil {
		t.Fatal("stats with every flag off")
	}
	if buildAllowanceSection(context.Background(), &testService{}, time.Now()) != nil {
		t.Fatal("/stats allowance section with every flag off")
	}
	if Quickstart("") != QuickstartFor("", board.Features{Ledger: board.LedgerShadow}) || !strings.Contains(QuickstartFor("", board.Features{Ledger: board.LedgerOn}), ShareRuleSentence) {
		t.Fatal("the quickstart explains the allowance only while the ledger decides")
	}
	if renderQuickstart(false, nil) != quickstartHTML || !strings.Contains(string(renderQuickstart(true, nil)), "next.allowance") {
		t.Fatal("quickstart HTML")
	}
}
