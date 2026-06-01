package gosearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rawSearchFixtureSSE wraps a raw_web_search tool response (results array) in an
// MCP Streamable-HTTP SSE frame: "event: message\ndata: {json}\n\n".
func rawSearchFixtureSSE(results []Result) string {
	inner, _ := json.Marshal(map[string]any{"results": results})
	outer, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": string(inner)},
			},
		},
	})
	return "event: message\ndata: " + string(outer) + "\n\n"
}

// TestSearch_UsesRawWebSearchToolAndBothAcceptTypes guards the regression where
// Client.Search called the renamed-away "searxng_web_search" tool (go-search
// renamed it to "raw_web_search") and sent only "text/event-stream" in the
// Accept header, which the MCP Streamable-HTTP transport rejects with HTTP 400.
func TestSearch_UsesRawWebSearchToolAndBothAcceptTypes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, "application/json") || !strings.Contains(accept, "text/event-stream") {
			http.Error(w, "Accept must contain both application/json and text/event-stream", http.StatusBadRequest)
			return
		}
		var rpc struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if rpc.Params.Name != "raw_web_search" {
			http.Error(w, "tool must be raw_web_search, got "+rpc.Params.Name, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, rawSearchFixtureSSE([]Result{
			{URL: "https://example.com", Title: "Example", Description: "snippet", Score: 1.0},
		}))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	c.ok.Store(true)

	results, err := c.Search(context.Background(), "test query", "")
	if err != nil {
		t.Fatalf("Search must call raw_web_search with both Accept types and succeed; got: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://example.com" {
		t.Fatalf("unexpected results: %+v", results)
	}
}
