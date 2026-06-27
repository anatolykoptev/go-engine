package search

import (
	"errors"
	"testing"

	"github.com/anatolykoptev/go-engine/fetch"
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
			got := collectResults(ch, m, 1000, noopCancel, nil)

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

// TestCollectResults_CaptchaOutcome verifies that a source returning
// *ErrRateLimited (the typed anti-bot signal) is classified as outcome=captcha
// rather than outcome=fail, and that the engine is Marked in BlockCache when
// non-nil. Legit-empty (nil error, zero results) must stay outcome=empty and
// must NOT mark the BlockCache — this is the false-positive guard.
//
// RED-ON-REVERT contracts:
//   - Revert the captcha branch in collectResults (fall through to fail):
//     wantCaptcha==1 fails (got 0), wantFail==0 fails (got 1).
//   - Revert the empty-is-not-captcha guard (key on result count):
//     wantCaptcha==0 fails for the empty sub-test (got 1).
//   - Remove the nil-blockCache guard:
//     nil-blockCache sub-test panics → test failure.
func TestCollectResults_CaptchaOutcome(t *testing.T) {
	t.Run("ErrRateLimited → outcome captcha + host marked", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0) // default TTL + cap
		ch := make(chan directResult, 1)
		ch <- directResult{label: "ddg", err: &ErrRateLimited{Engine: "ddg"}}
		close(ch)

		got := collectResults(ch, m, 1000, func() {}, bc)

		snap := m.Snapshot()
		if sumOutcome(snap, "captcha") != 1 {
			t.Errorf("outcome=captcha: got %v, want 1 (snapshot: %v)", sumOutcome(snap, "captcha"), snap)
		}
		if sumOutcome(snap, "fail") != 0 {
			t.Errorf("outcome=fail: got %v, want 0 (ErrRateLimited must not be classified as fail; snapshot: %v)", sumOutcome(snap, "fail"), snap)
		}
		if !bc.IsBlocked("ddg") {
			t.Error("ddg must be Marked in BlockCache after captcha detection")
		}
		if len(got) != 0 {
			t.Errorf("result count: got %d, want 0", len(got))
		}
	})

	t.Run("zero results no error → empty NOT captcha host NOT marked", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		ch <- directResult{label: "brave", results: nil, err: nil}
		close(ch)

		got := collectResults(ch, m, 1000, func() {}, bc)

		snap := m.Snapshot()
		if sumOutcome(snap, "empty") != 1 {
			t.Errorf("outcome=empty: got %v, want 1 (snapshot: %v)", sumOutcome(snap, "empty"), snap)
		}
		if sumOutcome(snap, "captcha") != 0 {
			t.Errorf("outcome=captcha: got %v, want 0 (legit-empty must not become captcha; snapshot: %v)", sumOutcome(snap, "captcha"), snap)
		}
		if bc.IsBlocked("brave") {
			t.Error("brave must NOT be Marked in BlockCache on legit-empty result")
		}
		if len(got) != 0 {
			t.Errorf("result count: got %d, want 0", len(got))
		}
	})

	t.Run("nil BlockCache + ErrRateLimited → no panic captcha counted", func(t *testing.T) {
		m := metrics.New()
		ch := make(chan directResult, 1)
		ch <- directResult{label: "ddg", err: &ErrRateLimited{Engine: "ddg"}}
		close(ch)

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("collectResults panicked with nil blockCache: %v", r)
			}
		}()

		got := collectResults(ch, m, 1000, func() {}, nil)

		snap := m.Snapshot()
		if sumOutcome(snap, "captcha") != 1 {
			t.Errorf("outcome=captcha: got %v, want 1 (nil BlockCache must not suppress captcha metric; snapshot: %v)", sumOutcome(snap, "captcha"), snap)
		}
		if len(got) != 0 {
			t.Errorf("result count: got %d, want 0", len(got))
		}
	})
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
