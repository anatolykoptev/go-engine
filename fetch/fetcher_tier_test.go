package fetch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDoer is a test double for stealthDoer. Records call counts and returns
// a configurable response.
type fakeDoer struct {
	calls  atomic.Int32
	status int
	body   []byte
	hdrs   map[string]string
	err    error
}

func (f *fakeDoer) DoCtx(_ context.Context, _, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
	f.calls.Add(1)
	return f.body, f.hdrs, f.status, f.err
}

// largeHTML returns an HTML body larger than softBlockBodyThreshold (512 bytes).
func largeHTML(content string) []byte {
	return []byte("<html><body>" + content + strings.Repeat(" ", 600) + "</body></html>")
}

// newDirectFirstBase builds a Fetcher with directFirst enabled, no proxy, fast retries.
// Callers inject directClient and optionally proxyClient.
func newDirectFirstBase() *Fetcher {
	return New(
		WithDirectFirst(true),
		WithTimeout(5*time.Second),
		WithRetryConfig(RetryConfig{
			MaxRetries:  0,
			InitialWait: time.Millisecond,
			MaxWait:     time.Millisecond,
			Multiplier:  1,
		}),
		WithBlockTTL(5*time.Minute),
	)
}

// Test 1: Direct succeeds (200 OK, large body) → proxy never called.
func Test_DirectFirst_HappyPath(t *testing.T) {
	wantBody := largeHTML("hello direct")

	direct := &fakeDoer{
		status: http.StatusOK,
		body:   wantBody,
		hdrs:   map[string]string{"content-type": "text/html; charset=utf-8"},
	}
	proxy := &fakeDoer{
		status: http.StatusOK,
		body:   []byte("should not be reached"),
		hdrs:   map[string]string{},
	}

	f := newDirectFirstBase()
	f.directClient = direct
	f.proxyClient = proxy

	body, err := f.FetchBody(context.Background(), "https://example.com/page")
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if string(body) != string(wantBody) {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
	if got := direct.calls.Load(); got != 1 {
		t.Errorf("direct called %d times, want 1", got)
	}
	if got := proxy.calls.Load(); got != 0 {
		t.Errorf("proxy called %d times, want 0", got)
	}
}

// Test 2: Direct 403 → proxy 200 → body from proxy, blockCache marks host.
func Test_DirectFirst_403_EscalatesToProxy(t *testing.T) {
	wantBody := largeHTML("proxy response")

	direct := &fakeDoer{
		status: http.StatusForbidden,
		body:   []byte("forbidden"),
		hdrs:   map[string]string{},
	}
	proxy := &fakeDoer{
		status: http.StatusOK,
		body:   wantBody,
		hdrs:   map[string]string{"content-type": "text/html"},
	}

	f := newDirectFirstBase()
	f.directClient = direct
	f.proxyClient = proxy

	const testURL = "https://blocked.example.com/page"
	body, err := f.FetchBody(context.Background(), testURL)
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if string(body) != string(wantBody) {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
	if got := direct.calls.Load(); got != 1 {
		t.Errorf("direct called %d times, want 1", got)
	}
	if got := proxy.calls.Load(); got != 1 {
		t.Errorf("proxy called %d times, want 1", got)
	}

	// blockCache must have marked the host.
	host := urlHost(testURL)
	if !f.blockCache.IsBlocked(host) {
		t.Errorf("host %q not marked in blockCache after 403", host)
	}
}

// Test 3: Direct returns CF "Just a moment..." challenge body → escalates to proxy.
func Test_DirectFirst_CFChallenge_Detected(t *testing.T) {
	wantBody := largeHTML("real content from proxy")

	direct := &fakeDoer{
		status: http.StatusOK,
		// CF challenge page — classifyBlock detects "Just a moment..."
		body: []byte(`<html><head><title>Just a moment...</title></head><body>` +
			`<div>Checking your browser before accessing.</div></body></html>`),
		hdrs: map[string]string{"content-type": "text/html"},
	}
	proxy := &fakeDoer{
		status: http.StatusOK,
		body:   wantBody,
		hdrs:   map[string]string{"content-type": "text/html"},
	}

	f := newDirectFirstBase()
	f.directClient = direct
	f.proxyClient = proxy

	body, err := f.FetchBody(context.Background(), "https://cf-site.example.com/")
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if string(body) != string(wantBody) {
		t.Errorf("body = %q, want %q", body, wantBody)
	}
	if got := direct.calls.Load(); got != 1 {
		t.Errorf("direct called %d times, want 1", got)
	}
	if got := proxy.calls.Load(); got != 1 {
		t.Errorf("proxy called %d times, want 1", got)
	}
}

// Test 4: Negative cache — first call 403→proxy, second call same host skips direct.
func Test_DirectFirst_NegativeCache_SkipsDirect(t *testing.T) {
	wantBody := largeHTML("ok")

	direct := &fakeDoer{
		status: http.StatusForbidden,
		body:   []byte("nope"),
		hdrs:   map[string]string{},
	}
	proxy := &fakeDoer{
		status: http.StatusOK,
		body:   wantBody,
		hdrs:   map[string]string{"content-type": "text/html"},
	}

	f := newDirectFirstBase()
	f.directClient = direct
	f.proxyClient = proxy

	const host = "cached.example.com"

	// First call: 403 direct → proxy.
	if _, err := f.FetchBody(context.Background(), "https://"+host+"/one"); err != nil {
		t.Fatalf("first call: %v", err)
	}

	directAfterFirst := direct.calls.Load()
	if directAfterFirst != 1 {
		t.Fatalf("expected 1 direct call after first request, got %d", directAfterFirst)
	}

	// Second call same host: blockCache hit → direct must NOT be called again.
	if _, err := f.FetchBody(context.Background(), "https://"+host+"/two"); err != nil {
		t.Fatalf("second call: %v", err)
	}

	if got := direct.calls.Load(); got != directAfterFirst {
		t.Errorf("direct called %d times total, want %d (blockCache should have skipped)", got, directAfterFirst)
	}
	if got := proxy.calls.Load(); got != 2 {
		t.Errorf("proxy called %d times, want 2 (once per FetchBody)", got)
	}
}

// Test 5: ProxyFirstDomain match → direct never called, proxy used immediately.
func Test_ProxyFirstDomain_SkipsDirect(t *testing.T) {
	wantBody := largeHTML("linkedin jobs")

	direct := &fakeDoer{
		status: http.StatusOK,
		body:   []byte("should not be reached"),
		hdrs:   map[string]string{},
	}
	proxy := &fakeDoer{
		status: http.StatusOK,
		body:   wantBody,
		hdrs:   map[string]string{"content-type": "text/html"},
	}

	f := newDirectFirstBase()
	f.directClient = direct
	f.proxyClient = proxy
	// proxyFirstDomains is initialized in New() when directFirst=true via
	// NewProxyFirstDomains(nil) which includes linkedin.com by default.

	body, err := f.FetchBody(context.Background(), "https://www.linkedin.com/jobs/view/123")
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if string(body) != string(wantBody) {
		t.Errorf("body = %q, want %q", body, wantBody)
	}

	if got := direct.calls.Load(); got != 0 {
		t.Errorf("direct called %d times, want 0 (proxy-first domain)", got)
	}
	if got := proxy.calls.Load(); got != 1 {
		t.Errorf("proxy called %d times, want 1", got)
	}
}

// Test 6: No proxy budget (proxyClient nil) + 403 direct → returns error, no panic.
func Test_NoProxyBudget_DirectOnly(t *testing.T) {
	direct := &fakeDoer{
		status: http.StatusForbidden,
		body:   []byte("blocked"),
		hdrs:   map[string]string{},
	}

	f := newDirectFirstBase()
	f.directClient = direct
	// proxyClient remains nil — no proxy budget.

	_, err := f.FetchBody(context.Background(), "https://blocked.example.com/page")
	if err == nil {
		t.Fatal("expected error when direct blocks and no proxy available")
	}

	var statusErr *HttpStatusError
	if !errors.As(err, &statusErr) {
		t.Errorf("expected *HttpStatusError, got %T: %v", err, err)
	} else if statusErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode = %d, want 403", statusErr.StatusCode)
	}
}

// Test 7: Legacy mode (WithDirectFirst=false) → byte-identical to current behaviour.
// directClient must remain nil; proxyClient must remain nil when no proxy pool given.
func Test_LegacyMode_ProxyFirst(t *testing.T) {
	wantBody := largeHTML("legacy response")

	proxy := &fakeDoer{
		status: http.StatusOK,
		body:   wantBody,
		hdrs:   map[string]string{"content-type": "text/html"},
	}

	// Default (no WithDirectFirst at all).
	fDefault := New(
		WithTimeout(5*time.Second),
		WithRetryConfig(RetryConfig{MaxRetries: 0, InitialWait: time.Millisecond, MaxWait: time.Millisecond, Multiplier: 1}),
	)
	// Explicit false.
	fExplicit := New(
		WithDirectFirst(false),
		WithTimeout(5*time.Second),
		WithRetryConfig(RetryConfig{MaxRetries: 0, InitialWait: time.Millisecond, MaxWait: time.Millisecond, Multiplier: 1}),
	)

	// Both should use proxyClient (injected) if set, else fetchViaHTTP.
	// In legacy mode with a proxyClient injected, proxy must be used.
	fDefault.proxyClient = proxy
	fExplicit.proxyClient = proxy

	bodyDefault, errDefault := fDefault.FetchBody(context.Background(), "https://example.com/")
	if errDefault != nil {
		t.Fatalf("default: %v", errDefault)
	}

	bodyExplicit, errExplicit := fExplicit.FetchBody(context.Background(), "https://example.com/")
	if errExplicit != nil {
		t.Fatalf("explicit false: %v", errExplicit)
	}

	if string(bodyDefault) != string(bodyExplicit) {
		t.Errorf("body mismatch: default=%q, explicit=%q", bodyDefault, bodyExplicit)
	}
	if string(bodyDefault) != string(wantBody) {
		t.Errorf("body = %q, want %q", bodyDefault, wantBody)
	}

	// directClient must be nil in legacy mode.
	if fDefault.directClient != nil {
		t.Error("directClient should be nil in legacy mode (default)")
	}
	if fExplicit.directClient != nil {
		t.Error("directClient should be nil in legacy mode (explicit false)")
	}
}
