package search

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// mojeekFixtureHTML is a minimal but structurally faithful Mojeek SERP.
// Selectors verified against live Mojeek HTML on 2026-06-18:
//
//	ul.results-standard > li > h2 > a.title  (title + href)
//	li > p.s                                  (snippet)
const mojeekFixtureHTML = `<html><body>
<ul class="results-standard">
<li class="r1"><h2><a class="title" href="https://go.dev/">The Go Programming Language</a></h2><p class="s">Go is an open source programming language.</p></li>
<li class="r2"><h2><a class="title" href="https://golangweekly.com/">Go Weekly</a></h2><p class="s">The week in Go, hand-picked.</p></li>
</ul>
</body></html>`

func TestParseMojeekHTML_HappyPath(t *testing.T) {
	results, err := ParseMojeekHTML([]byte(mojeekFixtureHTML))
	if err != nil {
		t.Fatalf("ParseMojeekHTML: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "The Go Programming Language" {
		t.Errorf("results[0].Title = %q", results[0].Title)
	}
	if results[0].URL != "https://go.dev/" {
		t.Errorf("results[0].URL = %q", results[0].URL)
	}
	if results[0].Content != "Go is an open source programming language." {
		t.Errorf("results[0].Content = %q", results[0].Content)
	}
	if results[0].Metadata["engine"] != "mojeek" {
		t.Errorf("Metadata[engine] = %q, want mojeek", results[0].Metadata["engine"])
	}
}

func TestParseMojeekHTML_ZeroResults(t *testing.T) {
	// No ul.results-standard → 0 results, no error (graceful degrade)
	results, err := ParseMojeekHTML([]byte(`<html><body><p>No results</p></body></html>`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestSearchMojeekDirect_NonOK(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, http.StatusInternalServerError, nil
	}}
	_, err := SearchMojeekDirect(context.Background(), bc, "golang", nil)
	if err == nil {
		t.Error("expected error on 500, got nil")
	}
}

func TestSearchMojeekDirect_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte(mojeekFixtureHTML), nil, http.StatusOK, nil
	}}
	_, err := SearchMojeekDirect(ctx, bc, "golang", nil)
	if err == nil {
		t.Error("expected error on cancelled context, got nil")
	}
}
