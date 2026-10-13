package services

import (
	"testing"
	"time"
)

func TestCurrentCongressTermBoundary(t *testing.T) {
	for _, c := range []struct {
		day  string
		want int64
	}{
		{"2026-10-13", 119}, {"2026-12-31", 119}, {"2027-01-01", 119}, {"2027-01-03", 119},
		{"2027-01-04", 120}, {"2027-06-01", 120}, {"2028-01-02", 120}, {"2029-01-04", 121},
	} {
		day, _ := time.Parse("2006-01-02", c.day)
		if got := currentCongress(day); got != c.want {
			t.Errorf("%s: congress %d, want %d", c.day, got, c.want)
		}
	}
}
