package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
)

const yepEndpoint = "https://api.yep.com/fs/2/search"

// Yep searches Yep.com (Ahrefs) via its public JSON API.
// No API key required. Own independent index.
type Yep struct {
	httpClient *http.Client
}

// YepOption configures Yep.
type YepOption func(*Yep)

// WithYepHTTPClient sets the HTTP client (for proxy support).
func WithYepHTTPClient(c *http.Client) YepOption {
	return func(y *Yep) { y.httpClient = c }
}

// NewYep creates a Yep search client.
func NewYep(opts ...YepOption) *Yep {
	y := &Yep{httpClient: http.DefaultClient}
	for _, o := range opts {
		o(y)
	}
	return y
}

// Search implements Provider. Queries Yep via JSON API.
func (y *Yep) Search(ctx context.Context, query string, opts SearchOpts) ([]Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	args := url.Values{
		"client":     {"web"},
		"gl":         {"us"},
		"no_correct": {"false"},
		"q":          {query},
		"safeSearch": {"off"},
		"type":       {"web"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, yepEndpoint+"?"+args.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("yep: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://yep.com/")

	resp, err := y.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yep request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yep status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) //nolint:mnd
	if err != nil {
		return nil, fmt.Errorf("yep read body: %w", err)
	}

	results, err := ParseYepJSON(data)
	if err != nil {
		return nil, fmt.Errorf("yep parse: %w", err)
	}

	slog.Debug("yep results", slog.Int("count", len(results)))
	return applyLimit(results, opts.Limit), nil
}

// yepResponse is the top-level JSON response: ["Ok", {results: [...]}]
type yepResponse struct {
	Results []yepResult `json:"results"`
	Total   int         `json:"total"`
}

type yepResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	Type    string `json:"type"` // "Organic", "Alt_search_engine"
}

// ParseYepJSON parses the Yep API response.
// Format: ["Ok", {"results": [...], "total": N}]
func ParseYepJSON(data []byte) ([]Result, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	if len(raw) < 2 {
		return nil, fmt.Errorf("unexpected response length %d", len(raw))
	}

	var status string
	if err := json.Unmarshal(raw[0], &status); err != nil || status != "Ok" {
		return nil, fmt.Errorf("yep status: %s", string(raw[0]))
	}

	var body yepResponse
	if err := json.Unmarshal(raw[1], &body); err != nil {
		return nil, fmt.Errorf("unmarshal body: %w", err)
	}

	results := make([]Result, 0, len(body.Results))
	for _, r := range body.Results {
		if r.Type != "Organic" || r.URL == "" || r.Title == "" {
			continue
		}
		results = append(results, Result{
			Title:    r.Title,
			URL:      r.URL,
			Content:  r.Snippet,
			Score:    directResultScore,
			Metadata: map[string]string{"engine": "yep"},
		})
	}
	return results, nil
}
