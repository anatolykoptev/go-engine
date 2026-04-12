package websearch

import (
	"encoding/json"
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
