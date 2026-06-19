package pipeline

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/anatolykoptev/go-engine/sources"
)

// slowSource simulates a source that blocks for `delay` before returning results.
// It does NOT observe context cancellation — modelling a BrowserDoer.Do that
// ignores context (TCP stall scenario).
type slowSource struct {
	name  string
	delay time.Duration
	res   []sources.Result
}

func (s *slowSource) Name() string { return s.name }
func (s *slowSource) Search(_ context.Context, _ sources.Query) ([]sources.Result, error) {
	// Deliberately ignore ctx to test that the outer timeout still fires.
	time.Sleep(s.delay)
	return s.res, nil
}

// TestSearchSources_PerSourceTimeout verifies that a slow source does not delay
// the fan-out past the per-source deadline. Without the per-source timeout the
// test would block for at least 2s; with it the result should arrive in < 500ms.
//
// Falsification: if the per-source-timeout logic is removed from searchSources
// (e.g. reverting to plain wg.Wait()), the test fails because the slow source
// takes 2s and the 500ms ceiling is exceeded.
func TestSearchSources_PerSourceTimeout(t *testing.T) {
	fast := &mockSource{
		name:    "fast",
		results: []sources.Result{{Title: "fast result", URL: "http://fast.example.com"}},
	}
	slow := &slowSource{
		name:  "slow",
		delay: 2 * time.Second, // much longer than perSourceTimeout
	}

	p := NewPipeline(
		WithSources(fast, slow),
		WithPerSourceTimeout(200*time.Millisecond),
		WithEarlyReturnAt(100), // high enough to not trigger early-return
	)

	start := time.Now()
	results := p.searchSources(context.Background(), "test query")
	elapsed := time.Since(start)

	// The slow source must NOT delay the result past the per-source timeout.
	// Allow generous 500ms headroom above the 200ms timeout for CI scheduling jitter.
	if elapsed > 500*time.Millisecond {
		t.Errorf("searchSources took %v, want <= 500ms (slow source should have been cut off at 200ms)", elapsed)
	}

	// Fast source results must still arrive.
	if len(results) == 0 {
		t.Error("expected at least one result from fast source")
	}
	found := false
	for _, r := range results {
		if r.Title == "fast result" {
			found = true
			break
		}
	}
	if !found {
		t.Error("fast source result not found in output")
	}
}

// TestSearchSources_EarlyReturn verifies that once earlyReturnAt results are
// collected, in-flight sources are cancelled and the fan-out returns promptly
// without waiting for the remaining sources to finish.
func TestSearchSources_EarlyReturn(t *testing.T) {
	fast1 := &mockSource{
		name:    "fast1",
		results: []sources.Result{{Title: "r1"}, {Title: "r2"}},
	}
	fast2 := &mockSource{
		name:    "fast2",
		results: []sources.Result{{Title: "r3"}},
	}
	slow := &slowSource{
		name:  "slow",
		delay: 2 * time.Second,
		res:   []sources.Result{{Title: "slow-result"}},
	}

	// earlyReturnAt=3: should return as soon as 3 results arrive from fast1+fast2,
	// cancelling the slow source before it finishes its 2s sleep.
	p := NewPipeline(
		WithSources(fast1, fast2, slow),
		WithPerSourceTimeout(3*time.Second),
		WithEarlyReturnAt(3),
	)

	start := time.Now()
	results := p.searchSources(context.Background(), "test")
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Errorf("searchSources took %v, want <= 500ms (early-return should fire after 3 results)", elapsed)
	}
	// Final count may exceed earlyReturnAt (spec allows it), but must be >= 3.
	if len(results) < 3 {
		t.Errorf("results = %d, want >= 3", len(results))
	}
}

// TestRunPipelineSourceWithTimeout_BufferedChannelInvariant verifies that
// runPipelineSourceWithTimeout never leaks the inner goroutine, even when the
// caller's srcCtx fires before the inner goroutine completes. This tests the
// "done MUST be buffered (cap >= 1)" invariant documented on the function.
//
// Mechanism: we call runPipelineSourceWithTimeout N times with a source whose
// sleep outlives srcCtx. After each call we immediately drain ch (freeing the
// outer send slot). With a BUFFERED done chan the inner goroutine can drain
// its result into done and exit. With an UNBUFFERED done chan the inner
// goroutine is permanently blocked on "done <- fnResult{...}" because nobody
// receives from done after runPipelineSourceWithTimeout returns on the
// srcCtx.Done branch.
//
// After waiting for the sources to finish sleeping we assert that the goroutine
// count returned to baseline. A single leaked goroutine might hide inside ±1
// scheduler noise, but N=5 leaks cannot: they raise the count by 5, which is
// well above any jitter threshold.
//
// Falsification: change done := make(chan fnResult, 1) to make(chan fnResult)
// in runPipelineSourceWithTimeout; the NumGoroutine assertion goes RED with a
// count delta of N.
func TestRunPipelineSourceWithTimeout_BufferedChannelInvariant(t *testing.T) {
	const N = 5
	srcDelay := 200 * time.Millisecond
	ctxTimeout := 50 * time.Millisecond

	// Sample goroutine baseline BEFORE any calls.
	runtime.GC()
	baseline := runtime.NumGoroutine()

	ch := make(chan pipelineSourceResult, N)

	// Launch N calls concurrently. Each ctx fires at ~50ms; each source sleeps
	// 200ms, so each inner goroutine is still alive when the outer call returns.
	for i := range N {
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		defer cancel()
		slow := &slowSource{
			name:  "slow",
			delay: srcDelay,
			res:   []sources.Result{{Title: "late"}},
		}
		start := time.Now()
		runPipelineSourceWithTimeout(ctx, slow, "q", ch)
		elapsed := time.Since(start)
		if elapsed > 200*time.Millisecond {
			t.Errorf("call %d: runPipelineSourceWithTimeout blocked %v, want <= 200ms", i, elapsed)
		}
		// Drain ch to prevent the outer pipelineSourceResult send from blocking
		// (ch is buffered N, so we're fine not draining immediately, but drain
		// anyway to keep the goroutine count signal clean).
		r := <-ch
		if r.err == nil {
			t.Errorf("call %d: expected context error, got nil", i)
		}
	}

	// Wait for inner goroutines to either:
	//   - drain into the buffered done chan and exit (buffered case — correct)
	//   - block permanently on unbuffered done (unbuffered case — leak)
	// srcDelay=200ms, ctxTimeout=50ms → inner goroutines need up to ~150ms more
	// after the outer call returns. Add 100ms CI jitter budget.
	time.Sleep(srcDelay + 100*time.Millisecond)

	after := runtime.NumGoroutine()
	// Each leaked inner goroutine adds 1 to the count. N=5 leaks → delta=5,
	// which cannot be masked by ±1 scheduler noise.
	if after > baseline+1 {
		t.Errorf("goroutine leak: baseline=%d after=%d (delta=%d) — %d inner goroutines are likely permanently blocked on an unbuffered done channel",
			baseline, after, after-baseline, after-baseline)
	}
}
