package board

import "testing"

// Every reader metric has a /stats label, and every label is a metric's.
func TestReaderMetricLabels(t *testing.T) {
	for _, m := range ReaderMetrics {
		if ReaderMetricLabels[m] == "" {
			t.Errorf("reader metric %s has no ReaderMetricLabels entry", m)
		}
	}
	if len(ReaderMetricLabels) != len(ReaderMetrics) {
		t.Errorf("ReaderMetricLabels has %d entries for %d metrics", len(ReaderMetricLabels), len(ReaderMetrics))
	}
}
