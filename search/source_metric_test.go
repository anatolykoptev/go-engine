package search

import (
	"context"
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
			got := collectResults(ch, m, 1000, noopCancel, nil, nil)

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

		got := collectResults(ch, m, 1000, func() {}, bc, nil)

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

		got := collectResults(ch, m, 1000, func() {}, bc, nil)

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

		got := collectResults(ch, m, 1000, func() {}, nil, nil)

		snap := m.Snapshot()
		if sumOutcome(snap, "captcha") != 1 {
			t.Errorf("outcome=captcha: got %v, want 1 (nil BlockCache must not suppress captcha metric; snapshot: %v)", sumOutcome(snap, "captcha"), snap)
		}
		if len(got) != 0 {
			t.Errorf("result count: got %d, want 0", len(got))
		}
	})
}

// TestCollectResults_TimeoutOutcome verifies that collectResults classifies
// per-source transport-timeouts (context.DeadlineExceeded) as outcome="timeout"
// and marks the engine in BlockCache — but ONLY for engines in the OxEscalate
// allowlist. This drives the ox-browser escalation tier in runOxEscalation.
//
// RED-ON-REVERT contracts:
//
//	(a) allowlisted DDG timeout → outcome=timeout + Marked:
//	    remove the errors.Is(DeadlineExceeded) branch → falls to fail, wantTimeout==1
//	    fails (got 0); bc.IsBlocked("ddg") == false → Marked assertion fails.
//	(b) non-allowlisted engine timeout → outcome=fail, NOT Marked:
//	    add non-allowlisted source to oxEscalate → outcome becomes timeout; test fails.
//	(c) context.Canceled (parent cancel) → outcome=fail, NOT Marked:
//	    change condition to also match Canceled → wantFail==1 fails (got 0),
//	    wantTimeout==0 fails (got 1).
//	(d) OxEscalate nil/empty → timeout never Marked (dormant byte-identical):
//	    initialise oxEscalate with ["ddg"] → bc.IsBlocked("ddg")==true; test fails.
//	(e) captcha branch still Marks with oxEscalate set:
//	    remove captcha Mark call → bc.IsBlocked("ddg")==false; sub-test fails.
func TestCollectResults_TimeoutOutcome(t *testing.T) {
	t.Run("(a) allowlisted engine DeadlineExceeded → outcome=timeout + host Marked", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		ch <- directResult{label: "ddg", err: context.DeadlineExceeded}
		close(ch)

		got := collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

		snap := m.Snapshot()
		if sumOutcome(snap, "timeout") != 1 {
			t.Errorf("outcome=timeout: got %v, want 1 (snapshot: %v)", sumOutcome(snap, "timeout"), snap)
		}
		if sumOutcome(snap, "fail") != 0 {
			t.Errorf("outcome=fail: got %v, want 0 (DeadlineExceeded+allowlist must be timeout; snapshot: %v)", sumOutcome(snap, "fail"), snap)
		}
		if !bc.IsBlocked("ddg") {
			t.Error("ddg must be Marked in BlockCache after transport-timeout so runOxEscalation escalates it")
		}
		if len(got) != 0 {
			t.Errorf("result count: got %d, want 0", len(got))
		}
	})

	t.Run("(b) non-allowlisted engine DeadlineExceeded → outcome=fail NOT Marked", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		// "yep" is not in the allowlist ["ddg", "brave"] → must not be escalated
		ch <- directResult{label: "yep", err: context.DeadlineExceeded}
		close(ch)

		_ = collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

		snap := m.Snapshot()
		if sumOutcome(snap, "fail") != 1 {
			t.Errorf("outcome=fail: got %v, want 1 (non-allowlisted timeout must stay fail; snapshot: %v)", sumOutcome(snap, "fail"), snap)
		}
		if sumOutcome(snap, "timeout") != 0 {
			t.Errorf("outcome=timeout: got %v, want 0 (non-allowlisted engine must not be classified as timeout; snapshot: %v)", sumOutcome(snap, "timeout"), snap)
		}
		if bc.IsBlocked("yep") {
			t.Error("yep must NOT be Marked in BlockCache: non-allowlisted timeout must not trigger escalation")
		}
	})

	t.Run("(c) context.Canceled (parent early-return) → outcome=fail NOT Marked", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		// context.Canceled is a parent-cancel (early-return), not a per-source deadline.
		// Must NOT be treated as a transport-timeout even for allowlisted engines.
		ch <- directResult{label: "ddg", err: context.Canceled}
		close(ch)

		_ = collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

		snap := m.Snapshot()
		if sumOutcome(snap, "fail") != 1 {
			t.Errorf("outcome=fail: got %v, want 1 (Canceled must be fail, not timeout; snapshot: %v)", sumOutcome(snap, "fail"), snap)
		}
		if sumOutcome(snap, "timeout") != 0 {
			t.Errorf("outcome=timeout: got %v, want 0 (Canceled must not become timeout; snapshot: %v)", sumOutcome(snap, "timeout"), snap)
		}
		if bc.IsBlocked("ddg") {
			t.Error("ddg must NOT be Marked: context.Canceled is not a block signal")
		}
	})

	t.Run("(d) OxEscalate nil → timeout never Marked (dormant byte-identical)", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		ch <- directResult{label: "ddg", err: context.DeadlineExceeded}
		close(ch)

		// nil oxEscalate: no engine is ever in the allowlist → must behave identically
		// to pre-P2 (timeout stays outcome=fail, nothing marked).
		_ = collectResults(ch, m, 1000, func() {}, bc, nil)

		if bc.IsBlocked("ddg") {
			t.Error("ddg must NOT be Marked when OxEscalate is nil (dormant-byte-identical invariant)")
		}
		snap := m.Snapshot()
		if sumOutcome(snap, "timeout") != 0 {
			t.Errorf("outcome=timeout: got %v, want 0 when OxEscalate nil (snapshot: %v)", sumOutcome(snap, "timeout"), snap)
		}
	})

	t.Run("(e) captcha branch still Marks + outcome=captcha with oxEscalate set", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		ch <- directResult{label: "ddg", err: &ErrRateLimited{Engine: "ddg"}}
		close(ch)

		_ = collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

		snap := m.Snapshot()
		if sumOutcome(snap, "captcha") != 1 {
			t.Errorf("outcome=captcha: got %v, want 1 (captcha branch must be unaffected by oxEscalate param; snapshot: %v)", sumOutcome(snap, "captcha"), snap)
		}
		if sumOutcome(snap, "timeout") != 0 {
			t.Errorf("outcome=timeout: got %v, want 0 (ErrRateLimited must not be reclassified as timeout; snapshot: %v)", sumOutcome(snap, "timeout"), snap)
		}
		if !bc.IsBlocked("ddg") {
			t.Error("ddg must be Marked in BlockCache: captcha branch unchanged by new oxEscalate param")
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
