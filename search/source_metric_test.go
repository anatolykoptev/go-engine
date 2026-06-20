package search

import (
	"errors"
	"testing"

	"github.com/anatolykoptev/go-engine/metrics"
	"github.com/anatolykoptev/go-engine/sources"
)

// TestRecordSourceResult verifies the per-source fan-out outcome counter is
// encoded as name{source=...,outcome=...} so the go-kit/metrics Prometheus
// bridge surfaces a per-source failure rate. Regression guard for the
// silent-redundancy gap: a source failing 100% (e.g. yep on the deprecated
// endpoint) was invisible because a sibling source covered the result set.
func TestRecordSourceResult(t *testing.T) {
	m := metrics.New()

	recordSourceResult(m, "yep", "fail")
	recordSourceResult(m, "yep", "fail")
	recordSourceResult(m, "yep", "ok")
	recordSourceResult(m, "yandex", "ok")

	snap := m.Snapshot()

	if got := snap["go_search_source_result_total{source=yep,outcome=fail}"]; got != 2 {
		t.Errorf("yep fail = %d, want 2 (snapshot: %v)", got, snap)
	}
	if got := snap["go_search_source_result_total{source=yep,outcome=ok}"]; got != 1 {
		t.Errorf("yep ok = %d, want 1", got)
	}
	if got := snap["go_search_source_result_total{source=yandex,outcome=ok}"]; got != 1 {
		t.Errorf("yandex ok = %d, want 1", got)
	}
}

// TestRecordSourceResult_NilSafe guards the nil-registry path: SearchDirect may
// run with cfg.Metrics == nil.
func TestRecordSourceResult_NilSafe(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recordSourceResult panicked on nil registry: %v", r)
		}
	}()
	recordSourceResult(nil, "yep", "fail")
}

// TestCollectResults_Outcomes is a table-driven test for collectResults that
// verifies the three outcome labels (ok, empty, fail) are recorded correctly.
//
// RED-ON-REVERT contract:
//   - If the "empty" branch (len(r.results)==0, no error) is removed, the
//     "empty" counter will read 0 and "ok" will increment instead — the
//     "source returns empty" row will fail.
//   - If the "ok" branch is removed, the "ok" counter will read 0.
//   - If the "fail" branch is removed, the "fail" counter will read 0.
func TestCollectResults_Outcomes(t *testing.T) {
	okResult := sources.Result{Title: "ok", URL: "https://example.com/ok"}

	type row struct {
		label   string
		results []sources.Result
		err     error
	}

	tests := []struct {
		name          string
		inputs        []row
		wantOK        int64
		wantEmpty     int64
		wantFail      int64
		wantResultLen int
	}{
		{
			name:          "source returns results → ok",
			inputs:        []row{{"src", []sources.Result{okResult}, nil}},
			wantOK:        1,
			wantEmpty:     0,
			wantFail:      0,
			wantResultLen: 1,
		},
		{
			name:          "source returns nil slice, no error → empty",
			inputs:        []row{{"src", nil, nil}},
			wantOK:        0,
			wantEmpty:     1,
			wantFail:      0,
			wantResultLen: 0,
		},
		{
			name:          "source returns zero-length slice, no error → empty",
			inputs:        []row{{"src", []sources.Result{}, nil}},
			wantOK:        0,
			wantEmpty:     1,
			wantFail:      0,
			wantResultLen: 0,
		},
		{
			name:          "source returns error → fail",
			inputs:        []row{{"src", nil, errors.New("connection refused")}},
			wantOK:        0,
			wantEmpty:     0,
			wantFail:      1,
			wantResultLen: 0,
		},
		{
			name: "mixed: ok + empty + fail across three sources",
			inputs: []row{
				{"a", []sources.Result{okResult}, nil},
				{"b", nil, nil},
				{"c", nil, errors.New("timeout")},
			},
			wantOK:        1,
			wantEmpty:     1,
			wantFail:      1,
			wantResultLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := metrics.New()
			ch := make(chan directResult, len(tt.inputs))
			for _, inp := range tt.inputs {
				ch <- directResult{label: inp.label, results: inp.results, err: inp.err}
			}
			close(ch)

			noopCancel := func() {}
			got := collectResults(ch, m, 1000, noopCancel)

			snap := m.Snapshot()

			// Count totals across all sources (labels per-source may differ).
			totalOK := sumOutcome(snap, "ok")
			totalEmpty := sumOutcome(snap, "empty")
			totalFail := sumOutcome(snap, "fail")

			if totalOK != tt.wantOK {
				t.Errorf("outcome=ok: got %v, want %v (snapshot: %v)", totalOK, tt.wantOK, snap)
			}
			if totalEmpty != tt.wantEmpty {
				t.Errorf("outcome=empty: got %v, want %v (snapshot: %v)", totalEmpty, tt.wantEmpty, snap)
			}
			if totalFail != tt.wantFail {
				t.Errorf("outcome=fail: got %v, want %v (snapshot: %v)", totalFail, tt.wantFail, snap)
			}
			if len(got) != tt.wantResultLen {
				t.Errorf("result count: got %d, want %d", len(got), tt.wantResultLen)
			}
		})
	}
}

// sumOutcome sums all snapshot counters whose key contains outcome=<o>.
func sumOutcome(snap map[string]int64, o string) int64 {
	var total int64
	needle := "outcome=" + o
	for k, v := range snap {
		if contains(k, needle) {
			total += v
		}
	}
	return total
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
