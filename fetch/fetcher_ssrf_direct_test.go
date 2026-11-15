package fetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	kithttputil "github.com/anatolykoptev/go-kit/httputil"
	stealth "github.com/anatolykoptev/go-stealth"
	"github.com/anatolykoptev/go-stealth/proxypool"
)

// These tests drive the Chrome-TLS tiers (direct and proxy) through their own
// entry points, not FetchBody: FetchBody refuses a blocked destination before
// any tier runs, which would make every assertion here pass whatever the tier's
// own client does.

// TestDirectTier_RefusesLoopbackTarget: direct-first ON is still guarded. The
// internal server's hit counter is the non-vacuous proof.
// Mutation: fetch/fetcher.go New(), build the direct client with no guard
// options (drop f.guard.stealthOptions()...). go-stealth's own stdlib floor then
// still refuses loopback, so this test alone is not RED for that mutation; the
// CGNAT test below is.
func TestDirectTier_RefusesLoopbackTarget(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := New(WithDirectFirst(true), WithTimeout(5*time.Second), noRetry())
	_, _, _, err := f.fetchDirectRaw(context.Background(), srv.URL, nil)
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("direct tier, loopback target: error = %v, want errors.Is ErrSSRFBlocked", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("internal target hit %d times, want 0", got)
	}

	if _, err = f.FetchBody(context.Background(), srv.URL); !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("FetchBody(direct-first, loopback) error = %v, want errors.Is ErrSSRFBlocked", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("internal target hit %d times via FetchBody, want 0", got)
	}
}

// TestDirectTier_UsesGoKitPolicy_NotStealthStdlibFloor proves the direct tier
// carries go-kit's richer policy (RFC 6598 CGNAT, 100.64.0.0/10) and not just
// go-stealth's stdlib default-deny floor, which does not list that range. The
// error must wrap go-kit's sentinel: the stdlib floor's error does not.
// Mutation: fetch/fetcher.go New(), build the direct client with no guard
// options (drop f.guard.stealthOptions()...).
func TestDirectTier_UsesGoKitPolicy_NotStealthStdlibFloor(t *testing.T) {
	f := New(WithDirectFirst(true), WithTimeout(5*time.Second), noRetry())

	start := time.Now()
	_, _, _, err := f.fetchDirectRaw(context.Background(), "http://100.64.0.1/pwn", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a guard refusal for a CGNAT target, got nil")
	}
	if !errors.Is(err, kithttputil.ErrSSRFBlocked) {
		t.Errorf("error = %v, want it to wrap kithttputil.ErrSSRFBlocked (the go-kit policy, not go-stealth's floor)", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("refused request took %v, want near-instant (the guard must fire before connect)", elapsed)
	}
}

// TestProxyTier_CarriesTheSharedGuard: the proxy client is built from the same
// options as the direct one, so its pre-request check refuses the destination
// even though its dial target is the proxy. The pool is an operator-configured
// stub; the request must be refused before it is used.
// Mutation: fetch/fetcher.go New(), drop f.guard.stealthOptions()... from the
// proxy client's options.
func TestProxyTier_CarriesTheSharedGuard(t *testing.T) {
	f := New(WithProxyPool(proxypool.NewStatic("http://203.0.113.9:3128")), WithTimeout(5*time.Second), noRetry())
	if !f.HasProxy() {
		t.Fatal("setup: proxy client was not built")
	}

	_, err := f.fetchViaProxy(context.Background(), "http://100.64.0.1/pwn", nil)
	if !errors.Is(err, kithttputil.ErrSSRFBlocked) {
		t.Fatalf("proxy tier, CGNAT target: error = %v, want go-kit ErrSSRFBlocked", err)
	}
}

// TestStealthOptions_ConnectTimeRefusal pins the connect-time check on a client built from the
// shared options: a name the pre-request check passes (fake resolver) but that
// the system resolver maps to loopback is refused at connect.
func TestStealthOptions_ConnectTimeRefusal(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	g := guard{resolver: publicResolver{}}
	c, err := stealth.NewClient(g.stealthOptions()...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, _, _, err = c.DoCtx(context.Background(), http.MethodGet, "http://localhost:"+port+"/", nil, nil)
	if !errors.Is(err, ErrSSRFBlocked) && !errors.Is(err, stealth.ErrSSRFBlocked) {
		t.Fatalf("connect to a loopback-resolving name: error = %v, want a guard refusal", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("loopback server saw %d request(s), want 0", got)
	}
}
