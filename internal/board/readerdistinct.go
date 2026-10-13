package board

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strings"
	"time"
)

// Distinct readers per entry point, per UTC day, without storing who read.
//
// The HTTP layer turns each counted request into a reader id: "a:" and the
// account for a request that names its agent, otherwise "h:" and 16 hex of
// HMAC-SHA256(daySalt, networkPrefix || 0x00 || userAgent), where the salt is
// random per UTC day and lives in memory only. The id itself is never stored:
// it is added to a HyperLogLog sketch (ReaderSketch), and only the sketch's
// registers reach storage. A sketch cannot be enumerated or inverted, and with
// the salt gone a day's ids cannot be recomputed or linked to another day's.
//
// Storage needs no schema change:
//
//	meta     reader_hll:YYYY-MM-DD:METRIC:CLASS       base64 of the packed sketch, kept ActivityDays
//	counters reader_distinct:YYYY-MM-DD:METRIC:CLASS  the sketch's estimate, kept like the request counts
//
// METRIC is one of ReaderMetrics or ReaderAnyMetric (the union of all entry
// points); CLASS is crawler or other. Each flush merges the new registers into
// the day's stored sketch and rewrites the estimate, so a restart neither loses
// nor double counts a reader whose id is stable (a named agent). Sketches older
// than ActivityDays are deleted; only their estimates remain.

// ReaderAnyMetric is the distinct-reader key for any entry point: a reader of
// llms.txt and skill.md on one day is one reader of the entry points.
const ReaderAnyMetric = "entry_points"

const (
	readerSketchPrefix   = "reader_hll:"
	readerDistinctPrefix = "reader_distinct:"
	// readerSketchP is the HyperLogLog precision: 2^11 registers of 6 bits
	// (1,536 bytes packed), a standard error of 1.04/sqrt(2048), about 2.3%.
	readerSketchP     = 11
	readerSketchM     = 1 << readerSketchP
	readerSketchBytes = readerSketchM * 6 / 8
)

// ReaderSketchError is the sketch's relative standard error, for notes.
const ReaderSketchError = "2.3%"

// ReaderSketch is a HyperLogLog sketch of reader ids.
type ReaderSketch [readerSketchM]uint8

// Add records one reader id.
func (h *ReaderSketch) Add(id string) {
	sum := sha256.Sum256([]byte(id))
	x := binary.BigEndian.Uint64(sum[:8])
	index := x >> (64 - readerSketchP)
	rank := uint8(bits.LeadingZeros64(x<<readerSketchP|1<<(readerSketchP-1)) + 1)
	if rank > h[index] {
		h[index] = rank
	}
}

// Merge folds other into h (the union of both sets).
func (h *ReaderSketch) Merge(other *ReaderSketch) {
	for i, r := range other {
		if r > h[i] {
			h[i] = r
		}
	}
}

// Estimate is the approximate number of distinct ids added, exact in practice
// for a handful and within a few percent beyond.
func (h *ReaderSketch) Estimate() int64 {
	m := float64(readerSketchM)
	sum, zeros := 0.0, 0
	for _, r := range h {
		sum += math.Ldexp(1, -int(r))
		if r == 0 {
			zeros++
		}
	}
	if zeros == readerSketchM {
		return 0
	}
	estimate := 0.7213 / (1 + 1.079/m) * m * m / sum
	if estimate <= 2.5*m && zeros > 0 {
		estimate = m * math.Log(m/float64(zeros)) // linear counting
	}
	return int64(math.Round(estimate))
}

// MarshalBinary packs the registers at 6 bits each.
func (h *ReaderSketch) MarshalBinary() []byte {
	out := make([]byte, readerSketchBytes)
	for i := 0; i < readerSketchM; i += 4 {
		v := uint32(h[i]&63)<<18 | uint32(h[i+1]&63)<<12 | uint32(h[i+2]&63)<<6 | uint32(h[i+3]&63)
		j := i / 4 * 3
		out[j], out[j+1], out[j+2] = byte(v>>16), byte(v>>8), byte(v)
	}
	return out
}

// UnmarshalReaderSketch reverses MarshalBinary.
func UnmarshalReaderSketch(raw []byte) (*ReaderSketch, error) {
	if len(raw) != readerSketchBytes {
		return nil, errors.New("reader sketch: wrong length")
	}
	h := &ReaderSketch{}
	for i := 0; i < readerSketchM; i += 4 {
		j := i / 4 * 3
		v := uint32(raw[j])<<16 | uint32(raw[j+1])<<8 | uint32(raw[j+2])
		h[i], h[i+1], h[i+2], h[i+3] = uint8(v>>18&63), uint8(v>>12&63), uint8(v>>6&63), uint8(v&63)
	}
	return h, nil
}

// ValidReaderDistinctKey reports whether key may hold a distinct-reader
// sketch: a ReaderMetricKey of a reader metric or of ReaderAnyMetric.
func ValidReaderDistinctKey(key string) bool {
	if validReaderKey(key) {
		return true
	}
	metric, class, _ := strings.Cut(key, ":")
	return metric == ReaderAnyMetric && (class == "crawler" || class == "other")
}

func validReaderDay(day string) bool {
	_, err := time.Parse("2006-01-02", day)
	return err == nil && readerDay.MatchString(day)
}

// MergeReaderSketches folds the sketches of one UTC day into the stored ones,
// rewrites each key's estimate, and deletes sketches older than ActivityDays.
func (s *Store) MergeReaderSketches(ctx context.Context, day string, sketches map[string]*ReaderSketch) error {
	if !validReaderDay(day) {
		return fmt.Errorf("reader sketches: invalid day")
	}
	for key, h := range sketches {
		if !ValidReaderDistinctKey(key) || h == nil {
			return fmt.Errorf("reader sketches: invalid metric")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, h := range sketches {
		merged := *h
		stored, err := readSketch(ctx, tx, readerSketchPrefix+day+":"+key)
		if err != nil {
			return err
		}
		if stored != nil {
			merged.Merge(stored)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
			readerSketchPrefix+day+":"+key, base64.StdEncoding.EncodeToString(merged.MarshalBinary())); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO counters(scope,value) VALUES(?,?) ON CONFLICT(scope) DO UPDATE SET value=excluded.value",
			readerDistinctPrefix+day+":"+key, merged.Estimate()); err != nil {
			return err
		}
	}
	// Only the estimates outlive the stats window.
	cutoff := s.now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -(ActivityDays - 1)).Format("2006-01-02")
	if _, err = tx.ExecContext(ctx, "DELETE FROM meta WHERE key>=? AND key<?", readerSketchPrefix, readerSketchPrefix+cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

func readSketch(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, key string) (*ReaderSketch, error) {
	var encoded string
	err := q.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", key).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, nil // unreadable: start the day's sketch again
	}
	h, err := UnmarshalReaderSketch(raw)
	if err != nil {
		return nil, nil
	}
	return h, nil
}

// ReadReaderSketches returns one UTC day's stored sketches by key, so a caller
// can union them with sketches not yet written.
func (s *Store) ReadReaderSketches(ctx context.Context, day string) (map[string]*ReaderSketch, error) {
	if !validReaderDay(day) {
		return nil, fmt.Errorf("reader sketches: invalid day")
	}
	out := map[string]*ReaderSketch{}
	rows, err := s.db.QueryContext(ctx, "SELECT key,value FROM meta WHERE key>? AND key<?", readerSketchPrefix+day+":", readerSketchPrefix+day+";")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, encoded string
		if err = rows.Scan(&key, &encoded); err != nil {
			return nil, err
		}
		key = strings.TrimPrefix(key, readerSketchPrefix+day+":")
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || !ValidReaderDistinctKey(key) {
			continue
		}
		if h, err := UnmarshalReaderSketch(raw); err == nil {
			out[key] = h
		}
	}
	return out, rows.Err()
}

// readDistinctReaders fills Distinct on each day of out from the stored
// estimates.
func (s *Store) readDistinctReaders(ctx context.Context, out []DailyStats, index map[string]int) error {
	rows, err := s.db.QueryContext(ctx, "SELECT scope,value FROM counters WHERE scope>=? AND scope<?", readerDistinctPrefix+out[0].Day, readerDistinctPrefix+out[len(out)-1].Day+";")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var scope string
		var value int64
		if err = rows.Scan(&scope, &value); err != nil {
			return err
		}
		day, key, ok := strings.Cut(strings.TrimPrefix(scope, readerDistinctPrefix), ":")
		if i, known := index[day]; known && ok && ValidReaderDistinctKey(key) {
			out[i].Distinct[key] = value
		}
	}
	return rows.Err()
}
