package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anatolykoptev/go-engine/extract"
	"github.com/anatolykoptev/go-engine/llm"
	"github.com/anatolykoptev/go-engine/sources"
	"github.com/anatolykoptev/go-engine/text"
)

// --- Hard Red: Pipeline.Run concurrent calls ---

func TestHR_Pipeline_ConcurrentRun(t *testing.T) {
	contentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><p>Content here.</p></body></html>`))
	}))
	defer contentSrv.Close()

	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		inner := `{"answer":"ok","facts":[]}`
		b, _ := json.Marshal(inner)
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s}}]}`, string(b))
	}))
	defer llmSrv.Close()

	src := &mockSource{
		name:    "test",
		results: []sources.Result{{Title: "Test", URL: contentSrv.URL, Content: "snippet"}},
	}

	p := NewPipeline(
		WithSources(src),
		WithFetchFunc(stdFetchFn),
		WithExtractor(extract.New()),
		WithChunker(text.NewCharacterChunker(100, 10)),
		WithFilter(text.NewBM25Filter(3)),
		WithLLMClient(llm.New(llm.WithAPIBase(llmSrv.URL+"/v1"), llm.WithAPIKey("test"))),
	)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := p.Run(context.Background(), "test query")
			if err != nil {
				t.Errorf("Run: %v", err)
				return
			}
			if out.Query != "test query" {
				t.Errorf("query = %q", out.Query)
			}
		}()
	}
	wg.Wait()
}

// --- Hard Red: Pipeline with no components ---

func TestHR_Pipeline_NoComponents(t *testing.T) {
	p := NewPipeline()
	out, err := p.Run(context.Background(), "query")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Query != "query" {
		t.Errorf("query = %q", out.Query)
	}
	if out.Answer != "" {
		t.Errorf("answer = %q, want empty", out.Answer)
	}
}

// --- Hard Red: Pipeline with only sources, no fetcher ---

func TestHR_Pipeline_SourcesNoFetcher(t *testing.T) {
	src := &mockSource{
		name:    "test",
		results: []sources.Result{{Title: "Test", URL: "http://example.com", Content: "snippet"}},
	}
	p := NewPipeline(WithSources(src))

	out, err := p.Run(context.Background(), "query")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Should have sources but no answer (no LLM).
	if len(out.Sources) != 1 {
		t.Errorf("sources = %d, want 1", len(out.Sources))
	}
	if out.Answer != "" {
		t.Errorf("answer = %q, want empty (no LLM)", out.Answer)
	}
}

// --- Hard Red: Pipeline with fetcher but no extractor ---

func TestHR_Pipeline_FetcherNoExtractor(t *testing.T) {
	src := &mockSource{
		name:    "test",
		results: []sources.Result{{Title: "Test", URL: "http://example.com", Content: "snippet"}},
	}
	p := NewPipeline(
		WithSources(src),
		WithFetchFunc(func(_ context.Context, _ string) ([]byte, error) {
			return []byte("content"), nil
		}),
		// No extractor — fetch+extract step should be skipped.
	)

	out, err := p.Run(context.Background(), "query")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Sources) != 1 {
		t.Errorf("sources = %d", len(out.Sources))
	}
}

// --- Hard Red: all sources fail ---

func TestHR_Pipeline_AllSourcesFail(t *testing.T) {
	fail1 := &mockSource{name: "fail1", err: errors.New("boom1")}
	fail2 := &mockSource{name: "fail2", err: errors.New("boom2")}

	p := NewPipeline(WithSources(fail1, fail2))
	out, err := p.Run(context.Background(), "query")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// All sources failed → empty results.
	if out.Answer != "" {
		t.Errorf("answer = %q, want empty", out.Answer)
	}
	if len(out.Sources) != 0 {
		t.Errorf("sources = %d, want 0", len(out.Sources))
	}
}

// --- Hard Red: context cancellation during pipeline ---

func TestHR_Pipeline_ContextCancel(t *testing.T) {
	slowSrc := &slowMockSource{
		name:    "slow",
		results: []sources.Result{{Title: "Slow", URL: "http://example.com"}},
		delay:   200 * time.Millisecond,
	}

	p := NewPipeline(WithSources(slowSrc))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Should not hang forever.
	_, _ = p.Run(ctx, "query")
}

type slowMockSource struct {
	name    string
	results []sources.Result
	delay   time.Duration
}

func (s *slowMockSource) Name() string { return s.name }
func (s *slowMockSource) Search(ctx context.Context, _ sources.Query) ([]sources.Result, error) {
	select {
	case <-time.After(s.delay):
		return s.results, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// --- Hard Red: ParallelFetch maxConcurrency=1 ---

func TestHR_ParallelFetch_MaxConcurrency1(t *testing.T) {
	var active, maxActive atomic.Int64
	urls := make([]string, 10)
	for i := range urls {
		urls[i] = fmt.Sprintf("http://example.com/%d", i)
	}

	results := ParallelFetch(context.Background(), urls,
		func(_ context.Context, _ string) (string, error) {
			cur := active.Add(1)
			for {
				old := maxActive.Load()
				if cur <= old || maxActive.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			return "ok", nil
		},
		WithMaxConcurrency(1))

	if len(results) != 10 {
		t.Fatalf("results = %d, want 10", len(results))
	}
	if maxActive.Load() > 1 {
		t.Errorf("max concurrent = %d, want 1", maxActive.Load())
	}
}

// --- Hard Red: ParallelFetch all fail ---

func TestHR_ParallelFetch_AllFail(t *testing.T) {
	urls := []string{"http://a.com", "http://b.com", "http://c.com"}
	results := ParallelFetch(context.Background(), urls,
		func(_ context.Context, _ string) (string, error) {
			return "", errors.New("fail")
		})

	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	for _, r := range results {
		if r.Err == nil {
			t.Errorf("expected error for %s", r.URL)
		}
		if r.Content != "" {
			t.Errorf("expected empty content for %s", r.URL)
		}
	}
}

// --- Hard Red: ParallelFetch panic in fetchFn ---

func TestHR_ParallelFetch_PanicInFetchFn(t *testing.T) {
	urls := []string{"http://a.com", "http://b.com"}
	results := ParallelFetch(context.Background(), urls,
		func(_ context.Context, u string) (string, error) {
			if u == "http://a.com" {
				panic("boom")
			}
			return "ok", nil
		})

	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}

	// Panicking URL should have error captured.
	for _, r := range results {
		if r.URL == "http://a.com" {
			if r.Err == nil {
				t.Error("expected error for panicking URL")
			}
		}
		if r.URL == "http://b.com" {
			if r.Err != nil {
				t.Errorf("unexpected error for ok URL: %v", r.Err)
			}
			if r.Content != "ok" {
				t.Errorf("content = %q, want ok", r.Content)
			}
		}
	}
}

// --- Hard Red: duplicate URLs in source results ---

func TestHR_Pipeline_DuplicateURLs(t *testing.T) {
	contentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><p>Content.</p></body></html>`))
	}))
	defer contentSrv.Close()

	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		inner := `{"answer":"deduped","facts":[]}`
		b, _ := json.Marshal(inner)
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s}}]}`, string(b))
	}))
	defer llmSrv.Close()

	// Same URL from multiple sources.
	src := &mockSource{
		name: "dups",
		results: []sources.Result{
			{Title: "Same 1", URL: contentSrv.URL + "/page", Content: "a"},
			{Title: "Same 2", URL: contentSrv.URL + "/page", Content: "b"},
			{Title: "Same 3", URL: contentSrv.URL + "/page", Content: "c"},
		},
	}

	var fetchCount atomic.Int64
	fetchFn := func(ctx context.Context, rawURL string) ([]byte, error) {
		fetchCount.Add(1)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		body := make([]byte, 4096)
		n, _ := resp.Body.Read(body)
		return body[:n], nil
	}

	p := NewPipeline(
		WithSources(src),
		WithFetchFunc(fetchFn),
		WithExtractor(extract.New()),
		WithLLMClient(llm.New(llm.WithAPIBase(llmSrv.URL+"/v1"), llm.WithAPIKey("test"))),
	)

	out, err := p.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Answer == "" {
		t.Error("expected answer")
	}
	// Dedup: same URL should be fetched only once.
	if fetchCount.Load() != 1 {
		t.Errorf("fetchCount = %d, want 1 (URL dedup)", fetchCount.Load())
	}
}

// --- Hard Red: uniqueURLs with empty and nil ---

func TestHR_UniqueURLs_EmptyAndBlank(t *testing.T) {
	results := []sources.Result{
		{URL: ""},
		{URL: "http://a.com"},
		{URL: ""},
		{URL: "http://a.com"},
		{URL: "http://b.com"},
	}
	urls := uniqueURLs(results)
	if len(urls) != 2 {
		t.Errorf("urls = %d, want 2", len(urls))
	}
}

// --- Hard Red: Pipeline with extractor that returns empty content ---

func TestHR_Pipeline_ExtractorReturnsEmpty(t *testing.T) {
	src := &mockSource{
		name:    "test",
		results: []sources.Result{{Title: "Test", URL: "http://example.com"}},
	}

	p := NewPipeline(
		WithSources(src),
		WithFetchFunc(func(_ context.Context, _ string) ([]byte, error) {
			return []byte("<html></html>"), nil
		}),
		WithExtractor(&emptyExtractor{}),
	)

	out, err := p.Run(context.Background(), "query")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Empty extraction should not cause panic.
	_ = out
}

type emptyExtractor struct{}

func (e *emptyExtractor) Extract(_ []byte, _ *url.URL) (*extract.Result, error) {
	return &extract.Result{Content: ""}, nil
}
