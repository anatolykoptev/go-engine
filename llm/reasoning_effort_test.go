package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anatolykoptev/go-engine/sources"
)

// reasoningCaptureServer records the reasoning_effort field from a request body.
func reasoningCaptureServer(t *testing.T, response string) (*httptest.Server, *string) {
	t.Helper()
	captured := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		if v, ok := body["reasoning_effort"]; ok {
			var s string
			_ = json.Unmarshal(v, &s)
			*captured = s
		}
		w.Header().Set("Content-Type", "application/json")
		resp := mockResponse{Choices: []mockChoice{{Message: mockMessage{Content: response}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return srv, captured
}

// TestDisableReasoning_SendsEffortUnconditionally verifies that DisableReasoning=true
// sends reasoning_effort:"none" regardless of model name — the go-kit allowlist
// (not go-engine) now controls per-endpoint gating.
//
// This test MUST FAIL if DisableReasoning guard is added back checking the model prefix.
func TestDisableReasoning_SendsEffortUnconditionally(t *testing.T) {
	const resp = `{"answer":"Paris","facts":[]}`
	srv, captured := reasoningCaptureServer(t, resp)
	defer srv.Close()

	// Model "groq-llama-70b" would NOT match "cerebras-" — the old guard would
	// have blocked sending reasoning_effort. New behavior: always send it;
	// go-kit filters per-endpoint.
	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("key"),
		WithModel("groq-llama-70b"),
		// Note: NO WithReasoningModels — it's removed
	)
	opts := SummarizeOpts{
		Query:            "capital of France?",
		TotalBudget:      500,
		CharsPerToken:    3.5,
		MaxOutputTokens:  200,
		DisableReasoning: true,
	}
	results := []sources.Result{{Title: "T", URL: "http://a.com", Content: "France capital Paris"}}
	_, err := c.SummarizeWithTier(context.Background(), opts, results, nil, rankedWeights, false)
	if err != nil {
		t.Fatalf("SummarizeWithTier: %v", err)
	}
	if *captured != "none" {
		t.Errorf("reasoning_effort = %q, want %q; DisableReasoning should send unconditionally", *captured, "none")
	}
}

// TestDisableReasoning_False_DoesNotSendEffort verifies that DisableReasoning=false
// (default) never sends reasoning_effort.
func TestDisableReasoning_False_DoesNotSendEffort(t *testing.T) {
	const resp = `{"answer":"Paris","facts":[]}`
	srv, captured := reasoningCaptureServer(t, resp)
	defer srv.Close()

	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("key"),
		WithModel("cerebras-glm-4.7"),
	)
	opts := SummarizeOpts{
		Query:            "capital of France?",
		TotalBudget:      500,
		CharsPerToken:    3.5,
		MaxOutputTokens:  200,
		DisableReasoning: false,
	}
	results := []sources.Result{{Title: "T", URL: "http://a.com", Content: "France capital Paris"}}
	_, err := c.SummarizeWithTier(context.Background(), opts, results, nil, rankedWeights, false)
	if err != nil {
		t.Fatalf("SummarizeWithTier: %v", err)
	}
	if *captured != "" {
		t.Errorf("reasoning_effort should be absent when DisableReasoning=false, got %q", *captured)
	}
}

// TestRewriteQuery_AlwaysSendsReasoningEffort verifies that RewriteQuery
// sends reasoning_effort:"none" unconditionally (no model-prefix check).
func TestRewriteQuery_AlwaysSendsReasoningEffort(t *testing.T) {
	srv, captured := reasoningCaptureServer(t, "golang http server")
	defer srv.Close()

	// Using a non-reasoning model name — old code would NOT send reasoning_effort
	// for this model. New code sends unconditionally.
	c := New(
		WithAPIBase(srv.URL),
		WithAPIKey("key"),
		WithModel("groq-llama-70b"),
	)
	got := c.RewriteQuery(context.Background(), "how to start http in go?")
	if got != "golang http server" {
		t.Errorf("got %q", got)
	}
	if *captured != "none" {
		t.Errorf("reasoning_effort = %q, want %q; RewriteQuery should send unconditionally", *captured, "none")
	}
}
