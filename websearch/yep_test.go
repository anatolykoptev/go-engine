package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseYepJSON(t *testing.T) {
	resp := []any{
		"Ok",
		map[string]any{
			"results": []map[string]any{
				{"url": "https://example.com/go", "title": "Go Guide", "snippet": "A guide to Go.", "type": "Organic"},
				{"url": "https://google.com/search?q=test", "title": "Search Google", "snippet": "", "type": "Alt_search_engine"},
				{"url": "https://example.com/ctx", "title": "Context in Go", "snippet": "Context usage.", "type": "Organic"},
			},
			"total": 50,
		},
	}
	data, _ := json.Marshal(resp)

	results, err := ParseYepJSON(data)
	if err != nil {
		t.Fatalf("ParseYepJSON: %v", err)
	}
	// Should skip Alt_search_engine
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Go Guide" {
		t.Errorf("results[0].Title = %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/go" {
		t.Errorf("results[0].URL = %q", results[0].URL)
	}
	if results[1].Title != "Context in Go" {
		t.Errorf("results[1].Title = %q", results[1].Title)
	}
	for i, r := range results {
		if r.Score != directResultScore {
			t.Errorf("results[%d].Score = %f, want %f", i, r.Score, directResultScore)
		}
		if r.Metadata["engine"] != "yep" {
			t.Errorf("results[%d].Metadata[engine] = %q, want yep", i, r.Metadata["engine"])
		}
	}
}

func TestParseYepJSON_Error(t *testing.T) {
	data := []byte(`["Error","endpoint don't exist"]`)
	_, err := ParseYepJSON(data)
	if err == nil {
		t.Fatal("expected error on Error status")
	}
}

func TestParseYepJSON_EmptyResults(t *testing.T) {
	resp := []any{"Ok", map[string]any{"results": []map[string]any{}, "total": 0}}
	data, _ := json.Marshal(resp)

	results, err := ParseYepJSON(data)
	if err != nil {
		t.Fatalf("ParseYepJSON: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestParseYepJSON_SkipsEmptyURL(t *testing.T) {
	resp := []any{
		"Ok",
		map[string]any{
			"results": []map[string]any{
				{"url": "", "title": "No URL", "snippet": "test", "type": "Organic"},
				{"url": "https://example.com", "title": "Has URL", "snippet": "test", "type": "Organic"},
			},
			"total": 2,
		},
	}
	data, _ := json.Marshal(resp)

	results, err := ParseYepJSON(data)
	if err != nil {
		t.Fatalf("ParseYepJSON: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (skip empty URL)", len(results))
	}
}


// TestYep_Search_RequestEndpoint is a regression guard for the 2026-06 endpoint
// migration: the deprecated https://api.yep.com/fs/2/search returns HTTP 501 and
// the old q= param returns 400 on the new endpoint. This locks in the working
// shape: path /search + param query=. See go-engine PR (yep endpoint migration).
func TestYep_Search_RequestEndpoint(t *testing.T) {
	var gotURL string
	bc := &mockBrowser{fn: func(_, u string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		gotURL = u
		return []byte(`["Ok",{"results":[],"total":0}]`), nil, http.StatusOK, nil
	}}

	y := NewYep(WithYepBrowser(bc))
	if _, err := y.Search(context.Background(), "golang context", SearchOpts{}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	if !strings.HasPrefix(gotURL, "https://api.yep.com/search?") {
		t.Errorf("endpoint = %q, want prefix https://api.yep.com/search?", gotURL)
	}
	if strings.Contains(gotURL, "/fs/2/search") {
		t.Errorf("request still uses deprecated /fs/2/search path: %q", gotURL)
	}
	if !strings.Contains(gotURL, "query=golang") {
		t.Errorf("request missing query= param (new endpoint requires it): %q", gotURL)
	}
	if strings.Contains(gotURL, "q=golang") {
		t.Errorf("request still uses deprecated q= param (returns 400 on new endpoint): %q", gotURL)
	}
}
