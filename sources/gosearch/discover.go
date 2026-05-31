package gosearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/anatolykoptev/go-engine/pipeline"
)

// DiscoverOpts configures a Discover call.
type DiscoverOpts struct {
	// Source is the profile passed to go-search's research tool (e.g. "piternow",
	// "places", "events"). An empty string lets go-search apply its default
	// query shaping.
	Source string
	// Depth controls LLM synthesis: "fast" returns catalog URLs with no LLM
	// call (~2s); any other value triggers full synthesis (~8–150s).
	// Use "fast" for discovery feeds — the caller re-LLMs each URL downstream.
	Depth string
}

// Discover calls go-search's research MCP tool and returns candidate URLs as
// []Result. Unlike Search (which calls raw_web_search), this routes through
// go-search's buildSourceQueries so source-profile shaping (catalog query
// suffixes, geo-category biases) applies.
//
// MCP tool: "research"
// Params:   query, source, depth
// Response: MCP JSON-RPC envelope → result.content[0].text → pipeline.SearchOutput
//
// The Sources array from SearchOutput is mapped to []Result. URL and Title are
// populated; Description carries the snippet when present (research/fast omits it).
// Use depth="fast" for a URL-only discovery feed with no LLM overhead (~2s vs ~150s).
func (c *Client) Discover(ctx context.Context, query string, opts DiscoverOpts) ([]Result, error) {
	if c.baseURL == "" {
		return nil, errors.New("go-search client not configured")
	}

	args := map[string]any{"query": query}
	if opts.Source != "" {
		args["source"] = opts.Source
	}
	if opts.Depth != "" {
		args["depth"] = opts.Depth
	}

	rpcReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "research",
			"arguments": args,
		},
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("discover: marshal rpc request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("discover: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := c.http.Do(req) //nolint:gosec // baseURL is server config, not user input
	if err != nil {
		c.ok.Store(false)
		return nil, fmt.Errorf("discover: go-search request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		c.ok.Store(false)
		return nil, fmt.Errorf("discover: go-search returned status %d", resp.StatusCode)
	}

	jsonData, err := extractSSEData(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("discover: parse SSE response: %w", err)
	}

	return parseDiscoverResponse(jsonData)
}

// parseDiscoverResponse extracts source URLs from a research tool JSON-RPC response.
// The research tool encodes its output as pipeline.SearchOutput in
// result.content[0].text — only Sources is consumed to avoid coupling to the
// full SearchOutput shape.
func parseDiscoverResponse(data []byte) ([]Result, error) {
	var rpcResp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &rpcResp); err != nil {
		return nil, fmt.Errorf("parse rpc response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if len(rpcResp.Result.Content) == 0 {
		return nil, errors.New("empty content in rpc response")
	}

	// research tool encodes pipeline.SearchOutput JSON in content[0].text.
	var out pipeline.SearchOutput
	if err := json.Unmarshal([]byte(rpcResp.Result.Content[0].Text), &out); err != nil {
		return nil, fmt.Errorf("parse search output: %w", err)
	}

	if len(out.Sources) == 0 {
		return nil, nil
	}

	results := make([]Result, 0, len(out.Sources))
	for _, s := range out.Sources {
		if s.URL == "" {
			continue
		}
		results = append(results, Result{
			URL:         s.URL,
			Title:       s.Title,
			Description: s.Snippet,
		})
	}
	return results, nil
}
