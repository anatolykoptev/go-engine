package websearch

import (
	"errors"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestParseBingHTML(t *testing.T) {
	html := `<html><body>
		<ol id="b_results">
			<li class="b_algo">
				<h2><a href="https://www.bing.com/ck/a?u=a1aHR0cHM6Ly9leGFtcGxlLmNvbS9nb2xhbmctY29udGV4dA">Golang Context Guide</a></h2>
				<div class="b_caption"><p class="b_lineclamp2">A complete guide to context in Go.</p></div>
			</li>
			<li class="b_algo">
				<h2><a href="https://pkg.go.dev/context">context package - Go Packages</a></h2>
				<div class="b_caption"><p class="b_lineclamp2">Package context defines the Context type.</p></div>
			</li>
			<li class="b_algo">
				<h2><a href="https://www.bing.com/ck/a?u=a1aHR0cHM6Ly9yZWRkaXQuY29tL3IvZ29sYW5n">Context API best practices : r/golang</a></h2>
				<div class="b_caption"><p class="b_lineclamp3">Discussion about context usage.</p></div>
			</li>
		</ol>
	</body></html>`

	results, err := ParseBingHTML([]byte(html))
	if err != nil {
		t.Fatalf("ParseBingHTML: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}

	// First result: Bing redirect URL should be unwrapped.
	if results[0].Title != "Golang Context Guide" {
		t.Errorf("results[0].Title = %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/golang-context" {
		t.Errorf("results[0].URL = %q, want unwrapped URL", results[0].URL)
	}
	if results[0].Content != "A complete guide to context in Go." {
		t.Errorf("results[0].Content = %q", results[0].Content)
	}

	// Second result: direct URL (no bing redirect).
	if results[1].URL != "https://pkg.go.dev/context" {
		t.Errorf("results[1].URL = %q", results[1].URL)
	}

	// Third result: another Bing redirect.
	if results[2].URL != "https://reddit.com/r/golang" {
		t.Errorf("results[2].URL = %q, want unwrapped reddit URL", results[2].URL)
	}

	for i, r := range results {
		if r.Score != directResultScore {
			t.Errorf("results[%d].Score = %f, want %f", i, r.Score, directResultScore)
		}
		if r.Metadata["engine"] != "bing" {
			t.Errorf("results[%d].Metadata[engine] = %q, want bing", i, r.Metadata["engine"])
		}
	}
}

func TestBingUnwrapURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bing redirect with a1 prefix",
			in:   "https://www.bing.com/ck/a?!&&p=abc&u=a1aHR0cHM6Ly9leGFtcGxlLmNvbQ&ntb=1",
			want: "https://example.com",
		},
		{
			name: "direct URL passthrough",
			in:   "https://pkg.go.dev/context",
			want: "https://pkg.go.dev/context",
		},
		{
			name: "bing redirect with path",
			in:   "https://www.bing.com/ck/a?u=a1aHR0cHM6Ly9tZWRpdW0uY29tL0BhdXRob3IvYXJ0aWNsZQ",
			want: "https://medium.com/@author/article",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bingUnwrapURL(tt.in)
			if got != tt.want {
				t.Errorf("bingUnwrapURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBing_RateLimitStatus(t *testing.T) {
	bc := &mockBrowser{fn: func(_, _ string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		return []byte("error"), nil, http.StatusTooManyRequests, nil
	}}

	b := NewBing(WithBingBrowser(bc))
	_, err := b.Search(context.Background(), "test", SearchOpts{})
	if err == nil {
		t.Fatal("expected error on 429 status")
	}
	rl := &ErrRateLimited{}
	ok := errors.As(err, &rl)
	if !ok {
		t.Fatalf("expected ErrRateLimited, got %T: %v", err, err)
	}
	if rl.Engine != "bing" {
		t.Errorf("Engine = %q, want bing", rl.Engine)
	}
}

func TestBing_NoBrowser(t *testing.T) {
	b := NewBing()
	_, err := b.Search(context.Background(), "test", SearchOpts{})
	if err == nil {
		t.Fatal("expected error when no BrowserDoer provided")
	}
}

func TestBing_Search_Mock(t *testing.T) {
	html := `<html><body>
		<ol id="b_results">
			<li class="b_algo">
				<h2><a href="https://example.com/go">Go Programming</a></h2>
				<div class="b_caption"><p class="b_lineclamp2">Go is great.</p></div>
			</li>
		</ol>
	</body></html>`

	bc := &mockBrowser{fn: func(method, u string, _ map[string]string, _ io.Reader) ([]byte, map[string]string, int, error) {
		if method != http.MethodGet {
			t.Errorf("method = %q, want GET", method)
		}
		return []byte(html), nil, http.StatusOK, nil
	}}

	b := NewBing(WithBingBrowser(bc))
	results, err := b.Search(context.Background(), "golang", SearchOpts{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Title != "Go Programming" {
		t.Errorf("Title = %q", results[0].Title)
	}
	if results[0].URL != "https://example.com/go" {
		t.Errorf("URL = %q", results[0].URL)
	}
}

// TestParseBingHTML_TilkLayout verifies the new Bing SERP layout returned by
// ox-browser /fetch, where result links use a.tilk with href (Bing redirect)
// and cite tags for display URLs, instead of the classic h2 > a pattern.
//
// Falsification: remove the a.tilk fallback from ParseBingHTML → this test
// goes RED (0 results parsed from tilk layout).
func TestParseBingHTML_TilkLayout(t *testing.T) {
	html := `<html><body>
		<ol id="b_results">
			<li class="b_algo">
				<div class="b_tpcn"><a class="tilk" aria-label="rust-lang.org" href="https://www.bing.com/ck/a?!&&p=abc&u=a1aHR0cHM6Ly9ydXN0LWxhbmcub3JnLw&ntb=1"></a></div>
				<div class="b_caption"><cite>https://rust-lang.org</cite><p class="b_lineclamp2">Rust programming language official site.</p></div>
			</li>
			<li class="b_algo">
				<div class="b_tpcn"><a class="tilk" aria-label="doc.rust-lang.org" href="https://www.bing.com/ck/a?u=a1aHR0cHM6Ly9kb2MucnVzdC1sYW5nLm9yZy9ib29r"></a></div>
				<div class="b_caption"><cite>https://doc.rust-lang.org</cite><p>The Rust book.</p></div>
			</li>
		</ol>
	</body></html>`

	results, err := ParseBingHTML([]byte(html))
	if err != nil {
		t.Fatalf("ParseBingHTML: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (tilk layout)", len(results))
	}

	// First result: tilk href is a Bing redirect → unwrapped to rust-lang.org
	if results[0].URL != "https://rust-lang.org/" {
		t.Errorf("results[0].URL = %q, want https://rust-lang.org/", results[0].URL)
	}

	// Second result: tilk href → unwrapped to doc.rust-lang.org/book
	if results[1].URL != "https://doc.rust-lang.org/book" {
		t.Errorf("results[1].URL = %q, want https://doc.rust-lang.org/book", results[1].URL)
	}
}

// TestParseBingHTML_CiteOnlyLayout verifies the fallback where only cite tags
// are available (no h2 > a, no a.tilk href) — ParseBingHTML should extract
// the URL from cite text.
func TestParseBingHTML_CiteOnlyLayout(t *testing.T) {
	html := `<html><body>
		<ol id="b_results">
			<li class="b_algo">
				<div class="b_caption"><cite>github.com/rust-lang/rust</cite><p>Rust on GitHub.</p></div>
			</li>
		</ol>
	</body></html>`

	results, err := ParseBingHTML([]byte(html))
	if err != nil {
		t.Fatalf("ParseBingHTML: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (cite-only fallback)", len(results))
	}
	if results[0].URL != "https://github.com/rust-lang/rust" {
		t.Errorf("results[0].URL = %q, want https://github.com/rust-lang/rust", results[0].URL)
	}
}
