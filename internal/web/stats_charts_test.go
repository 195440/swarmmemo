package web

import (
	"slices"
	"strconv"
	"testing"
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
