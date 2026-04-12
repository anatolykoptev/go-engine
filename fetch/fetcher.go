// Package fetch provides HTTP body retrieval with retry, proxy rotation,
// and Chrome TLS fingerprint impersonation via go-stealth.
//
// The [Fetcher] is the main entry point. Configure with functional options:
//
//	f := fetch.New(fetch.WithProxyPool(pool), fetch.WithTimeout(30*time.Second))
//	body, err := f.FetchBody(ctx, "https://example.com")
package fetch

import (
	"context"
	"fmt"
	"net/http"
	"time"

	stealth "github.com/anatolykoptev/go-stealth"
	"github.com/anatolykoptev/go-stealth/proxypool"
)

// Default configuration values.
const (
	defaultTimeout             = 30 * time.Second
	defaultMaxIdleConns        = 20
	defaultMaxIdleConnsPerHost = 5
	defaultIdleConnTimeout     = 30 * time.Second
	defaultTLSHandshakeTimeout = 15 * time.Second
	defaultMaxRedirects        = 10
	browserClientTimeoutSec    = 15
)

// RetryConfig controls retry behavior (re-exported from go-stealth).
type RetryConfig = stealth.RetryConfig

// DefaultRetryConfig is suitable for most HTTP calls.
var DefaultRetryConfig = stealth.DefaultRetryConfig

// FetchRetryConfig is tuned for web page fetching (slower initial, more patience).
var FetchRetryConfig = RetryConfig{
	MaxRetries:  3,
	InitialWait: 1 * time.Second,
	MaxWait:     10 * time.Second,
	Multiplier:  2.0,
}

// Fetcher retrieves HTTP response bodies with optional proxy routing.
type Fetcher struct {
	httpClient     *http.Client
	browserClient  *stealth.BrowserClient
	retryConfig    RetryConfig
	retryTracker   *stealth.RetryTracker
	proxyPool      proxypool.ProxyPool    // deferred: used to build browserClient in New()
	cookieProvider stealth.CookieProvider // deferred: passed to stealth.NewClient in New()
	byparrURL      string                 // Byparr fallback URL (empty = disabled)
	oxBrowserURL   string                 // ox-browser /fetch-smart fallback (empty = disabled)
	goBrowserURL   string                 // go-browser /render fallback (empty = disabled)
}

// Option configures a Fetcher.
type Option func(*Fetcher)

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(f *Fetcher) { f.httpClient.Timeout = d }
}

// WithRetryConfig sets the retry configuration.
func WithRetryConfig(rc RetryConfig) Option {
	return func(f *Fetcher) { f.retryConfig = rc }
}

// WithProxyPool enables proxy rotation via a ProxyPool.
// The BrowserClient is created in New() after all options are applied,
// so WithCookieSolver can be combined regardless of option order.
func WithProxyPool(pool proxypool.ProxyPool) Option {
	return func(f *Fetcher) {
		if pool != nil {
			f.proxyPool = pool
		}
	}
}

// WithCookieSolver enables Cloudflare challenge solving via go-stealth's CookieProvider.
// Requires WithProxyPool (no effect without a BrowserClient).
func WithCookieSolver(provider stealth.CookieProvider) Option {
	return func(f *Fetcher) {
		f.cookieProvider = provider
	}
}

// New creates a Fetcher with the given options.
func New(opts ...Option) *Fetcher {
	f := &Fetcher{
		httpClient: &http.Client{
			Timeout: defaultTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        defaultMaxIdleConns,
				MaxIdleConnsPerHost: defaultMaxIdleConnsPerHost,
				IdleConnTimeout:     defaultIdleConnTimeout,
				DisableCompression:  false,
				TLSHandshakeTimeout: defaultTLSHandshakeTimeout,
			},
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= defaultMaxRedirects {
					return fmt.Errorf("stopped after %d redirects", defaultMaxRedirects)
				}
				return nil
			},
		},
		retryConfig: FetchRetryConfig,
	}
	for _, o := range opts {
		o(f)
	}

	// Build BrowserClient after all options are applied (order-independent).
	if f.proxyPool != nil {
		stealthOpts := []stealth.ClientOption{
			stealth.WithTimeout(browserClientTimeoutSec),
			stealth.WithProxyPool(f.proxyPool),
			stealth.WithFollowRedirects(),
		}
		if f.cookieProvider != nil {
			stealthOpts = append(stealthOpts, stealth.WithCookieSolver(f.cookieProvider))
		}
		if bc, err := stealth.NewClient(stealthOpts...); err == nil {
			f.browserClient = bc
		}
	}

	return f
}

// FetchBody retrieves the response body bytes from a URL.
// Routes through BrowserClient (residential proxy) when available,
// falls back to standard HTTP client otherwise.
// When a RetryTracker is configured, it checks ShouldRetry before each request
// and records the outcome (attempt or success) after.
func (f *Fetcher) FetchBody(ctx context.Context, url string) ([]byte, error) {
	permanent := f.retryTracker != nil && !f.retryTracker.ShouldRetry(url)
	if permanent && !f.hasFallback() {
		return nil, ErrPermanentlyFailed
	}

	body, err := f.fetchPrimary(ctx, url, permanent)
	body, err = f.tryFallbacks(ctx, url, body, err)

	if f.retryTracker != nil {
		if err != nil {
			f.retryTracker.RecordAttempt(url, err)
		} else {
			f.retryTracker.RecordSuccess(url)
		}
	}

	return body, err
}

// hasFallback reports whether any fallback renderer is configured.
func (f *Fetcher) hasFallback() bool {
	return f.oxBrowserURL != "" || f.goBrowserURL != "" || f.byparrURL != ""
}

// fetchPrimary attempts the primary fetch method (proxy or plain HTTP).
func (f *Fetcher) fetchPrimary(ctx context.Context, url string, permanent bool) ([]byte, error) {
	switch {
	case permanent:
		return nil, ErrPermanentlyFailed
	case f.browserClient != nil:
		return f.fetchViaProxy(ctx, url)
	default:
		return f.fetchViaHTTP(ctx, url)
	}
}

// tryFallbacks runs the fallback chain when the primary fetch fails.
// Order: ox-browser → go-browser /render → legacy Byparr.
// Each fallback gets the shorter of its own timeout or the parent context deadline.
func (f *Fetcher) tryFallbacks(ctx context.Context, url string, body []byte, err error) ([]byte, error) {
	if err != nil && f.oxBrowserURL != "" {
		obCtx, obCancel := childTimeout(ctx, oxBrowserTimeout+5*time.Second)
		defer obCancel()
		if fallback, obErr := f.fetchViaOxBrowser(obCtx, url); obErr == nil {
			return fallback, nil
		}
	}

	if err != nil && f.goBrowserURL != "" {
		if ctx.Err() != nil {
			return body, err
		}
		gbCtx, gbCancel := childTimeout(ctx, goBrowserTimeout)
		defer gbCancel()
		if fallback, gbErr := f.fetchViaGoBrowser(gbCtx, url); gbErr == nil {
			return fallback, nil
		}
	}

	if err != nil && f.byparrURL != "" {
		if ctx.Err() != nil {
			return body, err
		}
		fbCtx, fbCancel := childTimeout(ctx, byparrTimeout)
		defer fbCancel()
		if fallback, fbErr := f.fetchViaByparr(fbCtx, url); fbErr == nil {
			return fallback, nil
		}
	}

	return body, err
}

// childTimeout returns a context with the shorter of maxDur or the parent's remaining deadline.
func childTimeout(parent context.Context, maxDur time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < maxDur {
			return context.WithTimeout(parent, remaining)
		}
	}
	return context.WithTimeout(parent, maxDur)
}

// HasProxy reports whether the fetcher has a proxy-backed BrowserClient.
func (f *Fetcher) HasProxy() bool {
	return f.browserClient != nil
}

// BrowserClient returns the underlying stealth BrowserClient, or nil.
// Use this to share the browser client with search functions (DDG, Startpage).
func (f *Fetcher) BrowserClient() *stealth.BrowserClient {
	return f.browserClient
}

// fetchViaProxy routes through BrowserClient with Chrome TLS fingerprint.
func (f *Fetcher) fetchViaProxy(ctx context.Context, fetchURL string) ([]byte, error) {
	headers := ChromeHeaders()
	headers["accept"] = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

	return RetryDo(ctx, f.retryConfig, func() ([]byte, error) {
		data, _, status, err := f.browserClient.Do(http.MethodGet, fetchURL, headers, nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, &stealth.HttpStatusError{StatusCode: status}
		}
		return data, nil
	})
}

// fetchViaHTTP uses the standard HTTP client with retry.
func (f *Fetcher) fetchViaHTTP(ctx context.Context, fetchURL string) ([]byte, error) {
	resp, err := RetryHTTP(ctx, f.retryConfig, func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", RandomUserAgent())
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Accept-Encoding", "gzip, deflate")
		return f.httpClient.Do(req) //nolint:gosec // URL is user-provided by design
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &stealth.HttpStatusError{StatusCode: resp.StatusCode}
	}

	return ReadResponseBody(resp)
}
