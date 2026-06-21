package websearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTokenDoer is a BrowserDoer that returns canned responses for token endpoint calls.
type fakeTokenDoer struct {
	calls   atomic.Int64
	fn      func(method, url string, headers map[string]string, body io.Reader) ([]byte, map[string]string, int, error)
}

func (f *fakeTokenDoer) Do(method, url string, headers map[string]string, body io.Reader) ([]byte, map[string]string, int, error) {
	f.calls.Add(1)
	return f.fn(method, url, headers, body)
}

// tokenJSON builds a canned access_token JSON response.
func tokenJSON(token string, expiresIn int) []byte {
	return []byte(fmt.Sprintf(`{"access_token":%q,"token_type":"bearer","expires_in":%d}`, token, expiresIn))
}

// TestTokenManager_CacheHit verifies that two Token() calls within margin hit
// the fake doer exactly once.
// Mutation gate: remove the `now() < expiresAt` guard and the second call
// triggers a second doer call → test fails.
func TestTokenManager_CacheHit(t *testing.T) {
	fixedNow := time.Now()
	now := func() time.Time { return fixedNow }

	creds := RedditCredentials{
		ClientID:     "test-id",
		ClientSecret: "test-secret",
		UserAgent:    "test-agent/1.0",
	}

	doer := &fakeTokenDoer{fn: func(method, url string, headers map[string]string, body io.Reader) ([]byte, map[string]string, int, error) {
		if method != http.MethodPost {
			t.Errorf("expected POST, got %s", method)
		}
		if !strings.Contains(url, "access_token") {
			t.Errorf("unexpected token URL: %s", url)
		}
		return tokenJSON("cached-token", 3600), nil, http.StatusOK, nil
	}}

	tm := NewRedditTokenManager(creds, now)

	tok1, err := tm.Token(context.Background(), doer)
	if err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if tok1 != "cached-token" {
		t.Fatalf("first Token = %q, want cached-token", tok1)
	}

	tok2, err := tm.Token(context.Background(), doer)
	if err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if tok2 != "cached-token" {
		t.Fatalf("second Token = %q, want cached-token", tok2)
	}

	if n := doer.calls.Load(); n != 1 {
		t.Errorf("doer called %d times, want 1 (cache should prevent second call)", n)
	}
}

// TestTokenManager_RefreshAfterMargin verifies that once the clock advances
// past expiresAt, a second Token() call refreshes via doer.
// Mutation gate: freeze now() so it never advances → refresh never triggers → test fails.
func TestTokenManager_RefreshAfterMargin(t *testing.T) {
	var currentTime time.Time
	currentTime = time.Now()
	now := func() time.Time { return currentTime }

	creds := RedditCredentials{
		ClientID:     "test-id",
		ClientSecret: "test-secret",
		UserAgent:    "test-agent/1.0",
	}

	callCount := 0
	doer := &fakeTokenDoer{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		callCount++
		if callCount == 1 {
			return tokenJSON("first-token", 100), nil, http.StatusOK, nil
		}
		return tokenJSON("refreshed-token", 3600), nil, http.StatusOK, nil
	}}

	tm := NewRedditTokenManager(creds, now)

	tok1, err := tm.Token(context.Background(), doer)
	if err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if tok1 != "first-token" {
		t.Fatalf("first Token = %q, want first-token", tok1)
	}

	// Advance clock well past the expiry window (100s - 60s margin = 40s window + 1s)
	currentTime = currentTime.Add(41 * time.Second)

	tok2, err := tm.Token(context.Background(), doer)
	if err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if tok2 != "refreshed-token" {
		t.Fatalf("second Token = %q, want refreshed-token", tok2)
	}

	if callCount != 2 {
		t.Errorf("doer called %d times, want 2 (should have refreshed)", callCount)
	}
}

// TestTokenManager_401_OnPost verifies that a 401 from the token endpoint
// returns ErrCredentialInvalid and nothing is cached.
// Mutation gate: remove the 401 guard → error becomes a wrapped non-credential
// error → errors.Is(err, ErrCredentialInvalid) fails → test fails.
func TestTokenManager_401_OnPost(t *testing.T) {
	creds := RedditCredentials{
		ClientID:     "test-id",
		ClientSecret: "test-secret",
		UserAgent:    "test-agent/1.0",
	}

	doer := &fakeTokenDoer{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte(`{"error":"invalid_grant"}`), nil, http.StatusUnauthorized, nil
	}}

	tm := NewRedditTokenManager(creds, nil)

	_, err := tm.Token(context.Background(), doer)
	if err == nil {
		t.Fatal("expected error on 401 token POST, got nil")
	}
	if !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("expected ErrCredentialInvalid, got %T: %v", err, err)
	}

	// Nothing should be cached: a second call must hit doer again.
	var secondCallCount atomic.Int64
	doer2 := &fakeTokenDoer{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		secondCallCount.Add(1)
		return tokenJSON("fresh-token", 3600), nil, http.StatusOK, nil
	}}
	tok, err := tm.Token(context.Background(), doer2)
	if err != nil {
		t.Fatalf("retry Token: %v", err)
	}
	if tok != "fresh-token" {
		t.Fatalf("retry Token = %q, want fresh-token", tok)
	}
	if secondCallCount.Load() != 1 {
		t.Error("expected doer2 to be called once (nothing cached from 401)")
	}
}
