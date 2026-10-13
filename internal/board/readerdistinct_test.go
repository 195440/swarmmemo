package board

import (
	"context"
	"math"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestReaderSketchEstimates(t *testing.T) {
	h := &ReaderSketch{}
	if h.Estimate() != 0 {
		t.Fatal("an empty sketch must estimate 0")
	}
	for i := 0; i < 100; i++ {
		h.Add("h:same")
	}
	if got := h.Estimate(); got != 1 {
		t.Fatalf("one id added 100 times = %d, want 1", got)
	}
	h.Add("h:other")
	if got := h.Estimate(); got != 2 {
		t.Fatalf("two ids = %d, want 2", got)
	}
	for _, n := range []int{50, 1000, 20000} {
		h := &ReaderSketch{}
		for i := 0; i < n; i++ {
			h.Add("a:" + strconv.Itoa(i))
		}
		got := h.Estimate()
		if math.Abs(float64(got-int64(n)))/float64(n) > 0.07 {
			t.Fatalf("%d ids estimated %d", n, got)
		}
		// Packing round-trips, and merging a sketch with itself changes nothing.
		raw := h.MarshalBinary()
		if len(raw) != 1536 {
			t.Fatalf("packed sketch is %d bytes", len(raw))
		}
		back, err := UnmarshalReaderSketch(raw)
		if err != nil || *back != *h {
			t.Fatal("sketch does not round-trip")
		}
		back.Merge(h)
		if back.Estimate() != got {
			t.Fatal("self-merge changed the estimate")
		}
	}
}

func TestReaderSketchesMergePersistAndExpire(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "d.db"), Config{ServiceID: "swarmmemo.com"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	ctx := context.Background()
	day := "2026-10-13"
	key := ReaderMetricKey("llms_txt", "other")
	anyKey := ReaderMetricKey(ReaderAnyMetric, "other")
	first := &ReaderSketch{}
	first.Add("h:1")
	first.Add("h:2")
	if err = store.MergeReaderSketches(ctx, day, map[string]*ReaderSketch{key: first, anyKey: first}); err != nil {
		t.Fatal(err)
	}
	// A second flush (or a restart's) adds one new reader and repeats one.
	second := &ReaderSketch{}
	second.Add("h:2")
	second.Add("h:3")
	if err = store.MergeReaderSketches(ctx, day, map[string]*ReaderSketch{key: second}); err != nil {
		t.Fatal(err)
	}
	stats, err := store.ReadDailyStats(ctx, now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := stats[1].Distinct[key]; got != 3 {
		t.Fatalf("distinct after two merges = %d, want 3", got)
	}
	if got := stats[1].Distinct[anyKey]; got != 2 {
		t.Fatalf("entry_points = %d, want 2", got)
	}
	if len(stats[1].Reads) != 0 || len(stats[0].Distinct) != 0 {
		t.Fatalf("distinct estimates leaked into reads or another day: %+v", stats)
	}
	sketches, err := store.ReadReaderSketches(ctx, day)
	if err != nil || sketches[key] == nil || sketches[key].Estimate() != 3 {
		t.Fatalf("stored sketches: %v %v", sketches, err)
	}
	if store.MergeReaderSketches(ctx, day, map[string]*ReaderSketch{"h:1:other": first}) == nil || store.MergeReaderSketches(ctx, "2026-1-1", nil) == nil {
		t.Fatal("an unknown metric or a malformed day must be refused")
	}

	// Past the stats window the sketch is deleted and the estimate kept.
	now = now.AddDate(0, 0, ActivityDays)
	if err = store.MergeReaderSketches(ctx, now.Format("2006-01-02"), map[string]*ReaderSketch{key: second}); err != nil {
		t.Fatal(err)
	}
	if sketches, _ = store.ReadReaderSketches(ctx, day); len(sketches) != 0 {
		t.Fatal("a sketch older than the stats window survived")
	}
	var value int64
	if err = store.db.QueryRow("SELECT value FROM counters WHERE scope=?", readerDistinctPrefix+day+":"+key).Scan(&value); err != nil || value != 3 {
		t.Fatalf("the old day's estimate was not kept: %d %v", value, err)
	}
}
