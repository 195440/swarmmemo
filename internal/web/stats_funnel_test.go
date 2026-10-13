package web

import (
	"context"
	"os"
	"strings"
	"testing"

	"swarmmemo/internal/board"
)

type funnelPublicService struct {
	board.Service
}

func (funnelPublicService) Features() board.Features { return board.Features{FunnelPublic: true} }

func (funnelPublicService) FunnelPublic(context.Context, int) ([]board.FunnelPublicCohort, []board.FunnelPublicWeek, error) {
	var out []board.FunnelPublicCohort
	for _, l := range board.FunnelPublicLines {
		out = append(out, board.FunnelPublicCohort{Week: "2026-09-28", Kind: "own_key", Line: l.Name, N: 12, Pct: 25})
	}
	return out, []board.FunnelPublicWeek{{Week: "2026-09-28", Counts: map[string]int{"general:useful": 4}}}, nil
}

// TestStatsFunnelLabelsFromGo: every label of the public funnel lines is
// board.FunnelPublicLines'; the template restates none of them.
func TestStatsFunnelLabelsFromGo(t *testing.T) {
	v := buildFunnelPublic(context.Background(), funnelPublicService{})
	if v == nil || len(v.Rows) != len(board.FunnelPublicLines) || len(v.Feedback) != 1 {
		t.Fatalf("view %+v", v)
	}
	tmpl, err := os.ReadFile("templates/stats.html")
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range board.FunnelPublicLines {
		if v.Rows[i].Label != l.Label {
			t.Errorf("row %d label %q, want board's %q", i, v.Rows[i].Label, l.Label)
		}
		if strings.Contains(string(tmpl), l.Label) {
			t.Errorf("templates/stats.html restates %q; it must come from board.FunnelPublicLines", l.Label)
		}
	}
	if buildFunnelPublic(context.Background(), struct{ board.Service }{}) != nil {
		t.Fatal("funnel lines without FUNNEL_PUBLIC")
	}
}
