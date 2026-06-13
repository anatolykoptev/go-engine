package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// quota429Server returns a chat-completions endpoint where primaryModel always
// returns 429 and fallbackModel always returns 200 with okBody.
// primaryHits and fallbackHits count per-model request hits.
func quota429Server(t *testing.T, primaryModel, fallbackModel, okBody string) (
	*httptest.Server, *atomic.Int64, *atomic.Int64,
) {
	t.Helper()
	var primaryHits, fallbackHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		model, _ := req["model"].(string)
		switch model {
		case primaryModel:
			primaryHits.Add(1)
			http.Error(w, `{"error":{"message":"quota exceeded"}}`, http.StatusTooManyRequests)
		case fallbackModel:
			fallbackHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(mockResponse{
				Choices: []mockChoice{{Message: mockMessage{Content: okBody}}},
			})
		default:
			http.Error(w, "unexpected model: "+model, http.StatusBadRequest)
		}
	}))
	return srv, &primaryHits, &fallbackHits
}

// TestCooldown_SkipsExhaustedModel drives FailThreshold (2) 429 failures on
// the primary, then verifies that subsequent calls skip the primary entirely
// (primaryHits stops climbing) while the fallback continues serving.
//
// RED-on-revert: comment out the `kitllm.WithModelCooldown(...)` line in
// New() and re-run. The primary keeps receiving hits on every call (no skip),
// so the "primary hit count must not grow after cooldown" assertion fails.
func TestCooldown_SkipsExhaustedModel(t *testing.T) {
	const (
		primaryModel  = "primary-quota"
		fallbackModel = "fallback-ok"
		okBody        = "served by fallback"
	)

	srv, primaryHits, fallbackHits := quota429Server(t, primaryModel, fallbackModel, okBody)
	defer srv.Close()

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("test-key"),
		WithModel(primaryModel),
		WithModelFallbackChain([]string{fallbackModel}),
	)

	ctx := context.Background()

	// Phase 1 — warm-up: FailThreshold calls so cooldown activates.
	// Each call: try primary (429) → fallback (200).
	for i := range 2 {
		out, err := c.Complete(ctx, "hello")
		if err != nil {
			t.Fatalf("warm-up call %d: unexpected error: %v", i+1, err)
		}
		if out != okBody {
			t.Fatalf("warm-up call %d: got %q, want %q", i+1, out, okBody)
		}
	}

	if primaryHits.Load() < 2 {
		t.Fatalf("expected ≥2 primary hits during warm-up, got %d", primaryHits.Load())
	}
	if fallbackHits.Load() < 2 {
		t.Fatalf("expected ≥2 fallback hits during warm-up, got %d", fallbackHits.Load())
	}

	// Snapshot before post-cooldown phase.
	primaryBefore := primaryHits.Load()
	fallbackBefore := fallbackHits.Load()

	// Phase 2 — post-cooldown: primary must be skipped on all 3 calls.
	for i := range 3 {
		out, err := c.Complete(ctx, "hello again")
		if err != nil {
			t.Fatalf("post-cooldown call %d: unexpected error: %v", i+1, err)
		}
		if out != okBody {
			t.Fatalf("post-cooldown call %d: got %q, want %q", i+1, out, okBody)
		}
	}

	primaryAfter := primaryHits.Load()
	fallbackAfter := fallbackHits.Load()

	if primaryAfter != primaryBefore {
		t.Errorf("primary hit count grew after cooldown: before=%d after=%d — cooldown did not skip primary",
			primaryBefore, primaryAfter)
	}
	if fallbackAfter <= fallbackBefore {
		t.Errorf("fallback hit count did not grow: before=%d after=%d",
			fallbackBefore, fallbackAfter)
	}
}

// TestCooldown_ObserverFires verifies WithModelCooldownObserver fires a
// cooling=true event when the primary crosses FailThreshold, and that the
// re-exported type compiles correctly.
func TestCooldown_ObserverFires(t *testing.T) {
	const (
		primaryModel  = "primary-quota"
		fallbackModel = "fallback-ok"
	)

	srv, _, _ := quota429Server(t, primaryModel, fallbackModel, "ok")
	defer srv.Close()

	type event struct {
		model   string
		cooling bool
		d       time.Duration
	}
	var events []event

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("k"),
		WithModel(primaryModel),
		WithModelFallbackChain([]string{fallbackModel}),
		WithModelCooldownObserver(func(model string, cooling bool, d time.Duration) {
			events = append(events, event{model: model, cooling: cooling, d: d})
		}),
	)

	ctx := context.Background()

	// Two calls trigger FailThreshold and cause the observer to fire.
	for range 2 {
		_, _ = c.Complete(ctx, "hello")
	}

	if len(events) == 0 {
		t.Fatal("cooldown observer never fired — expected at least one cooling=true event")
	}

	// At least one event must be cooling=true for the primary model.
	var sawEntry bool
	for _, ev := range events {
		if ev.model == primaryModel && ev.cooling {
			sawEntry = true
			if ev.d <= 0 {
				t.Errorf("cooling=true event has d=%v, want >0", ev.d)
			}
		}
	}
	if !sawEntry {
		t.Errorf("no cooling=true event for primary model %q in events %+v", primaryModel, events)
	}
}
