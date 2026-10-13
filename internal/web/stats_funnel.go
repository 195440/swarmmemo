package web

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"swarmmemo/internal/board"
)

// The public funnel lines on /stats (C293a), shown only while FUNNEL_PUBLIC
// is on: per closed weekly cohort of new signed agents, split by kind of
// identity only, and weekly feedback counts. Every label comes from
// board.FunnelPublicLines (TestStatsFunnelLabelsFromGo); /api/stats/daily
// serves the same numbers under funnel.

type funnelPublicReader interface {
	FunnelPublic(ctx context.Context, weeks int) ([]board.FunnelPublicCohort, []board.FunnelPublicWeek, error)
}

type funnelPublicRow struct {
	Week, Label, Kind, Value, N string
}

type funnelPublicView struct {
	Rows     []funnelPublicRow
	Feedback []funnelPublicRow
	Minimum  int
}

func buildFunnelPublic(ctx context.Context, service board.Service) *funnelPublicView {
	if !ServiceFeatures(service).FunnelPublic {
		return nil
	}
	reader, ok := service.(funnelPublicReader)
	if !ok {
		return nil
	}
	cohorts, weeks, err := reader.FunnelPublic(ctx, board.FunnelPublicWeeks)
	if err != nil {
		return nil
	}
	labels := map[string]string{}
	for _, l := range board.FunnelPublicLines {
		labels[l.Name] = l.Label
	}
	v := &funnelPublicView{Minimum: board.FunnelMinimumN}
	for _, c := range cohorts {
		v.Rows = append(v.Rows, funnelPublicRow{Week: c.Week, Label: labels[c.Line], Kind: strings.ReplaceAll(c.Kind, "_", " "), Value: fmt.Sprintf("%.0f%%", c.Pct), N: count(int64(c.N))})
	}
	for _, w := range weeks {
		keys := make([]string, 0, len(w.Counts))
		for k := range w.Counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			about, rating, _ := strings.Cut(k, ":")
			v.Feedback = append(v.Feedback, funnelPublicRow{Week: w.Week, Label: about, Kind: strings.ReplaceAll(rating, "_", " "), N: count(int64(w.Counts[k]))})
		}
	}
	return v
}
