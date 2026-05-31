package fetch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// headerCapture is a stealthDoer that records the headers map passed to DoCtx.
type headerCapture struct {
	calls   atomic.Int32
	headers map[string]string // headers from the last DoCtx call
	status  int
	body    []byte
}

func (h *headerCapture) DoCtx(_ context.Context, _, _ string, headers map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
	h.calls.Add(1)
	// Snapshot the headers map (copy so caller mutations don't alias).
	snap := make(map[string]string, len(headers))
	for k, v := range headers {
		snap[k] = v
	}
	h.headers = snap
	if h.status == 0 {
		h.status = http.StatusOK
	}
	if h.body == nil {
		h.body = largeHTML("ok")
	}
	return h.body, map[string]string{}, h.status, nil
}

// TestFetchBodyWithHeaders_HTTPPath_AcceptJSON verifies that when Accept:application/json
// is passed as an extra header, the plain-HTTP path sends exactly one Accept header
// with the value "application/json" (overriding the built-in text/html default).
func TestFetchBodyWithHeaders_HTTPPath_AcceptJSON(t *testing.T) {
	var gotAccept string
	var acceptCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		// http.Header.Get returns the first value; Values() returns all.
		acceptCount = len(r.Header.Values("Accept"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	f := New(
		WithTimeout(5*time.Second),
		WithRetryConfig(RetryConfig{MaxRetries: 0, InitialWait: time.Millisecond, MaxWait: time.Millisecond, Multiplier: 1}),
	)
	body, err := f.FetchBodyWithHeaders(context.Background(), srv.URL, map[string]string{"Accept": "application/json"})
	if err != nil {
		t.Fatalf("FetchBodyWithHeaders: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if acceptCount != 1 {
		t.Errorf("Accept header count = %d, want exactly 1", acceptCount)
	}
}

// TestFetchBodyWithHeaders_HTTPPath_NilExtra verifies that nil extra leaves behavior
// identical to FetchBody — no Accept override, no panic.
func TestFetchBodyWithHeaders_HTTPPath_NilExtra(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	f := New(WithTimeout(5 * time.Second))

	bodyA, errA := f.FetchBody(context.Background(), srv.URL)
	bodyB, errB := f.FetchBodyWithHeaders(context.Background(), srv.URL, nil)

	if errA != nil || errB != nil {
		t.Fatalf("FetchBody=%v FetchBodyWithHeaders(nil)=%v", errA, errB)
	}
	if string(bodyA) != string(bodyB) {
		t.Errorf("FetchBody and FetchBodyWithHeaders(nil) returned different bodies")
	}
	// Accept must be the built-in HTML default, not application/json.
	if gotAccept == "application/json" {
		t.Errorf("nil extra should not set Accept=application/json; got %q", gotAccept)
	}
}

// TestFetchBodyWithHeaders_DirectPath_AcceptJSON verifies that when using the
// direct-first tier, a custom Accept: application/json is forwarded to the
// stealthDoer (Chrome-TLS, no proxy) via the headers map.
func TestFetchBodyWithHeaders_DirectPath_AcceptJSON(t *testing.T) {
	direct := &headerCapture{status: http.StatusOK, body: largeHTML("direct-json")}

	f := newDirectFirstBase()
	f.directClient = direct

	extra := map[string]string{"Accept": "application/json"}
	_, err := f.FetchBodyWithHeaders(context.Background(), "https://example.com/api", extra)
	if err != nil {
		t.Fatalf("FetchBodyWithHeaders: %v", err)
	}
	if direct.calls.Load() != 1 {
		t.Errorf("direct called %d times, want 1", direct.calls.Load())
	}
	// The accept key is lowercased by mergeHeaders.
	if got := direct.headers["accept"]; got != "application/json" {
		t.Errorf("direct got accept=%q, want application/json", got)
	}
	// Must be exactly one accept key (not both "accept" and "Accept").
	for k := range direct.headers {
		if k != "accept" && k == "Accept" {
			t.Errorf("found unexpected uppercase Accept key alongside lowercase accept")
		}
	}
}

// TestFetchBodyWithHeaders_ProxyPath_AcceptJSON verifies that a custom Accept header
// is forwarded to the proxy-tier stealthDoer when proxy is available and direct is
// blocked (hard 403).
func TestFetchBodyWithHeaders_ProxyPath_AcceptJSON(t *testing.T) {
	direct := &headerCapture{status: http.StatusForbidden}
	proxy := &headerCapture{status: http.StatusOK, body: largeHTML("proxy-json")}

	f := newDirectFirstBase()
	f.directClient = direct
	f.proxyClient = proxy

	extra := map[string]string{"accept": "application/json"}
	_, err := f.FetchBodyWithHeaders(context.Background(), "https://example.com/api", extra)
	if err != nil {
		t.Fatalf("FetchBodyWithHeaders proxy path: %v", err)
	}
	if proxy.calls.Load() != 1 {
		t.Errorf("proxy called %d times, want 1", proxy.calls.Load())
	}
	if got := proxy.headers["accept"]; got != "application/json" {
		t.Errorf("proxy got accept=%q, want application/json", got)
	}
}

// TestFetchBody_DelegatesTo_FetchBodyWithHeaders_NilExtra verifies that FetchBody
// and FetchBodyWithHeaders(ctx, url, nil) produce byte-identical results on a real
// httptest server (regression guard: delegation must not alter behavior).
func TestFetchBody_DelegatesTo_FetchBodyWithHeaders_NilExtra(t *testing.T) {
	payload := []byte("<html><body>same content</body></html>")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	f := New(WithTimeout(5 * time.Second))

	a, errA := f.FetchBody(context.Background(), srv.URL)
	b, errB := f.FetchBodyWithHeaders(context.Background(), srv.URL, nil)

	if errA != nil || errB != nil {
		t.Fatalf("errA=%v errB=%v", errA, errB)
	}
	if string(a) != string(b) {
		t.Errorf("FetchBody and FetchBodyWithHeaders(nil) differ:\n  a=%q\n  b=%q", a, b)
	}
}

// TestMergeHeaders_LowercaseNormalization verifies that mergeHeaders normalises
// mixed-case extra keys to lowercase, matching ChromeHeaders convention.
func TestMergeHeaders_LowercaseNormalization(t *testing.T) {
	extra := map[string]string{
		"Accept":       "application/json",
		"X-Custom-Hdr": "value",
	}
	h := mergeHeaders(extra)

	if got, ok := h["accept"]; !ok || got != "application/json" {
		t.Errorf("mergeHeaders: accept=%q ok=%v, want application/json", got, ok)
	}
	if _, ok := h["Accept"]; ok {
		t.Error("mergeHeaders: unexpected uppercase 'Accept' key (should be normalised to lowercase)")
	}
	if got, ok := h["x-custom-hdr"]; !ok || got != "value" {
		t.Errorf("mergeHeaders: x-custom-hdr=%q ok=%v, want 'value'", got, ok)
	}
}

// TestFetchBodyWithHeaders_NilExtra_DirectTierPreservesLegacyAccept is a regression
// guard: nil extra must NOT change the Accept header on the direct (Chrome-TLS) tier.
// The legacy value is the stripped HTML accept (without image/avif,image/webp that
// ChromeHeaders seeds) — exact string must be preserved for existing callers.
func TestFetchBodyWithHeaders_NilExtra_DirectTierPreservesLegacyAccept(t *testing.T) {
	const legacyAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

	direct := &headerCapture{status: http.StatusOK, body: largeHTML("nil-extra regression")}

	f := newDirectFirstBase()
	f.directClient = direct

	_, err := f.FetchBody(context.Background(), "https://example.com/page")
	if err != nil {
		t.Fatalf("FetchBody: %v", err)
	}
	if got := direct.headers["accept"]; got != legacyAccept {
		t.Errorf("nil-extra changed direct-tier Accept:\n  got:  %q\n  want: %q", got, legacyAccept)
	}
}

// TestFetchBodyWithHeaders_NilExtra_ProxyTierPreservesLegacyAccept is a regression
// guard: nil extra must NOT change the Accept header on the proxy (Chrome-TLS) tier.
func TestFetchBodyWithHeaders_NilExtra_ProxyTierPreservesLegacyAccept(t *testing.T) {
	const legacyAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

	// Force proxy path: directFirst=false, proxy available.
	proxy := &headerCapture{status: http.StatusOK, body: largeHTML("proxy nil-extra regression")}

	f := New(
		WithTimeout(5*time.Second),
		WithRetryConfig(RetryConfig{MaxRetries: 0, InitialWait: time.Millisecond, MaxWait: time.Millisecond, Multiplier: 1}),
	)
	f.proxyClient = proxy

	_, err := f.FetchBody(context.Background(), "https://example.com/page")
	if err != nil {
		t.Fatalf("FetchBody via proxy: %v", err)
	}
	if got := proxy.headers["accept"]; got != legacyAccept {
		t.Errorf("nil-extra changed proxy-tier Accept:\n  got:  %q\n  want: %q", got, legacyAccept)
	}
}
