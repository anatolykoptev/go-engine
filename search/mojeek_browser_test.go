package search

import (
	"context"
	"testing"
)

// TestRunMojeek_UsesMojeekBrowserWhenSet asserts that when DirectConfig.MojeekBrowser
// is non-nil, runMojeek routes the Mojeek HTTP request through that doer (the
// residential-proxy-backed BrowserClient) instead of the shared cfg.Browser
// (the direct, no-proxy dualBrowser primary).
//
// Why this matters: Mojeek's block from a datacenter egress IP is per-network and
// served before TLS/headers (lighttpd 403 "automated queries"). The shared cfg.Browser
// primary is the direct tlsClientDoer (Chrome_131 JA3 over our DC IP) — which is
// permanently blocked. Only a residential exit IP works, which is the proxy-pool
// BrowserClient. The dualBrowser fallback escalates only on 402/407/5xx, NOT on the
// 403 Mojeek returns, so without a dedicated MojeekBrowser the request never reaches
// the residential proxy. This test pins the per-source routing.
//
// Falsification: revert runMojeek to `SearchMojeekDirect(ctx, cfg.Browser, ...)` and
// mojeekDoer.called stays 0 while sharedBrowser.called becomes 1 → RED.
func TestRunMojeek_UsesMojeekBrowserWhenSet(t *testing.T) {
	sharedBrowser := &stubDoer{status: 200, body: "<html></html>"}
	mojeekDoer := &stubDoer{status: 200, body: "<html></html>"}

	cfg := DirectConfig{
		Browser:       sharedBrowser,
		MojeekBrowser: mojeekDoer,
	}

	_, _ = runMojeek(context.Background(), cfg, "test query")

	if mojeekDoer.called != 1 {
		t.Errorf("runMojeek must route through cfg.MojeekBrowser when set; mojeekDoer.called=%d (want 1)", mojeekDoer.called)
	}
	if sharedBrowser.called != 0 {
		t.Errorf("runMojeek must NOT use cfg.Browser when MojeekBrowser is set; sharedBrowser.called=%d (want 0)", sharedBrowser.called)
	}
}

// TestRunMojeek_FallsBackToSharedBrowserWhenUnset asserts the backward-compatible
// path: when MojeekBrowser is nil (no residential proxy wired), runMojeek uses
// cfg.Browser exactly as before. This guarantees the new field is purely additive
// and the default behaviour is unchanged.
//
// Falsification: make runMojeek always use cfg.MojeekBrowser (even when nil) and this
// panics / fails → RED.
func TestRunMojeek_FallsBackToSharedBrowserWhenUnset(t *testing.T) {
	sharedBrowser := &stubDoer{status: 200, body: "<html></html>"}

	cfg := DirectConfig{
		Browser:       sharedBrowser,
		MojeekBrowser: nil,
	}

	_, _ = runMojeek(context.Background(), cfg, "test query")

	if sharedBrowser.called != 1 {
		t.Errorf("runMojeek must fall back to cfg.Browser when MojeekBrowser is nil; sharedBrowser.called=%d (want 1)", sharedBrowser.called)
	}
}

// TestRunMojeek_TypedNilMojeekBrowserFallsBackToSharedBrowser guards the
// documented typed-nil interface pitfall (the 2026-05-16 go-search prod panic):
// an interface variable holding a typed-nil pointer is NOT == nil, so a plain
// `!= nil` check would treat it as set, route Mojeek through it, and panic in
// bc.Do. runMojeek must use isNilInterface so a typed-nil MojeekBrowser falls
// back to cfg.Browser instead of dispatching on the nil pointer.
//
// Falsification: replace isNilInterface(cfg.MojeekBrowser) with
// cfg.MojeekBrowser != nil and this test panics (RED).
func TestRunMojeek_TypedNilMojeekBrowserFallsBackToSharedBrowser(t *testing.T) {
	sharedBrowser := &stubDoer{status: 200, body: "<html></html>"}

	// A typed-nil *stubDoer boxed into the BrowserDoer interface: != nil is true,
	// isNilInterface is false-positive-free.
	var typedNil *stubDoer
	cfg := DirectConfig{
		Browser:       sharedBrowser,
		MojeekBrowser: typedNil,
	}

	// Must not panic, and must use the shared browser.
	_, _ = runMojeek(context.Background(), cfg, "test query")

	if sharedBrowser.called != 1 {
		t.Errorf("runMojeek must fall back to cfg.Browser when MojeekBrowser is a typed-nil; sharedBrowser.called=%d (want 1)", sharedBrowser.called)
	}
	// Do not dereference typedNil.called — it is a nil *stubDoer. Reaching this
	// line without a panic from runMojeek is itself the proof that runMojeek did
	// NOT dispatch on the typed-nil (isNilInterface caught it); a plain != nil
	// guard would have panicked inside SearchMojeekDirect before returning.
}
