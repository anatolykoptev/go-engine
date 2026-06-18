package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/anatolykoptev/go-engine/metrics"
)

// hostRewriteTransport rewrites every outgoing request's scheme+host to match
// a test server, so we can intercept braveAPIEndpoint calls without patching
// the URL constant.
type hostRewriteTransport struct {
	base    http.RoundTripper
	testURL string // e.g. "http://127.0.0.1:12345"
}

func (tr *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, _ := url.Parse(tr.testURL)
	clone := req.Clone(req.Context())
	clone.URL.Scheme = target.Scheme
	clone.URL.Host = target.Host
	if tr.base == nil {
		tr.base = http.DefaultTransport
	}
	return tr.base.RoundTrip(clone)
}

// withTestServer temporarily replaces braveAPIHTTPClient with one that
// routes all requests to srv. Returns a cleanup function.
func withTestServer(srv *httptest.Server) func() {
	orig := braveAPIHTTPClient
	braveAPIHTTPClient = &http.Client{
		Transport: &hostRewriteTransport{
			base:    srv.Client().Transport,
			testURL: srv.URL,
		},
	}
	return func() { braveAPIHTTPClient = orig }
}

// newTestMetrics returns a fresh metrics registry.
func newTestMetrics() *metrics.Registry {
	return metrics.New()
}

// braveResultsJSON encodes a slice of braveAPIResult as the full Brave API response body.
func braveResultsJSON(results []braveAPIResult) []byte {
	resp := braveAPIResponse{}
	resp.Web.Results = results
	b, _ := json.Marshal(resp)
	return b
}

// --- tests ---

// TestSearchBraveAPI_EmptyKey: nil/empty key → nil results, no panic.
func TestSearchBraveAPI_EmptyKey(t *testing.T) {
	results, err := SearchBraveAPI(context.Background(), "", "test query", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for empty key, got %d", len(results))
	}
}

// TestSearchBraveAPI_ParsesResults: mock server → title/url/description parsed correctly.
func TestSearchBraveAPI_ParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") == "" {
			t.Error("expected X-Subscription-Token header")
		}
		if r.URL.Query().Get("q") == "" {
			t.Error("expected q param")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(braveResultsJSON([]braveAPIResult{
			{Title: "Go Programming", URL: "https://go.dev", Description: "The Go programming language"},
			{Title: "Go Blog", URL: "https://go.dev/blog", Description: "Official Go blog"},
		}))
	}))
	defer srv.Close()
	defer withTestServer(srv)()

	m := newTestMetrics()
	results, err := SearchBraveAPI(context.Background(), "test-key", "go programming", m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Go Programming" {
		t.Errorf("results[0].Title = %q, want 'Go Programming'", results[0].Title)
	}
	if results[0].URL != "https://go.dev" {
		t.Errorf("results[0].URL = %q", results[0].URL)
	}
	if results[0].Content != "The Go programming language" {
		t.Errorf("results[0].Content = %q", results[0].Content)
	}
	if results[0].Metadata["engine"] != "brave_api" {
		t.Errorf("Metadata[engine] = %q, want brave_api", results[0].Metadata["engine"])
	}

	// Verify ok metric was recorded.
	snap := m.Snapshot()
	okKey := "go_search_source_result_total{source=brave_api,outcome=ok}"
	if snap[okKey] != 1 {
		t.Errorf("expected ok metric=1, got %d (snap=%v)", snap[okKey], snap)
	}
}

// TestSearchBraveAPI_HTTP429: 429 → outcome="fail", nil results returned (not error).
func TestSearchBraveAPI_HTTP429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	defer withTestServer(srv)()

	m := newTestMetrics()
	results, err := SearchBraveAPI(context.Background(), "test-key", "query", m)
	if err != nil {
		t.Fatalf("expected nil error on 429, got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on 429")
	}
	snap := m.Snapshot()
	failKey := "go_search_source_result_total{source=brave_api,outcome=fail}"
	if snap[failKey] == 0 {
		t.Errorf("expected fail metric to be incremented on 429")
	}
}

// TestSearchBraveAPI_MalformedJSON: invalid JSON → nil results, no panic.
func TestSearchBraveAPI_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()
	defer withTestServer(srv)()

	m := newTestMetrics()
	results, err := SearchBraveAPI(context.Background(), "test-key", "test", m)
	if err != nil {
		t.Fatalf("expected nil error on malformed JSON, got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on malformed JSON")
	}
	snap := m.Snapshot()
	failKey := "go_search_source_result_total{source=brave_api,outcome=fail}"
	if snap[failKey] == 0 {
		t.Errorf("expected fail metric to be incremented on malformed JSON")
	}
}

// TestSearchBraveAPI_MetricLabel: "brave_api" metric label is distinct from "brave" scraper.
func TestSearchBraveAPI_MetricLabel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	defer withTestServer(srv)()

	m := newTestMetrics()
	_, _ = SearchBraveAPI(context.Background(), "key", "query", m)

	snap := m.Snapshot()
	braveAPIKey := "go_search_source_result_total{source=brave_api,outcome=fail}"
	if snap[braveAPIKey] == 0 {
		t.Errorf("expected brave_api metric, not found: %v", snap)
	}
	// Scraper label "brave" must NOT appear.
	braveScraperKey := "go_search_source_result_total{source=brave,outcome=fail}"
	if snap[braveScraperKey] != 0 {
		t.Errorf("unexpected brave scraper metric found: %v", snap)
	}
}

// TestSearchBraveAPI_FiltersEmptyTitleOrURL: results with empty title or URL are dropped.
func TestSearchBraveAPI_FiltersEmptyTitleOrURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(braveResultsJSON([]braveAPIResult{
			{Title: "", URL: "https://skip-no-title.com", Description: "no title"},
			{Title: "NoURL", URL: "", Description: "no url"},
			{Title: "Valid", URL: "https://good.com", Description: "keep"},
		}))
	}))
	defer srv.Close()
	defer withTestServer(srv)()

	results, err := SearchBraveAPI(context.Background(), "key", "test", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1 (filtered)", len(results))
	}
	if len(results) > 0 && results[0].URL != "https://good.com" {
		t.Errorf("wrong result URL: %q", results[0].URL)
	}
}
