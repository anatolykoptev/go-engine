package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// slowOrFastServer returns a test server where requests for slowModel sleep
// longer than the given delay before responding, and requests for fastModel
// (or any other model) respond immediately with okBody.
func slowOrFastServer(t *testing.T, slowModel string, delay time.Duration, okBody string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		model, _ := req["model"].(string)
		if model == slowModel {
			// Block until the request context is cancelled (per-attempt timeout
			// fires), or until the delay elapses if somehow not cancelled.
			select {
			case <-r.Context().Done():
				// Caller cancelled — return a server error so the chain
				// receives an error and can advance to the next endpoint.
				http.Error(w, "request cancelled", http.StatusServiceUnavailable)
				return
			case <-time.After(delay):
			}
			// Fell through delay without cancellation — still return a 500
			// so the chain can advance (shouldn't happen in the timeout test).
			http.Error(w, "slow model timeout", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse{
			Choices: []mockChoice{{Message: mockMessage{Content: okBody}}},
		})
	}))
}

// TestWithPerAttemptTimeout_FiresThroughEngine verifies that the engine's
// WithPerAttemptTimeout option delegates to go-kit's transport-level
// per-attempt deadline, cutting the slow primary model and falling over to
// the fast secondary within the expected wall-time window.
func TestWithPerAttemptTimeout_FiresThroughEngine(t *testing.T) {
	const perAttemptTimeout = 120 * time.Millisecond
	const slowDelay = 2 * time.Second // >> perAttemptTimeout

	srv := slowOrFastServer(t, "slow-model", slowDelay, "ok from fast-model")
	defer srv.Close()

	type obsCall struct {
		model string
		ok    bool
	}
	var calls []obsCall
	obs := func(ep Endpoint, err error) {
		calls = append(calls, obsCall{model: ep.Model, ok: err == nil})
	}

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("k"),
		WithModel("slow-model"),
		WithModelFallbackChain([]string{"fast-model"}),
		WithPerAttemptTimeout(perAttemptTimeout),
		WithModelChainObserver(obs),
	)

	start := time.Now()
	// Outer ctx is generous — timeout must be per-attempt, not outer.
	out, err := c.Complete(context.Background(), "hi")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "ok from fast-model" {
		t.Errorf("output = %q, want %q", out, "ok from fast-model")
	}

	// Must complete well within 2 × perAttemptMs + network overhead.
	// Upper bound is generous to avoid flakiness on loaded CI.
	const maxExpected = 500 * time.Millisecond
	if elapsed > maxExpected {
		t.Errorf("elapsed %v exceeds %v — per-attempt timeout did not fire through engine->kit delegation", elapsed, maxExpected)
	}

	// Observer must have seen 2 calls: slow-model (fail) + fast-model (ok).
	if len(calls) != 2 {
		t.Fatalf("observer calls = %d, want 2 (slow-fail + fast-ok): %+v", len(calls), calls)
	}
	if calls[0].model != "slow-model" || calls[0].ok {
		t.Errorf("calls[0] = %+v, want {slow-model, false}", calls[0])
	}
	if calls[1].model != "fast-model" || !calls[1].ok {
		t.Errorf("calls[1] = %+v, want {fast-model, true}", calls[1])
	}
}

// TestWithPerAttemptTimeout_UnsetNoop verifies that without WithPerAttemptTimeout
// the slow model is NOT cut short — the chain only advances on hard errors
// (500), not on time. This confirms backward compatibility: adding the option
// is required to opt in; existing callers are unaffected.
func TestWithPerAttemptTimeout_UnsetNoop(t *testing.T) {
	// Use a delay shorter than our test budget but longer than a fast response,
	// so if per-attempt timeout erroneously fires we'd see the fast fallback.
	// Without the timeout option, the slow model must complete (via the delay
	// elapsing and returning 500, which is retryable → advance to fast-model),
	// OR if the server blocks forever on context cancel — the outer ctx is
	// background so it never cancels. We use a short delay (200ms) so the
	// slow-model actually returns its 500 and the chain advances normally.
	const slowDelay = 200 * time.Millisecond

	srv := slowOrFastServer(t, "slow-for-noop", slowDelay, "ok noop")
	defer srv.Close()

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("k"),
		WithModel("slow-for-noop"),
		WithModelFallbackChain([]string{"fast-for-noop"}),
		// NO WithPerAttemptTimeout
	)

	start := time.Now()
	out, err := c.Complete(context.Background(), "hi")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "ok noop" {
		t.Errorf("output = %q, want %q", out, "ok noop")
	}
	// Without the option, we must have waited at least the slow delay.
	if elapsed < slowDelay {
		t.Errorf("elapsed %v < slowDelay %v — per-attempt timeout fired without being set", elapsed, slowDelay)
	}
}
