package search

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/anatolykoptev/go-engine/sources"
	"github.com/anatolykoptev/go-engine/websearch"
)

// TestRunRedditTier1TransientEscalatesToTier2 asserts that when Tier 1 returns
// an ErrTransient-wrapped error, Tier 2 IS called exactly once.
//
// RED: without the isEscalatable(ErrTransient) change, isEscalatable returns false
// for a transient error → Tier 2 is NOT called → tier2Called == 0 → test fails.
func TestRunRedditTier1TransientEscalatesToTier2(t *testing.T) {
	// Token manager returns a transient error (simulating a 5xx from token endpoint
	// that is wrapped with ErrTransient).
	transientErr := fmt.Errorf("reddit oauth: token: reddit token: status 500: %w", websearch.ErrTransient)
	tm := &spyTokenMgr{tokenErr: transientErr}

	tier2Called := 0
	cfg := DirectConfig{
		Browser:            &stubDoer{},
		RedditTokenManager: tm,
		RedditUserAgent:    "test/1.0",
		RedditCookieSearch: func(_ context.Context, _ string) ([]sources.Result, error) {
			tier2Called++
			return okResults(), nil
		},
	}

	res, err := runReddit(context.Background(), cfg, "golang")
	if err != nil {
		t.Fatalf("transient escalation: unexpected error: %v", err)
	}
	if tier2Called != 1 {
		t.Errorf("transient escalation: Tier2 must be called exactly once when Tier1 returns ErrTransient; got %d call(s)", tier2Called)
	}
	if len(res) == 0 {
		t.Error("transient escalation: expected ≥1 result from Tier2, got 0")
	}
}

// TestIsEscalatableTransient verifies that isEscalatable returns true for an
// error wrapping ErrTransient.
//
// RED: without the ErrTransient case in isEscalatable, errors.Is check is missing
// → isEscalatable(transientWrapped) returns false → test fails.
func TestIsEscalatableTransient(t *testing.T) {
	transientWrapped := fmt.Errorf("reddit oauth search: status 500: %w", websearch.ErrTransient)

	if !isEscalatable(transientWrapped) {
		t.Errorf("isEscalatable(ErrTransient-wrapped) = false, want true")
	}

	// Direct sentinel should also be escalatable.
	if !isEscalatable(websearch.ErrTransient) {
		t.Errorf("isEscalatable(websearch.ErrTransient) = false, want true")
	}

	// Sanity-check: non-transient plain error must not escalate.
	if isEscalatable(errors.New("generic error")) {
		t.Error("isEscalatable(generic error) = true, want false")
	}
}
