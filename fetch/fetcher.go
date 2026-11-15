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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strings"
	"sync/atomic"
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

// stealthDoer is a minimal interface satisfied by *stealth.BrowserClient.
// Extracted at consumer point for testability (interface small = easy to mock).
// Both directClient and proxyClient use this interface.
type stealthDoer interface {
	DoCtx(ctx context.Context, method, urlStr string, headers map[string]string, body io.Reader) ([]byte, map[string]string, int, error)
}

// Fetcher retrieves HTTP response bodies with optional proxy routing.
type Fetcher struct {
	httpClient        *http.Client // plain-HTTP tier; always derived from guard in New()
	timeout           time.Duration
	guard             guard // destination policy shared by every client built in New()
	browserClient     *stealth.BrowserClient
	proxyClient       stealthDoer // proxy-tier doer; set to browserClient in New(), injectable in tests
	directClient      stealthDoer // Chrome TLS, no proxy — used for direct-first tier; *stealth.BrowserClient in prod, mock in tests
	retryConfig       RetryConfig
	retryTracker      *stealth.RetryTracker
	proxyPool         proxypool.ProxyPool    // deferred: used to build browserClient in New()
	pool              *guardedPool           // proxyPool wrapped so it never answers "" (see guardedPool)
	lastPoolWarn      atomic.Int64           // unix nanos of the last dry-pool warning
	poolFallback      stealthDoer            // guarded no-proxy client for requests the pool had no proxy for
	cookieProvider    stealth.CookieProvider // deferred: passed to stealth.NewClient in New()
	byparrURL         string                 // Byparr fallback URL (empty = disabled)
	oxBrowserURL      string                 // ox-browser /fetch fallback (empty = disabled)
	goBrowserURL      string                 // go-browser /render fallback (empty = disabled)
	directFirst       bool                   // when true, try direct before proxy
	blockCache        *DirectBlockCache      // tracks hosts that blocked direct requests
	proxyFirstDomains *ProxyFirstDomains     // domains that always skip direct tier
	metrics           fetchMetrics           // tier observability; noopMetrics unless WithMetrics is called
}

// Option configures a Fetcher.
type Option func(*Fetcher)

// WithTimeout sets the HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(f *Fetcher) { f.timeout = d }
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

// WithDirectFirst enables the direct-first tiered fallback strategy.
//
// When true, FetchBody attempts a direct Chrome-TLS request (no proxy) first,
// escalating to the proxy tier only when anti-bot signals are detected:
// HTTP 403/429/503, Cloudflare/PerimeterX/DataDome/Imperva challenge bodies,
// soft-block heuristic (200 OK + body <512B + text/html), or connection errors.
//
// When false (default), legacy proxy-first behaviour is preserved — byte-identical
// to the previous implementation. Consumers must opt in explicitly.
func WithDirectFirst(enabled bool) Option {
	return func(f *Fetcher) { f.directFirst = enabled }
}

// WithProxyFirstHosts extends the built-in proxy-first domain list with extra entries.
// The entries are domain suffixes (e.g. "example.com" matches "sub.example.com").
// Has no effect when WithDirectFirst(false) (default).
func WithProxyFirstHosts(hosts []string) Option {
	return func(f *Fetcher) {
		if f.proxyFirstDomains == nil {
			f.proxyFirstDomains = NewProxyFirstDomains(hosts)
		} else {
			// Merge into a fresh slice to avoid mutating caller's backing array.
			merged := make([]string, 0, len(hosts)+len(defaultProxyFirstHosts))
			merged = append(merged, hosts...)
			merged = append(merged, defaultProxyFirstHosts...)
			f.proxyFirstDomains = NewProxyFirstDomains(merged)
		}
	}
}

// WithBlockTTL sets the direct-block cache TTL (default 10 minutes).
// Has no effect when WithDirectFirst(false) (default).
func WithBlockTTL(d time.Duration) Option {
	return func(f *Fetcher) {
		if f.blockCache == nil {
			f.blockCache = NewDirectBlockCache(d, defaultBlockCacheCap)
		} else {
			f.blockCache.ttl = d
		}
	}
}

// WithMetrics wires a fetchMetrics implementation into the Fetcher for tier
// observability. Use NewPromMetrics(prometheus.DefaultRegisterer) for production.
// Not called by default — metrics are no-ops until this option is applied.
func WithMetrics(m fetchMetrics) Option {
	return func(f *Fetcher) { f.metrics = m }
}

// New creates a Fetcher with the given options.
//
// Every client built here dials a destination the caller supplied, so every
// one is derived from the same destination guard (see guard.go) whatever the
// options: there is no mode in which a tier is built without it.
func New(opts ...Option) *Fetcher {
	f := &Fetcher{
		timeout:     defaultTimeout,
		retryConfig: FetchRetryConfig,
		metrics:     noopMetrics{},
	}
	for _, o := range opts {
		o(f)
	}

	// Plain-HTTP tier: built after the options so the timeout is final, and
	// only ever through the guard.
	f.httpClient = f.guard.httpClient(&http.Client{
		Timeout: f.timeout,
		Transport: &http.Transport{
			MaxIdleConns:        defaultMaxIdleConns,
			MaxIdleConnsPerHost: defaultMaxIdleConnsPerHost,
			IdleConnTimeout:     defaultIdleConnTimeout,
			DisableCompression:  false,
			TLSHandshakeTimeout: defaultTLSHandshakeTimeout,
			// go-kit replaces DialContext, which switches HTTP/2 off unless it
			// is forced; the transport had it before the guard was added.
			ForceAttemptHTTP2: true,
		},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= defaultMaxRedirects {
				return fmt.Errorf("stopped after %d redirects", defaultMaxRedirects)
			}
			return nil
		},
	})

	// Build BrowserClient after all options are applied (order-independent).
	// Proxied tier: the connect-time check sees the proxy (or, when the pool
	// runs dry, the destination itself), the URL and redirect checks see the
	// destination. All three tiers come from the shared guard.
	if f.proxyPool != nil {
		f.pool = newGuardedPool(f.proxyPool)
		stealthOpts := append([]stealth.ClientOption{
			stealth.WithTimeout(browserClientTimeoutSec),
			stealth.WithProxyPool(f.pool),
			stealth.WithProxy(emptyPoolProxy),
			stealth.WithFollowRedirects(),
			stealth.WithRetryOnBlock(2),
		}, f.guard.stealthOptions()...)
		if f.cookieProvider != nil {
			stealthOpts = append(stealthOpts, stealth.WithCookieSolver(f.cookieProvider))
		}
		if bc, err := stealth.NewClient(stealthOpts...); err == nil {
			f.browserClient = bc
			f.proxyClient = bc
		}
		// A request the pool has no proxy for must not dial through the proxy
		// client (its no-proxy dialer carries no connect-time hook); it runs on
		// this guarded direct client instead.
		if fb, err := stealth.NewClient(append([]stealth.ClientOption{
			stealth.WithTimeout(browserClientTimeoutSec),
			stealth.WithFollowRedirects(),
		}, f.guard.stealthOptions()...)...); err == nil {
			f.poolFallback = fb
		}
	}

	// Build a no-proxy Chrome-TLS client for the direct tier (directFirst mode only).
	// Direct tier dials the destination from this process itself; it gets the
	// same shared guard as every other tier.
	// Skip if directClient was already injected (e.g. in tests).
	if f.directFirst && f.directClient == nil {
		if dc, err := stealth.NewClient(append([]stealth.ClientOption{
			stealth.WithTimeout(browserClientTimeoutSec),
			stealth.WithFollowRedirects(),
		}, f.guard.stealthOptions()...)...); err == nil {
			f.directClient = dc
		}
		if f.blockCache == nil {
			f.blockCache = NewDirectBlockCache(defaultBlockTTL, defaultBlockCacheCap)
		}
		if f.proxyFirstDomains == nil {
			f.proxyFirstDomains = NewProxyFirstDomains(nil)
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
	return f.FetchBodyWithHeaders(ctx, url, nil)
}

// FetchBodyWithHeaders retrieves the response body bytes from a URL, merging
// extra HTTP headers over the built-in Chrome browser defaults.
//
// Keys in extra are normalised to lowercase before merging so that
//
//	extra["Accept"] = "application/json"
//
// correctly overrides the built-in "accept" header. Passing nil extra is
// equivalent to calling [FetchBody] — all tiering, fallback, block-cache,
// and metrics behaviour is preserved.
func (f *Fetcher) FetchBodyWithHeaders(ctx context.Context, url string, extra map[string]string) ([]byte, error) {
	// Destination guard, once, before any tier: the forwarding fallbacks
	// (ox-browser, go-browser, Byparr) dial the URL from another service, so a
	// refusal from a primary tier must never reach them as a retry target.
	if err := f.checkDestination(ctx, url); err != nil {
		return nil, err
	}

	permanent := f.retryTracker != nil && !f.retryTracker.ShouldRetry(url)
	if permanent && !f.hasFallback() {
		return nil, ErrPermanentlyFailed
	}

	body, err := f.fetchPrimaryWithHeaders(ctx, url, permanent, extra)
	body, err = f.tryFallbacks(ctx, url, body, err)
	if isSSRFBlocked(err) {
		err = terminalIfBlocked(err)
		f.countRefusal(err) // refused by a tier's own guard: redirect hop, rebind, or proxy/direct URL check
	}
	if errors.Is(err, ErrResolveFailed) {
		f.reportResolveRefused(ctx, url, err) // a tier's own check could not resolve the name
	}

	if f.retryTracker != nil {
		if err != nil {
			f.retryTracker.RecordAttempt(url, err)
		} else {
			f.retryTracker.RecordSuccess(url)
		}
	}

	return body, err
}

// checkDestination is the guard's single entry point for a URL. A blocked
// destination is refused. A name that does not resolve is refused too (a
// forwarder could resolve it to somewhere the local lookup never saw), but only
// after one retry of the lookup, because a transient resolver failure is not
// evidence about the host; it is counted and logged separately and returned as
// ErrResolveFailed, never as ErrSSRFBlocked.
func (f *Fetcher) checkDestination(ctx context.Context, rawURL string) error {
	err := f.guard.checkRaw(ctx, rawURL)
	if err != nil && isSSRFBlocked(err) && resolveCause(err) != nil && ctx.Err() == nil {
		err = f.guard.checkRaw(ctx, rawURL) // one retry, lookups only: no address was blocked
	}
	switch {
	case err == nil:
		return nil
	case isSSRFBlocked(err) && resolveCause(err) != nil:
		f.reportResolveRefused(ctx, rawURL, err)
		return asResolveError(err, hostOf(rawURL))
	default:
		f.countRefusal(err)
		return terminalIfBlocked(err)
	}
}

// reportResolveRefused counts and logs a destination refused because its name
// did not resolve, for the entry check and for a tier's own check alike. The
// log carries the host, never the URL. The caller giving up (cancel, deadline)
// is not the resolver failing and is neither counted nor logged.
func (f *Fetcher) reportResolveRefused(ctx context.Context, rawURL string, err error) {
	if ctx.Err() != nil {
		return
	}
	f.metrics.incResolveRefused()
	slog.Warn("fetch: destination not fetched, name did not resolve",
		slog.String("host", hostOf(rawURL)), slog.Any("error", resolveCause(err)))
}

// hostOf returns the host of rawURL for logging, never the URL itself.
func hostOf(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// countRefusal bumps the guard-refusal counter for a blocked destination. A name
// that merely failed to resolve is refused too (fail closed) but is not counted:
// dead hosts are routine and would drown the signal.
func (f *Fetcher) countRefusal(err error) {
	if isSSRFBlocked(err) && !isResolveFailure(err) {
		f.metrics.incSSRFBlocked()
	}
}

// mergeHeaders returns a copy of ChromeHeaders with extra merged over it.
// Keys in extra are normalised to lowercase to match the ChromeHeaders convention
// (all-lowercase keys) so a single "accept" key survives regardless of input case.
// Passing nil extra returns a plain ChromeHeaders copy.
func mergeHeaders(extra map[string]string) map[string]string {
	h := ChromeHeaders()
	for k, v := range extra {
		h[strings.ToLower(k)] = v
	}
	return h
}

// hasFallback reports whether any fallback renderer is configured.
func (f *Fetcher) hasFallback() bool {
	return f.oxBrowserURL != "" || f.goBrowserURL != "" || f.byparrURL != ""
}

// fetchPrimaryWithHeaders attempts the primary fetch method with optional extra headers.
//
// When directFirst is enabled the order is:
//  1. Domain hint (proxyFirstDomains) → straight to proxy.
//  2. blockCache hit → straight to proxy (repeat-blocked host within TTL).
//  3. Direct attempt via directClient (Chrome TLS, no proxy).
//  4. classifyBlock → if sigHard|sigSoft|sigTLS → mark host in blockCache, escalate to proxy.
//  5. No proxy available → return direct result as-is.
//
// When directFirst is disabled (default) the legacy proxy-first behaviour is preserved.
// extra is forwarded to all HTTP call sites and merged over built-in Chrome defaults.
func (f *Fetcher) fetchPrimaryWithHeaders(ctx context.Context, url string, permanent bool, extra map[string]string) ([]byte, error) {
	if permanent {
		return nil, ErrPermanentlyFailed
	}

	// Legacy mode: proxy-first (or plain HTTP when no proxy configured).
	if !f.directFirst {
		return f.fetchViaProxyOrHTTPWithHeaders(ctx, url, extra)
	}

	return f.fetchDirectFirstWithHeaders(ctx, url, extra)
}

// fetchViaProxyOrHTTPWithHeaders routes through proxy if available, otherwise plain HTTP.
// extra is merged over built-in Chrome headers on each request.
func (f *Fetcher) fetchViaProxyOrHTTPWithHeaders(ctx context.Context, url string, extra map[string]string) ([]byte, error) {
	if f.proxyClient != nil {
		return f.fetchViaProxy(ctx, url, extra)
	}
	return f.fetchViaHTTP(ctx, url, extra)
}

// fetchDirectFirstWithHeaders implements the direct-first tiered fallback strategy.
// extra is merged over built-in Chrome headers on every outbound request.
// Steps:
//  1. Domain hint (proxyFirstDomains) → straight to proxy.
//  2. blockCache hit → straight to proxy (repeat-blocked host within TTL).
//  3. Direct attempt via directClient (Chrome TLS, no proxy).
//  4. classifyBlock → if blocked signal → mark host, escalate to proxy.
//  5. No proxy available → return direct result/error.
func (f *Fetcher) fetchDirectFirstWithHeaders(ctx context.Context, url string, extra map[string]string) ([]byte, error) {
	// 1. Domain hint → skip direct, go straight to proxy.
	if f.proxyFirstDomains != nil && f.proxyFirstDomains.MatchURL(url) {
		f.metrics.incEscalation("domain_hint")
		return f.fetchProxyAndRecordWithHeaders(ctx, url, extra)
	}

	// 2. blockCache hit → skip direct, go straight to proxy.
	var host string
	if u, err := neturl.Parse(url); err == nil {
		host = u.Host
	}
	if f.blockCache != nil && f.blockCache.IsBlocked(host) {
		f.metrics.incEscalation("cached")
		return f.fetchProxyAndRecordWithHeaders(ctx, url, extra)
	}

	// 3. Direct attempt via Chrome-TLS directClient (no proxy).
	body, hdrs, status, directErr := f.fetchDirectRaw(ctx, url, extra)

	// A guard refusal is terminal. classifyBlock reads any transport error as a
	// TLS-level block, which would mark the host in the block cache (so the next
	// request skips the direct tier) and escalate to the proxy/plain tier.
	if isRefusal(directErr) {
		f.metrics.incTier("direct", "err")
		return nil, terminalIfBlocked(directErr)
	}

	// 4. Classify block signal.
	sig := classifyBlock(status, hdrs, body, directErr)
	if sig == sigNone {
		// Direct succeeded cleanly.
		if directErr != nil {
			f.metrics.incTier("direct", "err")
			return nil, directErr
		}
		f.metrics.incTier("direct", "ok")
		return body, nil
	}

	// Blocked — record signal, mark host in cache.
	f.metrics.incBlockSignal(sig.label())
	if f.blockCache != nil && host != "" {
		f.blockCache.Mark(host)
		f.metrics.setBlockCacheHosts(f.blockCache.Len())
	}

	// 5. Escalate to proxy if available.
	if f.proxyClient != nil {
		f.metrics.incEscalation(sig.label())
		return f.fetchProxyAndRecordWithHeaders(ctx, url, extra)
	}

	// No proxy budget — handling depends on signal strength:
	// - sigSoft: body is real content (suspect but not a hard error); return it
	//   and let the caller decide. Returning HttpStatusError{200} would be
	//   semantically confusing (200 OK as error).
	// - sigTLS: connection-level failure; return the original transport error.
	// - sigHard: hard block; return an HttpStatusError with the actual status.
	if sig == sigSoft {
		return body, nil
	}
	if directErr != nil {
		return nil, directErr
	}
	return nil, &HttpStatusError{StatusCode: status}
}

// fetchProxyAndRecordWithHeaders routes to proxy tier, records the tier counter,
// and forwards extra headers.
func (f *Fetcher) fetchProxyAndRecordWithHeaders(ctx context.Context, url string, extra map[string]string) ([]byte, error) {
	body, err := f.fetchViaProxyOrHTTPWithHeaders(ctx, url, extra)
	if err != nil {
		f.metrics.incTier("proxy", "err")
	} else {
		f.metrics.incTier("proxy", "ok")
	}
	return body, err
}

// tryFallbacks runs the fallback chain when the primary fetch fails.
// Order: ox-browser → go-browser /render → legacy Byparr.
// Each fallback gets the shorter of its own timeout or the parent context deadline.
func (f *Fetcher) tryFallbacks(ctx context.Context, url string, body []byte, err error) ([]byte, error) {
	// A destination-guard refusal is about the URL, not the tier: handing the
	// same URL to a forwarder would dial it from a service that has no say here.
	if isRefusal(err) {
		return body, terminalIfBlocked(err)
	}

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
	return f.proxyClient != nil
}

// BrowserClient returns the underlying stealth BrowserClient, or nil.
// Use this to share the browser client with search functions (DDG, Startpage).
func (f *Fetcher) BrowserClient() *stealth.BrowserClient {
	return f.browserClient
}

// DirectClient returns the no-proxy Chrome-TLS stealth client built when
// WithDirectFirst(true) was set. Returns nil otherwise. Consumers should
// nil-check before use.
//
// Implementation note: the internal directClient field is typed as stealthDoer
// (an unexported interface) to allow test injection of mocks. In production the
// concrete type is always *stealth.BrowserClient (built by New()). The type
// assertion returns nil for test doubles, which is safe — callers only use
// DirectClient() to obtain the real client for wiring into search.BrowserDoer.
func (f *Fetcher) DirectClient() *stealth.BrowserClient {
	if f.directClient == nil {
		return nil
	}
	bc, _ := f.directClient.(*stealth.BrowserClient)
	return bc
}

// fetchViaProxy routes through the proxy-tier stealthDoer (BrowserClient with Chrome TLS fingerprint).
// extra headers are merged over the built-in Chrome defaults; caller-supplied values take
// precedence (e.g. extra["Accept"]="application/json" overrides the legacy HTML accept).
func (f *Fetcher) fetchViaProxy(ctx context.Context, fetchURL string, extra map[string]string) ([]byte, error) {
	headers := ChromeHeaders()
	headers["accept"] = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	// Override the UA with the proxy client's identity so it matches the JA3
	// fingerprint the stealth BrowserClient actually presents. stealth.ChromeHeaders
	// returns a Chrome 146 UA while the default profile is Chrome 131 — a
	// self-inconsistent pair. In tests (mock proxyClient), the type assertion
	// fails and the ChromeHeaders default is preserved.
	if bc, ok := f.proxyClient.(*stealth.BrowserClient); ok && bc != nil {
		headers["user-agent"] = bc.Identity().UserAgent
	}
	for k, v := range extra {
		headers[strings.ToLower(k)] = v
	}

	// A pool with no proxies goes straight to the guarded direct client: the dead
	// proxy would only cost go-stealth's retries.
	if f.pool.empty() && f.poolFallback != nil {
		return f.fetchViaPoolFallback(ctx, fetchURL, headers)
	}

	body, err := RetryDo(ctx, f.retryConfig, func() ([]byte, error) {
		data, _, status, err := f.proxyClient.DoCtx(ctx, http.MethodGet, fetchURL, headers, nil)
		if err != nil {
			if isDeadProxyDial(err) {
				return nil, &poolDryError{cause: err}
			}
			return nil, terminalIfBlocked(err)
		}
		if status != http.StatusOK {
			return nil, &stealth.HttpStatusError{StatusCode: status}
		}
		return data, nil
	})
	// The pool ran dry for THIS request: its dial went to the dead emptyPoolProxy.
	// Attribution is per call, from the error. A healthy proxied request that
	// failed for its own reason must not be re-sent direct, which would put the
	// real address on the wire; only this one is.
	var dry *poolDryError
	if errors.As(err, &dry) && f.poolFallback != nil {
		return f.fetchViaPoolFallback(ctx, fetchURL, headers)
	}
	return body, err
}

// poolWarnInterval bounds the dry-pool warning: a drained pool fails every
// request, and one line per request would bury the log.
const poolWarnInterval = time.Minute

// fetchViaPoolFallback runs a request the proxy pool had no proxy for on the
// guarded no-proxy client. This is real-address egress by policy (a dry pool
// falls back to direct), so it is counted on go_engine_fetch_tier_total with
// tier "direct_fallback" and warned about at most once per poolWarnInterval.
func (f *Fetcher) fetchViaPoolFallback(ctx context.Context, fetchURL string, headers map[string]string) ([]byte, error) {
	now := time.Now().UnixNano()
	if last := f.lastPoolWarn.Load(); now-last >= int64(poolWarnInterval) && f.lastPoolWarn.CompareAndSwap(last, now) {
		slog.Warn("fetch: proxy pool had no proxy, fetching on the guarded direct client")
	}
	body, err := RetryDo(ctx, f.retryConfig, func() ([]byte, error) {
		data, _, status, derr := f.poolFallback.DoCtx(ctx, http.MethodGet, fetchURL, headers, nil)
		if derr != nil {
			return nil, terminalIfBlocked(derr)
		}
		if status != http.StatusOK {
			return nil, &stealth.HttpStatusError{StatusCode: status}
		}
		return data, nil
	})
	if err != nil {
		f.metrics.incTier("direct_fallback", "err")
	} else {
		f.metrics.incTier("direct_fallback", "ok")
	}
	return body, err
}

// fetchViaHTTP uses the standard HTTP client with retry.
// extra headers are merged over the built-in defaults; caller-supplied values
// take precedence (e.g. extra["Accept"]="application/json" overrides "text/html").
func (f *Fetcher) fetchViaHTTP(ctx context.Context, fetchURL string, extra map[string]string) ([]byte, error) {
	resp, err := RetryHTTP(ctx, f.retryConfig, func() (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", RandomUserAgent())
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Accept-Encoding", "gzip, deflate")
		// Merge caller-supplied headers over the defaults above.
		// http.Header.Set canonicalises keys, so extra["Accept"] and extra["accept"]
		// both target the same canonical slot.
		for k, v := range extra {
			req.Header.Set(k, v)
		}
		resp, err := f.httpClient.Do(req) //nolint:gosec // URL is user-provided by design
		return resp, terminalIfBlocked(err)
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

// fetchDirectRaw issues a single direct request via directClient (Chrome TLS, no proxy)
// and returns the raw response components without retry.
// extra headers are merged over built-in Chrome defaults before sending; caller-supplied
// values take precedence (e.g. extra["Accept"]="application/json" overrides legacy HTML accept).
// Returns zero values and an error on connection failure.
func (f *Fetcher) fetchDirectRaw(ctx context.Context, fetchURL string, extra map[string]string) (body []byte, hdrs http.Header, status int, err error) {
	if f.directClient == nil {
		return nil, nil, 0, errNoDirectClient
	}

	headers := ChromeHeaders()
	headers["accept"] = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
	// Override the UA with the direct client's identity so it matches the JA3
	// fingerprint the stealth BrowserClient actually presents (same rationale
	// as fetchViaProxy). In tests (mock directClient), the type assertion
	// fails and the ChromeHeaders default is preserved.
	if bc, ok := f.directClient.(*stealth.BrowserClient); ok && bc != nil {
		headers["user-agent"] = bc.Identity().UserAgent
	}
	for k, v := range extra {
		headers[strings.ToLower(k)] = v
	}

	data, respHdrs, respStatus, doErr := f.directClient.DoCtx(ctx, http.MethodGet, fetchURL, headers, nil)
	if doErr != nil {
		return nil, nil, 0, terminalIfBlocked(doErr)
	}

	// Convert stealth response headers (map[string]string) to http.Header.
	h := make(http.Header, len(respHdrs))
	for k, v := range respHdrs {
		h.Set(k, v)
	}

	return data, h, respStatus, nil
}

// errNoDirectClient is returned when fetchDirectRaw is called without a directClient.
var errNoDirectClient = errors.New("fetch: directClient not initialised")
