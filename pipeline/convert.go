package pipeline

import (
	"github.com/anatolykoptev/go-engine/search"
	"github.com/anatolykoptev/go-engine/sources"
)

// uniqueURLs returns deduplicated, non-empty URLs from source results.
func uniqueURLs(results []sources.Result) []string {
	seen := make(map[string]struct{}, len(results))
	urls := make([]string, 0, len(results))
	for _, r := range results {
		if r.URL == "" {
			continue
		}
		if _, ok := seen[r.URL]; ok {
			continue
		}
		seen[r.URL] = struct{}{}
		urls = append(urls, r.URL)
	}
	return urls
}

// toSearchResults converts sources.Result slice to search.Result slice
// for the LLM Summarize call.
func toSearchResults(results []sources.Result) []search.Result {
	out := make([]search.Result, len(results))
	for i, r := range results {
		out[i] = search.Result{
			Title:   r.Title,
			Content: r.Content,
			URL:     r.URL,
			Score:   r.Score,
		}
	}
	return out
}
