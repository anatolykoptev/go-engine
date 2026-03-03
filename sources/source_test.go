package sources_test

import (
	"context"
	"errors"
	"testing"

	"github.com/anatolykoptev/go-engine/sources"
)

// mockSource implements Source for testing.
type mockSource struct {
	name    string
	results []sources.Result
	err     error
}

func (m *mockSource) Name() string { return m.name }

func (m *mockSource) Search(_ context.Context, _ sources.Query) ([]sources.Result, error) {
	return m.results, m.err
}

// TestSource_Interface verifies that mockSource satisfies the Source interface.
func TestSource_Interface(t *testing.T) {
	var _ sources.Source = &mockSource{}
}

// TestSource_Search verifies that a mock source returns expected results.
func TestSource_Search(t *testing.T) {
	results := []sources.Result{
		{Title: "Go 1.26", URL: "https://go.dev", Content: "Release notes", Score: 0.9},
	}
	src := &mockSource{name: "test", results: results}

	got, err := src.Search(context.Background(), sources.Query{Text: "golang"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if got[0].Title != "Go 1.26" {
		t.Errorf("title = %q, want %q", got[0].Title, "Go 1.26")
	}
	if src.Name() != "test" {
		t.Errorf("name = %q, want %q", src.Name(), "test")
	}
}

// TestSource_SearchError verifies that errors propagate correctly.
func TestSource_SearchError(t *testing.T) {
	want := errors.New("api unavailable")
	src := &mockSource{name: "fail", err: want}

	_, err := src.Search(context.Background(), sources.Query{})
	if !errors.Is(err, want) {
		t.Errorf("got error %v, want %v", err, want)
	}
}

// TestQuery_Defaults verifies zero values of Query.
func TestQuery_Defaults(t *testing.T) {
	var q sources.Query

	if q.Text != "" {
		t.Errorf("Text = %q, want empty", q.Text)
	}
	if q.Limit != 0 {
		t.Errorf("Limit = %d, want 0", q.Limit)
	}
	if q.TimeRange != "" {
		t.Errorf("TimeRange = %q, want empty", q.TimeRange)
	}
	if q.Language != "" {
		t.Errorf("Language = %q, want empty", q.Language)
	}
	if q.Extra != nil {
		t.Errorf("Extra = %v, want nil", q.Extra)
	}
}

// TestQuery_Fields verifies Query fields can be set and read.
func TestQuery_Fields(t *testing.T) {
	q := sources.Query{
		Text:      "test query",
		Limit:     10,
		TimeRange: "week",
		Language:  "en",
		Extra:     map[string]string{"source": "hn"},
	}

	if q.Text != "test query" {
		t.Errorf("Text = %q", q.Text)
	}
	if q.Limit != 10 {
		t.Errorf("Limit = %d", q.Limit)
	}
	if q.TimeRange != "week" {
		t.Errorf("TimeRange = %q", q.TimeRange)
	}
	if q.Language != "en" {
		t.Errorf("Language = %q", q.Language)
	}
	if q.Extra["source"] != "hn" {
		t.Errorf("Extra[source] = %q", q.Extra["source"])
	}
}

// TestResult_Metadata verifies metadata access on Result.
func TestResult_Metadata(t *testing.T) {
	r := sources.Result{
		Title:   "Test",
		URL:     "https://example.com",
		Content: "Some content",
		Score:   0.75,
		Metadata: map[string]string{
			"author": "Alice",
			"tags":   "go,testing",
		},
	}

	if r.Metadata["author"] != "Alice" {
		t.Errorf("author = %q, want %q", r.Metadata["author"], "Alice")
	}
	if r.Metadata["tags"] != "go,testing" {
		t.Errorf("tags = %q, want %q", r.Metadata["tags"], "go,testing")
	}
	if r.Score != 0.75 {
		t.Errorf("Score = %v, want 0.75", r.Score)
	}
}

// TestResult_Defaults verifies zero values of Result.
func TestResult_Defaults(t *testing.T) {
	var r sources.Result

	if r.Title != "" {
		t.Errorf("Title = %q, want empty", r.Title)
	}
	if r.URL != "" {
		t.Errorf("URL = %q, want empty", r.URL)
	}
	if r.Score != 0 {
		t.Errorf("Score = %v, want 0", r.Score)
	}
	if r.Metadata != nil {
		t.Errorf("Metadata = %v, want nil", r.Metadata)
	}
}
