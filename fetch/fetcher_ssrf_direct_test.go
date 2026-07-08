package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	kithttputil "github.com/anatolykoptev/go-kit/httputil"
)

// TestDirectTier_SSRFGuard_RedirectToLoopback_Blocked proves the production-wired
// direct tier (New(WithDirectFirst(true)) building f.directClient) refuses to follow
// a redirect into an internal (loopback) target — the redirect-swallow SSRF class
// this wiring closes (go-stealth's backend previously followed 3xx hops internally,
// so the outer client never re-observed them). The internal server's hit counter is
// the non-vacuous proof: a broken guard would show hits > 0.
func TestDirectTier_SSRFGuard_RedirectToLoopback_Blocked(t *testing.T) {
	var internalHits int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&internalHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer internal.Close()

	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/pwn", http.StatusFound)
	}))
	defer attacker.Close()

	f := New(WithDirectFirst(true), WithTimeout(5*time.Second))

	_, err := f.FetchBody(context.Background(), attacker.URL)
	if err == nil {
		t.Fatal("expected SSRF-blocked error for redirect-to-loopback, got nil")
	}
	if got := atomic.LoadInt32(&internalHits); got != 0 {
		t.Fatalf("internal target hit %d times, want 0 (redirect hop must be refused before reaching it)", got)
	}
}

// TestDirectTier_SSRFGuard_UsesGoKitPolicy_NotStealthStdlibFloor proves the direct
// tier is wired to go-kit's RICHER SSRF policy (RFC 6598 CGNAT, 100.64.0.0/10), not
// merely go-stealth v1.18.0's stdlib-only default-deny floor (loopback/private/
// link-local/unspecified/multicast only — see go-stealth's isBlockedIP). CGNAT space
// is none of those, so go-stealth's own built-in tier-3 guard would let a request to
// it through unblocked; only go-kit's IsBlockedIP (which explicitly lists
// 100.64.0.0/10) flags it.
//
// Fetched directly (no redirect indirection): an httptest "attacker" server always
// binds on loopback, so a redirect FROM it is itself blocked by go-stealth's stdlib
// floor before the redirect is even followed — that would make a
// redirect-to-CGNAT test pass regardless of this diff's wiring (vacuous). Targeting
// the CGNAT literal directly isolates exactly the policy upgrade this PR makes.
//
// This is the genuine RED-on-revert case for THIS diff (fetch/fetcher.go's
// WithDialControl/WithRedirectGuard/WithRequestURLGuard calls): the sibling
// loopback-redirect test above stays green even with this wiring fully reverted
// (go-stealth's own stdlib floor already blocks loopback) — it would NOT catch a
// regression that dropped just the go-kit policy upgrade. This test would: reverting
// the wiring lets the CGNAT request through unblocked, changing both the error
// (or nil error) and the errors.Is result below. Verified empirically: reverting the
// three stealth.With*Guard/Control calls in New() turns this test RED.
func TestDirectTier_SSRFGuard_UsesGoKitPolicy_NotStealthStdlibFloor(t *testing.T) {
	f := New(WithDirectFirst(true), WithTimeout(5*time.Second))

	start := time.Now()
	_, err := f.FetchBody(context.Background(), "http://100.64.0.1/pwn")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected SSRF-blocked error for CGNAT target, got nil")
	}
	if !errors.Is(err, kithttputil.ErrSSRFBlocked) {
		t.Errorf("error = %v, want it to wrap kithttputil.ErrSSRFBlocked (proves the go-kit policy fired, not just go-stealth's stdlib floor)", err)
	}
	// The block must fire at RequestURLGuard before any connect(2). A
	// reverted/broken wiring would instead attempt a real TCP dial into unrouted
	// CGNAT space, which is slow (OS connect timeout) rather than near-instant.
	if elapsed > 3*time.Second {
		t.Errorf("blocked request took %v, want near-instant (suggests the guard did not fire before connect)", elapsed)
	}
}
