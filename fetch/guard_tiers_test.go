package fetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	kithttputil "github.com/anatolykoptev/go-kit/httputil"
	stealth "github.com/anatolykoptev/go-stealth"
	"github.com/anatolykoptev/go-stealth/proxypool"
)

func retryThrice() Option {
	return WithRetryConfig(RetryConfig{MaxRetries: 3, InitialWait: time.Millisecond, MaxWait: time.Millisecond, Multiplier: 1})
}

// dryPool reports one proxy but has none to give: the state of a pool that runs
// dry between the Fetcher's Len() check and go-stealth's own Next().
type dryPool struct{}

func (dryPool) Next() string { return "" }
func (dryPool) Len() int     { return 1 }
func (dryPool) TransportProxy() func(*http.Request) (*neturl.URL, error) {
	return func(*http.Request) (*neturl.URL, error) { return nil, nil }
}

// A pool that runs dry must not reach a dial that has no connect-time hook.
// go-stealth reads "" from the pool as "no proxy" and tls-client then dials
// through a bare dialer, so a name that is public when checked and loopback when
// connected would be fetched. The pool is wrapped, and the request runs on the
// guarded direct client instead.
// Mutation: fetch/fetcher.go New(), pass f.proxyPool instead of f.pool to
// stealth.WithProxyPool.
func TestProxyTier_PoolRunsDry_RebindingHost_NeverDialsUnguarded(t *testing.T) {
	srv, hits := hitCounter(t)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	m := &atomicMetrics{}
	f := New(WithProxyPool(dryPool{}), WithTimeout(5*time.Second), noRetry(), withFixtureResolver(), WithMetrics(m))
	if !f.HasProxy() {
		t.Fatal("setup: proxy client was not built")
	}

	// "localhost" resolves to loopback for the system resolver at connect time;
	// the fixture resolver answers public for the URL checks.
	_, err := f.fetchViaProxy(context.Background(), "http://localhost:"+port+"/", nil)
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("dry pool, name resolving to loopback: error = %v, want errors.Is ErrSSRFBlocked", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("loopback server saw %d request(s) through a dry pool, want 0", got)
	}
	if m.fallbackErr.Load() != 1 {
		t.Errorf("dry-pool direct egress counted %d times, want 1 (tier direct_fallback)", m.fallbackErr.Load())
	}
}

// A pool that hands out a proxy URL tls-client cannot use makes SetProxy fail,
// and tls-client then rolls back to the PREVIOUS proxy; with none set that is a
// bare direct dialer without the connect-time hook. The proxy client therefore
// starts on a hooked dead proxy, and the request fails instead of dialing out.
// Mutation: fetch/fetcher.go New(), drop stealth.WithProxy(emptyPoolProxy).
func TestProxyTier_UnusableProxyURL_FailsClosedNotDirect(t *testing.T) {
	srv, hits := hitCounter(t)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	f := New(WithProxyPool(proxypool.NewStatic("bogus://proxy.invalid:1")), WithTimeout(5*time.Second), noRetry(), withFixtureResolver())
	_, err := f.fetchViaProxy(context.Background(), "http://localhost:"+port+"/", nil)
	if err == nil {
		t.Fatal("a request through an unusable proxy URL succeeded")
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("loopback server saw %d request(s) through an unusable proxy URL, want 0", got)
	}
}

// flakyResolver fails the first `failures` lookups with a DNS error, then
// answers public.
type flakyResolver struct {
	calls    atomic.Int32
	failures int32
}

func (r *flakyResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if r.calls.Add(1) <= r.failures {
		return nil, &net.DNSError{Err: "server misbehaving", Name: host, IsTemporary: true}
	}
	return []net.IPAddr{{IP: net.ParseIP(fixturePublicIP)}}, nil
}

// A transient resolver failure must not turn into a refused fetch: the lookup is
// retried once before the destination is refused.
// Mutation: fetch/fetcher.go checkDestination, delete the one retry of
// f.guard.checkRaw.
func TestFetchBody_TransientResolverFailure_IsRetriedOnce(t *testing.T) {
	res := &flakyResolver{failures: 1}
	direct := &fakeDoer{status: http.StatusOK, body: largeHTML("page"), hdrs: map[string]string{"content-type": "text/html"}}
	m := &atomicMetrics{}
	f := newDirectFirstBase(func(f *Fetcher) { f.guard.resolver = res })
	f.directClient = direct
	f.metrics = m

	if _, err := f.FetchBody(context.Background(), "http://flaky.example.com/"); err != nil {
		t.Fatalf("FetchBody after one transient lookup failure: %v", err)
	}
	if got := res.calls.Load(); got != 2 {
		t.Errorf("lookups = %d, want 2 (the failure, then the retry)", got)
	}
	if direct.calls.Load() != 1 {
		t.Errorf("direct tier called %d times, want 1", direct.calls.Load())
	}
	if m.resolveRefused.Load() != 0 || m.ssrfBlocked.Load() != 0 {
		t.Errorf("counters moved for a fetch that succeeded: resolve %d, ssrf %d", m.resolveRefused.Load(), m.ssrfBlocked.Load())
	}
}

// A destination that stays unresolvable is refused fail closed, but is reported
// as a resolve failure: it must not match ErrSSRFBlocked, must keep the DNS
// cause reachable, is counted on its own counter, and reaches no tier.
// Mutation: fetch/fetcher.go checkDestination, delete the
// `isSSRFBlocked(err) && resolveCause(err) != nil` case so the error falls
// through to the blocked-destination branch.
func TestFetchBody_UnresolvableHost_IsResolveFailureNotAttack(t *testing.T) {
	var oxHits atomic.Int32
	ox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oxHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ox.Close()
	direct := &fakeDoer{status: http.StatusOK, body: largeHTML("x"), hdrs: map[string]string{"content-type": "text/html"}}

	res := &flakyResolver{failures: 100}
	m := &atomicMetrics{}
	f := newDirectFirstBase(func(f *Fetcher) { f.guard.resolver = res })
	f.directClient = direct
	f.metrics = m
	WithOxBrowser(ox.URL)(f)

	_, err := f.FetchBody(context.Background(), "http://dead-host.example.com/")
	if !errors.Is(err, ErrResolveFailed) {
		t.Fatalf("error = %v, want errors.Is ErrResolveFailed", err)
	}
	if errors.Is(err, ErrSSRFBlocked) {
		t.Errorf("a failed lookup reads as an SSRF refusal: %v", err)
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Errorf("the *net.DNSError cause is not reachable: %v", err)
	}
	if got := res.calls.Load(); got != 2 {
		t.Errorf("lookups = %d, want 2 (one retry)", got)
	}
	if direct.calls.Load() != 0 || oxHits.Load() != 0 {
		t.Errorf("a tier was reached: direct %d, ox-browser %d", direct.calls.Load(), oxHits.Load())
	}
	if m.resolveRefused.Load() != 1 || m.ssrfBlocked.Load() != 0 {
		t.Errorf("counters: resolve %d (want 1), ssrf %d (want 0)", m.resolveRefused.Load(), m.ssrfBlocked.Load())
	}
}

// ctxResolver blocks until the context ends, then fails with the context error,
// as a resolver does when the caller's deadline passes mid-lookup.
type ctxResolver struct{ calls atomic.Int32 }

func (r *ctxResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	r.calls.Add(1)
	<-ctx.Done()
	return nil, fmt.Errorf("lookup %s: %w", host, ctx.Err())
}

// A caller's deadline that ends the lookup is not a resolver failure to count,
// is not retried, and keeps errors.Is(context.DeadlineExceeded) working.
// Mutation: fetch/fetcher.go checkDestination, drop the `ctx.Err() == nil`
// clause on the retry (it would retry on a dead context).
func TestFetchBody_DeadlineDuringLookup_IsNotAnAttackOrCounted(t *testing.T) {
	res := &ctxResolver{}
	m := &atomicMetrics{}
	f := newDirectFirstBase(func(f *Fetcher) { f.guard.resolver = res })
	f.metrics = m

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := f.FetchBody(ctx, "http://slow.example.com/")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want errors.Is context.DeadlineExceeded", err)
	}
	if errors.Is(err, ErrSSRFBlocked) {
		t.Errorf("a deadline reads as an SSRF refusal: %v", err)
	}
	if !errors.Is(err, ErrResolveFailed) {
		t.Errorf("error = %v, want errors.Is ErrResolveFailed", err)
	}
	if got := res.calls.Load(); got != 1 {
		t.Errorf("lookups = %d, want 1 (no retry once the context has ended)", got)
	}
	if m.resolveRefused.Load() != 0 || m.ssrfBlocked.Load() != 0 {
		t.Errorf("counters moved for the caller's own deadline: resolve %d, ssrf %d", m.resolveRefused.Load(), m.ssrfBlocked.Load())
	}
}

// A refused connect arrives as *net.OpError, which the retry layer treats as a
// transient network failure. On the plain tier the refusal must be one attempt.
// The refusal is injected through the dialer's Control hook so the pre-request
// check passes and the connect itself is refused.
// Mutation: fetch/fetcher.go fetchViaHTTP, return err instead of
// terminalIfBlocked(err) from the request closure.
func TestPlainTier_DialTimeRefusal_IsOneAttempt(t *testing.T) {
	var controls atomic.Int32
	dialer := &net.Dialer{Control: func(string, string, syscall.RawConn) error {
		controls.Add(1)
		return fmt.Errorf("%w: refused at connect", kithttputil.ErrSSRFBlocked)
	}}
	f := New(WithTimeout(5*time.Second), retryThrice(), func(f *Fetcher) { f.guard.dialer = dialer })

	_, err := f.fetchViaHTTP(context.Background(), "http://"+fixturePublicIP+":9/", nil)
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("error = %v, want errors.Is ErrSSRFBlocked", err)
	}
	if got := controls.Load(); got != 1 {
		t.Fatalf("connect attempted %d times, want 1 (a refusal is not retried)", got)
	}
}

// The same on the proxy tier: a dial refusal from the proxy client is one
// attempt, not four.
// Mutation: fetch/fetcher.go fetchViaProxy, return err instead of
// terminalIfBlocked(err) from the request closure.
func TestProxyTier_DialTimeRefusal_IsOneAttempt(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("%w: refused at connect", stealth.ErrSSRFBlocked)}
	proxy := &fakeDoer{err: refused}
	f := New(WithTimeout(5*time.Second), retryThrice(), withFixtureResolver())
	f.proxyClient = proxy

	_, err := f.fetchViaProxy(context.Background(), "http://target.example.com/", nil)
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Errorf("error = %v, want errors.Is ErrSSRFBlocked", err)
	}
	if got := proxy.calls.Load(); got != 1 {
		t.Errorf("proxy client called %d times, want 1 (a refusal is not retried)", got)
	}
}

// The connect-time check on a Chrome-TLS client built from stealthOptions() is
// go-kit's. CGNAT is outside go-stealth's own stdlib floor, so only go-kit's
// dial control refuses it, before any connect. The request-URL guard is switched
// off for this client so the dial control is what is under test.
// Mutation: fetch/guard.go stealthOptions, drop stealth.WithDialControl(dial).
func TestStealthOptions_DialControlIsGoKit(t *testing.T) {
	g := guard{}
	c, err := stealth.NewClient(append(g.stealthOptions(),
		stealth.WithRequestURLGuard(nil), stealth.WithTimeout(2))...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	start := time.Now()
	_, _, _, err = c.DoCtx(context.Background(), http.MethodGet, "http://100.64.0.1:81/", nil, nil)
	if !errors.Is(err, kithttputil.ErrSSRFBlocked) {
		t.Fatalf("dial to a CGNAT address: error = %v, want go-kit ErrSSRFBlocked", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("refusal took %v, want it before any connect", elapsed)
	}
}

// A redirect hop into a go-kit-only range is refused by the per-hop guard on a
// Chrome-TLS client. The first leg is a loopback server, so the client's dial
// control and request-URL guard are overridden for the test; the redirect guard
// is the one from stealthOptions().
// Mutation: fetch/guard.go stealthOptions, drop stealth.WithRedirectGuard(redirect).
func TestStealthOptions_RedirectGuardIsGoKit(t *testing.T) {
	var startHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startHits.Add(1)
		http.Redirect(w, r, "http://100.64.0.1:81/pwn", http.StatusFound)
	}))
	defer srv.Close()

	g := guard{}
	c, err := stealth.NewClient(append(g.stealthOptions(),
		stealth.WithRequestURLGuard(nil),
		stealth.WithDialControl(func(string, string) error { return nil }),
		stealth.WithFollowRedirects(), stealth.WithTimeout(2))...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	start := time.Now()
	_, _, _, err = c.DoCtx(context.Background(), http.MethodGet, srv.URL+"/start", nil, nil)
	if !errors.Is(err, kithttputil.ErrSSRFBlocked) {
		t.Fatalf("redirect into a CGNAT address: error = %v, want go-kit ErrSSRFBlocked", err)
	}
	if startHits.Load() != 1 {
		t.Errorf("first leg hit %d times, want 1", startHits.Load())
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("refusal took %v, want it before any connect", elapsed)
	}
}
