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
			got, _ := collectResults(ch, m, 1000, noopCancel, nil, nil)

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

		got, _ := collectResults(ch, m, 1000, func() {}, bc, nil)

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

		got, _ := collectResults(ch, m, 1000, func() {}, bc, nil)

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

		got, _ := collectResults(ch, m, 1000, func() {}, nil, nil)

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
// genuine per-source timeouts (errPerSourceTimeout via context.Cause) as
// outcome="timeout" and marks the engine in BlockCache — but ONLY for engines in
// the OxEscalate allowlist. Crucially, an inherited parent deadline
// (context.DeadlineExceeded, not errPerSourceTimeout) does NOT trigger timeout or
// Mark: a short-budget caller must not pin every in-flight engine for 10 m.
//
// RED-ON-REVERT contracts:
//
//	(a) allowlisted DDG per-source timeout (errPerSourceTimeout) → outcome=timeout + Marked:
//	    change the timeout case back to errors.Is(DeadlineExceeded) →
//	    errPerSourceTimeout no longer matches → falls to blocked (non-Canceled,
//	    non-DeadlineExceeded, allowlisted) → outcome=blocked not timeout; test fails.
//	    OR remove the timeout case entirely → falls to fail; test fails.
//	(b) non-allowlisted engine timeout → outcome=fail, NOT Marked:
//	    add non-allowlisted source to oxEscalate → outcome becomes timeout; test fails.
//	(c) context.Canceled (parent cancel) → outcome=fail, NOT Marked:
//	    change condition to also match Canceled → wantFail==1 fails (got 0).
//	(d) OxEscalate nil/empty → timeout never Marked (dormant byte-identical):
//	    initialise oxEscalate with ["ddg"] → bc.IsBlocked("ddg")==true; test fails.
//	(e) captcha branch still Marks with oxEscalate set:
//	    remove captcha Mark call → bc.IsBlocked("ddg")==false; sub-test fails.
//	(f) parent deadline (DeadlineExceeded) → outcome=fail NOT Marked:
//	    revert to errors.Is(DeadlineExceeded) in timeout case → DeadlineExceeded
//	    IS classified as timeout → bc.IsBlocked("ddg")==true; test fails.
func TestCollectResults_TimeoutOutcome(t *testing.T) {
	t.Run("(a) allowlisted engine per-source timeout → outcome=timeout + host Marked", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		// errPerSourceTimeout is what context.Cause(srcCtx) returns when the
		// per-source WithTimeoutCause deadline fires in runSourceWithTimeout.
		ch <- directResult{label: "ddg", err: errPerSourceTimeout}
		close(ch)

		got, _ := collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

		snap := m.Snapshot()
		if sumOutcome(snap, "timeout") != 1 {
			t.Errorf("outcome=timeout: got %v, want 1 (snapshot: %v)", sumOutcome(snap, "timeout"), snap)
		}
		if sumOutcome(snap, "fail") != 0 {
			t.Errorf("outcome=fail: got %v, want 0 (per-source timeout+allowlist must be timeout; snapshot: %v)", sumOutcome(snap, "fail"), snap)
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

		_, _ = collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

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

		_, _ = collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

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
		_, _ = collectResults(ch, m, 1000, func() {}, bc, nil)

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

		_, _ = collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

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

	t.Run("(f) parent deadline (DeadlineExceeded from context.Cause) → outcome=fail NOT Marked", func(t *testing.T) {
		// When the parent context's deadline fires before the per-source timer,
		// context.Cause(srcCtx) returns context.DeadlineExceeded (propagated from
		// the parent), not errPerSourceTimeout. This must NOT trigger outcome=timeout
		// or Mark — a short-budget caller must not pin every in-flight allowlisted
		// engine for the full 10 m TTL.
		//
		// RED-ON-REVERT: revert handleSourceError timeout case to check
		// errors.Is(r.err, context.DeadlineExceeded) → parent DeadlineExceeded IS
		// matched → outcome=timeout + bc.IsBlocked("ddg")==true → both assertions fail.
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		// Inject context.DeadlineExceeded directly — this is what context.Cause
		// returns when the parent ctx deadline fires before the per-source timer.
		ch <- directResult{label: "ddg", err: context.DeadlineExceeded}
		close(ch)

		_, _ = collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

		snap := m.Snapshot()
		if sumOutcome(snap, "timeout") != 0 {
			t.Errorf("outcome=timeout: got %v, want 0 (parent deadline must not become timeout; snapshot: %v)", sumOutcome(snap, "timeout"), snap)
		}
		if sumOutcome(snap, "blocked") != 0 {
			t.Errorf("outcome=blocked: got %v, want 0 (parent deadline must not become blocked; snapshot: %v)", sumOutcome(snap, "blocked"), snap)
		}
		if bc.IsBlocked("ddg") {
			t.Error("ddg must NOT be Marked: parent deadline propagation must not trigger escalation")
		}
		if sumOutcome(snap, "fail") != 1 {
			t.Errorf("outcome=fail: got %v, want 1 (parent deadline → fail; snapshot: %v)", sumOutcome(snap, "fail"), snap)
		}
	})
}

// TestCollectResults_BlockedOutcome verifies that an allowlisted engine that fails
// with ANY hard error (not captcha, not DeadlineExceeded) is classified as
// outcome="blocked" and Marked in BlockCache so runOxEscalation will escalate it
// to the stealth-render tier.
//
// This closes the DDG d.js-challenge gap: the challenge returns a JS body that the
// DDG decoder cannot parse as JSON, producing errors.New("ddg d.js: json parse:
// invalid character 'l'..."). Under v1.44 this fell through to outcome="fail" and
// was never Marked → escalation never fired. With the new blocked case it Marks.
//
// RED-ON-REVERT contracts:
//
//	(a) allowlisted DDG parse-error → outcome=blocked + Marked:
//	    remove the blocked case → falls to default → outcome=fail; test fails.
//	    remove the allowlist check → non-allowlisted source also Marks; (b) fails.
//	(b) non-allowlisted engine same error → outcome=fail NOT Marked:
//	    add non-allowlisted source to oxEscalate → becomes blocked; test fails.
//	(c) allowlisted context.Canceled → outcome=fail NOT Marked:
//	    remove !errors.Is(Canceled) guard → Canceled becomes blocked; test fails.
//	(d) allowlisted nil error (legit-empty path) → NOT covered by this function
//	    (collectResults skips handleSourceError for nil err); guard in collectResults.
//	(e) oxEscalate nil/empty → no blocked-mark (dormant byte-identical):
//	    set oxEscalate=["ddg"] in sub-test (e) → ddg Marked; test fails.
//	(f) captcha + timeout still Mark with their own labels under oxEscalate set:
//	    covered by existing TestCollectResults_CaptchaOutcome (e) and
//	    TestCollectResults_TimeoutOutcome (e).
func TestCollectResults_BlockedOutcome(t *testing.T) {
	parseErr := errors.New("ddg d.js: ddg json parse: invalid character 'l' looking for beginning of value")

	t.Run("(a) allowlisted engine parse-error → outcome=blocked + host Marked", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		ch <- directResult{label: "ddg", err: parseErr}
		close(ch)

		got, _ := collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

		snap := m.Snapshot()
		if sumOutcome(snap, "blocked") != 1 {
			t.Errorf("outcome=blocked: got %v, want 1 (snapshot: %v)", sumOutcome(snap, "blocked"), snap)
		}
		if sumOutcome(snap, "fail") != 0 {
			t.Errorf("outcome=fail: got %v, want 0 (parse-error+allowlist must be blocked, not fail; snapshot: %v)", sumOutcome(snap, "fail"), snap)
		}
		if !bc.IsBlocked("ddg") {
			t.Error("ddg must be Marked in BlockCache after parse-error on allowlisted engine")
		}
		if len(got) != 0 {
			t.Errorf("result count: got %d, want 0", len(got))
		}
	})

	t.Run("(b) non-allowlisted engine same parse-error → outcome=fail NOT Marked", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		ch <- directResult{label: "yep", err: parseErr}
		close(ch)

		_, _ = collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

		snap := m.Snapshot()
		if sumOutcome(snap, "fail") != 1 {
			t.Errorf("outcome=fail: got %v, want 1 (non-allowlisted source must stay fail; snapshot: %v)", sumOutcome(snap, "fail"), snap)
		}
		if sumOutcome(snap, "blocked") != 0 {
			t.Errorf("outcome=blocked: got %v, want 0 (non-allowlisted engine must not be marked; snapshot: %v)", sumOutcome(snap, "blocked"), snap)
		}
		if bc.IsBlocked("yep") {
			t.Error("yep must NOT be Marked: non-allowlisted sources must never trigger escalation")
		}
	})

	t.Run("(c) allowlisted engine context.Canceled → outcome=fail NOT Marked", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		ch <- directResult{label: "ddg", err: context.Canceled}
		close(ch)

		_, _ = collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

		snap := m.Snapshot()
		if sumOutcome(snap, "fail") != 1 {
			t.Errorf("outcome=fail: got %v, want 1 (Canceled must be fail, not blocked; snapshot: %v)", sumOutcome(snap, "fail"), snap)
		}
		if sumOutcome(snap, "blocked") != 0 {
			t.Errorf("outcome=blocked: got %v, want 0 (Canceled must never Mark; snapshot: %v)", sumOutcome(snap, "blocked"), snap)
		}
		if bc.IsBlocked("ddg") {
			t.Error("ddg must NOT be Marked: context.Canceled is a parent early-return, not a block signal")
		}
	})

	t.Run("(d) allowlisted engine nil error (legit-empty) → outcome=empty NOT Marked", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		// nil err, zero results: handleSourceError is NOT called for this path.
		ch <- directResult{label: "ddg", results: nil, err: nil}
		close(ch)

		got, _ := collectResults(ch, m, 1000, func() {}, bc, []string{"ddg", "brave"})

		snap := m.Snapshot()
		if sumOutcome(snap, "empty") != 1 {
			t.Errorf("outcome=empty: got %v, want 1 (legit-empty must stay empty; snapshot: %v)", sumOutcome(snap, "empty"), snap)
		}
		if sumOutcome(snap, "blocked") != 0 {
			t.Errorf("outcome=blocked: got %v, want 0 (nil-err empty must never be blocked; snapshot: %v)", sumOutcome(snap, "blocked"), snap)
		}
		if bc.IsBlocked("ddg") {
			t.Error("ddg must NOT be Marked: legit-empty is not a block signal")
		}
		if len(got) != 0 {
			t.Errorf("result count: got %d, want 0", len(got))
		}
	})

	t.Run("(e) oxEscalate nil → parse-error stays fail NOT Marked (dormant byte-identical)", func(t *testing.T) {
		m := metrics.New()
		bc := fetch.NewDirectBlockCache(0, 0)
		ch := make(chan directResult, 1)
		ch <- directResult{label: "ddg", err: parseErr}
		close(ch)

		_, _ = collectResults(ch, m, 1000, func() {}, bc, nil)

		snap := m.Snapshot()
		if sumOutcome(snap, "blocked") != 0 {
			t.Errorf("outcome=blocked: got %v, want 0 when oxEscalate is nil (dormant-byte-identical; snapshot: %v)", sumOutcome(snap, "blocked"), snap)
		}
		if bc.IsBlocked("ddg") {
			t.Error("ddg must NOT be Marked when oxEscalate is nil (dormant invariant)")
		}
		// Stays fail — same behaviour as v1.44 with empty oxEscalate.
		if sumOutcome(snap, "fail") != 1 {
			t.Errorf("outcome=fail: got %v, want 1 (dormant → falls to default; snapshot: %v)", sumOutcome(snap, "fail"), snap)
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
