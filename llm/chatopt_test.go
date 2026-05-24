package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestWithChatModel_ReexportOverridesModel(t *testing.T) {
	var captured string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		captured, _ = req["model"].(string)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse{
			Choices: []mockChoice{{Message: mockMessage{Content: "ok"}}},
		})
	}))
	defer srv.Close()

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("k"),
		WithModel("client-default"),
	)
	_, err := c.Complete(context.Background(), "hi", WithChatModel("per-call-override"))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	mu.Lock()
	got := captured
	mu.Unlock()
	if got != "per-call-override" {
		t.Errorf("model = %q, want per-call-override", got)
	}
}

func TestSummarizeWithTier_ChatOptModelOverride(t *testing.T) {
	var captured string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		captured, _ = req["model"].(string)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mockResponse{
			Choices: []mockChoice{{Message: mockMessage{Content: `{"answer":"ok"}`}}},
		})
	}))
	defer srv.Close()

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("k"),
		WithModel("client-default"),
	)
	_, err := c.SummarizeWithTier(context.Background(),
		SummarizeOpts{Query: "q", TotalBudget: 1000, CharsPerToken: 4, MaxOutputTokens: 100},
		nil, nil, nil, false,
		WithChatModel("summarize-override"),
	)
	if err != nil {
		t.Fatalf("SummarizeWithTier: %v", err)
	}
	mu.Lock()
	got := captured
	mu.Unlock()
	if got != "summarize-override" {
		t.Errorf("model = %q, want summarize-override", got)
	}
}
