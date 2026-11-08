# go-engine

AI-ready web stack for Go — search, fetch, extract, chunk, rank.

[![Go 1.26+](https://img.shields.io/badge/go-1.26%2B-blue)](https://go.dev/dl/)
[![Tests](https://github.com/anatolykoptev/go-engine/actions/workflows/ci.yml/badge.svg)](https://github.com/anatolykoptev/go-engine/actions)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)
[![Go Reference](https://pkg.go.dev/badge/github.com/anatolykoptev/go-engine.svg)](https://pkg.go.dev/github.com/anatolykoptev/go-engine)

### Install

```bash
go get github.com/anatolykoptev/go-engine
```

### Packages

| Package | Description |
|---------|-------------|
| `extract` | Content extraction from HTML (trafilatura → goquery → regex chain), optional LLM fallback for thin pages |
| `fetch` | HTTP fetcher with proxy pool, retry, direct-first tier with a per-host block cache, and an optional fallback fetcher |
| `imagesearch` | Image search engines (Bing, Brave, DDG, Google, Yandex) fused by weighted round-robin |
| `llm` | OpenAI-compatible chat client with retry, API key and endpoint fallback, cooldowns and fact-quote verification |
| `metrics` | Atomic counter registry with Prometheus-compatible export (delegates to go-kit) |
| `pipeline` | Parallel fetch and search-output formatting for search-to-LLM pipelines |
| `search` | Direct fan-out over web engines with pacing, per-source timeouts, block cache, result fusion (WRR) and dedup |
| `sources` | Source integrations and API client primitives (HN, YouTube, GitHub URLs, Context7, Hugging Face, go-search MCP client) |
| `text` | Chunker, BM25 filter, token estimator and query classification |
| `webtext` | HTML cleaning and truncation helpers shared by the other packages |
| `websearch` | Web search provider adapters (SearXNG, DDG, Bing, Brave, Reddit, arXiv) behind one `Provider` interface |

### Quickstart

Search through a SearXNG instance (it needs one running; the adapters for DDG, Bing and Brave
take a `BrowserDoer` instead), fetch each hit and extract its text:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "net/url"
    "time"

    "github.com/anatolykoptev/go-engine/extract"
    "github.com/anatolykoptev/go-engine/fetch"
    "github.com/anatolykoptev/go-engine/websearch"
)

func main() {
    ctx := context.Background()

    provider := websearch.NewSearXNG("http://localhost:8080")
    results, err := provider.Search(ctx, "Go generics tutorial",
        websearch.SearchOpts{Language: "en", Limit: 3})
    if err != nil {
        log.Fatal(err)
    }

    fetcher := fetch.New(fetch.WithTimeout(30 * time.Second))
    extractor := extract.New(extract.WithMaxContentLen(2000))

    for _, r := range results {
        body, err := fetcher.FetchBody(ctx, r.URL)
        if err != nil {
            continue
        }
        u, _ := url.Parse(r.URL)
        page, err := extractor.Extract(ctx, body, u)
        if err != nil {
            continue
        }
        fmt.Printf("## %s\n%s\n\n", page.Title, page.Content)
    }
}
```

### Configuration

The library is configured in code through functional options. Two optional environment
variables are read: `LLM_COOLDOWN_SECONDS` (`llm`, endpoint cooldown, default 5 minutes when
unset or invalid) and the two pacing variables of the direct engine tier in `search/pacer.go`
(minimum delay and jitter in milliseconds).

### Status

Stable, semver-tagged (`v1.x`); versions and the changelog come from release-please.
`docs/ARCHITECTURE.md` and `docs/ROADMAP.md` describe the v1.3 design and predate later
packages; the code and [CHANGELOG.md](CHANGELOG.md) are authoritative.

### Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md); agents start at [CLAUDE.md](CLAUDE.md). All documents are
listed in [docs/INDEX.md](docs/INDEX.md).

### License

Apache 2.0 — see [LICENSE](LICENSE).
