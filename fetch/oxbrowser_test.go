package fetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFetchViaOxBrowser_HitsFetchEndpoint asserts the request targets /fetch,
// not the deprecated /fetch-smart. Mutation probe: revert endpoint to
// "/fetch-smart" → server sees /fetch-smart, path assertion fails (RED).
func TestFetchViaOxBrowser_HitsFetchEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fetch" {
			t.Errorf("endpoint = %q, want /fetch", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oxFetchResponse{
			Status: 200, Body: "<html></html>",
		})
	}))
	defer srv.Close()

	f := New(WithOxBrowser(srv.URL))
	if _, err := f.fetchViaOxBrowser(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("fetchViaOxBrowser: %v", err)
	}
}

// TestFetchViaOxBrowser_ReadsBodyKey asserts the raw page is read from the
// `body` field and passed through verbatim (raw HTML, not markdown). Mutation
// probe: change oxFetchResponse.Body tag to `json:"content"` → field stays
// empty → "empty response" error (RED). This is the silent-corruption guard:
// an HTML extractor fed these bytes; markdown would yield empty/garbage.
func TestFetchViaOxBrowser_ReadsBodyKey(t *testing.T) {
	want := "<html><body><h1>Real Page</h1></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      200,
			"body":        want,
			"cf_detected": false,
			"elapsed_ms":  42,
		})
	}))
	defer srv.Close()

	f := New(WithOxBrowser(srv.URL))
	got, err := f.fetchViaOxBrowser(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("fetchViaOxBrowser: %v", err)
	}
	if string(got) != want {
		t.Errorf("body = %q, want %q (raw HTML pass-through)", got, want)
	}
}

// TestFetchViaOxBrowser_ReadShapedResponseDoesNotSilentlyYieldEmpty asserts a
// response shaped like /read (only `content`, no `body`) errors rather than
// returning empty bytes with a nil error. Mutation probe: make the empty-body
// check return `nil, nil` instead of erroring → test sees empty body + nil
// err and fails (RED). Catches the markdown-into-HTML-parser silent failure.
func TestFetchViaOxBrowser_ReadShapedResponseDoesNotSilentlyYieldEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// /read-shaped: `content` present, `body` absent.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  200,
			"content": "# Example\nThis is markdown, not HTML.",
		})
	}))
	defer srv.Close()

	f := New(WithOxBrowser(srv.URL))
	got, err := f.fetchViaOxBrowser(context.Background(), "https://example.com")
	if err == nil {
		t.Fatalf("expected error for /read-shaped response with no `body`, got nil err and body=%q", got)
	}
}

// TestFetchViaOxBrowser_502SurfacesError asserts a 502 carrying a structured
// `error` field surfaces that error text cleanly, distinct from a transport
// failure (which returns before any HTTP status is seen). Mutation probe:
// remove the errResp.Error branch (revert to truncate-only) → error becomes
// the raw JSON blob `{"error":"upstream timeout"}` and the exact-match
// assertion fails (RED).
func TestFetchViaOxBrowser_502SurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": 502,
			"error":  "upstream timeout",
		})
	}))
	defer srv.Close()

	f := New(WithOxBrowser(srv.URL))
	_, err := f.fetchViaOxBrowser(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("expected error for 502 response")
	}
	want := "ox-browser HTTP 502: upstream timeout"
	if err.Error() != want {
		t.Errorf("error = %q, want %q (structured error surfaced, not raw JSON)", err.Error(), want)
	}
}

// TestFetchViaOxBrowser_502NoErrorField falls back to the truncated body when
// the 502 carries no structured error field. Mutation probe: always surface
// errResp.Error even when empty → error would be "ox-browser HTTP 502: " with
// no body text, failing the "contains gateway body" assertion (RED).
func TestFetchViaOxBrowser_502NoErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("Bad Gateway plain text"))
	}))
	defer srv.Close()

	f := New(WithOxBrowser(srv.URL))
	_, err := f.fetchViaOxBrowser(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("expected error for 502 response")
	}
	if !strings.Contains(err.Error(), "Bad Gateway plain text") {
		t.Errorf("error = %q, want it to contain the truncated body", err.Error())
	}
}

// TestFetchViaOxBrowser_NoInertField asserts the request JSON carries only
// real /fetch fields (url, timeout) — no inert format/max_length (those belong
// to /read). Mutation probe: add `Format string \`json:"format"\“ to
// oxFetchRequest and set it → decoded request has a "format" key, the
// no-extra-keys assertion fails (RED). An inert field reads as configured
// behaviour later.
func TestFetchViaOxBrowser_NoInertField(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oxFetchResponse{Status: 200, Body: "<html></html>"})
	}))
	defer srv.Close()

	f := New(WithOxBrowser(srv.URL))
	if _, err := f.fetchViaOxBrowser(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("fetchViaOxBrowser: %v", err)
	}
	if got["url"] != "https://example.com" {
		t.Errorf("request url = %v, want https://example.com", got["url"])
	}
	if got["timeout"] == nil {
		t.Error("request missing `timeout` (real /fetch field)")
	}
	for k := range got {
		if k == "format" || k == "max_length" || k == "markdown" || k == "text" {
			t.Errorf("request carries inert field %q (not a /fetch field)", k)
		}
	}
}

// TestFetchViaOxBrowser_TransportFailureDistinct asserts a transport-level
// failure (server unreachable) is reported via the "ox-browser call:" prefix,
// not as an HTTP status — the 502-with-error path can never produce this
// prefix. Mutation probe: swap the client.Do error wrap to "ox-browser HTTP
// 0: ..." → prefix assertion fails (RED).
func TestFetchViaOxBrowser_TransportFailureDistinct(t *testing.T) {
	// Server closed immediately → connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close()

	f := New(WithOxBrowser(srv.URL))
	_, err := f.fetchViaOxBrowser(context.Background(), "https://example.com")
	if err == nil {
		t.Fatal("expected transport error for unreachable server")
	}
	if !strings.Contains(err.Error(), "ox-browser call:") {
		t.Errorf("transport error = %q, want `ox-browser call:` prefix (distinct from HTTP-status errors)", err.Error())
	}
}

// TestFetchBody_OxBrowserFallback confirms the fallback chain routes a failed
// primary fetch through ox-browser /fetch and returns the raw HTML body.
func TestFetchBody_OxBrowserFallback(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer primary.Close()

	oxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fetch" {
			t.Errorf("fallback endpoint = %q, want /fetch", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oxFetchResponse{
			Status: 200, Body: "<html>fallback</html>",
		})
	}))
	defer oxSrv.Close()

	f := New(
		WithTimeout(2*time.Second),
		WithRetryConfig(RetryConfig{
			MaxRetries:  1,
			InitialWait: 10 * time.Millisecond,
			MaxWait:     50 * time.Millisecond,
			Multiplier:  1.5,
		}),
		WithOxBrowser(oxSrv.URL),
	)

	body, err := f.FetchBody(context.Background(), primary.URL)
	if err != nil {
		t.Fatalf("FetchBody with ox-browser fallback: %v", err)
	}
	if string(body) != "<html>fallback</html>" {
		t.Errorf("body = %q, want fallback HTML", body)
	}
}
