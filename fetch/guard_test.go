package fetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kithttputil "github.com/anatolykoptev/go-kit/httputil"
	stealth "github.com/anatolykoptev/go-stealth"
)

// The destination guard must hold in every mode, not only in direct-first mode.
// Each test names the call site whose removal it is meant to catch.

const loopbackIP = "127.0.0.1"

// mapResolver answers from a fixed table and a public default, with no DNS.
type mapResolver map[string]string

func (m mapResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if ip, ok := m[host]; ok {
		return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
	}
	return []net.IPAddr{{IP: net.ParseIP(fixturePublicIP)}}, nil
}

// rebindResolver answers public on the first lookup (the pre-request check) and
// loopback on every later one (the connect-time lookup): a DNS rebind.
type rebindResolver struct{ calls atomic.Int32 }

func (r *rebindResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	if r.calls.Add(1) == 1 {
		return []net.IPAddr{{IP: net.ParseIP(fixturePublicIP)}}, nil
	}
	return []net.IPAddr{{IP: net.ParseIP(loopbackIP)}}, nil
}

func noRetry() Option {
	return WithRetryConfig(RetryConfig{MaxRetries: 0, InitialWait: time.Millisecond, MaxWait: time.Millisecond, Multiplier: 1})
}

// hitCounter is an httptest server that counts the requests it receives.
func hitCounter(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("internal"))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// Plain tier, direct-first OFF, no proxy: the configuration with no other guard.
// Mutation: fetch/fetcher.go New(), build the plain client without the guard
// (assign the raw http.Client instead of f.guard.httpClient(...)).
func TestPlainTier_DirectFirstOff_NoProxy_RefusesLoopback(t *testing.T) {
	srv, hits := hitCounter(t)

	f := New(WithTimeout(5*time.Second), noRetry())
	if f.directFirst || f.HasProxy() {
		t.Fatal("setup: want direct-first off and no proxy")
	}

	// Tier-level call: bypasses the FetchBody pre-check so this pins the client.
	_, err := f.fetchViaHTTP(context.Background(), srv.URL, nil)
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("fetchViaHTTP(loopback) error = %v, want errors.Is ErrSSRFBlocked", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("loopback server saw %d request(s), want 0", got)
	}

	// Public entry point.
	_, err = f.FetchBody(context.Background(), srv.URL)
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("FetchBody(loopback) error = %v, want errors.Is ErrSSRFBlocked", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("loopback server saw %d request(s) via FetchBody, want 0", got)
	}
}

// A public-looking URL that answers 30x with a Location on an internal address
// is refused at the hop. The hop is simulated with a resolver table, no DNS.
// Mutation: same as above (plain client built without the guard).
func TestPlainTier_PublicRedirectToInternal_RefusedAtHop(t *testing.T) {
	cases := map[string]string{
		"name resolving to loopback": "http://internal.test/secret",
		"literal loopback":           "http://127.0.0.1:9/secret",
		"cloud metadata literal":     "http://169.254.169.254/latest/meta-data/",
	}
	for name, location := range cases {
		t.Run(name, func(t *testing.T) {
			var secretHits, startHits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/start" {
					startHits.Add(1)
					http.Redirect(w, r, location, http.StatusFound)
					return
				}
				secretHits.Add(1)
				_, _ = w.Write([]byte("internal"))
			}))
			defer srv.Close()
			counted := &countingTransport{next: dialTo(srv)}

			f := New(
				WithTimeout(5*time.Second), noRetry(),
				func(f *Fetcher) {
					f.guard.resolver = mapResolver{"internal.test": loopbackIP}
					f.guard.transport = counted
				},
			)

			_, err := f.fetchViaHTTP(context.Background(), "http://"+fixtureHost+"/start", nil)
			if !errors.Is(err, ErrSSRFBlocked) {
				t.Fatalf("redirect to %s: error = %v, want errors.Is ErrSSRFBlocked", location, err)
			}
			if startHits.Load() != 1 {
				t.Fatalf("start URL hits = %d, want 1 (the public hop is allowed)", startHits.Load())
			}
			if secretHits.Load() != 0 || counted.hits.Load() != 1 {
				t.Fatalf("internal hop reached the network: secret hits %d, transport requests %d (want 0 and 1)",
					secretHits.Load(), counted.hits.Load())
			}
		})
	}
}

// A name that is public when checked and internal when dialed is refused at
// dial time. The real dial-guarded transport is used; only DNS is faked.
// Mutation: same as above (plain client built without the guard); the request
// then reaches the listener.
func TestPlainTier_HostRebindingToLoopback_RefusedAtDial(t *testing.T) {
	srv, hits := hitCounter(t)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	res := &rebindResolver{}

	f := New(WithTimeout(5*time.Second), noRetry(), func(f *Fetcher) { f.guard.resolver = res })

	// "localhost" really resolves to loopback for the system resolver, so a
	// client with no connect-time guard would reach the listener; the fake
	// resolver answers public for the pre-request check only.
	_, err := f.fetchViaHTTP(context.Background(), "http://localhost:"+port+"/", nil)
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("rebinding host: error = %v, want errors.Is ErrSSRFBlocked", err)
	}
	if res.calls.Load() < 2 {
		t.Fatalf("resolver consulted %d time(s), want >= 2 (pre-request check then dial)", res.calls.Load())
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("loopback server saw %d request(s), want 0", got)
	}
}

// A public host still fetches through the plain tier, and the guard's own
// resolver is the one consulted (the hook is a resolver and a network leg, not
// a switch).
func TestPlainTier_PublicHost_StillFetches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("public page"))
	}))
	defer srv.Close()

	f := New(withFixtureNetwork(srv), WithTimeout(5*time.Second), noRetry())
	body, err := f.FetchBody(context.Background(), fixtureURL(srv))
	if err != nil {
		t.Fatalf("FetchBody(public host): %v", err)
	}
	if string(body) != "public page" {
		t.Fatalf("body = %q, want %q", body, "public page")
	}
}

// FetchBody refuses a blocked destination before any tier runs, including the
// forwarding fallbacks. Mock tiers never guard themselves, so this pins the
// single choke point.
// Mutation: fetch/fetcher.go FetchBodyWithHeaders, delete the f.guard.checkRaw
// call at the top.
func TestFetchBody_BlockedDestination_ReachesNoTier(t *testing.T) {
	var oxHits atomic.Int32
	ox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oxHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ox.Close()

	direct := &fakeDoer{status: http.StatusOK, body: largeHTML("internal"), hdrs: map[string]string{"content-type": "text/html"}}
	proxy := &fakeDoer{status: http.StatusOK, body: largeHTML("internal"), hdrs: map[string]string{"content-type": "text/html"}}

	for _, target := range []string{
		"http://127.0.0.1:8080/health",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1:8080/health",
		"file:///etc/passwd",
	} {
		f := newDirectFirstBase()
		f.directClient = direct
		f.proxyClient = proxy
		WithOxBrowser(ox.URL)(f)

		_, err := f.FetchBody(context.Background(), target)
		if !errors.Is(err, ErrSSRFBlocked) {
			t.Errorf("FetchBody(%s) error = %v, want errors.Is ErrSSRFBlocked", target, err)
		}
	}
	if direct.calls.Load() != 0 || proxy.calls.Load() != 0 || oxHits.Load() != 0 {
		t.Fatalf("a tier was reached: direct %d, proxy %d, ox-browser %d (want 0 each)",
			direct.calls.Load(), proxy.calls.Load(), oxHits.Load())
	}
}

// errResolver fails every lookup the way the system resolver does for a dead name.
type errResolver struct{}

func (errResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// go-kit fails closed on a lookup error: a name that does not resolve is refused
// before any tier or forwarder, without a retry, and is not counted as an attack.
// Mutation: fetch/fetcher.go countRefusal, drop the !isResolveFailure(err) clause.
func TestFetchBody_UnresolvableHost_IsRefusedAndNotCounted(t *testing.T) {
	var oxHits atomic.Int32
	ox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oxHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ox.Close()
	direct := &fakeDoer{status: http.StatusOK, body: largeHTML("x"), hdrs: map[string]string{"content-type": "text/html"}}

	m := &atomicMetrics{}
	f := newDirectFirstBase()
	f.guard.resolver = errResolver{}
	f.directClient = direct
	f.metrics = m
	WithOxBrowser(ox.URL)(f)

	_, err := f.FetchBody(context.Background(), "https://dead-host.example.com/")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("error = %v, want errors.Is ErrSSRFBlocked (fail closed on a lookup error)", err)
	}
	if direct.calls.Load() != 0 || oxHits.Load() != 0 {
		t.Errorf("a tier was reached for an unresolvable host: direct %d, ox-browser %d", direct.calls.Load(), oxHits.Load())
	}
	if got := m.ssrfBlocked.Load(); got != 0 {
		t.Errorf("ssrf-blocked counter = %d for a name that failed to resolve, want 0", got)
	}
}

// A guard refusal raised by a tier (a redirect hop, a rebound dial) is terminal:
// the direct tier does not read it as an anti-bot block, so nothing is escalated
// to the proxy or plain tier, no forwarder is called, and the host is not
// remembered as "direct blocked" (which would send the next request straight to
// the plain tier).
// Mutations: (a) fetch/fetcher.go fetchDirectFirstWithHeaders, delete the
// isSSRFBlocked(directErr) early return; (b) fetch/fetcher.go tryFallbacks,
// delete the isSSRFBlocked(err) early return.
func TestDirectFirst_GuardRefusal_IsTerminal(t *testing.T) {
	for _, withProxy := range []bool{false, true} {
		name := "no proxy"
		if withProxy {
			name = "with proxy"
		}
		t.Run(name, func(t *testing.T) {
			var oxHits atomic.Int32
			ox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				oxHits.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer ox.Close()

			plainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("plain tier body"))
			}))
			defer plainSrv.Close()
			plain := &countingTransport{next: dialTo(plainSrv)}

			// The tier's own guard refuses (as a redirect hop into an internal
			// address would): the error the real stealth client returns.
			direct := &fakeDoer{err: fmt.Errorf("tls request: redirect hop: %w", stealth.ErrSSRFBlocked)}
			proxy := &fakeDoer{status: http.StatusOK, body: largeHTML("proxy body"), hdrs: map[string]string{"content-type": "text/html"}}

			m := &atomicMetrics{}
			f := newDirectFirstBase(func(f *Fetcher) { f.guard.transport = plain })
			f.directClient = direct
			f.metrics = m
			WithOxBrowser(ox.URL)(f)
			if withProxy {
				f.proxyClient = proxy
			}

			const target = "http://target.example.com/page"
			for i := 1; i <= 2; i++ {
				_, err := f.FetchBody(context.Background(), target)
				if !errors.Is(err, ErrSSRFBlocked) {
					t.Errorf("request %d: error = %v, want errors.Is ErrSSRFBlocked", i, err)
				}
			}

			if got := direct.calls.Load(); got != 2 {
				t.Errorf("direct tier called %d times, want 2 (a refusal must not put the host in the block cache)", got)
			}
			if f.blockCache.IsBlocked("target.example.com") {
				t.Error("host was added to the direct-block cache after a guard refusal")
			}
			if got := proxy.calls.Load(); got != 0 {
				t.Errorf("proxy tier called %d times after a guard refusal, want 0", got)
			}
			if got := plain.hits.Load(); got != 0 {
				t.Errorf("plain HTTP tier made %d request(s) after a guard refusal, want 0", got)
			}
			if got := oxHits.Load(); got != 0 {
				t.Errorf("ox-browser forwarder received %d request(s) after a guard refusal, want 0", got)
			}
			if got := m.ssrfBlocked.Load(); got != 2 {
				t.Errorf("ssrf-blocked counter = %d, want 2", got)
			}
		})
	}
}

// A refused connect surfaces as a *net.OpError, which the retry classifier treats
// as a transient network failure. terminalIfBlocked must take it out of that
// class without losing errors.Is matching on either sentinel.
// Mutation: fetch/guard.go terminalIfBlocked, return err unchanged.
func TestTerminalIfBlocked_RefusedDial_IsNotRetryable(t *testing.T) {
	refused := &net.OpError{
		Op: "dial", Net: "tcp",
		Err: fmt.Errorf("%w: 127.0.0.1 (tcp)", kithttputil.ErrSSRFBlocked),
	}
	if !stealth.IsRetryable(refused) {
		t.Fatal("setup: a raw refused dial is expected to look retryable to the retry layer")
	}

	got := terminalIfBlocked(fmt.Errorf("Get %q: %w", "http://x/", refused))
	if stealth.IsRetryable(got) {
		t.Error("a guard refusal is retryable: the retry layer would re-dial it with backoff")
	}
	if !errors.Is(got, ErrSSRFBlocked) || !errors.Is(got, kithttputil.ErrSSRFBlocked) {
		t.Errorf("errors.Is lost the sentinel through terminalIfBlocked: %v", got)
	}
	unrelated := errors.New("boom")
	if !errors.Is(terminalIfBlocked(unrelated), unrelated) || isSSRFBlocked(terminalIfBlocked(unrelated)) {
		t.Error("terminalIfBlocked must return a non-guard error unchanged")
	}
	if terminalIfBlocked(nil) != nil {
		t.Error("terminalIfBlocked(nil) must be nil")
	}
}

// A refusal on the plain tier is a single attempt: no backoff, no second dial.
// Mutation: fetch/fetcher.go fetchViaHTTP, drop terminalIfBlocked around Do.
func TestPlainTier_GuardRefusal_IsOneAttempt(t *testing.T) {
	srv, hits := hitCounter(t)
	f := New(WithTimeout(5*time.Second), WithRetryConfig(RetryConfig{
		MaxRetries: 3, InitialWait: 200 * time.Millisecond, MaxWait: 200 * time.Millisecond, Multiplier: 1,
	}))

	start := time.Now()
	_, err := f.fetchViaHTTP(context.Background(), srv.URL, nil)
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("error = %v, want errors.Is ErrSSRFBlocked", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("loopback server saw %d request(s), want 0", hits.Load())
	}
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Errorf("refusal took %v: it was retried with backoff (want one attempt)", elapsed)
	}
	if !strings.Contains(err.Error(), "SSRF") {
		t.Errorf("error text %q does not say it was a guard refusal", err)
	}
}
