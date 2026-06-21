package search

import (
	"context"
	"errors"
	"testing"

	"github.com/anatolykoptev/go-engine/sources"
	"github.com/anatolykoptev/go-engine/websearch"
)

// redditLegacyFixture is a valid Reddit JSON listing response that SearchRedditDirect
// (and its underlying websearch.Reddit.Search) can parse without error.
const redditLegacyFixture = `{"data":{"children":[{"data":{"title":"Legacy result","permalink":"/r/golang/comments/abc/legacy/","selftext":"body","score":10,"num_comments":2,"subreddit":"golang","url":"https://reddit.com/r/golang/comments/abc/legacy/"}}]}}`

// spyTokenMgr is a minimal RedditTokenManager for runReddit tests.
// It records Token call count and Invalidate call count.
type spyTokenMgr struct {
	token     string
	tokenErr  error
	callCount int
	invCount  int
}

func (s *spyTokenMgr) Token(_ context.Context, _ websearch.BrowserDoer) (string, error) {
	s.callCount++
	return s.token, s.tokenErr
}

func (s *spyTokenMgr) Invalidate() { s.invCount++ }

// okResults returns a non-empty result slice for use as tier return values.
func okResults() []sources.Result {
	return []sources.Result{{Title: "T", URL: "https://example.com/t"}}
}

// TestRunRedditLegacyInvariant asserts that when no tier fields are set in
// DirectConfig, runReddit falls through to the legacy SearchRedditDirect path,
// which routes via cfg.Browser.
//
// RED-ON-REVERT: comment out the legacy call inside runReddit → cfg.Browser is
// never called → sharedBrowser.called == 0 → test fails.
func TestRunRedditLegacyInvariant(t *testing.T) {
	sharedBrowser := &stubDoer{status: 200, body: redditLegacyFixture}

	cfg := DirectConfig{
		Browser: sharedBrowser,
		// All tier fields deliberately zero (nil) → legacy path must activate.
	}

	res, err := runReddit(context.Background(), cfg, "golang")
	if err != nil {
		t.Fatalf("legacy path: unexpected error: %v", err)
	}
	if sharedBrowser.called == 0 {
		t.Error("legacy path: cfg.Browser must be called by SearchRedditDirect, but it was not (legacy call is missing)")
	}
	// Legacy path returns real results parsed from the fixture.
	if len(res) == 0 {
		t.Errorf("legacy path: expected ≥1 result from fixture, got 0")
	}
}

// TestRunRedditTier1SuccessNoEscalation asserts that when RedditTokenManager is
// set and Tier1 returns non-empty results, Tier2 and Tier3 closures are NOT called.
//
// RED-ON-REVERT: remove the early-return on Tier1 success → Tier2/Tier3 called
// despite success → tier2Called > 0 → test fails.
func TestRunRedditTier1SuccessNoEscalation(t *testing.T) {
	// Tier1: OAuth — stubbed token + browser that returns a valid listing JSON.
	oauthBrowser := &stubDoer{status: 200, body: redditLegacyFixture}
	tm := &spyTokenMgr{token: "bearer-tok"}

	tier2Called := 0
	tier3Called := 0

	cfg := DirectConfig{
		Browser:            oauthBrowser,
		RedditTokenManager: tm,
		RedditUserAgent:    "test/1.0",
		RedditCookieSearch: func(_ context.Context, _ string) ([]sources.Result, error) {
			tier2Called++
			return okResults(), nil
		},
		RedditBrowserRender: func(_ context.Context, _ string) ([]sources.Result, error) {
			tier3Called++
			return okResults(), nil
		},
	}

	res, err := runReddit(context.Background(), cfg, "golang")
	if err != nil {
		t.Fatalf("tier1 success: unexpected error: %v", err)
	}
	if len(res) == 0 {
		t.Error("tier1 success: expected ≥1 result, got 0")
	}
	if tier2Called != 0 {
		t.Errorf("tier1 success: Tier2 must NOT be called; got %d call(s)", tier2Called)
	}
	if tier3Called != 0 {
		t.Errorf("tier1 success: Tier3 must NOT be called; got %d call(s)", tier3Called)
	}
}

// TestRunRedditTier1RateLimitedEscalatesToTier2 asserts that when Tier1 returns
// *ErrRateLimited, Tier2 IS called exactly once.
//
// RED-ON-REVERT: remove the Tier1→Tier2 escalation → tier2Called == 0 → test fails.
func TestRunRedditTier1RateLimitedEscalatesToTier2(t *testing.T) {
	// OAuth doer returns 429 → SearchOAuth returns *ErrRateLimited.
	oauthBrowser := &stubDoer{status: 429, body: "rate limited"}
	tm := &spyTokenMgr{token: "bearer-tok"}

	tier2Called := 0
	cfg := DirectConfig{
		Browser:            oauthBrowser,
		RedditTokenManager: tm,
		RedditUserAgent:    "test/1.0",
		RedditCookieSearch: func(_ context.Context, _ string) ([]sources.Result, error) {
			tier2Called++
			return okResults(), nil
		},
	}

	res, err := runReddit(context.Background(), cfg, "golang")
	if err != nil {
		t.Fatalf("tier1 rate-limited: unexpected error: %v", err)
	}
	if tier2Called != 1 {
		t.Errorf("tier1 rate-limited: Tier2 must be called exactly once; got %d", tier2Called)
	}
	if len(res) == 0 {
		t.Error("tier1 rate-limited: expected ≥1 result from Tier2, got 0")
	}
}

// TestRunRedditTier1ParseErrorNoEscalation asserts that a non-escalatable parse
// error from Tier1 is returned immediately without calling Tier2.
//
// RED-ON-REVERT: classify the parse error as escalatable → Tier2 gets called →
// tier2Called == 1 → test fails on the tier2Called == 0 assertion.
func TestRunRedditTier1ParseErrorNoEscalation(t *testing.T) {
	// Token call succeeds; browser returns garbled JSON that ParseRedditJSON cannot parse.
	// The resulting error wraps a json decode error — not *ErrRateLimited → non-escalatable.
	oauthBrowser := &stubDoer{status: 200, body: `not-json-at-all-{{{`}
	tm := &spyTokenMgr{token: "bearer-tok"}

	tier2Called := 0
	cfg := DirectConfig{
		Browser:            oauthBrowser,
		RedditTokenManager: tm,
		RedditUserAgent:    "test/1.0",
		RedditCookieSearch: func(_ context.Context, _ string) ([]sources.Result, error) {
			tier2Called++
			return okResults(), nil
		},
	}

	_, err := runReddit(context.Background(), cfg, "golang")
	if err == nil {
		t.Fatal("parse error path: expected non-nil error, got nil")
	}
	if tier2Called != 0 {
		t.Errorf("parse error path: Tier2 must NOT be called on non-escalatable error; got %d call(s)", tier2Called)
	}
}

// TestRunRedditCredentialInvalidSkipsTier2CallsTier3 asserts the ErrCredentialInvalid
// nuance: Tier1 returns ErrCredentialInvalid → Tier2 (cookie) is SKIPPED → Tier3
// (browser render) IS called.
//
// RED-ON-REVERT:
//   - Remove the ErrCredentialInvalid→skip-Tier2 logic → Tier2 is called → tier2Called==1 → test fails.
//   - Remove the Tier3 escalation for ErrCredentialInvalid → Tier3 not called → tier3Called==0 → test fails.
func TestRunRedditCredentialInvalidSkipsTier2CallsTier3(t *testing.T) {
	// Token manager returns ErrCredentialInvalid (simulate 401 on token endpoint).
	tm := &spyTokenMgr{tokenErr: websearch.ErrCredentialInvalid}

	tier2Called := 0
	tier3Called := 0
	cfg := DirectConfig{
		Browser:            &stubDoer{}, // Browser is wired but oauth will fail before using it
		RedditTokenManager: tm,
		RedditUserAgent:    "test/1.0",
		RedditCookieSearch: func(_ context.Context, _ string) ([]sources.Result, error) {
			tier2Called++
			return okResults(), nil
		},
		RedditBrowserRender: func(_ context.Context, _ string) ([]sources.Result, error) {
			tier3Called++
			return okResults(), nil
		},
	}

	res, err := runReddit(context.Background(), cfg, "golang")
	if err != nil {
		t.Fatalf("credential invalid: unexpected error: %v", err)
	}
	if tier2Called != 0 {
		t.Errorf("credential invalid: Tier2 must be SKIPPED; got %d call(s)", tier2Called)
	}
	if tier3Called != 1 {
		t.Errorf("credential invalid: Tier3 must be called exactly once; got %d call(s)", tier3Called)
	}
	if len(res) == 0 {
		t.Error("credential invalid: expected ≥1 result from Tier3, got 0")
	}
}

// TestRunRedditAllTiersFallThrough asserts graceful-empty: when all active tiers
// fall through (rate-limited / empty), runReddit returns nil, nil.
//
// RED-ON-REVERT: change the fall-through to return an error → err != nil → test fails.
func TestRunRedditAllTiersFallThrough(t *testing.T) {
	// Tier1: 429 → ErrRateLimited.
	oauthBrowser := &stubDoer{status: 429, body: "rate limited"}
	tm := &spyTokenMgr{token: "bearer-tok"}

	// Tier2: rate-limited too.
	rl2 := &ErrRateLimited{Engine: "reddit-cookie"}
	tier2Called := 0

	// Tier3: rate-limited too.
	rl3 := &ErrRateLimited{Engine: "reddit-render"}
	tier3Called := 0

	cfg := DirectConfig{
		Browser:            oauthBrowser,
		RedditTokenManager: tm,
		RedditUserAgent:    "test/1.0",
		RedditCookieSearch: func(_ context.Context, _ string) ([]sources.Result, error) {
			tier2Called++
			return nil, rl2
		},
		RedditBrowserRender: func(_ context.Context, _ string) ([]sources.Result, error) {
			tier3Called++
			return nil, rl3
		},
	}

	res, err := runReddit(context.Background(), cfg, "golang")
	if err != nil {
		t.Fatalf("all-tiers-fall-through: expected nil error, got %v", err)
	}
	if res != nil {
		t.Errorf("all-tiers-fall-through: expected nil result slice, got %v", res)
	}
	if tier2Called != 1 {
		t.Errorf("all-tiers-fall-through: Tier2 must be called once; got %d", tier2Called)
	}
	if tier3Called != 1 {
		t.Errorf("all-tiers-fall-through: Tier3 must be called once; got %d", tier3Called)
	}
}

// TestRunRedditIsEscalatable verifies the escalation predicate directly.
// Rate-limited errors → true. Context errors and parse errors → false.
// ErrCredentialInvalid → false (handled separately in the orchestrator).
//
// RED-ON-REVERT: change isEscalatable to always return true → all errors escalate
// → TestRunRedditTier1ParseErrorNoEscalation will fail (and this test fails too).
func TestRunRedditIsEscalatable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"rate-limited", &ErrRateLimited{Engine: "reddit-oauth"}, true},
		{"context cancelled", context.Canceled, false},
		{"context deadline", context.DeadlineExceeded, false},
		{"parse error", errors.New("reddit parse: json decode: invalid character"), false},
		{"credential invalid", websearch.ErrCredentialInvalid, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEscalatable(tt.err)
			if got != tt.want {
				t.Errorf("isEscalatable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
