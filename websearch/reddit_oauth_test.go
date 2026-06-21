package websearch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

// spyTokenManager is a RedditTokenManager that records Invalidate calls.
type spyTokenManager struct {
	token           string
	tokenErr        error
	invalidated     bool
	invalidateCount int
}

func (s *spyTokenManager) Token(_ context.Context, _ BrowserDoer) (string, error) {
	return s.token, s.tokenErr
}

func (s *spyTokenManager) Invalidate() {
	s.invalidated = true
	s.invalidateCount++
}

// redditListingFixture is a valid OAuth listing response (same shape as public API).
const redditListingFixture = `{
	"data": {
		"children": [
			{
				"data": {
					"title": "OAuth search result",
					"permalink": "/r/golang/comments/xyz789/oauth_search_result/",
					"selftext": "Found via OAuth.",
					"score": 99,
					"num_comments": 7,
					"subreddit": "golang",
					"url": "https://www.reddit.com/r/golang/comments/xyz789/oauth_search_result/"
				}
			}
		]
	}
}`

// TestSearchOAuth_429_ReturnsRateLimited verifies that a 429 response from
// oauth.reddit.com is mapped to *ErrRateLimited.
// Mutation gate: remove the isRateLimitStatus(status) check → status 429 falls
// through as a generic error → errors.As(err, &rl) fails → test fails.
func TestSearchOAuth_429_ReturnsRateLimited(t *testing.T) {
	tm := &spyTokenManager{token: "bearer-tok"}

	doer := &mockBrowser{fn: func(method, url string, headers map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if method == http.MethodPost {
			// token POST — should not be called since tm provides token directly
			t.Errorf("unexpected POST to token endpoint from SearchOAuth")
		}
		return []byte("rate limited"), nil, http.StatusTooManyRequests, nil
	}}

	_, err := SearchOAuth(context.Background(), doer, tm, "golang", "test-agent/1.0")
	if err == nil {
		t.Fatal("expected error on 429, got nil")
	}

	var rl *ErrRateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("expected *ErrRateLimited, got %T: %v", err, err)
	}
}

// TestSearchOAuth_RateLimitJSON verifies that a rate-limit JSON body (200 OK)
// is also mapped to *ErrRateLimited.
// Mutation gate: remove isRedditRateLimited check → falls through to ParseRedditJSON
// which will produce 0 results and no error → test fails.
func TestSearchOAuth_RateLimitJSON(t *testing.T) {
	tm := &spyTokenManager{token: "bearer-tok"}

	body := `{"error": 429, "message": "Too Many Requests"}`
	doer := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte(body), nil, http.StatusOK, nil
	}}

	_, err := SearchOAuth(context.Background(), doer, tm, "golang", "test-agent/1.0")
	if err == nil {
		t.Fatal("expected error on rate-limit JSON body, got nil")
	}

	var rl *ErrRateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("expected *ErrRateLimited on JSON body, got %T: %v", err, err)
	}
}

// TestSearchOAuth_401_InvalidatesAndErrors verifies that a 401 from the
// search endpoint calls tm.Invalidate() and returns a non-nil, non-credential error.
// Mutation gate: remove the 401→Invalidate branch → tm.Invalidate() never called
// → invalidated==false → test fails.
func TestSearchOAuth_401_InvalidatesAndErrors(t *testing.T) {
	tm := &spyTokenManager{token: "bearer-tok"}

	doer := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte(`{"error":"401","message":"Unauthorized"}`), nil, http.StatusUnauthorized, nil
	}}

	_, err := SearchOAuth(context.Background(), doer, tm, "golang", "test-agent/1.0")
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
	if !tm.invalidated {
		t.Error("expected tm.Invalidate() to be called on 401 response, but it was not")
	}
	// Must NOT be ErrCredentialInvalid — 401 on GET means expired token, not bad creds.
	if errors.Is(err, ErrCredentialInvalid) {
		t.Error("401 on GET must not return ErrCredentialInvalid (token expired ≠ bad credentials)")
	}
}

// TestSearchOAuth_200_ParsesResults verifies that a 200 response is parsed
// correctly via the existing ParseRedditJSON.
// Mutation gate: replace ParseRedditJSON with a stub returning nil →
// len(results)==0 → len check fails → test fails.
func TestSearchOAuth_200_ParsesResults(t *testing.T) {
	tm := &spyTokenManager{token: "bearer-tok"}

	doer := &mockBrowser{fn: func(method, url string, headers map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		// Verify Authorization header is set correctly.
		if headers["Authorization"] != "Bearer bearer-tok" {
			t.Errorf("Authorization header = %q, want %q", headers["Authorization"], "Bearer bearer-tok")
		}
		return []byte(redditListingFixture), nil, http.StatusOK, nil
	}}

	results, err := SearchOAuth(context.Background(), doer, tm, "golang", "test-agent/1.0")
	if err != nil {
		t.Fatalf("SearchOAuth: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Title != "OAuth search result" {
		t.Errorf("Title = %q, want %q", results[0].Title, "OAuth search result")
	}
	if results[0].Metadata["engine"] != "reddit" {
		t.Errorf("Metadata[engine] = %q, want reddit", results[0].Metadata["engine"])
	}
}

// TestSearchOAuth_TokenError verifies that a token fetch error is propagated.
func TestSearchOAuth_TokenError(t *testing.T) {
	tokenErr := errors.New("token-fetch-failed")
	tm := &spyTokenManager{tokenErr: tokenErr}

	doer := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		t.Error("doer should not be called when token fetch fails")
		return nil, nil, 0, nil
	}}

	_, err := SearchOAuth(context.Background(), doer, tm, "golang", "test-agent/1.0")
	if err == nil {
		t.Fatal("expected error from token failure, got nil")
	}
	if !errors.Is(err, tokenErr) {
		t.Errorf("expected tokenErr in chain, got %v", err)
	}
}

// TestSearchOAuth_429_WithRetryAfter verifies that a 429 response with a lowercase
// "retry-after" header (as returned by go-stealth BrowserDoer backends which
// canonicalise all header keys to lowercase) sets rl.RetryAfter = 120s.
// Mutation gate: change respHeaders["retry-after"] back to respHeaders["Retry-After"]
// in reddit.go → ok not true → rl.RetryAfter stays 0 → test fails.
func TestSearchOAuth_429_WithRetryAfter(t *testing.T) {
	tm := &spyTokenManager{token: "bearer-tok"}

	doer := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte("rate limited"), map[string]string{"retry-after": "120"}, http.StatusTooManyRequests, nil
	}}

	_, err := SearchOAuth(context.Background(), doer, tm, "golang", "test-agent/1.0")
	if err == nil {
		t.Fatal("expected error on 429, got nil")
	}

	var rl *ErrRateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("expected *ErrRateLimited, got %T: %v", err, err)
	}
	const want = 120 * time.Second
	if rl.RetryAfter != want {
		t.Errorf("RetryAfter = %v, want %v (lowercase header not read)", rl.RetryAfter, want)
	}
}
