package search

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestParseWikipediaJSON_HappyPath(t *testing.T) {
	data := []byte(`{"query":{"search":[{"title":"Go (programming language)","snippet":"Go is a <span>statically typed</span> language","pageid":12345}]}}`)
	results, err := ParseWikipediaJSON(data, "en")
	if err != nil {
		t.Fatalf("ParseWikipediaJSON: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	r := results[0]
	if r.Title != "Go (programming language)" {
		t.Errorf("Title = %q, want Go (programming language)", r.Title)
	}
	if r.Content == "" {
		t.Error("Content should not be empty")
	}
	// HTML stripped: snippet contains <span> tag, should be gone
	for _, tag := range []string{"<span>", "</span>"} {
		if containsStr(r.Content, tag) {
			t.Errorf("Content still contains HTML tag %q: %q", tag, r.Content)
		}
	}
	if r.Metadata["engine"] != "wikipedia" {
		t.Errorf("Metadata[engine] = %q, want wikipedia", r.Metadata["engine"])
	}
	if r.Score != 1.0 {
		t.Errorf("Score = %f, want 1.0", r.Score)
	}
	// URL should contain the title (path-escaped)
	if !containsStr(r.URL, "wikipedia.org/wiki/") {
		t.Errorf("URL missing expected path: %q", r.URL)
	}
}

func TestParseWikipediaJSON_EmptyResults(t *testing.T) {
	data := []byte(`{"query":{"search":[]}}`)
	results, err := ParseWikipediaJSON(data, "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestSearchWikipediaDirect_NonOK(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, http.StatusInternalServerError, nil
	}}
	_, err := SearchWikipediaDirect(context.Background(), bc, "golang", "en", nil)
	if err == nil {
		t.Error("expected error on 500, got nil")
	}
}

func TestSearchWikipediaDirect_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte(`{"query":{"search":[]}}`), nil, http.StatusOK, nil
	}}
	_, err := SearchWikipediaDirect(ctx, bc, "golang", "en", nil)
	if err == nil {
		t.Error("expected error on cancelled context, got nil")
	}
}

func TestSearchWikipediaDirect_RateLimit(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, http.StatusTooManyRequests, nil
	}}
	results, err := SearchWikipediaDirect(context.Background(), bc, "golang", "en", nil)
	if err != nil {
		t.Errorf("expected nil error on 429, got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on 429, got %v", results)
	}
}

// containsStr is a helper to avoid importing strings in tests.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
