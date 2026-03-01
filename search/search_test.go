package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-engine/fetch"
)

// mockBrowser implements BrowserDoer for testing.
type mockBrowser struct {
	fn func(method, url string, headers map[string]string, body io.Reader) ([]byte, map[string]string, int, error)
}

func (m *mockBrowser) Do(method, url string, headers map[string]string, body io.Reader) ([]byte, map[string]string, int, error) {
	return m.fn(method, url, headers, body)
}

// --- SearXNG tests ---

func TestSearXNG_Search(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("path = %q, want /search", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "test query" {
			t.Errorf("q = %q, want test query", r.URL.Query().Get("q"))
		}
		if r.URL.Query().Get("format") != "json" {
			t.Errorf("format = %q, want json", r.URL.Query().Get("format"))
		}
		if r.Header.Get("X-Forwarded-For") != "127.0.0.1" {
			t.Errorf("X-Forwarded-For = %q, want 127.0.0.1", r.Header.Get("X-Forwarded-For"))
		}

		resp := searxngResponse{
			Results: []Result{
				{Title: "Result 1", Content: "Content 1", URL: "https://example.com/1", Score: 0.9},
				{Title: "Result 2", Content: "Content 2", URL: "https://example.com/2", Score: 0.5},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	s := NewSearXNG(srv.URL)
	results, err := s.Search(context.Background(), "test query", "", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Result 1" {
		t.Errorf("results[0].Title = %q, want Result 1", results[0].Title)
	}
}

func TestSearXNG_SearchParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("language") != "en" {
			t.Errorf("language = %q, want en", q.Get("language"))
		}
		if q.Get("time_range") != "month" {
			t.Errorf("time_range = %q, want month", q.Get("time_range"))
		}
		if q.Get("engines") != "google" {
			t.Errorf("engines = %q, want google", q.Get("engines"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	s := NewSearXNG(srv.URL)
	_, err := s.Search(context.Background(), "query", "en", "month", "google")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
}

func TestSearXNG_SearchSkipsAllLanguage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("language") != "" {
			t.Errorf("language should not be set for 'all', got %q", r.URL.Query().Get("language"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	s := NewSearXNG(srv.URL)
	_, err := s.Search(context.Background(), "query", "all", "", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
}

// --- Filter tests ---

func TestFilterByScore(t *testing.T) {
	results := []Result{
		{Title: "A", Score: 0.9},
		{Title: "B", Score: 0.5},
		{Title: "C", Score: 0.3},
		{Title: "D", Score: 0.8},
	}

	filtered := FilterByScore(results, 0.6, 1)
	if len(filtered) != 2 {
		t.Fatalf("got %d results, want 2", len(filtered))
	}
	if filtered[0].Title != "A" || filtered[1].Title != "D" {
		t.Errorf("wrong results: %v", filtered)
	}
}

func TestFilterByScore_MinKeep(t *testing.T) {
	results := []Result{
		{Title: "A", Score: 0.1},
		{Title: "B", Score: 0.2},
		{Title: "C", Score: 0.05},
	}

	// All below threshold but minKeep=2.
	filtered := FilterByScore(results, 0.5, 2)
	if len(filtered) != 2 {
		t.Fatalf("got %d results, want 2 (minKeep)", len(filtered))
	}
}

func TestFilterByScore_MinKeepExceedsResults(t *testing.T) {
	results := []Result{
		{Title: "A", Score: 0.1},
	}

	// minKeep > len(results): return all.
	filtered := FilterByScore(results, 0.5, 5)
	if len(filtered) != 1 {
		t.Fatalf("got %d results, want 1 (all available)", len(filtered))
	}
}

func TestDedupByDomain(t *testing.T) {
	results := []Result{
		{Title: "A1", URL: "https://example.com/page1"},
		{Title: "A2", URL: "https://example.com/page2"},
		{Title: "A3", URL: "https://example.com/page3"},
		{Title: "B1", URL: "https://other.com/page1"},
	}

	deduped := DedupByDomain(results, 2)
	if len(deduped) != 3 {
		t.Fatalf("got %d results, want 3", len(deduped))
	}
	// Should keep A1, A2, B1 (A3 exceeds maxPerDomain=2 for example.com).
	if deduped[0].Title != "A1" || deduped[1].Title != "A2" || deduped[2].Title != "B1" {
		t.Errorf("wrong results: %v", deduped)
	}
}

func TestDedupByDomain_InvalidURL(t *testing.T) {
	results := []Result{
		{Title: "A", URL: "https://valid.com/page"},
		{Title: "B", URL: "://invalid"},
		{Title: "C", URL: "https://valid.com/page2"},
	}

	deduped := DedupByDomain(results, 1)
	// Should skip invalid URL and keep only first per domain.
	if len(deduped) != 1 {
		t.Fatalf("got %d results, want 1", len(deduped))
	}
	if deduped[0].Title != "A" {
		t.Errorf("wrong result: %v", deduped[0])
	}
}

// --- DDG tests ---

func TestParseDDGHTML(t *testing.T) {
	html := `<html><body>
		<div class="result">
			<a class="result__a" href="https://example.com/page1">Example Page</a>
			<span class="result__snippet">This is a snippet.</span>
		</div>
		<div class="result">
			<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fother.com%2Fpage&rut=abc">Other Page</a>
			<span class="result__snippet">Another snippet.</span>
		</div>
	</body></html>`

	results, err := ParseDDGHTML([]byte(html))
	if err != nil {
		t.Fatalf("ParseDDGHTML: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Example Page" {
		t.Errorf("results[0].Title = %q, want Example Page", results[0].Title)
	}
	if results[0].URL != "https://example.com/page1" {
		t.Errorf("results[0].URL = %q, want https://example.com/page1", results[0].URL)
	}
	if results[1].URL != "https://other.com/page" {
		t.Errorf("results[1].URL = %q, want https://other.com/page (unwrapped)", results[1].URL)
	}
}

func TestDDGUnwrapURL(t *testing.T) {
	tests := []struct {
		name string
		href string
		want string
	}{
		{"direct URL", "https://example.com/page", "https://example.com/page"},
		{"wrapped URL", "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage&rut=abc", "https://example.com/page"},
		{"no uddg param", "//duckduckgo.com/l/?foo=bar", ""},
		{"relative URL", "/some/path", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DDGUnwrapURL(tt.href)
			if got != tt.want {
				t.Errorf("DDGUnwrapURL(%q) = %q, want %q", tt.href, got, tt.want)
			}
		})
	}
}

func TestParseDDGResponse(t *testing.T) {
	jsonResp := `[
		{"t": "Example <b>Title</b>", "a": "Some <em>content</em>", "u": "https://example.com/1", "c": ""},
		{"t": "Other", "a": "Text", "u": "", "c": "https://other.com"},
		{"t": "", "a": "No title", "u": "https://skip.com", "c": ""},
		{"t": "DDG Internal", "a": "", "u": "https://duckduckgo.com/about", "c": ""}
	]`

	results, err := ParseDDGResponse([]byte(jsonResp))
	if err != nil {
		t.Fatalf("ParseDDGResponse: %v", err)
	}
	// Should get 2: skip empty title and DDG internal.
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Example Title" {
		t.Errorf("results[0].Title = %q, want 'Example Title' (HTML stripped)", results[0].Title)
	}
	if results[1].URL != "https://other.com" {
		t.Errorf("results[1].URL = %q, want https://other.com (from C field)", results[1].URL)
	}
}

func TestParseDDGResponse_JSONP(t *testing.T) {
	jsonp := `DDGjsonp_abc123([{"t":"Title","a":"Content","u":"https://example.com","c":""}])`
	results, err := ParseDDGResponse([]byte(jsonp))
	if err != nil {
		t.Fatalf("ParseDDGResponse JSONP: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

func TestExtractVQD(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"single quotes", `<script>vqd='abc123'</script>`, "abc123"},
		{"double quotes", `<script>vqd="def456"</script>`, "def456"},
		{"no quotes", `vqd=xyz789-_test`, "xyz789-_test"},
		{"not found", `<html>no vqd here</html>`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractVQD(tt.body)
			if got != tt.want {
				t.Errorf("ExtractVQD() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Startpage tests ---

func TestParseStartpageHTML(t *testing.T) {
	html := `<html><body>
		<div class="w-gl__result">
			<a class="w-gl__result-title" href="https://example.com/sp1">SP Result 1</a>
			<p class="w-gl__description">Startpage snippet 1.</p>
		</div>
		<div class="w-gl__result">
			<a class="w-gl__result-title" href="https://example.com/sp2">SP Result 2</a>
			<p class="w-gl__description">Startpage snippet 2.</p>
		</div>
		<div class="w-gl__result">
			<a class="w-gl__result-title" href="https://www.startpage.com/do/proxy">Ad Result</a>
			<p class="w-gl__description">This is an ad.</p>
		</div>
	</body></html>`

	results, err := ParseStartpageHTML([]byte(html))
	if err != nil {
		t.Fatalf("ParseStartpageHTML: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (ad filtered)", len(results))
	}
	if results[0].Title != "SP Result 1" {
		t.Errorf("results[0].Title = %q, want SP Result 1", results[0].Title)
	}
	if results[0].Content != "Startpage snippet 1." {
		t.Errorf("results[0].Content = %q, want 'Startpage snippet 1.'", results[0].Content)
	}
}

// --- Integration tests with mockBrowser ---

func TestSearchDDGDirect_Mock(t *testing.T) {
	htmlResponse := `<html><body>
		<div class="result">
			<a class="result__a" href="https://example.com/ddg1">DDG Result</a>
			<span class="result__snippet">DDG snippet.</span>
		</div>
	</body></html>`

	bc := &mockBrowser{fn: func(method, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte(htmlResponse), nil, http.StatusOK, nil
	}}

	results, err := SearchDDGDirect(context.Background(), bc, "test", "", nil)
	if err != nil {
		t.Fatalf("SearchDDGDirect: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Title != "DDG Result" {
		t.Errorf("results[0].Title = %q, want DDG Result", results[0].Title)
	}
}

func TestSearchStartpageDirect_Mock(t *testing.T) {
	htmlResponse := `<html><body>
		<div class="w-gl__result">
			<a class="w-gl__result-title" href="https://example.com/sp">SP Result</a>
			<p class="w-gl__description">SP description.</p>
		</div>
	</body></html>`

	bc := &mockBrowser{fn: func(method, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if method != "POST" {
			t.Errorf("method = %q, want POST", method)
		}
		if !strings.Contains(url, "startpage.com/sp/search") {
			t.Errorf("url = %q, want startpage search URL", url)
		}
		return []byte(htmlResponse), nil, http.StatusOK, nil
	}}

	results, err := SearchStartpageDirect(context.Background(), bc, "test", "", nil)
	if err != nil {
		t.Fatalf("SearchStartpageDirect: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
}

// --- SearchDirect fan-out test ---

func TestSearchDirect_BothEnabled(t *testing.T) {
	bc := &mockBrowser{fn: func(_, url string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if strings.Contains(url, "duckduckgo") {
			return []byte(`<html><body>
				<div class="result">
					<a class="result__a" href="https://ddg.example.com">DDG</a>
					<span class="result__snippet">DDG snippet</span>
				</div>
			</body></html>`), nil, http.StatusOK, nil
		}
		if strings.Contains(url, "startpage") {
			return []byte(`<html><body>
				<div class="w-gl__result">
					<a class="w-gl__result-title" href="https://sp.example.com">SP</a>
					<p class="w-gl__description">SP snippet</p>
				</div>
			</body></html>`), nil, http.StatusOK, nil
		}
		return nil, nil, http.StatusNotFound, nil
	}}

	cfg := DirectConfig{
		Browser:   bc,
		DDG:       true,
		Startpage: true,
		Retry:     defaultTestRetry(),
	}

	results := SearchDirect(context.Background(), cfg, "test", "en")
	if len(results) < 2 {
		t.Errorf("got %d results, want at least 2 (from DDG + Startpage)", len(results))
	}
}

func TestSearchDirect_NilBrowser(t *testing.T) {
	cfg := DirectConfig{
		Browser: nil,
		DDG:     true,
	}
	results := SearchDirect(context.Background(), cfg, "test", "en")
	if results != nil {
		t.Error("expected nil results when browser is nil")
	}
}

func defaultTestRetry() fetch.RetryConfig {
	return fetch.RetryConfig{
		MaxRetries:  0,
		InitialWait: 0,
		MaxWait:     0,
		Multiplier:  1,
	}
}
