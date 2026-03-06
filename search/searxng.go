package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/anatolykoptev/go-engine/fetch"
	"github.com/anatolykoptev/go-engine/metrics"
	"github.com/anatolykoptev/go-engine/sources"
)

const (
	metricSearchRequests = "search_requests"
	languageAll          = "all"
)

// searxngResult is a raw SearXNG result with flexible Metadata type.
// SearXNG may return metadata as either a map or an empty string.
type searxngResult struct {
	Title    string          `json:"title"`
	URL      string          `json:"url"`
	Content  string          `json:"content"`
	Score    float64         `json:"score"`
	Engines  []string        `json:"engines"`
	Metadata json.RawMessage `json:"metadata"`
}

// searxngResponse is the JSON response from SearXNG API.
type searxngResponse struct {
	Results []searxngResult `json:"results"`
}

// toResults converts raw SearXNG results to sources.Result,
// gracefully handling metadata that may be a string or map.
func (r *searxngResponse) toResults() []sources.Result {
	out := make([]sources.Result, len(r.Results))
	for i, sr := range r.Results {
		out[i] = sources.Result{
			Title:   sr.Title,
			URL:     sr.URL,
			Content: sr.Content,
			Score:   sr.Score,
		}
		if len(sr.Metadata) > 0 && sr.Metadata[0] == '{' {
			_ = json.Unmarshal(sr.Metadata, &out[i].Metadata)
		}
	}
	return out
}

// SearXNG queries a local SearXNG instance for search results.
type SearXNG struct {
	baseURL    string
	httpClient *http.Client
	metrics    *metrics.Registry
}

// SearXNGOption configures a SearXNG client.
type SearXNGOption func(*SearXNG)

// WithHTTPClient sets the HTTP client for SearXNG requests.
func WithHTTPClient(c *http.Client) SearXNGOption {
	return func(s *SearXNG) { s.httpClient = c }
}

// WithMetrics sets the metrics registry for tracking request counts.
func WithMetrics(m *metrics.Registry) SearXNGOption {
	return func(s *SearXNG) { s.metrics = m }
}

// NewSearXNG creates a SearXNG client.
func NewSearXNG(baseURL string, opts ...SearXNGOption) *SearXNG {
	s := &SearXNG{
		baseURL:    baseURL,
		httpClient: http.DefaultClient,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SearchQuery queries SearXNG using a sources.Query.
// Reads Extra["categories"] and Extra["engines"] if set.
func (s *SearXNG) SearchQuery(ctx context.Context, q sources.Query) ([]sources.Result, error) {
	engines := q.Extra["engines"]
	categories := q.Extra["categories"]

	u, err := url.Parse(s.baseURL + "/search")
	if err != nil {
		return nil, err
	}
	params := u.Query()
	params.Set("q", q.Text)
	params.Set("format", "json")
	if q.Language != "" && q.Language != languageAll {
		params.Set("language", q.Language)
	}
	if q.TimeRange != "" {
		params.Set("time_range", q.TimeRange)
	}
	if engines != "" {
		params.Set("engines", engines)
	}
	if categories != "" {
		params.Set("categories", categories)
	}
	u.RawQuery = params.Encode()

	if s.metrics != nil {
		s.metrics.Incr(metricSearchRequests)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// SearXNG botdetection requires X-Forwarded-For to identify the client IP.
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	resp, err := fetch.RetryHTTP(ctx, fetch.DefaultRetryConfig, func() (*http.Response, error) {
		return s.httpClient.Do(req) //nolint:bodyclose,gosec // closed below; URL is caller-provided
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return data.toResults(), nil
}

// Search queries SearXNG and returns results.
func (s *SearXNG) Search(ctx context.Context, query, language, timeRange, engines string) ([]sources.Result, error) {
	u, err := url.Parse(s.baseURL + "/search")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	if language != "" && language != languageAll {
		q.Set("language", language)
	}
	if timeRange != "" {
		q.Set("time_range", timeRange)
	}
	if engines != "" {
		q.Set("engines", engines)
	}
	u.RawQuery = q.Encode()

	if s.metrics != nil {
		s.metrics.Incr(metricSearchRequests)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// SearXNG botdetection requires X-Forwarded-For to identify the client IP.
	req.Header.Set("X-Forwarded-For", "127.0.0.1")

	resp, err := fetch.RetryHTTP(ctx, fetch.DefaultRetryConfig, func() (*http.Response, error) {
		return s.httpClient.Do(req) //nolint:bodyclose,gosec // closed below; URL is caller-provided
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var data searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	return data.toResults(), nil
}
