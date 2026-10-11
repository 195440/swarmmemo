package rubric

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"time"
)

// Observations are what a harness measured about one subject: per check,
// pass/fail events with timestamps, metric samples, or na.
type Observations struct {
	Schema      string              `json:"schema"`
	Rubric      string              `json:"rubric,omitempty"` // "ID" or "ID@VERSION"; checked when present
	Subject     Subject             `json:"subject"`
	AsOf        string              `json:"as_of"`
	DisputeOpen bool                `json:"dispute_open,omitempty"`
	Checks      map[string]CheckObs `json:"checks"`
	Value       map[string]string   `json:"value,omitempty"`
	asOf        time.Time
}

// Subject names what was tested (RFC 0015 §4).
type Subject struct {
	Type              string `json:"type"`
	Key               string `json:"key"`
	ID                string `json:"id,omitempty"`
	Revision          string `json:"revision,omitempty"`
	RevisionChangedAt string `json:"revision_changed_at,omitempty"`
	revChanged        time.Time
}

// CheckObs is one check's evidence: events (rate checks), samples (curve
// checks), or na with a reason.
type CheckObs struct {
	NA      bool     `json:"na,omitempty"`
	Reason  string   `json:"reason,omitempty"`
	Events  []Event  `json:"events,omitempty"`
	Samples []Sample `json:"samples,omitempty"`
}

// Event is pass and fail counts observed at one time. Weight (0, 1] scales
// the event, e.g. for an outside run (design §3.8); 0 or absent means 1.
type Event struct {
	At     string  `json:"at"`
	Pass   int     `json:"pass"`
	Fail   int     `json:"fail"`
	Weight float64 `json:"weight,omitempty"`
	at     time.Time
}

// Sample is one continuous measurement, e.g. a latency in ms.
type Sample struct {
	At     string  `json:"at"`
	Value  float64 `json:"value"`
	Weight float64 `json:"weight,omitempty"`
	at     time.Time
}

var (
	reasonRE   = regexp.MustCompile(`^[\x20-\x7e]{0,200}$`)
	valueKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	valueRE    = regexp.MustCompile(`^[\x20-\x7e]{0,80}$`)
)

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	if t.Year() < 2000 || t.Year() > 9999 {
		return time.Time{}, fmt.Errorf("year out of range")
	}
	return t.UTC(), nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ParseObservations reads observations and validates them against r.
func ParseObservations(raw []byte, r *Rubric) (*Observations, error) {
	if len(raw) > MaxObservationsBytes {
		return nil, fmt.Errorf("observations: file over %d bytes", MaxObservationsBytes)
	}
	var o Observations
	if err := decodeStrict(raw, &o); err != nil {
		return nil, fmt.Errorf("observations: %v", err)
	}
	bad := func(format string, a ...any) error { return fmt.Errorf("observations: "+format, a...) }
	if o.Schema != "swarmmemo.observations/1" {
		return nil, bad("schema must be swarmmemo.observations/1")
	}
	if o.Rubric != "" && o.Rubric != r.ID && o.Rubric != r.ID+"@"+r.Version {
		return nil, bad("rubric %q is not %s@%s", o.Rubric, r.ID, r.Version)
	}
	typeOK := false
	for _, t := range r.SubjectTypes {
		typeOK = typeOK || t == o.Subject.Type
	}
	if !typeOK {
		return nil, bad("subject type %q is not one of the rubric's", o.Subject.Type)
	}
	if o.Subject.Key == "" || len(o.Subject.Key) > 2048 || !reasonRE.MatchString(o.Subject.Key[:min(len(o.Subject.Key), 200)]) ||
		len(o.Subject.ID) > 64 || len(o.Subject.Revision) > 80 || !valueRE.MatchString(o.Subject.ID) || !valueRE.MatchString(o.Subject.Revision) {
		return nil, bad("subject")
	}
	for _, c := range []byte(o.Subject.Key) {
		if c < 0x20 || c > 0x7e {
			return nil, bad("subject key: printable ASCII only")
		}
	}
	var err error
	if o.asOf, err = parseTime(o.AsOf); err != nil {
		return nil, bad("as_of: %v", err)
	}
	if o.Subject.RevisionChangedAt != "" {
		if o.Subject.revChanged, err = parseTime(o.Subject.RevisionChangedAt); err != nil || o.Subject.revChanged.After(o.asOf) {
			return nil, bad("subject.revision_changed_at")
		}
	}
	if len(o.Value) > 16 {
		return nil, bad("value: at most 16 fields")
	}
	for k, v := range o.Value {
		if !valueKeyRE.MatchString(k) || !valueRE.MatchString(v) {
			return nil, bad("value %q", k)
		}
	}
	total := 0
	for id, c := range o.Checks {
		ch := r.check(id)
		if ch == nil {
			return nil, bad("check %q is not in rubric %s", id, r.ID)
		}
		if !reasonRE.MatchString(c.Reason) {
			return nil, bad("check %s: reason", id)
		}
		if c.NA && (len(c.Events) > 0 || len(c.Samples) > 0) {
			return nil, bad("check %s: na with evidence", id)
		}
		if ch.Metric == "rate" && len(c.Samples) > 0 {
			return nil, bad("check %s: a rate check takes events, not samples", id)
		}
		if ch.Metric == "curve" && len(c.Events) > 0 {
			return nil, bad("check %s: a curve check takes samples, not events", id)
		}
		if len(c.Events) > MaxEventsPerCheck || len(c.Samples) > MaxSamplesPerCheck {
			return nil, bad("check %s: too many events or samples", id)
		}
		total += len(c.Events) + len(c.Samples)
		for i := range c.Events {
			e := &c.Events[i]
			if e.at, err = parseTime(e.At); err != nil || e.at.After(o.asOf) {
				return nil, bad("check %s: event time %q (must be at or before as_of)", id, e.At)
			}
			if e.Pass < 0 || e.Fail < 0 || e.Pass > MaxCount || e.Fail > MaxCount || e.Pass+e.Fail == 0 {
				return nil, bad("check %s: event counts", id)
			}
			if !finite(e.Weight) || e.Weight < 0 || e.Weight > 1 {
				return nil, bad("check %s: event weight", id)
			}
			e.Weight += 0 // -0 → 0
		}
		for i := range c.Samples {
			s := &c.Samples[i]
			if s.at, err = parseTime(s.At); err != nil || s.at.After(o.asOf) {
				return nil, bad("check %s: sample time %q (must be at or before as_of)", id, s.At)
			}
			if !finite(s.Value) || s.Value < 0 || s.Value > 1e12 {
				return nil, bad("check %s: sample value", id)
			}
			if !finite(s.Weight) || s.Weight < 0 || s.Weight > 1 {
				return nil, bad("check %s: sample weight", id)
			}
			s.Weight += 0
			s.Value += 0
		}
		// Order never matters to the score: sort so the canonical form, and
		// so the seed, is the same whatever order a harness wrote.
		sort.SliceStable(c.Events, func(i, j int) bool {
			a, b := c.Events[i], c.Events[j]
			if !a.at.Equal(b.at) {
				return a.at.Before(b.at)
			}
			if a.Pass != b.Pass {
				return a.Pass < b.Pass
			}
			if a.Fail != b.Fail {
				return a.Fail < b.Fail
			}
			return a.Weight < b.Weight
		})
		sort.SliceStable(c.Samples, func(i, j int) bool {
			a, b := c.Samples[i], c.Samples[j]
			if !a.at.Equal(b.at) {
				return a.at.Before(b.at)
			}
			if a.Value != b.Value {
				return a.Value < b.Value
			}
			return a.Weight < b.Weight
		})
		for i := range c.Events {
			c.Events[i].At = formatTime(c.Events[i].at)
		}
		for i := range c.Samples {
			c.Samples[i].At = formatTime(c.Samples[i].at)
		}
		o.Checks[id] = c
	}
	if total > MaxEventsTotal {
		return nil, bad("more than %d events and samples", MaxEventsTotal)
	}
	o.AsOf = formatTime(o.asOf)
	if o.Subject.RevisionChangedAt != "" {
		o.Subject.RevisionChangedAt = formatTime(o.Subject.revChanged)
	}
	return &o, nil
}

// Canonical is the observations re-encoded: map keys sorted, events and
// samples in time order, times in UTC. Its SHA-256 seeds the draws.
func (o *Observations) Canonical() []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(o)
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
}

func sha256Hex(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
