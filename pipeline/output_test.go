package pipeline

import (
	"testing"

	"github.com/anatolykoptev/go-engine/llm"
	"github.com/anatolykoptev/go-engine/search"
)

func TestFormatOutput_TruncateAnswer(t *testing.T) {
	out := SearchOutput{Answer: "abcdefghij"}
	got := FormatOutput(out, OutputOpts{MaxAnswerChars: 5})
	if got.Answer != "abcde..." {
		t.Errorf("answer = %q, want %q", got.Answer, "abcde...")
	}
}

func TestFormatOutput_NoTruncateUnderLimit(t *testing.T) {
	out := SearchOutput{Answer: "short"}
	got := FormatOutput(out, OutputOpts{MaxAnswerChars: 100})
	if got.Answer != "short" {
		t.Errorf("answer = %q", got.Answer)
	}
}

func TestFormatOutput_StripSnippets(t *testing.T) {
	out := SearchOutput{
		Sources: []SourceItem{
			{Index: 1, Title: "T", URL: "http://a.com", Snippet: "snip"},
		},
	}
	got := FormatOutput(out, OutputOpts{IncludeSnippets: false})
	if got.Sources[0].Snippet != "" {
		t.Error("snippet should be stripped")
	}
}

func TestFormatOutput_KeepSnippets(t *testing.T) {
	out := SearchOutput{
		Sources: []SourceItem{
			{Index: 1, Title: "T", URL: "http://a.com", Snippet: "snip"},
		},
	}
	got := FormatOutput(out, OutputOpts{IncludeSnippets: true})
	if got.Sources[0].Snippet != "snip" {
		t.Error("snippet should be kept")
	}
}

func TestFormatOutput_MaxSources(t *testing.T) {
	out := SearchOutput{
		Sources: []SourceItem{
			{Index: 1}, {Index: 2}, {Index: 3}, {Index: 4}, {Index: 5},
		},
	}
	got := FormatOutput(out, OutputOpts{MaxSources: 3})
	if len(got.Sources) != 3 {
		t.Errorf("sources = %d, want 3", len(got.Sources))
	}
}

func TestBuildSearchOutput(t *testing.T) {
	llmOut := &llm.StructuredOutput{
		Answer: "Test answer",
		Facts:  []llm.FactItem{{Point: "Fact1", Sources: []int{1}}},
	}
	results := []search.Result{
		{Title: "T1", URL: "http://a.com", Content: "c1"},
		{Title: "T2", URL: "http://b.com", Content: "c2"},
	}
	got := BuildSearchOutput("q", llmOut, results)
	if got.Query != "q" {
		t.Errorf("query = %q", got.Query)
	}
	if got.Answer != "Test answer" {
		t.Errorf("answer = %q", got.Answer)
	}
	if len(got.Facts) != 1 {
		t.Errorf("facts = %d", len(got.Facts))
	}
	if len(got.Sources) != 2 {
		t.Errorf("sources = %d", len(got.Sources))
	}
	if got.Sources[0].Index != 1 || got.Sources[1].Index != 2 {
		t.Error("indices incorrect")
	}
	if got.Sources[0].Snippet != "c1" {
		t.Errorf("snippet = %q", got.Sources[0].Snippet)
	}
}

func TestDefaultOutputOpts(t *testing.T) {
	if DefaultOutputOpts.MaxAnswerChars != 3000 {
		t.Errorf("MaxAnswerChars = %d", DefaultOutputOpts.MaxAnswerChars)
	}
	if DefaultOutputOpts.MaxSources != 8 {
		t.Errorf("MaxSources = %d", DefaultOutputOpts.MaxSources)
	}
	if DefaultOutputOpts.IncludeSnippets {
		t.Error("IncludeSnippets should be false by default")
	}
}
