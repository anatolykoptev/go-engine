## go-engine

AI-ready web stack for Go — search, fetch, extract, chunk, rank.

[![Go 1.26+](https://img.shields.io/badge/go-1.26%2B-blue)](https://go.dev/dl/)
[![Tests](https://github.com/anatolykoptev/go-engine/actions/workflows/test.yml/badge.svg)](https://github.com/anatolykoptev/go-engine/actions)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/anatolykoptev/go-engine.svg)](https://pkg.go.dev/github.com/anatolykoptev/go-engine)

### Install

```bash
go get github.com/anatolykoptev/go-engine
```

### Packages

| Package | Description |
|---------|-------------|
| `cache` | In-memory + Redis tiered cache with TTL |
| `extract` | Content extraction from HTML (trafilatura → goquery → regex chain) |
| `fetch` | HTTP fetcher with proxy pool, retry, and timeout |
| `llm` | OpenAI-compatible chat client with streaming and token budgeting |
| `metrics` | Atomic counter registry with Prometheus-compatible export |
| `pipeline` | Orchestrated fetch → extract → chunk → rank pipeline |
| `search` | Result fusion (WRR), dedup, and scoring across multiple backends |
| `sources` | API client primitives and auth helpers (HN, YouTube, GitHub, etc.) |
| `text` | Chunker, BM25 filter, and token estimator |
| `websearch` | Web search provider adapters (SearXNG, DDG, Startpage, arxiv, etc.) |

### Quickstart

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/anatolykoptev/go-engine/fetch"
    "github.com/anatolykoptev/go-engine/search"
    "github.com/anatolykoptev/go-engine/websearch"
    "github.com/anatolykoptev/go-engine/extract"
)

func main() {
    ctx := context.Background()

    fetcher := fetch.New(fetch.WithTimeout(30))
    extractor := extract.NewChain()

    searxng := websearch.NewSearXNG("http://searxng:8080", fetcher)
    engine := search.NewFuse([]search.SearchEngine{searxng})

    results, err := engine.Search(ctx, search.Query{Text: "Go generics tutorial"})
    if err != nil {
        log.Fatal(err)
    }

    for _, r := range results[:3] {
        body, _ := fetcher.FetchBody(ctx, r.URL)
        extracted, _ := extractor.Extract(ctx, body, nil)
        fmt.Printf("## %s\n%s\n\n", r.Title, extracted.Text[:200])
    }
}
```

### License

Apache 2.0 — see [LICENSE](LICENSE).
