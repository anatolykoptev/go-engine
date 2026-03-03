package search

import (
	"strings"
	"testing"

	"github.com/anatolykoptev/go-engine/sources"
)

func TestResultsToMarkdown_Basic(t *testing.T) {
	results := []sources.Result{
		{Title: "Go Programming", URL: "https://go.dev", Content: "The Go programming language."},
		{Title: "Go Tutorial", URL: "https://go.dev/tour", Content: "A tour of Go."},
	}

	md := ResultsToMarkdown(results)

	if !strings.Contains(md, "## 1. [Go Programming](https://go.dev)") {
		t.Errorf("missing first result header in:\n%s", md)
	}
	if !strings.Contains(md, "The Go programming language.") {
		t.Errorf("missing first snippet in:\n%s", md)
	}
	if !strings.Contains(md, "## 2. [Go Tutorial](https://go.dev/tour)") {
		t.Errorf("missing second result header in:\n%s", md)
	}
}

func TestResultsToMarkdown_Empty(t *testing.T) {
	md := ResultsToMarkdown(nil)
	if md != "" {
		t.Errorf("expected empty string, got %q", md)
	}
}

func TestResultsToMarkdown_NoContent(t *testing.T) {
	results := []sources.Result{
		{Title: "Title", URL: "https://example.com", Content: ""},
	}
	md := ResultsToMarkdown(results)
	if !strings.Contains(md, "## 1. [Title](https://example.com)") {
		t.Errorf("missing header in:\n%s", md)
	}
	// No content line should be present.
	lines := strings.Split(strings.TrimSpace(md), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line (header only), got %d", len(lines))
	}
}

func TestResultsToMarkdown_SpecialCharsInTitle(t *testing.T) {
	results := []sources.Result{
		{Title: "Go [1.22] Release & Notes", URL: "https://go.dev", Content: "Release notes."},
	}
	md := ResultsToMarkdown(results)
	// Brackets in title should be preserved.
	if !strings.Contains(md, "Go [1.22] Release & Notes") {
		t.Errorf("title should be preserved:\n%s", md)
	}
}
