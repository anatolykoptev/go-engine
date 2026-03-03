package search

import (
	"context"
	"io"
	"log/slog"
	"sync"

	"github.com/anatolykoptev/go-engine/fetch"
	"github.com/anatolykoptev/go-engine/metrics"
	"github.com/anatolykoptev/go-engine/sources"
)

// BrowserDoer performs HTTP requests with browser-like TLS fingerprint.
// *stealth.BrowserClient satisfies this interface.
type BrowserDoer interface {
	Do(method, url string, headers map[string]string, body io.Reader) ([]byte, map[string]string, int, error)
}

// DirectConfig controls the SearchDirect fan-out behavior.
type DirectConfig struct {
	Browser   BrowserDoer
	DDG       bool
	Startpage bool
	Retry     fetch.RetryConfig
	Metrics   *metrics.Registry
}

// SearchDirect queries enabled direct scrapers in parallel.
// Returns merged results from all direct sources. Failures are non-fatal.
func SearchDirect(ctx context.Context, cfg DirectConfig, query, language string) []sources.Result {
	if cfg.Browser == nil {
		return nil
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var all []sources.Result

	if cfg.DDG {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := fetch.RetryDo(ctx, cfg.Retry, func() ([]sources.Result, error) {
				return SearchDDGDirect(ctx, cfg.Browser, query, "wt-wt", cfg.Metrics)
			})
			if err != nil {
				slog.Debug("ddg direct failed", slog.Any("error", err))
				return
			}
			slog.Debug("ddg direct results", slog.Int("count", len(results)))
			mu.Lock()
			all = append(all, results...)
			mu.Unlock()
		}()
	}

	if cfg.Startpage {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := fetch.RetryDo(ctx, cfg.Retry, func() ([]sources.Result, error) {
				return SearchStartpageDirect(ctx, cfg.Browser, query, language, cfg.Metrics)
			})
			if err != nil {
				slog.Debug("startpage direct failed", slog.Any("error", err))
				return
			}
			slog.Debug("startpage direct results", slog.Int("count", len(results)))
			mu.Lock()
			all = append(all, results...)
			mu.Unlock()
		}()
	}

	wg.Wait()
	return all
}
