package extract

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

const sampleHTML = `<!DOCTYPE html>
<html>
<head><title>Test Article</title></head>
<body>
<header><nav>Navigation</nav></header>
<main>
<article>
<h1>Test Article Title</h1>
<p>This is the first paragraph of the article with meaningful content.</p>
<p>This is the second paragraph with more details about the topic.</p>
<p>The third paragraph concludes the article with a summary.</p>
</article>
</main>
<footer>Footer content</footer>
<script>var x = 1;</script>
<style>.hidden { display: none; }</style>
</body>
</html>`

const minimalHTML = `<html><head><title>Minimal</title></head><body><p>Hello World</p></body></html>`

const noArticleHTML = `<html>
<head>
<title>No Article</title>
<meta property="og:title" content="OG Title Here">
</head>
<body>
<script>alert('xss')</script>
<style>body { color: red; }</style>
<nav>Menu items</nav>
<p>Main body text here.</p>
<footer>Footer</footer>
</body>
</html>`

func TestExtractor_Extract_Trafilatura(t *testing.T) {
	ext := New()
	pageURL, _ := url.Parse("https://example.com/article")

	result, err := ext.Extract(context.Background(), []byte(sampleHTML), pageURL)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if result.Title == "" {
		t.Error("expected non-empty title")
	}
	if result.Content == "" {
		t.Error("expected non-empty content")
	}
	// Content should contain article text.
	if !strings.Contains(result.Content, "paragraph") {
		t.Errorf("content missing article text: %q", result.Content[:min(200, len(result.Content))])
	}
	// Should NOT contain script/style content.
	if strings.Contains(result.Content, "var x") {
		t.Error("content should not contain script")
	}
}

func TestExtractor_Extract_MinimalHTML(t *testing.T) {
	ext := New()
	result, err := ext.Extract(context.Background(), []byte(minimalHTML), nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(result.Content, "Hello World") {
		t.Errorf("content should contain 'Hello World', got: %q", result.Content)
	}
}

func TestExtractor_Extract_MaxContentLen(t *testing.T) {
	ext := New(WithMaxContentLen(20))
	result, err := ext.Extract(context.Background(), []byte(sampleHTML), nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(result.Content) > 23 { // 20 + "..."
		t.Errorf("content length %d exceeds max 23 (20 + ...)", len(result.Content))
	}
	if !strings.HasSuffix(result.Content, "...") {
		t.Errorf("truncated content should end with '...', got: %q", result.Content)
	}
}

func TestExtractor_GoquerySetsTitle(t *testing.T) {
	ext := New()
	// Use HTML that trafilatura will likely fall back on.
	result, err := ext.extractGoquery([]byte(noArticleHTML))
	if err != nil {
		t.Fatalf("extractGoquery: %v", err)
	}
	if result.Title == "" {
		t.Error("expected non-empty title from goquery")
	}
}

func TestExtractor_GoquerySetsOGTitle(t *testing.T) {
	ext := New()
	html := `<html><head><meta property="og:title" content="OG Title"></head><body><p>text</p></body></html>`
	result, err := ext.extractGoquery([]byte(html))
	if err != nil {
		t.Fatalf("extractGoquery: %v", err)
	}
	if result.Title != "OG Title" {
		t.Errorf("title = %q, want %q", result.Title, "OG Title")
	}
}

func TestExtractor_GoqueryStripsBoilerplate(t *testing.T) {
	ext := New()
	result, err := ext.extractGoquery([]byte(noArticleHTML))
	if err != nil {
		t.Fatalf("extractGoquery: %v", err)
	}
	if strings.Contains(result.Content, "Menu items") {
		t.Error("nav content should be stripped")
	}
	if strings.Contains(result.Content, "Footer") {
		t.Error("footer content should be stripped")
	}
	if strings.Contains(result.Content, "alert") {
		t.Error("script content should be stripped")
	}
}

func TestExtractor_RegexExtractsTitle(t *testing.T) {
	ext := New()
	result, err := ext.extractRegex([]byte(`<html><title>Regex Title</title><body>text</body></html>`))
	if err != nil {
		t.Fatalf("extractRegex: %v", err)
	}
	if result.Title != "Regex Title" {
		t.Errorf("title = %q, want %q", result.Title, "Regex Title")
	}
}

func TestExtractor_RegexExtractsOGTitle(t *testing.T) {
	ext := New()
	html := `<html><head><meta property="og:title" content="My OG Title"></head><body>text</body></html>`
	result, err := ext.extractRegex([]byte(html))
	if err != nil {
		t.Fatalf("extractRegex: %v", err)
	}
	if result.Title != "My OG Title" {
		t.Errorf("title = %q, want %q", result.Title, "My OG Title")
	}
}

func TestExtractor_RegexStripsScriptStyle(t *testing.T) {
	ext := New()
	html := `<html><body><script>var x=1;</script><style>.a{}</style><p>Visible text</p></body></html>`
	result, err := ext.extractRegex([]byte(html))
	if err != nil {
		t.Fatalf("extractRegex: %v", err)
	}
	if strings.Contains(result.Content, "var x") {
		t.Error("script content should be stripped")
	}
	if strings.Contains(result.Content, ".a{}") {
		t.Error("style content should be stripped")
	}
	if !strings.Contains(result.Content, "Visible text") {
		t.Error("visible text should be preserved")
	}
}

func TestExtractor_EmptyBody(t *testing.T) {
	ext := New()
	result, err := ext.Extract(context.Background(), []byte(""), nil)
	if err != nil {
		t.Fatalf("Extract empty: %v", err)
	}
	// Should return empty result, not error.
	_ = result
}

func TestExtractor_InvalidHTML(t *testing.T) {
	ext := New()
	result, err := ext.Extract(context.Background(), []byte("not html at all, just text"), nil)
	if err != nil {
		t.Fatalf("Extract invalid: %v", err)
	}
	// Regex fallback should extract plain text.
	if !strings.Contains(result.Content, "not html at all") {
		t.Errorf("expected plain text in content, got: %q", result.Content)
	}
}

const hiddenElementsHTML = `<html>
<head><title>Hidden Test</title></head>
<body>
<div aria-hidden="true">Screen reader hidden</div>
<div hidden>HTML5 hidden</div>
<div class="cookie-banner">Accept cookies</div>
<div class="social-share">Share this</div>
<p>Visible content here</p>
</body>
</html>`

func TestExtractor_GoqueryStripsHiddenElements(t *testing.T) {
	ext := New()
	result, err := ext.extractGoquery([]byte(hiddenElementsHTML))
	if err != nil {
		t.Fatalf("extractGoquery: %v", err)
	}
	for _, unwanted := range []string{"Screen reader hidden", "HTML5 hidden", "cookie", "Share this"} {
		if strings.Contains(result.Content, unwanted) {
			t.Errorf("content should not contain %q", unwanted)
		}
	}
	if !strings.Contains(result.Content, "Visible content") {
		t.Error("visible content should be preserved")
	}
}

func TestExtractor_FormatTextDefault(t *testing.T) {
	ext := New() // default = FormatText
	result, err := ext.Extract(context.Background(), []byte(sampleHTML), nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if result.Format != FormatText {
		t.Errorf("format = %q, want %q", result.Format, FormatText)
	}
}

func TestExtractor_FormatMarkdown(t *testing.T) {
	ext := New(WithFormat(FormatMarkdown))
	result, err := ext.Extract(context.Background(), []byte(sampleHTML), nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if result.Content == "" {
		t.Error("expected non-empty markdown content")
	}
	if result.Format != FormatMarkdown {
		t.Errorf("format = %q, want %q", result.Format, FormatMarkdown)
	}
}

func TestExtractor_FormatHTML(t *testing.T) {
	ext := New(WithFormat(FormatHTML))
	result, err := ext.extractGoquery([]byte(sampleHTML))
	if err != nil {
		t.Fatalf("extractGoquery: %v", err)
	}
	// Should contain HTML tags.
	if !strings.Contains(result.Content, "<") {
		t.Error("HTML format should contain HTML tags")
	}
	if result.Format != FormatHTML {
		t.Errorf("format = %q, want %q", result.Format, FormatHTML)
	}
}

func TestExtractor_GoqueryMarkdown(t *testing.T) {
	ext := New(WithFormat(FormatMarkdown))
	html := `<html><body><h1>Title</h1><p>Paragraph with <a href="https://example.com">a link</a>.</p></body></html>`
	result, err := ext.extractGoquery([]byte(html))
	if err != nil {
		t.Fatalf("extractGoquery: %v", err)
	}
	// Markdown should contain link syntax.
	if !strings.Contains(result.Content, "[a link]") || !strings.Contains(result.Content, "(https://example.com)") {
		t.Errorf("markdown should contain link: %q", result.Content)
	}
}

const attrHTML = `<html>
<head><title>Attr Test</title></head>
<body>
<p style="color:red" class="intro" data-track="click" onclick="alert(1)">
  Visit <a href="https://example.com" class="link" style="font-weight:bold">Example</a>
</p>
</body>
</html>`

func TestExtractor_GoqueryStripsAttributes(t *testing.T) {
	ext := New(WithFormat(FormatHTML))
	result, err := ext.extractGoquery([]byte(attrHTML))
	if err != nil {
		t.Fatalf("extractGoquery: %v", err)
	}
	if !strings.Contains(result.Content, `href="https://example.com"`) {
		t.Error("href should be preserved in HTML output")
	}
	for _, attr := range []string{"style=", "class=", "data-track", "onclick"} {
		if strings.Contains(result.Content, attr) {
			t.Errorf("attribute %q should be stripped", attr)
		}
	}
}

func TestExtractor_ImplementsStrategy(t *testing.T) {
	var _ Strategy = New()
}
