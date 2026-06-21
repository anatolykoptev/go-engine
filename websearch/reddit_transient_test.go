package websearch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

// TestSearchOAuth_5xx_ReturnsErrTransient verifies that a 5xx from the OAuth
// search GET is wrapped with ErrTransient.
// RED: ErrTransient does not exist yet → compile error.
func TestSearchOAuth_5xx_ReturnsErrTransient(t *testing.T) {
	tm := &spyTokenManager{token: "bearer-tok"}

	doer := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte("internal server error"), nil, http.StatusInternalServerError, nil
	}}

	_, err := SearchOAuth(context.Background(), doer, tm, "golang", "test-agent/1.0")
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("expected errors.Is(err, ErrTransient) to be true; got %T: %v", err, err)
	}
}

// TestSearchOAuth_503_ReturnsErrTransient verifies that a 503 (service unavailable)
// from the OAuth search GET is also wrapped with ErrTransient.
func TestSearchOAuth_503_ReturnsErrTransient(t *testing.T) {
	tm := &spyTokenManager{token: "bearer-tok"}

	doer := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte("service unavailable"), nil, http.StatusServiceUnavailable, nil
	}}

	_, err := SearchOAuth(context.Background(), doer, tm, "golang", "test-agent/1.0")
	if err == nil {
		t.Fatal("expected error on 503, got nil")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("expected errors.Is(err, ErrTransient) to be true on 503; got %T: %v", err, err)
	}
}

// TestTokenFetch_5xx_ReturnsErrTransient verifies that a 5xx from the token POST
// is wrapped with ErrTransient.
// RED: ErrTransient does not exist yet → compile error.
func TestTokenFetch_5xx_ReturnsErrTransient(t *testing.T) {
	creds := RedditCredentials{
		ClientID:     "cid",
		ClientSecret: "csecret",
		UserAgent:    "test-agent/1.0",
	}
	tm := NewRedditTokenManager(creds, nil)

	doer := &mockBrowser{fn: func(method, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if method == http.MethodPost {
			return []byte("internal server error"), nil, http.StatusInternalServerError, nil
		}
		t.Errorf("unexpected method %s", method)
		return nil, nil, 0, nil
	}}

	_, err := tm.Token(context.Background(), doer)
	if err == nil {
		t.Fatal("expected error on 500 token POST, got nil")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("expected errors.Is(err, ErrTransient) to be true on 500 token POST; got %T: %v", err, err)
	}
}
