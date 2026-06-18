package search

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestParseMarginaliaJSON_HappyPath(t *testing.T) {
	data := []byte(`{"results":[{"url":"https://example.com","title":"Test Page","description":"A test page","quality":3.5},{"url":"https://example2.com","title":"Another Page","description":"Another page","quality":2.1}]}`)
	results, err := ParseMarginaliaJSON(data)
	if err != nil {
		t.Fatalf("ParseMarginaliaJSON: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Title != "Test Page" {
		t.Errorf("results[0].Title = %q, want Test Page", results[0].Title)
	}
	if results[0].URL != "https://example.com" {
		t.Errorf("results[0].URL = %q, want https://example.com", results[0].URL)
	}
	if results[0].Content != "A test page" {
		t.Errorf("results[0].Content = %q, want A test page", results[0].Content)
	}
	if results[0].Metadata["engine"] != "marginalia" {
		t.Errorf("Metadata[engine] = %q, want marginalia", results[0].Metadata["engine"])
	}
	if results[1].Title != "Another Page" {
		t.Errorf("results[1].Title = %q, want Another Page", results[1].Title)
	}
}

func TestParseMarginaliaJSON_Empty(t *testing.T) {
	data := []byte(`{"results":[]}`)
	results, err := ParseMarginaliaJSON(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestSearchMarginaliaDirect_NonOK(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, http.StatusInternalServerError, nil
	}}
	_, err := SearchMarginaliaDirect(context.Background(), bc, "golang", nil)
	if err == nil {
		t.Error("expected error on 500, got nil")
	}
}

func TestSearchMarginaliaDirect_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte(`{"results":[]}`), nil, http.StatusOK, nil
	}}
	_, err := SearchMarginaliaDirect(ctx, bc, "golang", nil)
	if err == nil {
		t.Error("expected error on cancelled context, got nil")
	}
}

func TestSearchMarginaliaDirect_RateLimit(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return nil, nil, http.StatusTooManyRequests, nil
	}}
	results, err := SearchMarginaliaDirect(context.Background(), bc, "golang", nil)
	if err != nil {
		t.Errorf("expected nil error on 429, got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on 429")
	}
}
