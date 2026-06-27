package websearch

import (
	"testing"
)

// TestDDGHTMLURL verifies that DDGHTMLURL returns the correct GET URL for the
// DuckDuckGo HTML SERP endpoint. P2 passes this URL to ox-browser /fetch so
// that the SERP request shape stays single-owned in websearch.
func TestDDGHTMLURL(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "simple query",
			query: "hello world",
			want:  "https://html.duckduckgo.com/html/?q=hello+world",
		},
		{
			name:  "special characters",
			query: "golang & rust",
			want:  "https://html.duckduckgo.com/html/?q=golang+%26+rust",
		},
		{
			name:  "empty query",
			query: "",
			want:  "https://html.duckduckgo.com/html/?q=",
		},
		{
			name:  "url meta-chars",
			query: "site:example.com foo=bar",
			want:  "https://html.duckduckgo.com/html/?q=site%3Aexample.com+foo%3Dbar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DDGHTMLURL(tt.query)
			if got != tt.want {
				t.Errorf("DDGHTMLURL(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

// TestBraveSearchURL verifies that BraveSearchURL returns the correct GET URL
// for the Brave Search HTML SERP endpoint. P2 passes this URL to ox-browser
// /fetch so that the SERP request shape stays single-owned in websearch.
func TestBraveSearchURL(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "simple query",
			query: "hello world",
			want:  "https://search.brave.com/search?q=hello+world&source=web",
		},
		{
			name:  "special characters",
			query: "golang & rust",
			want:  "https://search.brave.com/search?q=golang+%26+rust&source=web",
		},
		{
			name:  "empty query",
			query: "",
			want:  "https://search.brave.com/search?q=&source=web",
		},
		{
			name:  "url meta-chars",
			query: "site:example.com foo=bar",
			want:  "https://search.brave.com/search?q=site%3Aexample.com+foo%3Dbar&source=web",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BraveSearchURL(tt.query)
			if got != tt.want {
				t.Errorf("BraveSearchURL(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}
