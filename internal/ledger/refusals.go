package ledger

import (
	"errors"
	"sort"

	"swarmmemo/internal/allowance"
)

// Allowance refusals (C320c): how many spends and reserves the allowance
// refused, per UTC day, resource and code, so the operator dashboard shows
// when anyone hits a limit. A refusal happens inside the caller's
// transaction, which then rolls back, so the ledger counts in memory and
// the board writes the counts in its own short transaction (the sweeper,
// every minute). A count lost to a crash between two sweeps is at most a
// minute's.

// RefusalCodes are the codes counted: the caller's share used up
// (quota_exhausted), the day's shared slices dry (global_quota_exhausted),
// no share for the caller's band (no_share).
var RefusalCodes = []string{"quota_exhausted", "global_quota_exhausted", "no_share"}

// Refusal is one (day, resource, code) count.
type Refusal struct {
	Day      int64
	Resource allowance.Resource
	Code     string
	N        int64
}

type refusalKey struct {
	day  int64
	r    allowance.Resource
	code string
}

// refusalsMax bounds the distinct keys held between flushes (days ×
// resources × codes: a handful a day).
const refusalsMax = 4096

// noteRefusal counts err when it is one of RefusalCodes.
func (l *Ledger) noteRefusal(day int64, r allowance.Resource, err error) {
	var e *allowance.Err
	if !errors.As(err, &e) {
		return
	}
	counted := false
	for _, c := range RefusalCodes {
		counted = counted || c == e.Code
	}
	if !counted {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	k := refusalKey{day, r, e.Code}
	if _, ok := l.refusals[k]; ok || len(l.refusals) < refusalsMax {
		l.refusals[k]++
	}
}

// TakeRefusals returns the counts since the last take and clears them, in
// (day, resource, code) order.
func (l *Ledger) TakeRefusals() []Refusal {
	l.mu.Lock()
	m := l.refusals
	l.refusals = map[refusalKey]int64{}
	l.mu.Unlock()
	out := make([]Refusal, 0, len(m))
	for k, n := range m {
		out = append(out, Refusal{Day: k.day, Resource: k.r, Code: k.code, N: n})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Day != b.Day {
			return a.Day < b.Day
		}
		if a.Resource != b.Resource {
			return a.Resource < b.Resource
		}
		return a.Code < b.Code
	})
	return out
}

// PutBackRefusals adds counts back after a failed write.
func (l *Ledger) PutBackRefusals(list []Refusal) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, x := range list {
		k := refusalKey{x.Day, x.Resource, x.Code}
		if _, ok := l.refusals[k]; ok || len(l.refusals) < refusalsMax {
			l.refusals[k] += x.N
		}
	}
}
