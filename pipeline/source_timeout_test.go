package pipeline

import (
	"context"
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
// runPipelineSourceWithTimeout never blocks permanently on the send, even when
// the caller's srcCtx fires before the inner goroutine completes. This tests
// the "done MUST be buffered (cap >= 1)" invariant documented on the function.
func TestRunPipelineSourceWithTimeout_BufferedChannelInvariant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	slow := &slowSource{
		name:    "slow",
		delay:   200 * time.Millisecond, // outlives srcCtx
		res:     []sources.Result{{Title: "late"}},
	}

	ch := make(chan pipelineSourceResult, 1)

	start := time.Now()
	runPipelineSourceWithTimeout(ctx, slow, "q", ch)
	elapsed := time.Since(start)

	// The function must return promptly once ctx fires (not block on the inner fn).
	if elapsed > 200*time.Millisecond {
		t.Errorf("runPipelineSourceWithTimeout blocked for %v, should have returned at ~50ms ctx deadline", elapsed)
	}

	r := <-ch
	if r.err == nil {
		t.Error("expected context error, got nil")
	}
}
