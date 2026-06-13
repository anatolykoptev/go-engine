package llm

import (
	"context"
	"testing"
	"time"
)

// TestResolveCooldownDuration_Default verifies that with no option and no env
// the helper returns the 15m built-in default.
//
// RED-on-revert: change defaultCooldownDuration to something else and this
// test fails.
func TestResolveCooldownDuration_Default(t *testing.T) {
	t.Setenv("LLM_COOLDOWN_SECONDS", "")
	got := resolveCooldownDuration(0)
	if got != 15*time.Minute {
		t.Errorf("default: got %v, want 15m", got)
	}
}

// TestResolveCooldownDuration_Env verifies that LLM_COOLDOWN_SECONDS overrides
// the built-in default when no explicit option is passed.
//
// RED-on-revert: remove the env branch from resolveCooldownDuration and this
// test fails.
func TestResolveCooldownDuration_Env(t *testing.T) {
	t.Setenv("LLM_COOLDOWN_SECONDS", "3")
	got := resolveCooldownDuration(0)
	if got != 3*time.Second {
		t.Errorf("env: got %v, want 3s", got)
	}
}

// TestResolveCooldownDuration_Explicit verifies that an explicit option wins
// over both env and the built-in default.
//
// RED-on-revert: remove the explicit>0 branch and this test fails.
func TestResolveCooldownDuration_Explicit(t *testing.T) {
	t.Setenv("LLM_COOLDOWN_SECONDS", "3")
	got := resolveCooldownDuration(7 * time.Second)
	if got != 7*time.Second {
		t.Errorf("explicit: got %v, want 7s", got)
	}
}

// TestResolveCooldownDuration_EnvInvalid verifies that a non-numeric or
// non-positive LLM_COOLDOWN_SECONDS falls through to the default.
func TestResolveCooldownDuration_EnvInvalid(t *testing.T) {
	tests := []struct {
		env  string
		name string
	}{
		{"not-a-number", "non-numeric"},
		{"0", "zero"},
		{"-5", "negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LLM_COOLDOWN_SECONDS", tt.env)
			got := resolveCooldownDuration(0)
			if got != 15*time.Minute {
				t.Errorf("%s: got %v, want 15m", tt.name, got)
			}
		})
	}
}

// TestCooldown_WithModelCooldownDuration_Expiry is the primary integration
// proof. It drives a 2-model chain with a 2s cooldown (WithModelCooldownDuration),
// confirms the primary is skipped after FailThreshold 429 hits, waits ~2.1s for
// the cooldown window to expire, then confirms the primary is retried (i.e. the
// short duration was actually wired to kit, not the 15m default).
//
// RED-on-revert: revert WithModelCooldown(CooldownConfig{}) (removing the
// Default field) — the duration is then 60s and the ~2.1s wait does not expire
// the cooldown, so the primary is still skipped in Phase 3 and the assertion
// "primary must be retried after expiry" fails.
func TestCooldown_WithModelCooldownDuration_Expiry(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive: skipped in short mode")
	}

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
		WithModelCooldownDuration(2*time.Second), // short TTL for the test
	)

	ctx := context.Background()

	// Phase 1 — warm-up: exactly FailThreshold (2) calls to enter cooldown.
	for i := range 2 {
		out, err := c.Complete(ctx, "hello")
		if err != nil {
			t.Fatalf("warm-up call %d: %v", i+1, err)
		}
		if out != okBody {
			t.Fatalf("warm-up call %d: got %q, want %q", i+1, out, okBody)
		}
	}
	if primaryHits.Load() < 2 {
		t.Fatalf("expected ≥2 primary hits in warm-up, got %d", primaryHits.Load())
	}

	// Phase 2 — cooldown active: primary must be skipped.
	primaryBefore := primaryHits.Load()
	for i := range 3 {
		out, err := c.Complete(ctx, "while cooled")
		if err != nil {
			t.Fatalf("cooled call %d: %v", i+1, err)
		}
		if out != okBody {
			t.Fatalf("cooled call %d: got %q, want %q", i+1, out, okBody)
		}
	}
	if primaryHits.Load() != primaryBefore {
		t.Errorf("primary hit count grew during cooldown window: before=%d after=%d",
			primaryBefore, primaryHits.Load())
	}

	// Phase 3 — wait for the 2s TTL to expire, then verify primary is retried.
	// The kit's cooling() method is TTL-driven: once the window expires the next
	// call will attempt the primary again (it will still 429, so we just confirm
	// the hit count increased — proving the duration was actually wired).
	time.Sleep(2100 * time.Millisecond)
	primaryBeforeExpiry := primaryHits.Load()
	fallbackBeforeExpiry := fallbackHits.Load()

	out, err := c.Complete(ctx, "after expiry")
	if err != nil {
		t.Fatalf("post-expiry call: %v", err)
	}
	if out != okBody {
		t.Fatalf("post-expiry call: got %q, want %q", out, okBody)
	}

	if primaryHits.Load() <= primaryBeforeExpiry {
		t.Errorf("primary was NOT retried after cooldown expiry (hits before=%d after=%d) — "+
			"duration not wired; defaulting to 15m would keep cooldown active",
			primaryBeforeExpiry, primaryHits.Load())
	}
	if fallbackHits.Load() <= fallbackBeforeExpiry {
		t.Errorf("fallback must have been hit after the expired primary 429 (before=%d after=%d)",
			fallbackBeforeExpiry, fallbackHits.Load())
	}
}
