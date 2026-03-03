package extract

import (
	"context"
	"testing"
)

const thinHTML = `<html><head><title>Thin</title></head><body><p>Hi</p></body></html>`

func TestExtractor_LLMFallback_TriggeredOnThinContent(t *testing.T) {
	var called bool
	fallback := func(_ context.Context, _ string) (string, error) {
		called = true
		return "LLM extracted detailed content from the page", nil
	}

	ext := New(WithLLMFallback(fallback), WithMinExtractChars(100))
	result, err := ext.Extract(context.Background(), []byte(thinHTML), nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !called {
		t.Error("LLM fallback should have been called for thin content")
	}
	if result.Content != "LLM extracted detailed content from the page" {
		t.Errorf("content = %q, want LLM output", result.Content)
	}
}

func TestExtractor_LLMFallback_NotTriggeredOnGoodContent(t *testing.T) {
	var called bool
	fallback := func(_ context.Context, _ string) (string, error) {
		called = true
		return "should not be used", nil
	}

	ext := New(WithLLMFallback(fallback), WithMinExtractChars(10))
	// sampleHTML has plenty of content.
	result, err := ext.Extract(context.Background(), []byte(sampleHTML), nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if called {
		t.Error("LLM fallback should NOT be called when content is sufficient")
	}
	if result.Content == "" {
		t.Error("expected non-empty content from normal extraction")
	}
}

func TestExtractor_LLMFallback_NilFunc(t *testing.T) {
	// No fallback configured — should still work, return thin content.
	ext := New(WithMinExtractChars(100))
	result, err := ext.Extract(context.Background(), []byte(thinHTML), nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// Returns whatever the tiers produced (thin but present).
	_ = result
}
