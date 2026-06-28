package search

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anatolykoptev/go-engine/fetch"
	"github.com/anatolykoptev/go-engine/sources"
)

// ddgHTML returns minimal DDG HTML with n results.
func ddgHTML(n int) string {
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb,
			`<div class="result"><a class="result__a" href="https://example.com/ddg-%d">DDG Result %d</a><span class="result__snippet">snippet</span></div>`,
			i, i,
		)
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

// startpageHTML returns minimal Startpage HTML with n results.
func startpageHTML(n int) string {
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb,
			`<div class="w-gl__result"><a class="w-gl__result-title" href="https://example.com/sp-%d">SP Result %d</a><p class="w-gl__description">description %d</p></div>`,
			i, i, i,
		)
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

func noRetry() fetch.RetryConfig {
	return fetch.RetryConfig{
		MaxRetries:  0,
		InitialWait: 0,
		MaxWait:     0,
		Multiplier:  1,
	}
}

// TestSearchDirect_SlowSourceDoesNotDelay verifies that a slow/blocked source is
// cancelled by PerSourceTimeout and does not delay return of fast-source results.
//
// RED-ON-REVERT: If the inner goroutine + select on srcCtx.Done() is removed,
// the slow Brave goroutine blocks in Do() until our 30s timer fires → the channel
// is never drained → for-range blocks → test exceeds 1s timing assertion.
func TestSearchDirect_SlowSourceDoesNotDelay(t *testing.T) {
	fastHTML := ddgHTML(3)

	slowDone := make(chan struct{})
	t.Cleanup(func() { close(slowDone) })

	dispatch := &mockBrowser{fn: func(_, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if strings.Contains(url, "duckduckgo") {
			return []byte(fastHTML), nil, http.StatusOK, nil
		}
		// Brave: block until cleanup or 30s safety.
		select {
		case <-slowDone:
			return nil, nil, 0, context.Canceled
		case <-time.After(30 * time.Second):
			return nil, nil, 0, context.DeadlineExceeded
		}
	}}

	cfg := DirectConfig{
		Browser:          dispatch,
		DDG:              true,
		Brave:            true,
		Retry:            noRetry(),
		PerSourceTimeout: 200 * time.Millisecond,
		EarlyReturnAt:    100, // high — early-return must NOT be the mechanism here
	}

	start := time.Now()
	results, _ := SearchDirect(context.Background(), cfg, "test", "en")
	elapsed := time.Since(start)

	if elapsed >= 1*time.Second {
		t.Errorf("SearchDirect took %v, want < 1s (slow source cancelled by PerSourceTimeout=%v)", elapsed, cfg.PerSourceTimeout)
	}
	if len(results) == 0 {
		t.Error("expected results from fast DDG source, got none")
	}
}

// TestSearchDirect_EarlyReturnOnEnoughResults verifies that the fan-out returns
// as soon as EarlyReturnAt results are accumulated, cancelling the slow source.
//
// RED-ON-REVERT: Removing allDoneCancel() inside the earlyAt branch means
// allDoneCtx stays live → slow Brave goroutine is never cancelled early → it runs
// until PerSourceTimeout (5s) → test exceeds 500ms timing assertion.
func TestSearchDirect_EarlyReturnOnEnoughResults(t *testing.T) {
	// DDG and Startpage each return 6 fast results — 12 total exceeds EarlyReturnAt=10.
	fastDDG := ddgHTML(6)
	fastSP := startpageHTML(6)

	slowDone := make(chan struct{})
	t.Cleanup(func() { close(slowDone) })

	dispatch := &mockBrowser{fn: func(_, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if strings.Contains(url, "duckduckgo") {
			return []byte(fastDDG), nil, http.StatusOK, nil
		}
		if strings.Contains(url, "startpage") {
			return []byte(fastSP), nil, http.StatusOK, nil
		}
		// Brave: block until allDoneCtx cancels via early-return.
		select {
		case <-slowDone:
			return nil, nil, 0, context.Canceled
		case <-time.After(30 * time.Second):
			return nil, nil, 0, context.DeadlineExceeded
		}
	}}

	cfg := DirectConfig{
		Browser:          dispatch,
		DDG:              true,
		Startpage:        true,
		Brave:            true,
		Retry:            noRetry(),
		PerSourceTimeout: 5 * time.Second, // long — timeout alone won't save us
		EarlyReturnAt:    10,
	}

	start := time.Now()
	results, _ := SearchDirect(context.Background(), cfg, "test", "en")
	elapsed := time.Since(start)

	if elapsed >= 500*time.Millisecond {
		t.Errorf("SearchDirect took %v, want < 500ms (early-return after %d results)", elapsed, cfg.EarlyReturnAt)
	}
	if len(results) < 10 {
		t.Errorf("got %d results, want >= 10", len(results))
	}
}

// TestSearchDirect_FastResultsNotDiscarded verifies that results from fast sources
// are preserved even when a slow source is present.
//
// RED-ON-REVERT: In the old mutex+WaitGroup pattern, wg.Wait() blocked until all
// goroutines finished. A slow source holding the wait meant callers never saw fast
// results until the slow one timed out. The channel-based drain in collectResults
// guarantees all sent results are read before returning — fast results not lost.
func TestSearchDirect_FastResultsNotDiscarded(t *testing.T) {
	fastHTML := ddgHTML(10)

	slowDone := make(chan struct{})
	t.Cleanup(func() { close(slowDone) })

	dispatch := &mockBrowser{fn: func(_, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if strings.Contains(url, "duckduckgo") {
			return []byte(fastHTML), nil, http.StatusOK, nil
		}
		// Startpage: block until ctx cancel via PerSourceTimeout.
		select {
		case <-slowDone:
			return nil, nil, 0, context.Canceled
		case <-time.After(30 * time.Second):
			return nil, nil, 0, context.DeadlineExceeded
		}
	}}

	cfg := DirectConfig{
		Browser:          dispatch,
		DDG:              true,
		Startpage:        true,
		Retry:            noRetry(),
		EarlyReturnAt:    5, // fires after 5 DDG results → cancels Startpage
		PerSourceTimeout: 200 * time.Millisecond,
	}

	results, _ := SearchDirect(context.Background(), cfg, "test", "en")
	if len(results) < 5 {
		t.Errorf("got %d results, want >= 5; fast DDG results were discarded", len(results))
	}
}

// TestSearchDirect_ParentDeadlineDoesNotMarkBlockCache verifies that a parent
// context whose deadline fires before the per-source timer does NOT Mark
// allowlisted engines in the BlockCache.
//
// This is the integration complement to TestCollectResults_TimeoutOutcome/(f):
// it exercises the real ctx propagation path through SearchDirect →
// runSourceWithTimeout → context.Cause → handleSourceError, confirming that
// context.Cause returns context.DeadlineExceeded (from the parent, not
// errPerSourceTimeout) and therefore the blocked-cache stays clean.
//
// RED-ON-REVERT:
//   - Revert runSourceWithTimeout to `err = srcCtx.Err()` (removing context.Cause) →
//     parent deadline still produces DeadlineExceeded, BUT the old timeout case
//     checked errors.Is(DeadlineExceeded) → that test would still fail on the
//     separate handleSourceError gate. The real RED is the combination:
//     revert SearchDirect to context.WithTimeout (no cause) AND revert
//     handleSourceError to errors.Is(DeadlineExceeded) → bc.IsBlocked("ddg")==true →
//     test fails.
func TestSearchDirect_ParentDeadlineDoesNotMarkBlockCache(t *testing.T) {
	slowDone := make(chan struct{})
	t.Cleanup(func() { close(slowDone) })

	dispatch := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		// All sources block until the parent context deadline fires.
		select {
		case <-slowDone:
			return nil, nil, 0, context.Canceled
		case <-time.After(30 * time.Second):
			return nil, nil, 0, context.DeadlineExceeded
		}
	}}

	bc := fetch.NewDirectBlockCache(0, 0)

	// Parent context deadline: 80 ms. PerSourceTimeout: 2 s (much longer).
	// The parent deadline fires first; all sources are cancelled via parent propagation.
	parentCtx, parentCancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer parentCancel()

	cfg := DirectConfig{
		Browser:          dispatch,
		DDG:              true,
		Brave:            true,
		Retry:            noRetry(),
		PerSourceTimeout: 2 * time.Second,
		EarlyReturnAt:    100,
		BlockCache:       bc,
		OxEscalate:       []string{"ddg", "brave"},
	}

	_, _ = SearchDirect(parentCtx, cfg, "test", "en")

	if bc.IsBlocked("ddg") {
		t.Error("ddg must NOT be Marked: parent deadline must not trigger escalation")
	}
	if bc.IsBlocked("brave") {
		t.Error("brave must NOT be Marked: parent deadline must not trigger escalation")
	}
}

// TestSearchDirect_PerSourceTimeoutMarksBlockCache verifies that a genuine
// per-source timeout (PerSourceTimeout fires before both the parent deadline and
// the source fn returning) Marks allowlisted engines in the BlockCache.
//
// This is the positive discriminator: the two tests together prove the
// conflation is fixed — parent deadline → no Mark, per-source timeout → Mark.
//
// RED-ON-REVERT: revert SearchDirect to context.WithTimeout (no cause) AND
// runSourceWithTimeout to srcCtx.Err() → per-source timeout still produces
// context.DeadlineExceeded → with the old errors.Is(DeadlineExceeded) check
// this test would still pass. The discriminator is the PAIR: this test passes
// under both old and new code, but ParentDeadlineDoesNotMark fails under old
// code — the pair together proves correctness.
func TestSearchDirect_PerSourceTimeoutMarksBlockCache(t *testing.T) {
	slowDone := make(chan struct{})
	t.Cleanup(func() { close(slowDone) })

	dispatch := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		select {
		case <-slowDone:
			return nil, nil, 0, context.Canceled
		case <-time.After(30 * time.Second):
			return nil, nil, 0, context.DeadlineExceeded
		}
	}}

	bc := fetch.NewDirectBlockCache(0, 0)

	// PerSourceTimeout: 80 ms. Parent context: healthy (no deadline).
	// The per-source timer fires first, producing errPerSourceTimeout via Cause.
	cfg := DirectConfig{
		Browser:          dispatch,
		DDG:              true,
		Brave:            true,
		Retry:            noRetry(),
		PerSourceTimeout: 80 * time.Millisecond,
		EarlyReturnAt:    100,
		BlockCache:       bc,
		OxEscalate:       []string{"ddg", "brave"},
	}

	_, _ = SearchDirect(context.Background(), cfg, "test", "en")

	if !bc.IsBlocked("ddg") {
		t.Error("ddg must be Marked: genuine per-source timeout must trigger escalation")
	}
	if !bc.IsBlocked("brave") {
		t.Error("brave must be Marked: genuine per-source timeout must trigger escalation")
	}
}

// TestRunSourceWithTimeout_DoneBranchDeadlineReconciled is the F3 regression
// guard for the F1 fix: when a source fn returns context.DeadlineExceeded
// through the done channel AND the per-source srcCtx has also expired, the
// error must be reconciled to errPerSourceTimeout so handleSourceError
// classifies it as timeout (not fail) and the engine gets Marked.
//
// Production scenario: BrowserDoer running on srcCtx detects the per-source
// deadline via its HTTP client, returns context.DeadlineExceeded through done.
// The select may pick done before srcCtx.Done(); without F1 the raw
// DeadlineExceeded propagates → timeout-case (errPerSourceTimeout) misses →
// blocked-case excludes DeadlineExceeded → falls to fail → no Mark.
//
// RED-ON-REVERT: remove the done-branch reconciliation block in
// runSourceWithTimeout:
//
//	if err != nil && srcCtx.Err() != nil && errors.Is(err, context.DeadlineExceeded) {
//	    err = context.Cause(srcCtx)
//	}
//
// → when done branch wins the select race, result.err stays context.DeadlineExceeded
// ≠ errPerSourceTimeout → t.Fatalf fires (probability ≈ 1 over 200 iterations).
func TestRunSourceWithTimeout_DoneBranchDeadlineReconciled(t *testing.T) {
	// Run many iterations: Go's select randomly chooses among ready cases.
	// With F1 both branches converge to errPerSourceTimeout. Without F1,
	// the done branch returns context.DeadlineExceeded → test fails.
	const iterations = 200

	for i := range iterations {
		// Per-source context: WithTimeoutCause so Cause == errPerSourceTimeout.
		srcCtx, srcCancel := context.WithTimeoutCause(
			context.Background(), time.Nanosecond, errPerSourceTimeout)
		// Ensure srcCtx has fired: srcCtx.Err() != nil, Cause = errPerSourceTimeout.
		<-srcCtx.Done()
		srcCancel()

		// fn returns context.DeadlineExceeded immediately (simulates BrowserDoer
		// aborting when its internal HTTP client detects per-source deadline).
		fn := func(_ context.Context) ([]sources.Result, error) {
			return nil, context.DeadlineExceeded
		}

		ch := make(chan directResult, 1)
		runSourceWithTimeout(srcCtx, "ddg", fn, ch)
		r := <-ch

		if !errors.Is(r.err, errPerSourceTimeout) {
			t.Fatalf("iter %d: err = %v, want errPerSourceTimeout "+
				"(done-branch must reconcile fn's DeadlineExceeded to errPerSourceTimeout "+
				"when srcCtx fired; F1 regression)", i, r.err)
		}
	}
}
