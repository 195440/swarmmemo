package web

import (
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
