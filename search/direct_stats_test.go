package search

// Tests for DirectStats: per-leg outcome accounting surfaced by SearchDirect.
//
// These tests guard the dormant-Degraded gap identified by the pr-review-council:
// a fully-blocked fan-out (attempted>0, ok==0) was previously indistinguishable
// from a genuine zero-result return. DirectStats exposes the difference.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-engine/fetch"
)

// TestSearchDirect_Stats_AllBlocked: every enabled leg returns an error.
// Expected: Attempted > 0, OK == 0, Failed == Attempted, Empty == 0.
//
// RED-ON-REVERT: if collectResults stops incrementing stats.Failed (or stops
// returning DirectStats entirely), Attempted stays 0 or OK grows non-zero,
// failing the all-blocked signal assertion.
func TestSearchDirect_Stats_AllBlocked(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, 0, errors.New("blocked by firewall")
	}}

	cfg := DirectConfig{
		Browser: bc,
		DDG:     true,
		Brave:   true,
		Retry:   noRetry(),
	}

	_, stats := SearchDirect(context.Background(), cfg, "test", "en")

	if stats.Attempted == 0 {
		t.Fatal("Attempted == 0; want > 0 (two legs were enabled)")
	}
	if stats.OK != 0 {
		t.Errorf("OK = %d, want 0 (all legs errored)", stats.OK)
	}
	if stats.Failed != stats.Attempted {
		t.Errorf("Failed = %d, want %d (== Attempted; all legs errored)", stats.Failed, stats.Attempted)
	}
	if stats.Empty != 0 {
		t.Errorf("Empty = %d, want 0 (no empty-healthy legs)", stats.Empty)
	}
	// Degraded signal: the key invariant callers check.
	if !(stats.Attempted > 0 && stats.OK == 0) {
		t.Errorf("degraded signal not set: Attempted=%d OK=%d", stats.Attempted, stats.OK)
	}
}

// TestSearchDirect_Stats_SomeOK: at least one leg returns results.
// Expected: OK > 0, Attempted >= OK.
//
// RED-ON-REVERT: if collectResults stops incrementing stats.OK, OK stays 0
// even when results are present — the go-search Collect layer would then
// incorrectly declare Degraded=true on healthy fan-outs.
func TestSearchDirect_Stats_SomeOK(t *testing.T) {
	ddgResult := ddgHTML(3)
	bc := &mockBrowser{fn: func(_, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if strings.Contains(url, "duckduckgo") {
			return []byte(ddgResult), nil, http.StatusOK, nil
		}
		// Brave returns an error — confirms mixed outcome is handled correctly.
		return nil, nil, 0, errors.New("brave blocked")
	}}

	cfg := DirectConfig{
		Browser: bc,
		DDG:     true,
		Brave:   true,
		Retry:   noRetry(),
	}

	results, stats := SearchDirect(context.Background(), cfg, "test", "en")

	if len(results) == 0 {
		t.Fatal("expected results from DDG leg, got none")
	}
	if stats.OK == 0 {
		t.Errorf("OK = 0, want > 0 (DDG leg returned results)")
	}
	if stats.Attempted < stats.OK {
		t.Errorf("Attempted (%d) < OK (%d); invariant broken", stats.Attempted, stats.OK)
	}
	// Degraded signal must NOT fire when at least one leg succeeded.
	if stats.Attempted > 0 && stats.OK == 0 {
		t.Error("degraded signal incorrectly set: OK > 0 should clear it")
	}
}

// TestCollectResults_Stats_GenuineEmpty: legs returning 0 results without error
// (geo-block or silent-empty signature) count as Empty in DirectStats.
//
// This test drives collectResults directly with pre-built directResult values so
// it does not depend on scraper HTML parsing behaviour (different parsers may
// return empty-slice vs error for the same HTML body — that's a scraper concern,
// not a stats concern).
//
// Expected: Attempted = 2, OK = 0, Empty = 2, Failed = 0.
//
// RED-ON-REVERT: if collectResults stops incrementing stats.Empty (e.g. falls
// through to stats.Failed), the silent-block fingerprint becomes indistinguishable
// from a network error at the stats level, losing the diagnostic Empty field.
func TestCollectResults_Stats_GenuineEmpty(t *testing.T) {
	ch := make(chan directResult, 2)
	ch <- directResult{label: "ddg", results: nil, err: nil}
	ch <- directResult{label: "brave", results: nil, err: nil}
	close(ch)

	results, stats := collectResults(ch, nil, 1000, func() {}, nil, nil)

	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
	if stats.Attempted != 2 {
		t.Errorf("Attempted = %d, want 2", stats.Attempted)
	}
	if stats.OK != 0 {
		t.Errorf("OK = %d, want 0 (all legs returned empty)", stats.OK)
	}
	if stats.Empty != 2 {
		t.Errorf("Empty = %d, want 2 (both legs returned nil results, no error)", stats.Empty)
	}
	if stats.Failed != 0 {
		t.Errorf("Failed = %d, want 0 (no error legs)", stats.Failed)
	}
	// Degraded signal holds for the silent-empty case too.
	if !(stats.Attempted > 0 && stats.OK == 0) {
		t.Errorf("degraded signal not set: Attempted=%d OK=%d", stats.Attempted, stats.OK)
	}
}

// TestSearchDirect_Stats_NilBrowser: browser nil → no legs launched.
// Expected: zero-value DirectStats (Attempted == 0).
func TestSearchDirect_Stats_NilBrowser(t *testing.T) {
	cfg := DirectConfig{
		Browser: nil,
		DDG:     true,
	}
	_, stats := SearchDirect(context.Background(), cfg, "test", "en")
	if stats.Attempted != 0 || stats.OK != 0 || stats.Empty != 0 || stats.Failed != 0 {
		t.Errorf("nil browser: want zero stats, got %+v", stats)
	}
}

// TestSearchDirect_Stats_NoEnginesEnabled: no legs enabled → Attempted == 0.
func TestSearchDirect_Stats_NoEnginesEnabled(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, 0, errors.New("should not be called")
	}}
	cfg := DirectConfig{Browser: bc} // no DDG/Brave/etc. enabled
	_, stats := SearchDirect(context.Background(), cfg, "test", "en")
	if stats.Attempted != 0 {
		t.Errorf("no engines enabled: want Attempted=0, got %d", stats.Attempted)
	}
}

// TestSearchDirect_Stats_IsBlockedSkipNotCounted: an IsBlocked-skipped engine
// (BlockCache pre-marked) must NOT count toward Attempted.
// This ensures that BlockCache-skipped engines are transparent to the Degraded signal
// — only launched legs matter.
func TestSearchDirect_Stats_IsBlockedSkipNotCounted(t *testing.T) {
	ddgResult := ddgHTML(2)
	bc := &mockBrowser{fn: func(_, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if strings.Contains(url, "duckduckgo") {
			return []byte(ddgResult), nil, http.StatusOK, nil
		}
		return nil, nil, 0, errors.New("unexpected call to non-DDG engine")
	}}

	cache := fetch.NewDirectBlockCache(0, 0)
	cache.Mark("brave") // pre-block brave

	cfg := DirectConfig{
		Browser:    bc,
		DDG:        true,
		Brave:      true,
		Retry:      noRetry(),
		BlockCache: cache,
	}

	results, stats := SearchDirect(context.Background(), cfg, "test", "en")

	if len(results) == 0 {
		t.Fatal("expected DDG results")
	}
	// Brave was skipped (IsBlocked), DDG ran and succeeded.
	// Attempted should be 1 (DDG only), not 2.
	if stats.Attempted != 1 {
		t.Errorf("Attempted = %d, want 1 (brave was IsBlocked-skipped)", stats.Attempted)
	}
	if stats.OK != 1 {
		t.Errorf("OK = %d, want 1 (DDG returned results)", stats.OK)
	}
}

// TestSearchDirect_Stats_Invariant: OK + Empty + Failed == Attempted for any outcome mix.
// Property test over three representative configurations.
func TestSearchDirect_Stats_Invariant(t *testing.T) {
	ddgResult := ddgHTML(2)

	cases := []struct {
		name string
		cfg  DirectConfig
	}{
		{
			name: "all_ok",
			cfg: DirectConfig{
				Browser: &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
					return []byte(ddgResult), nil, http.StatusOK, nil
				}},
				DDG:   true,
				Brave: true,
				Retry: noRetry(),
			},
		},
		{
			name: "all_fail",
			cfg: DirectConfig{
				Browser: &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
					return nil, nil, 0, errors.New("fail")
				}},
				DDG:   true,
				Brave: true,
				Retry: noRetry(),
			},
		},
		{
			name: "mixed",
			cfg: DirectConfig{
				Browser: &mockBrowser{fn: func(_, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
					if strings.Contains(url, "duckduckgo") {
						return []byte(ddgResult), nil, http.StatusOK, nil
					}
					return nil, nil, 0, errors.New("fail")
				}},
				DDG:   true,
				Brave: true,
				Retry: noRetry(),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stats := SearchDirect(context.Background(), tc.cfg, "test", "en")
			if got := stats.OK + stats.Empty + stats.Failed; got != stats.Attempted {
				t.Errorf("invariant broken: OK(%d)+Empty(%d)+Failed(%d)=%d != Attempted(%d)",
					stats.OK, stats.Empty, stats.Failed, got, stats.Attempted)
			}
		})
	}
}
