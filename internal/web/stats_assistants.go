package web

import (
	"context"
	"sort"
	"time"

	"swarmmemo/internal/board"
)

// The /stats "Pages assistants fetch" table sums board.ReadAssistantFetches
// over the last ClientStatsDays UTC days: the published assistant_fetches
// objects of /api/stats/daily?days=7 added up. It shows each family's
// AssistantPagesShown largest paths; each cell carries its family and path
// (data-key FAMILY.PATH, or FAMILY.other for the rest) and raw number, so
// the parity test holds the page to the API.

type assistantFetchReader interface {
	ReadAssistantFetches(ctx context.Context, end time.Time, days int) ([]board.AssistantDay, error)
}

// AssistantPagesShown is how many paths per family the table names.
const AssistantPagesShown = 10

type assistantsView struct {
	Days     int
	Minimum  int // board.ClientCountMinimum
	Families []assistantFamilyRows
}

type assistantFamilyRows struct {
	Family string

	Rows []assistantPathRow
}

type assistantPathRow struct {
	Path string // "" for the rest
	Cell cell
}

func buildAssistantStats(ctx context.Context, service board.Service, now time.Time) *assistantsView {
	reader, ok := service.(assistantFetchReader)
	if !ok {
		return nil
	}
	days, err := reader.ReadAssistantFetches(ctx, now, ClientStatsDays)
	if err != nil {
		return nil
	}
	paths := map[string]map[string]int64{}
	other := map[string]int64{}
	for _, d := range days {
		for _, f := range d.Families {
			if paths[f.Family] == nil {
				paths[f.Family] = map[string]int64{}
			}
			for _, p := range f.Paths {
				paths[f.Family][p.Name] += p.Count
			}
			other[f.Family] += f.Other
		}
	}
	v := &assistantsView{Days: ClientStatsDays, Minimum: board.ClientCountMinimum}
	for _, family := range board.AssistantFetchFamilies {
		named, ok := paths[family]
		if !ok {
			continue
		}
		list := make([]board.ReferrerCount, 0, len(named))
		total := other[family]
		for path, n := range named {
			list = append(list, board.ReferrerCount{Name: path, Count: n})
			total += n
		}
		if total == 0 {
			continue
		}
		sort.Slice(list, func(a, b int) bool {
			return list[a].Count > list[b].Count || list[a].Count == list[b].Count && list[a].Name < list[b].Name
		})
		rows := assistantFamilyRows{Family: family}
		rest := other[family]
		for i, p := range list {
			if i >= AssistantPagesShown {
				rest += p.Count
				continue
			}
			rows.Rows = append(rows.Rows, assistantPathRow{Path: p.Name, Cell: cell{Key: family + "." + p.Name, Value: p.Count, Text: count(p.Count)}})
		}
		if rest > 0 {
			rows.Rows = append(rows.Rows, assistantPathRow{Cell: cell{Key: family + ".other", Value: rest, Text: count(rest)}})
		}
		v.Families = append(v.Families, rows)
	}
	return v
}
