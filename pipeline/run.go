// Package pipeline orchestrates the full search pipeline:
// sources → fetch → extract → chunk → filter → llm.
package pipeline

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"

	"github.com/anatolykoptev/go-engine/extract"
	"github.com/anatolykoptev/go-engine/llm"
	"github.com/anatolykoptev/go-engine/sources"
	"github.com/anatolykoptev/go-engine/text"
)

const defaultMaxConcurrency = 10

// Pipeline orchestrates: sources → fetch → extract → chunk → filter → llm.
type Pipeline struct {
	sources   []sources.Source
	fetchFn   func(ctx context.Context, url string) ([]byte, error)
	extractor extract.Strategy
	chunker   text.Chunker
	filter    text.Filter
	llm       *llm.Client
	maxConc   int
}

// Option configures a Pipeline.
type Option func(*Pipeline)

// WithSources adds search sources to the pipeline.
func WithSources(srcs ...sources.Source) Option {
	return func(p *Pipeline) { p.sources = append(p.sources, srcs...) }
}

// WithFetchFunc sets the HTTP fetch function used to retrieve page content.
func WithFetchFunc(fn func(ctx context.Context, url string) ([]byte, error)) Option {
	return func(p *Pipeline) { p.fetchFn = fn }
}

// WithExtractor sets the HTML extraction strategy.
func WithExtractor(s extract.Strategy) Option {
	return func(p *Pipeline) { p.extractor = s }
}

// WithChunker sets the text chunker for splitting extracted content.
func WithChunker(c text.Chunker) Option {
	return func(p *Pipeline) { p.chunker = c }
}

// WithFilter sets the chunk filter for relevance ranking.
func WithFilter(f text.Filter) Option {
	return func(p *Pipeline) { p.filter = f }
}

// WithLLMClient sets the LLM client for answer generation.
func WithLLMClient(c *llm.Client) Option {
	return func(p *Pipeline) { p.llm = c }
}

// WithPipelineConcurrency sets the maximum number of concurrent fetches.
func WithPipelineConcurrency(n int) Option {
	return func(p *Pipeline) { p.maxConc = n }
}

// NewPipeline creates a Pipeline with the given options.
// Default max concurrency is 10.
func NewPipeline(opts ...Option) *Pipeline {
	p := &Pipeline{maxConc: defaultMaxConcurrency}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Run executes the full pipeline for a query and returns structured output.
func (p *Pipeline) Run(ctx context.Context, query string) (*SearchOutput, error) {
	// 1. Search all sources in parallel.
	srcResults := p.searchSources(ctx, query)
	if len(srcResults) == 0 {
		return &SearchOutput{Query: query}, nil
	}

	// 2. Collect unique URLs.
	urls := uniqueURLs(srcResults)

	// 3. Fetch + extract content.
	contents := make(map[string]string, len(urls))
	if p.fetchFn != nil && p.extractor != nil && len(urls) > 0 {
		fetchResults := ParallelFetch(ctx, urls, p.buildFetchFn(), WithMaxConcurrency(p.maxConc))
		for _, r := range fetchResults {
			if r.Err == nil && r.Content != "" {
				contents[r.URL] = r.Content
			}
		}
	}

	// 4. Chunk + filter (if configured).
	if p.chunker != nil {
		contents = p.chunkAndFilter(contents, query)
	}

	// 5. Summarize with LLM.
	if p.llm != nil {
		out, err := p.llm.Summarize(ctx, query, contentLimitChars, srcResults, contents)
		if err != nil {
			return nil, err
		}
		return p.buildOutput(query, out, srcResults), nil
	}

	// No LLM — return sources without answer.
	return p.buildOutput(query, &llm.StructuredOutput{}, srcResults), nil
}

const contentLimitChars = 6000

// buildFetchFn wraps p.fetchFn + p.extractor into the string-returning signature
// expected by ParallelFetch.
func (p *Pipeline) buildFetchFn() func(ctx context.Context, rawURL string) (string, error) {
	return func(ctx context.Context, rawURL string) (string, error) {
		body, err := p.fetchFn(ctx, rawURL)
		if err != nil {
			return "", err
		}
		u, _ := url.Parse(rawURL)
		result, err := p.extractor.Extract(body, u)
		if err != nil {
			return "", err
		}
		return result.Content, nil
	}
}

// searchSources fans out Search calls to all sources concurrently.
// Source errors are logged and skipped — partial results are returned.
func (p *Pipeline) searchSources(ctx context.Context, query string) []sources.Result {
	if len(p.sources) == 0 {
		return nil
	}

	type sourceOut struct {
		results []sources.Result
	}

	ch := make(chan sourceOut, len(p.sources))
	var wg sync.WaitGroup

	for _, src := range p.sources {
		wg.Add(1)
		go func(s sources.Source) {
			defer wg.Done()
			res, err := s.Search(ctx, sources.Query{Text: query})
			if err != nil {
				slog.WarnContext(ctx, "source search failed",
					"source", s.Name(),
					"err", err,
				)
				ch <- sourceOut{}
				return
			}
			ch <- sourceOut{results: res}
		}(src)
	}

	wg.Wait()
	close(ch)

	var all []sources.Result
	for out := range ch {
		all = append(all, out.results...)
	}
	return all
}

// chunkAndFilter applies the chunker and (optionally) the filter to each
// extracted content entry. Returns a new map with chunked/filtered content
// joined back to a single string per URL.
func (p *Pipeline) chunkAndFilter(contents map[string]string, query string) map[string]string {
	result := make(map[string]string, len(contents))
	for u, content := range contents {
		chunks := p.chunker.Chunk(content)
		if len(chunks) == 0 {
			continue
		}
		if p.filter != nil {
			chunks = p.filter.Filter(chunks, query)
		}
		result[u] = strings.Join(chunks, "\n\n")
	}
	return result
}

// buildOutput assembles the SearchOutput from LLM output and source results.
func (p *Pipeline) buildOutput(query string, out *llm.StructuredOutput, srcResults []sources.Result) *SearchOutput {
	searchOut := BuildSearchOutput(query, out, srcResults)
	return &searchOut
}
