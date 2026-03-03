package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	stealth "github.com/anatolykoptev/go-stealth"
)

func TestFetcher_WithRetryTracker_SkipsPermanent(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tracker := stealth.NewRetryTracker(3, time.Minute)
	f := New(
		WithTimeout(5*time.Second),
		WithRetryConfig(RetryConfig{
			MaxRetries:  0,
			InitialWait: time.Millisecond,
			MaxWait:     time.Millisecond,
			Multiplier:  1,
		}),
		WithRetryTracker(tracker),
	)

	// First call: should fail and record attempt.
	_, err := f.FetchBody(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 404")
	}

	// Second call: tracker should skip (404 is permanent).
	_, err = f.FetchBody(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrPermanentlyFailed) {
		t.Errorf("expected ErrPermanentlyFailed, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("server called %d times, want 1 (second call skipped by tracker)", got)
	}
}

func TestFetcher_RetryHook_ReceivesAttempts(t *testing.T) {
	var hookCalls atomic.Int32
	hook := func(_ context.Context, _, _ int, _ error) {
		hookCalls.Add(1)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := New(
		WithTimeout(5*time.Second),
		WithRetryConfig(RetryConfig{
			MaxRetries:  2,
			InitialWait: time.Millisecond,
			MaxWait:     10 * time.Millisecond,
			Multiplier:  1,
		}),
	)

	ctx := stealth.WithRetryHook(context.Background(), hook)
	_, _ = f.FetchBody(ctx, srv.URL)

	if got := hookCalls.Load(); got != 2 {
		t.Errorf("hook called %d times, want 2", got)
	}
}
