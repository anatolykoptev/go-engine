package search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anatolykoptev/go-engine/sources"
)

// --- Hard Red: FuseWRR ---

func TestHR_FuseWRR_ConcurrentCalls(t *testing.T) {
	sets := [][]sources.Result{
		{{Title: "A", URL: "http://a.com"}, {Title: "B", URL: "http://b.com"}},
		{{Title: "B", URL: "http://b.com"}, {Title: "C", URL: "http://c.com"}},
	}
	weights := []float64{1.0, 1.0}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := FuseWRR(sets, weights)
			if len(result) != 3 {
				t.Errorf("got %d results, want 3", len(result))
			}
		}()
	}
	wg.Wait()
}

func TestHR_FuseWRR_AllEmptyURLs(t *testing.T) {
	sets := [][]sources.Result{
		{{Title: "A", URL: ""}, {Title: "B", URL: ""}},
		{{Title: "C", URL: ""}},
	}
	result := FuseWRR(sets, []float64{1.0, 1.0})
	if len(result) != 0 {
		t.Errorf("got %d results, want 0 (all empty URLs skipped)", len(result))
	}
}

func TestHR_FuseWRR_WeightsLongerThanSets(t *testing.T) {
	sets := [][]sources.Result{
		{{Title: "A", URL: "http://a.com"}},
	}
	// More weights than result sets — extra weights ignored, no panic.
	result := FuseWRR(sets, []float64{1.0, 2.0, 3.0})
	if len(result) != 1 {
		t.Fatalf("got %d, want 1", len(result))
	}
}

func TestHR_FuseWRR_NilWeights(t *testing.T) {
	sets := [][]sources.Result{
		{{Title: "A", URL: "http://a.com"}},
		{{Title: "B", URL: "http://b.com"}},
	}
	// nil weights — defaults to 1.0 each.
	result := FuseWRR(sets, nil)
	if len(result) != 2 {
		t.Fatalf("got %d, want 2", len(result))
	}
}

func TestHR_FuseWRR_EmptySetsInMiddle(t *testing.T) {
	sets := [][]sources.Result{
		{{Title: "A", URL: "http://a.com"}},
		{}, // empty set in middle
		{{Title: "B", URL: "http://b.com"}},
	}
	result := FuseWRR(sets, []float64{1.0, 1.0, 1.0})
	if len(result) != 2 {
		t.Fatalf("got %d, want 2", len(result))
	}
}

func TestHR_FuseWRR_ZeroWeight(t *testing.T) {
	sets := [][]sources.Result{
		{{Title: "A", URL: "http://a.com"}},
		{{Title: "B", URL: "http://b.com"}},
	}
	// Weight 0 for second source — B should have score 0.
	result := FuseWRR(sets, []float64{1.0, 0.0})
	if len(result) != 2 {
		t.Fatalf("got %d, want 2", len(result))
	}
	// A (weight 1.0) should be first.
	if result[0].URL != "http://a.com" {
		t.Errorf("first = %q, want http://a.com", result[0].URL)
	}
	if result[1].Score != 0.0 {
		t.Errorf("second score = %f, want 0.0", result[1].Score)
	}
}

func TestHR_FuseWRR_LargeResultSets(t *testing.T) {
	// 3 sources × 100 results each, all unique URLs.
	var sets [][]sources.Result
	for s := 0; s < 3; s++ {
		set := make([]sources.Result, 100)
		for i := range set {
			set[i] = sources.Result{
				Title: fmt.Sprintf("S%d-R%d", s, i),
				URL:   fmt.Sprintf("http://s%d.com/%d", s, i),
			}
		}
		sets = append(sets, set)
	}
	result := FuseWRR(sets, []float64{1.0, 1.0, 1.0})
	if len(result) != 300 {
		t.Errorf("got %d, want 300", len(result))
	}
	// Must be sorted by score descending.
	for i := 1; i < len(result); i++ {
		if result[i].Score > result[i-1].Score {
			t.Fatalf("not sorted at index %d: %.6f > %.6f", i, result[i].Score, result[i-1].Score)
		}
	}
}

// --- Hard Red: DedupSnippets ---

func TestHR_DedupSnippets_ConcurrentCalls(t *testing.T) {
	results := []sources.Result{
		{Title: "A", URL: "http://a.com", Content: "go programming language", Score: 1.0},
		{Title: "B", URL: "http://b.com", Content: "go programming language for systems", Score: 0.9},
		{Title: "C", URL: "http://c.com", Content: "python machine learning", Score: 0.8},
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deduped := DedupSnippets(results, 0.85)
			if len(deduped) < 1 || len(deduped) > 3 {
				t.Errorf("unexpected result count: %d", len(deduped))
			}
		}()
	}
	wg.Wait()
}

func TestHR_DedupSnippets_SingleResult(t *testing.T) {
	results := []sources.Result{
		{Title: "Only", URL: "http://only.com", Content: "unique", Score: 1.0},
	}
	deduped := DedupSnippets(results, 0.85)
	if len(deduped) != 1 {
		t.Fatalf("got %d, want 1", len(deduped))
	}
}

func TestHR_DedupSnippets_LowerScoredSurvivesWhenFirst(t *testing.T) {
	// When i has lower score than j, i gets removed and j survives.
	results := []sources.Result{
		{Title: "Low", URL: "http://low.com", Content: "identical words here", Score: 0.1},
		{Title: "High", URL: "http://high.com", Content: "identical words here", Score: 0.9},
	}
	deduped := DedupSnippets(results, 0.5)
	if len(deduped) != 1 {
		t.Fatalf("got %d, want 1", len(deduped))
	}
	if deduped[0].URL != "http://high.com" {
		t.Errorf("kept = %q, want http://high.com (higher score)", deduped[0].URL)
	}
}

func TestHR_DedupSnippets_UnicodeContent(t *testing.T) {
	results := []sources.Result{
		{Title: "RU1", URL: "http://a.com", Content: "программирование на языке го для систем", Score: 1.0},
		{Title: "RU2", URL: "http://b.com", Content: "программирование на языке го для систем автоматизации", Score: 0.9},
		{Title: "CN", URL: "http://c.com", Content: "机器学习和深度学习", Score: 0.8},
	}
	deduped := DedupSnippets(results, 0.85)
	// RU1 and RU2 are similar in Cyrillic.
	if len(deduped) > 3 {
		t.Errorf("unexpected count: %d", len(deduped))
	}
	// Chinese result should always survive.
	found := false
	for _, r := range deduped {
		if r.URL == "http://c.com" {
			found = true
		}
	}
	if !found {
		t.Error("Chinese result should survive dedup")
	}
}

func TestHR_DedupSnippets_ThresholdOne(t *testing.T) {
	// threshold=1.0 means only exact identical vectors are deduped.
	results := []sources.Result{
		{Title: "A", URL: "http://a.com", Content: "hello world foo", Score: 1.0},
		{Title: "B", URL: "http://b.com", Content: "hello world bar", Score: 0.9},
	}
	deduped := DedupSnippets(results, 1.0)
	// cosine < 1.0, so no dedup should happen.
	if len(deduped) != 2 {
		t.Fatalf("got %d, want 2 (threshold=1.0, not exact)", len(deduped))
	}
}

func TestHR_DedupSnippets_EqualScores(t *testing.T) {
	// When scores are equal, the first one survives (>= comparison).
	results := []sources.Result{
		{Title: "First", URL: "http://first.com", Content: "same content", Score: 0.5},
		{Title: "Second", URL: "http://second.com", Content: "same content", Score: 0.5},
	}
	deduped := DedupSnippets(results, 0.5)
	if len(deduped) != 1 {
		t.Fatalf("got %d, want 1", len(deduped))
	}
	if deduped[0].URL != "http://first.com" {
		t.Errorf("kept = %q, want http://first.com (first wins on equal score)", deduped[0].URL)
	}
}

// --- Hard Red: cosineSimilarity edge cases ---

func TestHR_CosineSimilarity_IdenticalVectors(t *testing.T) {
	a := map[string]float64{"go": 3, "lang": 1}
	b := map[string]float64{"go": 3, "lang": 1}
	sim := cosineSimilarity(a, b)
	if sim < 0.999 || sim > 1.001 {
		t.Errorf("identical vectors: cosine = %f, want ~1.0", sim)
	}
}

func TestHR_CosineSimilarity_OrthogonalVectors(t *testing.T) {
	a := map[string]float64{"go": 1}
	b := map[string]float64{"python": 1}
	sim := cosineSimilarity(a, b)
	if sim != 0 {
		t.Errorf("orthogonal vectors: cosine = %f, want 0", sim)
	}
}

func TestHR_CosineSimilarity_BothEmpty(t *testing.T) {
	sim := cosineSimilarity(map[string]float64{}, map[string]float64{})
	if sim != 0 {
		t.Errorf("both empty: cosine = %f, want 0", sim)
	}
}

func TestHR_CosineSimilarity_NilMaps(t *testing.T) {
	sim := cosineSimilarity(nil, nil)
	if sim != 0 {
		t.Errorf("nil maps: cosine = %f, want 0", sim)
	}
}

// --- Hard Red: tokenize edge cases ---

func TestHR_Tokenize_EmptyString(t *testing.T) {
	vec := tokenize("")
	if len(vec) != 0 {
		t.Errorf("empty string: got %d tokens, want 0", len(vec))
	}
}

func TestHR_Tokenize_PunctuationOnly(t *testing.T) {
	vec := tokenize("!!! ... ??? --- ///")
	if len(vec) != 0 {
		t.Errorf("punctuation only: got %d tokens, want 0", len(vec))
	}
}

func TestHR_Tokenize_MixedCaseNormalization(t *testing.T) {
	vec := tokenize("Go GO go gO")
	if len(vec) != 1 {
		t.Fatalf("mixed case: got %d unique tokens, want 1", len(vec))
	}
	if vec["go"] != 4 {
		t.Errorf("go count = %f, want 4", vec["go"])
	}
}

// --- Hard Red: ResultsToMarkdown ---

func TestHR_ResultsToMarkdown_ConcurrentCalls(t *testing.T) {
	results := []sources.Result{
		{Title: "A", URL: "http://a.com", Content: "content a"},
		{Title: "B", URL: "http://b.com", Content: "content b"},
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			md := ResultsToMarkdown(results)
			if !strings.Contains(md, "## 1.") {
				t.Errorf("missing header")
			}
		}()
	}
	wg.Wait()
}

func TestHR_ResultsToMarkdown_LargeInput(t *testing.T) {
	results := make([]sources.Result, 200)
	for i := range results {
		results[i] = sources.Result{
			Title:   fmt.Sprintf("Title %d", i),
			URL:     fmt.Sprintf("http://example.com/%d", i),
			Content: strings.Repeat("word ", 100),
		}
	}
	md := ResultsToMarkdown(results)
	if !strings.Contains(md, "## 200.") {
		t.Error("missing last result header")
	}
}

func TestHR_ResultsToMarkdown_URLWithSpecialChars(t *testing.T) {
	results := []sources.Result{
		{Title: "Test", URL: "http://example.com/path?q=hello&lang=en#section", Content: "ok"},
	}
	md := ResultsToMarkdown(results)
	if !strings.Contains(md, "http://example.com/path?q=hello&lang=en#section") {
		t.Errorf("URL should be preserved verbatim:\n%s", md)
	}
}

func TestHR_ResultsToMarkdown_EmptyTitleAndURL(t *testing.T) {
	results := []sources.Result{
		{Title: "", URL: "", Content: "orphan content"},
	}
	md := ResultsToMarkdown(results)
	// Should not panic, should produce valid output.
	if !strings.Contains(md, "## 1.") {
		t.Errorf("should still produce numbered header:\n%s", md)
	}
}

// --- Hard Red: SearXNG SearchQuery ---

func TestHR_SearXNG_SearchQuery_NilExtra(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	s := NewSearXNG(srv.URL)
	// Query with nil Extra — should not panic.
	_, err := s.SearchQuery(context.Background(), sources.Query{Text: "test"})
	if err != nil {
		t.Fatalf("SearchQuery with nil Extra: %v", err)
	}
}

func TestHR_SearXNG_SearchQuery_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	s := NewSearXNG(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := s.SearchQuery(ctx, sources.Query{Text: "test"})
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}

func TestHR_SearXNG_SearchQuery_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{broken json`))
	}))
	defer srv.Close()

	s := NewSearXNG(srv.URL)
	_, err := s.SearchQuery(context.Background(), sources.Query{Text: "test"})
	if err == nil {
		t.Fatal("expected error on malformed JSON")
	}
}

func TestHR_SearXNG_SearchQuery_AllParams(t *testing.T) {
	var received string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	s := NewSearXNG(srv.URL)
	_, err := s.SearchQuery(context.Background(), sources.Query{
		Text:      "golang",
		Language:  "en",
		TimeRange: "week",
		Extra: map[string]string{
			"categories": "it",
			"engines":    "google",
		},
	})
	if err != nil {
		t.Fatalf("SearchQuery: %v", err)
	}
	for _, param := range []string{"categories=it", "engines=google", "language=en", "time_range=week"} {
		if !strings.Contains(received, param) {
			t.Errorf("missing param %q in %q", param, received)
		}
	}
}

func TestHR_SearXNG_SearchQuery_Concurrent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(searxngResponse{
			Results: []searxngResult{{Title: "R", URL: "http://r.com"}},
		})
	}))
	defer srv.Close()

	s := NewSearXNG(srv.URL)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results, err := s.SearchQuery(context.Background(), sources.Query{Text: "test"})
			if err != nil {
				t.Errorf("SearchQuery: %v", err)
				return
			}
			if len(results) != 1 {
				t.Errorf("got %d results, want 1", len(results))
			}
		}()
	}
	wg.Wait()
}

// --- Hard Red: ErrRateLimited ---

func TestHR_ErrRateLimited_EmptyEngine(t *testing.T) {
	err := &ErrRateLimited{Engine: ""}
	got := err.Error()
	if got != "rate limited by " {
		t.Errorf("Error() = %q", got)
	}
}

func TestHR_ErrRateLimited_NegativeRetryAfter(t *testing.T) {
	err := &ErrRateLimited{Engine: "test", RetryAfter: -1}
	got := err.Error()
	// Negative duration should use simple format (not > 0).
	if got != "rate limited by test" {
		t.Errorf("Error() = %q, want no retry suffix for negative", got)
	}
}

// --- Hard Red: isDDGRateLimited edge cases ---

func TestHR_IsDDGRateLimited_NilBody(t *testing.T) {
	if isDDGRateLimited(nil) {
		t.Error("nil body should not be rate limited")
	}
}

func TestHR_IsDDGRateLimited_MarkerInAttribute(t *testing.T) {
	// "blocked" appears inside an attribute value, not visible text.
	body := []byte(`<div class="blocked-banner">Normal content here</div>`)
	if !isDDGRateLimited(body) {
		// Our detector checks byte content, so attribute values still match.
		// This is intentional — better safe than sorry.
		t.Error("marker in attribute should still trigger detection")
	}
}

func TestHR_IsStartpageRateLimited_NilBody(t *testing.T) {
	if isStartpageRateLimited(nil) {
		t.Error("nil body should not be rate limited")
	}
}
