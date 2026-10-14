package web

import (
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Six weekly date labels overlap at 320px: every other one is minor, so a
// phone shows three; five or fewer stay whole (reported by paul-csv-agent).
func TestAxisMinorWhenCrowded(t *testing.T) {
	weekly := func(i int) string {
		if i%7 == 0 {
			return "d" + strconv.Itoa(i)
		}
		return ""
	}
	six := axisFor(36, weekly)
	if len(six) != 6 || six[0].Minor || !six[1].Minor || six[2].Minor || !six[5].Minor {
		t.Fatalf("six labels %+v", six)
	}
	for _, l := range axisFor(29, weekly) {
		if l.Minor {
			t.Fatalf("five labels stay whole: %+v", l)
		}
	}
}

// The hourly axis dates the first tick of each UTC day even when the
// six-hour stride misses midnight, and a dated tick is never minor, so a
// phone always shows the day (reported by survivor).
func TestHourAxisDatesEachDay(t *testing.T) {
	for _, end := range []string{"2026-10-15T03:00:00Z", "2026-10-15T00:00:00Z", "2026-10-15T13:00:00Z"} {
		last, _ := time.Parse(time.RFC3339, end)
		starts := make([]time.Time, statsHours)
		for i := range starts {
			starts[i] = last.Add(time.Duration(i-statsHours+1) * time.Hour)
		}
		axis := hourAxis(starts, 6)
		days := map[string]bool{}
		for i := 0; i < len(starts); i++ {
			if (len(starts)-1-i)%6 == 0 {
				days[starts[i].Format("2 Jan")] = true
			}
		}
		dated := map[string]bool{}
		for _, l := range axis {
			day := ""
			for d := range days {
				if strings.HasPrefix(l.Text, d) {
					day = d
				}
			}
			if day != "" {
				if dated[day] || !l.Major || l.Minor {
					t.Fatalf("end %s: dated tick %+v (axis %+v)", end, l, axis)
				}
				dated[day] = true
			} else if l.Major {
				t.Fatalf("end %s: undated major %+v", end, l)
			}
		}
		if len(dated) != len(days) {
			t.Fatalf("end %s: dated %v, ticked days %v: %+v", end, dated, days, axis)
		}
		shown := 0
		for _, l := range axis {
			if !l.Minor {
				shown++
			}
		}
		if len(axis) <= axisLabelsNarrow || shown > axisLabelsNarrow {
			t.Fatalf("end %s: %d labels, %d shown on a phone: %+v", end, len(axis), shown, axis)
		}
	}
	last, _ := time.Parse(time.RFC3339, "2026-10-15T03:00:00Z")
	starts := make([]time.Time, statsHours)
	for i := range starts {
		starts[i] = last.Add(time.Duration(i-statsHours+1) * time.Hour)
	}
	got := []string{}
	for _, l := range hourAxis(starts, 6) {
		got = append(got, l.Text)
	}
	if want := []string{"13 Oct 09:00", "15:00", "21:00", "14 Oct 03:00", "09:00", "15:00", "21:00", "15 Oct 03:00"}; !slices.Equal(got, want) {
		t.Fatalf("labels %q, want %q", got, want)
	}
}

// The bands follow the displayed range, not all-time counts: a channel busy
// in the range is drawn even if it is quiet all-time (outside report 150396fd).
func TestBandChannelsFollowRange(t *testing.T) {
	order := []string{"A", "B", "C", "D", "E", "F"} // all-time order
	rangeVia := map[string]int64{"A": 1, "B": 1, "C": 1, "D": 1, "E": 1, "F": 100}
	got := bandChannels(order, rangeVia, 5)
	if want := []string{"F", "A", "B", "C", "D"}; !slices.Equal(got, want) {
		t.Fatalf("bands %v, want %v", got, want)
	}
	if got := bandChannels(order, map[string]int64{"C": 2}, 5); !slices.Equal(got, []string{"C"}) {
		t.Fatalf("empty channels drawn: %v", got)
	}
}
