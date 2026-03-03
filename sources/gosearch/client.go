// Package gosearch provides an MCP client for go-search's searxng_web_search tool.
// Communicates via MCP Streamable HTTP (JSON-RPC over SSE).
package gosearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	maxResponseBytes = 2 * 1024 * 1024
	healthTimeout    = 3 * time.Second
)

// Client calls go-search MCP tools over HTTP.
type Client struct {
	baseURL string
	http    *http.Client
	ok      atomic.Bool
}

// Result is a single search result from go-search.
type Result struct {
	URL         string  `json:"url"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Score       float64 `json:"score"`
}

// NewClient creates a new go-search MCP client.
// If baseURL is empty, the client is disabled (Available returns false).
func NewClient(baseURL string, httpClient *http.Client) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    httpClient,
	}
	if baseURL == "" {
		return c
	}
	if httpClient == nil {
		c.http = &http.Client{Timeout: 30 * time.Second} //nolint:mnd
	}
	return c
}

// Available reports whether go-search was reachable at last health check.
func (c *Client) Available() bool {
	return c.baseURL != "" && c.ok.Load()
}

// CheckHealth probes the go-search /health endpoint and updates availability.
func (c *Client) CheckHealth(ctx context.Context) bool {
	if c.baseURL == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		c.ok.Store(false)
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		slog.Warn("go-search health check failed", slog.Any("error", err))
		c.ok.Store(false)
		return false
	}
	defer resp.Body.Close() //nolint:errcheck
	reachable := resp.StatusCode == http.StatusOK
	c.ok.Store(reachable)
	return reachable
}

// Search calls go-search's searxng_web_search tool via MCP JSON-RPC.
func (c *Client) Search(ctx context.Context, query, timeRange string) ([]Result, error) {
	if c.baseURL == "" {
		return nil, errors.New("go-search client not configured")
	}

	args := map[string]any{"query": query}
	if timeRange != "" {
		args["time_range"] = timeRange
	}

	rpcReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "searxng_web_search",
			"arguments": args,
		},
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal rpc request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		c.ok.Store(false)
		return nil, fmt.Errorf("go-search request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		c.ok.Store(false)
		return nil, fmt.Errorf("go-search returned status %d", resp.StatusCode)
	}

	jsonData, err := extractSSEData(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse SSE response: %w", err)
	}

	return parseRPCResponse(jsonData)
}
