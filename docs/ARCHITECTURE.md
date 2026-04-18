# go-engine Architecture

> AI-ready web stack for Go — search, fetch, extract, chunk, rank.

## Overview

go-engine provides a composable set of packages for building search and content-extraction pipelines. Services import go-engine as a library and compose the packages they need.

## Package Dependency Graph

```
pipeline/
├── fetch/      (HTTP fetching, proxy, retry)
├── extract/    (HTML → text/markdown)
├── search/     (SearXNG, DDG, Startpage)
├── llm/        (OpenAI-compat client)
├── cache/      (Memory + Redis tiered)
├── text/       (string utilities)
└── metrics/    (atomic counters)

sources/        (HN, YouTube, Context7, HuggingFace, WordPress, GitHub)
├── fetch/
├── llm/
└── text/
```

No circular dependencies. Lower packages (text, metrics, cache) have zero or minimal external deps. Higher packages (pipeline, sources) compose the lower ones.

## Core Interfaces

All packages communicate through interfaces, enabling testing with mocks and swapping implementations.

```go
// fetch.Fetcher — HTTP body retrieval
type Fetcher interface {
    FetchBody(ctx context.Context, url string) ([]byte, error)
}

// extract.Extractor — HTML content extraction
type Extractor interface {
    Extract(ctx context.Context, body []byte, pageURL *url.URL) (*ExtractResult, error)
}

// search.SearchEngine — web search backend
type SearchEngine interface {
    Search(ctx context.Context, q Query) ([]SearchResult, error)
    Name() string
}

// cache.Cache — key-value store with TTL
type Cache interface {
    Get(ctx context.Context, key string) ([]byte, bool)
    Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
}

// llm.Client — LLM completion
type Client interface {
    Complete(ctx context.Context, system, user string) (string, error)
}
```

## Configuration

Consumers configure go-engine via constructor options (functional options pattern):

```go
fetcher := fetch.New(
    fetch.WithProxyPool(pool),
    fetch.WithTimeout(30 * time.Second),
)

extractor := extract.NewChain() // default: trafilatura → goquery → regex

searchEngine := search.NewSearXNG("http://searxng:8080")

llmClient := llm.New(
    llm.WithAPIBase("https://api.openai.com/v1"),
    llm.WithAPIKey(key),
    llm.WithModel("gpt-4o-mini"),
)
```

No global `Init()` — each component is independently constructed and composed by the consumer.

## Testing Strategy

- **Unit tests** per package with `httptest.Server` for HTTP mocks
- **`miniredis`** for Redis cache tests (no real Redis needed)
- **Interface mocks** for cross-package testing
- **`go test -race`** on all packages (CI enforced)

## Versioning

- **v0.x.x** during extraction (no compatibility guarantees)
- **v1.0.0** after all 3 services migrated and API stable
- Follows Go module versioning: v2+ requires `/v2` import path suffix
