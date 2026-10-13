package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"swarmmemo/internal/board"
)

// The operator's funnel (board/funnel.go, C293a): GET /admin/funnel, behind
// the admin token like every /admin route, answers the same report
// `swarmmemo funnel --json` prints (board.FunnelReport). Never public.

type funnelPublicStore interface {
	FunnelPublic(ctx context.Context, weeks int) ([]board.FunnelPublicCohort, []board.FunnelPublicWeek, error)
}

type funnelStore interface {
	FunnelReport(context.Context, board.FunnelQuery) (*board.FunnelReport, error)
}

// funnelAdmin serves GET /admin/funnel after the admin token was checked;
// false leaves the request to the admin handler. Parameters: from and to
// (YYYY-MM-DD, UTC; default the last 45 days), split (one of
// board.FunnelSplits), organic=1.
func (s *Server) funnelAdmin(w http.ResponseWriter, r *http.Request) bool {
	store, ok := s.service.(funnelStore)
	if !ok || r.URL.Path != "/admin/funnel" {
		return false
	}
	if !readMethod(r) {
		methodError(w)
		return true
	}
	q := r.URL.Query()
	for key := range q {
		switch key {
		case "from", "to", "split", "organic", "cohort":
		default:
			writeError(w, bad("Parameters: from, to (YYYY-MM-DD), split ("+strings.Join(board.FunnelSplits, ", ")+"), organic (0 or 1), cohort (week)."))
			return true
		}
	}
	day := func(name string) (int64, bool) {
		v := q.Get(name)
		if v == "" {
			return 0, true
		}
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return 0, false
		}
		if name == "to" {
			return t.Unix() + 86399, true
		}
		return t.Unix(), true
	}
	from, ok1 := day("from")
	to, ok2 := day("to")
	organic := q.Get("organic")
	if !ok1 || !ok2 || (organic != "" && organic != "0" && organic != "1") || (q.Get("cohort") != "" && q.Get("cohort") != "week") {
		writeError(w, bad("from and to are YYYY-MM-DD, organic 0 or 1, cohort week."))
		return true
	}
	rep, err := store.FunnelReport(r.Context(), board.FunnelQuery{From: from, To: to, Split: q.Get("split"), Organic: organic == "1"})
	if err != nil {
		writeError(w, bad(err.Error()))
		return true
	}
	jsonResponse(w, 200, map[string]any{"ok": true, "data": rep})
	return true
}
