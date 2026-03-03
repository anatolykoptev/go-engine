package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anatolykoptev/go-engine/extract"
	"github.com/anatolykoptev/go-engine/llm"
	"github.com/anatolykoptev/go-engine/sources"
	"github.com/anatolykoptev/go-engine/text"
)

// mockSource implements sources.Source for testing.
type mockSource struct {
	name    string
	results []sources.Result
	err     error
}

func (m *mockSource) Name() string { return m.name }
func (m *mockSource) Search(_ context.Context, _ sources.Query) ([]sources.Result, error) {
	return m.results, m.err
}

// newLLMMockServer returns a test server that responds with a canned LLM JSON response.
func newLLMMockServer(t *testing.T) *httptest.Server {
	t.Helper()
	inner := `{"answer":"Test answer.","facts":[{"point":"Test fact","sources":[1]}]}`
	body := fmt.Sprintf(`{"choices":[{"message":{"content":%s}}]}`, mustMarshalString(inner))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func mustMarshalString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// stdFetchFn is the test fetch function that performs a real HTTP GET.
func stdFetchFn(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, nil
}

func TestPipeline_EndToEnd(t *testing.T) {
	// Content server: serves simple HTML.
	contentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p>Hello world content for testing.</p></body></html>`))
	}))
	defer contentSrv.Close()

	llmSrv := newLLMMockServer(t)
	defer llmSrv.Close()

	src := &mockSource{
		name: "test",
		results: []sources.Result{
			{Title: "Test Result", URL: contentSrv.URL, Content: "Hello world", Score: 1.0},
		},
	}

	ext := extract.New()
	llmClient := llm.New(
		llm.WithAPIBase(llmSrv.URL+"/v1"),
		llm.WithAPIKey("test-key"),
		llm.WithModel("test-model"),
	)

	p := NewPipeline(
		WithSources(src),
		WithFetchFunc(stdFetchFn),
		WithExtractor(ext),
		WithLLMClient(llmClient),
	)

	out, err := p.Run(context.Background(), "test query")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if out.Query != "test query" {
		t.Errorf("Query = %q, want %q", out.Query, "test query")
	}
	if out.Answer != "Test answer." {
		t.Errorf("Answer = %q, want %q", out.Answer, "Test answer.")
	}
	if len(out.Sources) == 0 {
		t.Error("expected at least one source")
	}
}

func TestPipeline_WithChunkerAndFilter(t *testing.T) {
	contentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Long enough content to produce multiple chunks with size=200, overlap=20.
		long := strings.Repeat("This is a longer sentence about Go programming and testing pipelines. ", 10)
		fmt.Fprintf(w, `<html><body><p>%s</p></body></html>`, long)
	}))
	defer contentSrv.Close()

	llmSrv := newLLMMockServer(t)
	defer llmSrv.Close()

	src := &mockSource{
		name: "test",
		results: []sources.Result{
			{Title: "Go Pipeline", URL: contentSrv.URL, Content: "Go testing"},
		},
	}

	ext := extract.New()
	llmClient := llm.New(
		llm.WithAPIBase(llmSrv.URL+"/v1"),
		llm.WithAPIKey("test-key"),
		llm.WithModel("test-model"),
	)

	p := NewPipeline(
		WithSources(src),
		WithFetchFunc(stdFetchFn),
		WithExtractor(ext),
		WithChunker(text.NewCharacterChunker(200, 20)),
		WithFilter(text.NewBM25Filter(3)),
		WithLLMClient(llmClient),
	)

	out, err := p.Run(context.Background(), "Go testing pipelines")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Answer == "" {
		t.Error("expected non-empty answer")
	}
	if len(out.Sources) == 0 {
		t.Error("expected at least one source")
	}
}

func TestPipeline_SourceError(t *testing.T) {
	llmSrv := newLLMMockServer(t)
	defer llmSrv.Close()

	contentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><p>content</p></body></html>`))
	}))
	defer contentSrv.Close()

	failSrc := &mockSource{name: "fail", err: errors.New("source unavailable")}
	okSrc := &mockSource{
		name: "ok",
		results: []sources.Result{
			{Title: "OK Result", URL: contentSrv.URL, Content: "content"},
		},
	}

	llmClient := llm.New(
		llm.WithAPIBase(llmSrv.URL+"/v1"),
		llm.WithAPIKey("test-key"),
		llm.WithModel("test-model"),
	)

	p := NewPipeline(
		WithSources(failSrc, okSrc),
		WithFetchFunc(stdFetchFn),
		WithExtractor(extract.New()),
		WithLLMClient(llmClient),
	)

	// Should not panic; partial source failure is not fatal.
	out, err := p.Run(context.Background(), "query")
	if err != nil {
		t.Fatalf("Run() should not fail on source error, got %v", err)
	}
	// We should still get results from the OK source.
	if len(out.Sources) == 0 {
		t.Error("expected sources from the ok source")
	}
}

func TestPipeline_EmptyResults(t *testing.T) {
	src := &mockSource{name: "empty", results: nil}

	p := NewPipeline(
		WithSources(src),
	)

	out, err := p.Run(context.Background(), "query")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.Query != "query" {
		t.Errorf("Query = %q", out.Query)
	}
	if out.Answer != "" {
		t.Errorf("Answer should be empty, got %q", out.Answer)
	}
	if len(out.Sources) != 0 {
		t.Errorf("Sources should be empty, got %d", len(out.Sources))
	}
}

func TestPipeline_ExtractorWithLLMFallback(t *testing.T) {
	var fallbackCalled bool
	fallbackFn := func(_ context.Context, _ string) (string, error) {
		fallbackCalled = true
		return "LLM extracted content from pipeline", nil
	}

	// Content server serves thin HTML (below minExtractChars threshold).
	contentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><p>Hi</p></body></html>`))
	}))
	defer contentSrv.Close()

	src := &mockSource{
		name: "test",
		results: []sources.Result{
			{Title: "Thin Page", URL: contentSrv.URL, Content: "Hi"},
		},
	}

	ext := extract.New(
		extract.WithLLMFallback(fallbackFn),
		extract.WithMinExtractChars(1000), // force fallback
	)

	p := NewPipeline(
		WithSources(src),
		WithFetchFunc(stdFetchFn),
		WithExtractor(ext),
	)

	out, err := p.Run(context.Background(), "test query")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !fallbackCalled {
		t.Error("LLM fallback should have been called through pipeline")
	}
	_ = out
}

func TestNewPipeline_Defaults(t *testing.T) {
	p := NewPipeline()
	if p.maxConc != defaultMaxConcurrency {
		t.Errorf("maxConc = %d, want %d", p.maxConc, defaultMaxConcurrency)
	}
}
