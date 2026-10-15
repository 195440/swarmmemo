package web

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"swarmmemo/internal/board"
)

// fixedActivity serves a made-up activity summary over a real store, so the
// tiles can be checked against known whole days.
type fixedActivity struct {
	*board.Store
	a *board.Activity
}

func (f fixedActivity) ReadActivity(context.Context) (*board.Activity, error) { return f.a, nil }

// A board born 39 days ago: ten posts and one new agent on every whole day,
// one post so far today.
func youngActivity() *board.Activity {
	now := time.Date(2026, 10, 13, 15, 20, 0, 0, time.UTC)
	a := &board.Activity{Generated: now, Via: map[string]int64{}, Totals: map[string]int64{}, AgentsFull7: 5, AgentsFull7Prior: 4, Agents7: 9, Agents7Prior: 1}
	start := now.Truncate(24*time.Hour).AddDate(0, 0, -(board.ActivityDays - 1))
	a.Days = make([]board.ActivityBucket, board.ActivityDays)
	for i := range a.Days {
		a.Days[i].Start = start.AddDate(0, 0, i)
		if back := board.ActivityDays - 1 - i; back == 0 {
			a.Days[i].Posts.Signed = 1
		} else if back <= 39 {
			a.Days[i].Posts.Signed, a.Days[i].NewAgents, a.Days[i].Replies = 10, 1, 2
		}
	}
	hour := now.Truncate(time.Hour).Add(-(board.ActivityHours - 1) * time.Hour)
	a.Hours = make([]board.ActivityBucket, board.ActivityHours)
	for i := range a.Hours {
		a.Hours[i].Start = hour.Add(time.Duration(i) * time.Hour)
		a.Hours[i].Posts.Anonymous = int64(i % 3)
	}
	return a
}

// The tiles compare whole UTC days (today, still filling, left out of both
// periods), say so, and show no change while the period before predates the
// board (Hugo: "196 +1207%, 30 days vs 15").
func TestStatsTilesCompareWholeDays(t *testing.T) {
	f := newArticleFixture(t)
	v, err := buildStats(context.Background(), fixedActivity{f.store, youngActivity()}, false)
	if err != nil {
		t.Fatal(err)
	}
	tiles := map[string]headTile{}
	for _, h := range v.Headline {
		tiles[h.Label] = h
	}
	if p := tiles["Posts"]; p.Value != "70" || p.Delta != "±0%" || p.Note != "7 full days, vs 70" {
		t.Errorf("posts tile %+v, want 70 against 70 over whole days", p)
	}
	if a := tiles["Active agents"]; a.Value != "5" || a.Delta != "+25%" || a.Note != "7 full days, vs 4" {
		t.Errorf("active agents tile %+v, want the whole-day counts", a)
	}
	if n := tiles["New agents"]; n.Value != "30" || n.Delta != "" || n.Note != "30 full days" || !strings.Contains(n.Tip, "younger than both periods") {
		t.Errorf("new agents tile %+v: the 30 days before predate the board, so no change", n)
	}
	if !strings.Contains(tiles["Replies"].Tip, "today left out") {
		t.Errorf("the tooltip does not say today is left out: %q", tiles["Replies"].Tip)
	}
	// The sparkline is the last 30 whole days: flat at ten, no drop at the end.
	if spark := string(tiles["Posts"].Spark); strings.Contains(spark, "100.0,22.0") || !strings.Contains(spark, "100.0,2.0") {
		t.Errorf("sparkline ends on the partial day: %s", spark)
	}
	if v.Hourly {
		t.Error("the day view is marked hourly")
	}
}

// The last point of a line is drawn apart: a dashed segment from the last
// whole x, no area under it, named in the legend, while the readout keeps
// every value.
func TestLineChartDrawsPartialLastPoint(t *testing.T) {
	c := buildLineChart("Line", "Note.", []string{"a", "b", "c"}, nil, todaySoFar, []chartSeries{
		{Class: "s-1", Label: "Posts", Values: []int64{1, 2, 3}, Format: count},
	})
	svg := string(c.SVG)
	for _, want := range []string{
		`<polygon class="area s-1" points="0,100 0.0,80.0 500.0,60.0 500.0,100"`,
		`<polyline class="line s-1" points="0.0,80.0 500.0,60.0"`,
		`data-values="1|2|3" data-ys="80.0|60.0|40.0"`,
		`<polyline class="line partial s-1" points="500.0,60.0 1000.0,40.0" fill="none"`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("chart lacks %q in %s", want, svg)
		}
	}
	if len(c.Legend) != 1 || c.Legend[0].Label != todaySoFar || !strings.Contains(c.Summary, "today so far, is dashed") {
		t.Errorf("legend %+v, summary %q", c.Legend, c.Summary)
	}
	// The partial segment carries no values, so the readout reads each series once.
	if n := strings.Count(svg, "data-values="); n != 1 {
		t.Errorf("%d series carry values, want 1", n)
	}
}

// /stats?bucket=hour draws the Activity chart per UTC hour over the last
// 48; the reads chart stays daily and says so.
func TestStatsHourlyView(t *testing.T) {
	f := newArticleFixture(t)
	f.post(board.Command{Room: "lobby", Page: "main", Text: "hello hours"})
	w := f.get("/stats?bucket=hour")
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("/stats?bucket=hour: %d", w.Code)
	}
	for _, want := range []string{
		`<a href="/stats#stats-activity">Day</a><a href="/stats?bucket=hour#stats-activity" aria-current="true">Hour</a>`,
		"Posts and active agents per hour", "Per UTC hour, the last 48.", ">This hour so far</span>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/stats?bucket=hour lacks %q", want)
		}
	}
	xs := regexp.MustCompile(`data-x="([^"]*)"`).FindStringSubmatch(body)
	if xs == nil {
		t.Fatal("no chart")
	}
	labels := strings.Split(xs[1], "|")
	if len(labels) != statsHours || !strings.HasSuffix(labels[0], ":00 UTC") {
		t.Fatalf("hourly chart has %d x labels, first %q", len(labels), labels[0])
	}
	if !regexp.MustCompile(`data-label="Posts" data-values="[0|]*\|1"`).MatchString(body) {
		t.Error("the current hour lacks its post")
	}
	// Anything else is the day view.
	if day := f.get("/stats?bucket=week").Body.String(); strings.Contains(day, "Posts and active agents per hour") || !strings.Contains(day, `<a href="/stats#stats-activity" aria-current="true">Day</a>`) {
		t.Error("an unknown bucket is not the day view")
	}
}
