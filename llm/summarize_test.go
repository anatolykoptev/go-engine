package llm

import (
	"strings"
	"testing"

	"github.com/anatolykoptev/go-engine/sources"
)

func TestBuildSourcesText_RankedAllocation(t *testing.T) {
	results := make([]sources.Result, 5)
	contents := make(map[string]string)
	for i := range results {
		results[i] = sources.Result{
			Title:   "Title",
			URL:     "https://example.com/" + string(rune('a'+i)),
			Content: "Snippet text for fallback",
		}
		// Each page has 10000 chars of content (way over any per-source budget).
		contents[results[i].URL] = strings.Repeat("word ", 2000)
	}

	out := BuildSourcesText(results, contents, 3000, 3.5)

	// Each source should have URL present.
	for i := range results {
		if !strings.Contains(out, results[i].URL) {
			t.Errorf("missing source %d URL in output", i)
		}
	}

	// Total output should be under totalBudget * charsPerToken + headers.
	maxChars := int(3000*3.5) + 5*100
	if len(out) > maxChars {
		t.Errorf("output too large: %d chars, max %d", len(out), maxChars)
	}

	// Source 0 content should exist.
	if !strings.Contains(out, "Content: ") {
		t.Error("expected Content: field for at least source 0")
	}
}

func TestBuildSourcesText_SnippetFallback(t *testing.T) {
	results := []sources.Result{
		{Title: "T1", URL: "https://a.com", Content: "snippet A"},
		{Title: "T2", URL: "https://b.com", Content: "snippet B"},
	}
	out := BuildSourcesText(results, nil, 3000, 3.5)
	if !strings.Contains(out, "Snippet: snippet A") {
		t.Error("expected snippet fallback for source without content")
	}
}

func TestBuildSourcesText_BeyondWeightsGetSnippets(t *testing.T) {
	// Create 8 results — sources beyond rankedWeights (5) should get snippets, not content
	results := make([]sources.Result, 8)
	contents := make(map[string]string)
	for i := range results {
		results[i] = sources.Result{
			Title:   "Title",
			URL:     "https://example.com/" + string(rune('a'+i)),
			Content: "Snippet for source " + string(rune('a'+i)),
		}
		contents[results[i].URL] = strings.Repeat("content ", 500)
	}

	out := BuildSourcesText(results, contents, 3000, 3.5)

	// Sources 0-4 should have "Content:" (ranked weights)
	// Sources 5-7 should have "Snippet:" (beyond weights)
	lines := strings.Split(out, "\n")
	contentCount := 0
	snippetCount := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "Content: ") {
			contentCount++
		}
		if strings.HasPrefix(line, "Snippet: ") {
			snippetCount++
		}
	}
	if contentCount != 5 {
		t.Errorf("expected 5 Content: entries (ranked), got %d", contentCount)
	}
	if snippetCount != 3 {
		t.Errorf("expected 3 Snippet: entries (beyond weights), got %d", snippetCount)
	}
}
