package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anatolykoptev/go-engine/fetch"
)

// ddgHTML returns minimal DDG HTML with n results.
func ddgHTML(n int) string {
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb,
			`<div class="result"><a class="result__a" href="https://example.com/ddg-%d">DDG Result %d</a><span class="result__snippet">snippet</span></div>`,
			i, i,
		)
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

// startpageHTML returns minimal Startpage HTML with n results.
func startpageHTML(n int) string {
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb,
			`<div class="w-gl__result"><a class="w-gl__result-title" href="https://example.com/sp-%d">SP Result %d</a><p class="w-gl__description">description %d</p></div>`,
			i, i, i,
		)
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

func noRetry() fetch.RetryConfig {
	return fetch.RetryConfig{
		MaxRetries:  0,
		InitialWait: 0,
		MaxWait:     0,
		Multiplier:  1,
	}
}

// TestSearchDirect_SlowSourceDoesNotDelay verifies that a slow/blocked source is
// cancelled by PerSourceTimeout and does not delay return of fast-source results.
//
// RED-ON-REVERT: If the inner goroutine + select on srcCtx.Done() is removed,
// the slow Brave goroutine blocks in Do() until our 30s timer fires → the channel
// is never drained → for-range blocks → test exceeds 1s timing assertion.
func TestSearchDirect_SlowSourceDoesNotDelay(t *testing.T) {
	fastHTML := ddgHTML(3)

	slowDone := make(chan struct{})
	t.Cleanup(func() { close(slowDone) })

	dispatch := &mockBrowser{fn: func(_, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if strings.Contains(url, "duckduckgo") {
			return []byte(fastHTML), nil, http.StatusOK, nil
		}
		// Brave: block until cleanup or 30s safety.
		select {
		case <-slowDone:
			return nil, nil, 0, context.Canceled
		case <-time.After(30 * time.Second):
			return nil, nil, 0, context.DeadlineExceeded
		}
	}}

	cfg := DirectConfig{
		Browser:          dispatch,
		DDG:              true,
		Brave:            true,
		Retry:            noRetry(),
		PerSourceTimeout: 200 * time.Millisecond,
		EarlyReturnAt:    100, // high — early-return must NOT be the mechanism here
	}

	start := time.Now()
	results := SearchDirect(context.Background(), cfg, "test", "en")
	elapsed := time.Since(start)

	if elapsed >= 1*time.Second {
		t.Errorf("SearchDirect took %v, want < 1s (slow source cancelled by PerSourceTimeout=%v)", elapsed, cfg.PerSourceTimeout)
	}
	if len(results) == 0 {
		t.Error("expected results from fast DDG source, got none")
	}
}

// TestSearchDirect_EarlyReturnOnEnoughResults verifies that the fan-out returns
// as soon as EarlyReturnAt results are accumulated, cancelling the slow source.
//
// RED-ON-REVERT: Removing allDoneCancel() inside the earlyAt branch means
// allDoneCtx stays live → slow Brave goroutine is never cancelled early → it runs
// until PerSourceTimeout (5s) → test exceeds 500ms timing assertion.
func TestSearchDirect_EarlyReturnOnEnoughResults(t *testing.T) {
	// DDG and Startpage each return 6 fast results — 12 total exceeds EarlyReturnAt=10.
	fastDDG := ddgHTML(6)
	fastSP := startpageHTML(6)

	slowDone := make(chan struct{})
	t.Cleanup(func() { close(slowDone) })

	dispatch := &mockBrowser{fn: func(_, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if strings.Contains(url, "duckduckgo") {
			return []byte(fastDDG), nil, http.StatusOK, nil
		}
		if strings.Contains(url, "startpage") {
			return []byte(fastSP), nil, http.StatusOK, nil
		}
		// Brave: block until allDoneCtx cancels via early-return.
		select {
		case <-slowDone:
			return nil, nil, 0, context.Canceled
		case <-time.After(30 * time.Second):
			return nil, nil, 0, context.DeadlineExceeded
		}
	}}

	cfg := DirectConfig{
		Browser:          dispatch,
		DDG:              true,
		Startpage:        true,
		Brave:            true,
		Retry:            noRetry(),
		PerSourceTimeout: 5 * time.Second, // long — timeout alone won't save us
		EarlyReturnAt:    10,
	}

	start := time.Now()
	results := SearchDirect(context.Background(), cfg, "test", "en")
	elapsed := time.Since(start)

	if elapsed >= 500*time.Millisecond {
		t.Errorf("SearchDirect took %v, want < 500ms (early-return after %d results)", elapsed, cfg.EarlyReturnAt)
	}
	if len(results) < 10 {
		t.Errorf("got %d results, want >= 10", len(results))
	}
}

// TestSearchDirect_FastResultsNotDiscarded verifies that results from fast sources
// are preserved even when a slow source is present.
//
// RED-ON-REVERT: In the old mutex+WaitGroup pattern, wg.Wait() blocked until all
// goroutines finished. A slow source holding the wait meant callers never saw fast
// results until the slow one timed out. The channel-based drain in collectResults
// guarantees all sent results are read before returning — fast results not lost.
func TestSearchDirect_FastResultsNotDiscarded(t *testing.T) {
	fastHTML := ddgHTML(10)

	slowDone := make(chan struct{})
	t.Cleanup(func() { close(slowDone) })

	dispatch := &mockBrowser{fn: func(_, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if strings.Contains(url, "duckduckgo") {
			return []byte(fastHTML), nil, http.StatusOK, nil
		}
		// Startpage: block until ctx cancel via PerSourceTimeout.
		select {
		case <-slowDone:
			return nil, nil, 0, context.Canceled
		case <-time.After(30 * time.Second):
			return nil, nil, 0, context.DeadlineExceeded
		}
	}}

	cfg := DirectConfig{
		Browser:          dispatch,
		DDG:              true,
		Startpage:        true,
		Retry:            noRetry(),
		EarlyReturnAt:    5, // fires after 5 DDG results → cancels Startpage
		PerSourceTimeout: 200 * time.Millisecond,
	}

	results := SearchDirect(context.Background(), cfg, "test", "en")
	if len(results) < 5 {
		t.Errorf("got %d results, want >= 5; fast DDG results were discarded", len(results))
	}
}
