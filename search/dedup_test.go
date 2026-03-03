package search

import (
	"testing"

	"github.com/anatolykoptev/go-engine/sources"
)

func TestDedupSnippets_RemovesDuplicates(t *testing.T) {
	results := []sources.Result{
		{Title: "A", URL: "http://a.com", Content: "golang is a programming language for building systems", Score: 1.0},
		{Title: "B", URL: "http://b.com", Content: "golang is a programming language for building systems software", Score: 0.9},
		{Title: "C", URL: "http://c.com", Content: "python is great for machine learning", Score: 0.8},
	}

	deduped := DedupSnippets(results, 0.85)
	// A and B are near-duplicates → keep A (higher score). C is unique.
	if len(deduped) != 2 {
		t.Fatalf("got %d results, want 2", len(deduped))
	}
	if deduped[0].URL != "http://a.com" {
		t.Errorf("first = %q, want http://a.com", deduped[0].URL)
	}
	if deduped[1].URL != "http://c.com" {
		t.Errorf("second = %q, want http://c.com", deduped[1].URL)
	}
}

func TestDedupSnippets_EmptyContent(t *testing.T) {
	results := []sources.Result{
		{Title: "A", URL: "http://a.com", Content: ""},
		{Title: "B", URL: "http://b.com", Content: ""},
		{Title: "C", URL: "http://c.com", Content: "unique content"},
	}
	deduped := DedupSnippets(results, 0.85)
	// Empty content should not be deduped (cosine is 0).
	if len(deduped) != 3 {
		t.Fatalf("got %d, want 3", len(deduped))
	}
}

func TestDedupSnippets_NilInput(t *testing.T) {
	deduped := DedupSnippets(nil, 0.85)
	if deduped != nil {
		t.Errorf("expected nil, got %v", deduped)
	}
}

func TestDedupSnippets_AllIdentical(t *testing.T) {
	results := []sources.Result{
		{Title: "A", URL: "http://a.com", Content: "same content here", Score: 1.0},
		{Title: "B", URL: "http://b.com", Content: "same content here", Score: 0.5},
		{Title: "C", URL: "http://c.com", Content: "same content here", Score: 0.3},
	}
	deduped := DedupSnippets(results, 0.85)
	// All identical → keep only the highest-scored one.
	if len(deduped) != 1 {
		t.Fatalf("got %d, want 1", len(deduped))
	}
	if deduped[0].URL != "http://a.com" {
		t.Errorf("kept = %q, want http://a.com (highest score)", deduped[0].URL)
	}
}

func TestDedupSnippets_ThresholdZero(t *testing.T) {
	results := []sources.Result{
		{Title: "A", URL: "http://a.com", Content: "hello world"},
		{Title: "B", URL: "http://b.com", Content: "hello world"},
	}
	// threshold=0 means any similarity > 0 deduplicates.
	deduped := DedupSnippets(results, 0.0)
	if len(deduped) != 1 {
		t.Fatalf("got %d, want 1", len(deduped))
	}
}
