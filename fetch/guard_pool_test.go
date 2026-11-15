package fetch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anatolykoptev/go-stealth/proxypool"
)

// doerFunc adapts a function to the stealthDoer the proxy and fallback tiers use.
type doerFunc func() ([]byte, map[string]string, int, error)

func (d doerFunc) DoCtx(context.Context, string, string, map[string]string, io.Reader) ([]byte, map[string]string, int, error) {
	return d()
}

// deadProxyOpError is the dial error a request gets when it was pointed at the
// dead emptyPoolProxy, whichever go-stealth version refuses or fails the dial.
func deadProxyOpError() error {
	return &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4zero, Port: 0}, Err: errors.New("connection refused")}
}

// recordHandler captures log records.
type recordHandler struct {
	mu   sync.Mutex
	recs []slog.Record
}

func (h *recordHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recs = append(h.recs, r.Clone())
	return nil
}
func (h *recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordHandler) WithGroup(string) slog.Handler      { return h }

func (h *recordHandler) warns(msgPrefix string) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.recs {
		if r.Level == slog.LevelWarn && strings.HasPrefix(r.Message, msgPrefix) {
			out = append(out, r)
		}
	}
	return out
}

// captureLogs routes the default logger to a recordHandler for the test.
func captureLogs(t *testing.T) *recordHandler {
	t.Helper()
	h := &recordHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// A healthy proxied request that fails for its own reason (here a 404) must not
// be re-sent direct, even when another goroutine drained the pool meanwhile: that
// would put the real address on the wire for a request the proxy was serving.
// The stub proxy drains the pool itself, in the middle of the call, which is the
// interleaving a concurrent request produces.
// Mutation: fetch/fetcher.go fetchViaProxy, `errors.As(err, &dry) &&` →
// `err != nil &&` (fall back on any failure).
func TestProxyTier_HealthyRequestFailing_IsNotResentDirect(t *testing.T) {
	m := &atomicMetrics{}
	f := New(WithTimeout(5*time.Second), noRetry(), withFixtureResolver(), WithMetrics(m))
	f.pool = newGuardedPool(dryPool{})
	direct := &fakeDoer{status: http.StatusOK, body: []byte("real address egress"), hdrs: map[string]string{}}
	f.poolFallback = direct
	f.proxyClient = doerFunc(func() ([]byte, map[string]string, int, error) {
		f.pool.Next() // another call finds the pool dry
		return []byte("not found"), nil, http.StatusNotFound, nil
	})

	_, err := f.fetchViaProxy(context.Background(), "http://target.example.com/", nil)
	var statusErr *HttpStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound {
		t.Fatalf("error = %v, want the proxy's own 404", err)
	}
	if got := direct.calls.Load(); got != 0 {
		t.Fatalf("a healthy proxied request was re-sent direct %d time(s), want 0", got)
	}
	if m.fallbackOK.Load()+m.fallbackErr.Load() != 0 {
		t.Errorf("direct-fallback egress counted for a request the proxy served")
	}
}

// A request whose dial went to the dead proxy is the one that falls back, once,
// and is not retried against the dead proxy first.
// Mutation: fetch/fetcher.go fetchViaProxy, `return nil, &poolDryError{cause: err}`
// → `return nil, terminalIfBlocked(err)`.
func TestProxyTier_DeadProxyDial_FallsBackOnceWithoutRetries(t *testing.T) {
	m := &atomicMetrics{}
	f := New(WithTimeout(5*time.Second), retryThrice(), withFixtureResolver(), WithMetrics(m))
	f.pool = newGuardedPool(dryPool{})
	var proxyCalls atomic.Int32
	f.proxyClient = doerFunc(func() ([]byte, map[string]string, int, error) {
		proxyCalls.Add(1)
		return nil, nil, 0, deadProxyOpError()
	})
	direct := &fakeDoer{status: http.StatusOK, body: []byte("page"), hdrs: map[string]string{}}
	f.poolFallback = direct

	body, err := f.fetchViaProxy(context.Background(), "http://target.example.com/", nil)
	if err != nil || string(body) != "page" {
		t.Fatalf("body = %q, error = %v, want the fallback's page", body, err)
	}
	if got := proxyCalls.Load(); got != 1 {
		t.Errorf("proxy client called %d times, want 1 (the dead proxy is not retried)", got)
	}
	if got := direct.calls.Load(); got != 1 {
		t.Errorf("direct fallback called %d times, want 1", got)
	}
	if m.fallbackOK.Load() != 1 {
		t.Errorf("direct_fallback ok counted %d times, want 1", m.fallbackOK.Load())
	}
}

// A pool that has no proxies at all goes straight to the guarded direct client:
// the proxy client is not asked, so no retries against the dead proxy are paid.
// Mutation: fetch/fetcher.go fetchViaProxy, delete the
// `f.pool.empty() && f.poolFallback != nil` shortcut.
func TestProxyTier_EmptyPool_GoesStraightToFallback(t *testing.T) {
	f := New(WithProxyPool(proxypool.NewStatic()), WithTimeout(5*time.Second), retryThrice(), withFixtureResolver())
	var proxyCalls atomic.Int32
	f.proxyClient = doerFunc(func() ([]byte, map[string]string, int, error) {
		proxyCalls.Add(1)
		return nil, nil, 0, deadProxyOpError()
	})
	direct := &fakeDoer{status: http.StatusOK, body: []byte("page"), hdrs: map[string]string{}}
	f.poolFallback = direct

	body, err := f.fetchViaProxy(context.Background(), "http://target.example.com/", nil)
	if err != nil || string(body) != "page" {
		t.Fatalf("body = %q, error = %v, want the fallback's page", body, err)
	}
	if got := proxyCalls.Load(); got != 0 {
		t.Errorf("proxy client asked %d time(s) with an empty pool, want 0", got)
	}
}

// Dry-pool direct egress is counted on every request and warned about at most
// once per interval.
// Mutation: fetch/fetcher.go fetchViaPoolFallback, replace
// `now-last >= int64(poolWarnInterval)` with `true`.
func TestPoolFallback_CountedEveryTime_WarnedOncePerInterval(t *testing.T) {
	logs := captureLogs(t)
	m := &atomicMetrics{}
	f := New(WithTimeout(5*time.Second), noRetry(), withFixtureResolver(), WithMetrics(m))
	f.pool = newGuardedPool(proxypool.NewStatic())
	f.proxyClient = doerFunc(func() ([]byte, map[string]string, int, error) { return nil, nil, 0, deadProxyOpError() })
	f.poolFallback = &fakeDoer{status: http.StatusOK, body: []byte("page"), hdrs: map[string]string{}}

	for range 3 {
		if _, err := f.fetchViaProxy(context.Background(), "http://target.example.com/", nil); err != nil {
			t.Fatalf("fetchViaProxy: %v", err)
		}
	}
	if got := m.fallbackOK.Load(); got != 3 {
		t.Errorf("direct_fallback counted %d times, want 3", got)
	}
	if got := len(logs.warns("fetch: proxy pool had no proxy")); got != 1 {
		t.Errorf("dry-pool warnings = %d for 3 fallbacks within one interval, want 1", got)
	}
}

// failAfterResolver answers public for the first okCalls lookups, then fails
// them with a DNS error: the entry check passes and a tier's own check fails.
type failAfterResolver struct {
	calls   atomic.Int32
	okCalls int32
}

func (r *failAfterResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if r.calls.Add(1) > r.okCalls {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return []net.IPAddr{{IP: net.ParseIP(fixturePublicIP)}}, nil
}

// A tier's own check failing to resolve the name is reported like the entry
// check's: a resolve failure (not an attack), counted on its own counter and
// logged with the host only.
// Mutation: fetch/fetcher.go FetchBodyWithHeaders, delete the
// `errors.Is(err, ErrResolveFailed)` branch that calls reportResolveRefused.
func TestFetchBody_TierResolveFailure_IsCountedAndLogged(t *testing.T) {
	logs := captureLogs(t)
	m := &atomicMetrics{}
	res := &failAfterResolver{okCalls: 1}
	f := New(WithTimeout(5*time.Second), noRetry(), WithMetrics(m), func(f *Fetcher) { f.guard.resolver = res })

	const target = "http://tier.example.com/secret/path?token=abc"
	_, err := f.FetchBody(context.Background(), target)
	if !errors.Is(err, ErrResolveFailed) || errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("error = %v, want a resolve failure that does not match ErrSSRFBlocked", err)
	}
	if res.calls.Load() < 2 {
		t.Fatalf("lookups = %d: the tier's own check did not run, so the test did not reach the tier path", res.calls.Load())
	}
	if m.resolveRefused.Load() != 1 || m.ssrfBlocked.Load() != 0 {
		t.Errorf("counters: resolve %d (want 1), ssrf %d (want 0)", m.resolveRefused.Load(), m.ssrfBlocked.Load())
	}
	warns := logs.warns("fetch: destination not fetched, name did not resolve")
	if len(warns) != 1 {
		t.Fatalf("resolve warnings = %d, want 1", len(warns))
	}
	warns[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "host" && a.Value.String() != "tier.example.com" {
			t.Errorf("host attribute = %q, want tier.example.com", a.Value.String())
		}
		if strings.Contains(a.Value.String(), "/secret") || strings.Contains(a.Value.String(), "token=") {
			t.Errorf("log attribute %s carries part of the URL: %q", a.Key, a.Value.String())
		}
		return true
	})
}
