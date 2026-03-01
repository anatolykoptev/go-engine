# go-engine: Shared Engine for go-* MCP Services

> **Module:** `github.com/anatolykoptev/go-engine`
> **Status:** Planning
> **Date:** 2026-02-28

## Problem

Three MCP services (`go-search`, `go-job`, `go-startup`) independently maintain near-identical `internal/engine/` packages — **~3500+ lines of copy-paste** with progressive regressions:

| File | go-search | go-job | go-startup | Purpose |
|------|:---------:|:------:|:----------:|---------|
| fetch_html.go | trafilatura | trafilatura | readability (!) | HTML → text extraction |
| fetch_http.go | fetchBody+proxy | fetchBody+proxy | no proxy (!) | HTTP transport |
| fetch_raw.go | v | v | — | Raw text fetch |
| search.go | v | v | v | SearXNG integration |
| pipeline/output.go | v | v | v | SmartSearch pipeline |
| cache.go | v | v | v | Tiered L1+L2 cache |
| llm.go | v | v | v | OpenAI-compat LLM client |
| detect.go | v | v | v | Query classification |
| textutil.go | v | v | v | String utilities |
| metrics.go | v | v | v | Atomic counters |
| config.go | v | v | v | Config struct + Init() |
| httpclient.go | v | v | stealth.go | BrowserClient wrapper |
| direct_ddg.go | v | v | — | DDG direct scraper |
| direct_startpage.go | v | v | — | Startpage direct scraper |
| sources/* (7 files) | v | v | — | HN, YouTube, Context7... |
| github_helpers.go | v | v | — | GitHub URL utilities |

**Known regressions in copies:**
- go-job: regex compiled per-call in `fetchWithFallback` (not pre-compiled)
- go-job: `fetchWithFallback` re-fetches URL (double network trip)
- go-startup: still on `go-readability` (not `go-trafilatura`)
- go-startup: no proxy pool — exposes server IP
- go-startup: uses `cenkalti/backoff` instead of go-stealth retry
- go-job/go-startup: magic numbers instead of named constants in sources

---

## Competitor Analysis

### Content Extraction

| Solution | Lang | Stars | Accuracy | Fallback | Metadata | Thread-safe |
|----------|------|------:|----------|----------|----------|:-----------:|
| **go-trafilatura** | Go | active | Best | 3-tier (trafilatura→readability→domdistiller) | Full (title/author/date/lang/categories) | Yes |
| go-readability (readeck) | Go | ~900 | Good | No | Partial (title/byline/image) | Yes |
| go-domdistiller | Go | active | Medium | No | Minimal | Yes |
| GoOse | Go | ~2.4k | Medium | No (3-stage internal) | Basic | Yes |
| **trafilatura** (Python) | Python | ~4.5k | Best | Multi-stage | Full | Yes |
| **Our fetch_html.go** | Go | — | Good→Best | trafilatura→goquery→regex | Title only | Yes |

**Decision:** Keep go-trafilatura as primary. Extract wrapper into `go-engine/extract`.

### HTTP Client / Retry

| Solution | Lang | Stars | Retry | Proxy | Rate Limit | RoundTripper | TLS Fingerprint |
|----------|------|------:|:-----:|:-----:|:----------:|:------------:|:---------------:|
| **hashicorp/go-retryablehttp** | Go | ~2k | Exp backoff, CheckRetry func | No | No | `StandardClient()` | No |
| **gojek/heimdall** | Go | ~2.6k | Exp backoff, circuit breaker | No | No | `Doer` interface | No |
| go-resty/resty | Go | ~10k | Configurable | No | No | No | No |
| **go-stealth** (own) | Go | — | Exp backoff + jitter | Pool rotation | Per-domain | Via tls-client | Chrome 131 |
| cenkalti/backoff | Go | ~5k | Library only | — | — | — | — |
| **Our fetch_http.go** | Go | — | go-stealth RetryDo | BrowserClient proxy | No | No | Via go-stealth |

**Decision:** Keep go-stealth as HTTP backend. Extract `fetchBody()`, retry config, BrowserClient wrapper into `go-engine/fetch`. Add per-domain rate limiting from colly pattern.

### Search Aggregation

| Solution | Lang | Stars | Engines | Dedup | Rate Limit | Scoring |
|----------|------|------:|---------|:-----:|:----------:|:-------:|
| **karust/openserp** | Go | ~613 | Google, Bing, Yandex, Baidu, DDG | URL-based | Per-engine `rate.Limiter` | Rank-based |
| SearXNG | Python | ~15k | 200+ engines | Cross-engine | Per-engine | Weighted score |
| **Our search.go** | Go | — | SearXNG, DDG direct, Startpage direct | Domain-based | No | Score filter |

**Decision:** Define `SearchEngine` interface (from openserp pattern). Extract SearXNG, DDG, Startpage implementations. Add URL normalization for dedup (purell).

### Caching

| Solution | Lang | Pattern | L1 | L2 | TTL | Singleflight |
|----------|------|---------|:--:|:--:|:---:|:------------:|
| **go-enriche** (own) | Go | Memory+Redis+Tiered | sync.Map | Redis JSON | Per-entry | Yes |
| geziyor cache | Go | Transport-level | Disk/LevelDB | — | RFC2616 | No |
| colly cache | Go | Transport-level | Disk | — | mtime | No |
| **Our cache.go** | Go | Custom tiered | sync.Map | Redis GET/SET | Global | No |

**Decision:** Port from current implementation. Add singleflight (from go-enriche pattern). Keep `Cache` interface from go-enriche.

### Web Scraping Frameworks (architecture patterns)

| Framework | Stars | Key Pattern | Applicable to go-engine |
|-----------|------:|-------------|:-----------------------:|
| **gocolly/colly** | ~25k | `Storage` interface, `CollectorOption func`, RoundTripper chain | Storage, options pattern |
| **geziyor** | ~640 | `cache.Cache` interface (3 backends), `RequestProcessor`/`ResponseProcessor` middleware | Cache interface, middleware |
| slotix/dataflowkit | ~710 | Fetch/Parse as separate microservices | Validates fetch/extract split |
| **go-rod** | ~5k | Auto-wait, `HijackRequests`, CDP-agnostic | Future: `BrowserFetcher` interface |

### MCP SDK Ecosystem

| Project | Stars | Key Pattern | Applicable |
|---------|------:|-------------|:----------:|
| **modelcontextprotocol/go-sdk** | ~4k | `mcp.AddTool`, middleware, `StreamableHTTPHandler` | Already used |
| mark3labs/mcp-go | ~8k | Same API as official SDK | Absorbed by official |
| sammcj/mcp-devtools | ~126 | One Go binary replaces multiple MCP servers, `internal/tools/` per-domain | Tool file layout |
| olgasafonova/mcp-otel-go | — | `server.AddReceivingMiddleware()` for OTel | Future: observability |
| **AgenticGoKit** | ~106 | `FunctionTool` interface + global registry | Tool composition |

### Multi-Service Go Ecosystems (code sharing patterns)

| Project | Stars | Pattern | Lesson |
|---------|------:|---------|--------|
| Mattermost | ~31k | `server/channels/app/` shared business logic layer | "App layer" pattern |
| Grafana Loki | ~25k | `pkg/` shared + `cmd/` per-binary | Standard monorepo pattern |
| Gitea | ~47k | `modules/` shared + `services/` per-domain | Module-based sharing |
| **uber-go/fx** | ~7k | DI framework with `fx.Module` | Overkill for our scale |

---

## Architecture

### Package Structure

```
~/src/go-engine/
├── go.mod                          github.com/anatolykoptev/go-engine
│
├── fetch/
│   ├── fetcher.go                  Fetcher interface + DefaultFetcher (fetchBody pattern)
│   ├── retry.go                    RetryConfig, RetryDo, RetryHTTP (from go-stealth)
│   ├── browser.go                  BrowserClient wrapper (go-stealth + proxy pool)
│   ├── transport.go                RoundTripper chain: cache → retry → stealth → http.Transport
│   ├── useragent.go                RandomUserAgent, ChromeHeaders (from go-stealth)
│   └── fetcher_test.go
│
├── extract/
│   ├── extractor.go                Extractor interface + ExtractResult
│   ├── trafilatura.go              go-trafilatura wrapper (primary)
│   ├── goquery.go                  goquery fallback
│   ├── regex.go                    Pre-compiled regex fallback (package-level vars)
│   ├── chain.go                    FallbackChain: tries extractors in order
│   ├── markdown.go                 HTML node → markdown conversion
│   └── extract_test.go
│
├── search/
│   ├── engine.go                   SearchEngine interface + SearchResult + Query
│   ├── searxng.go                  SearXNG implementation
│   ├── ddg.go                      DuckDuckGo direct scraper
│   ├── startpage.go                Startpage direct scraper
│   ├── aggregator.go               Concurrent fan-out + dedup + scoring
│   ├── dedup.go                    URL normalization (purell) + dedup
│   └── search_test.go
│
├── llm/
│   ├── client.go                   LLM client (OpenAI-compatible, fallback keys)
│   ├── retry.go                    LLM-specific retry with key rotation
│   └── client_test.go
│
├── cache/
│   ├── cache.go                    Cache interface (Get/Set/Delete)
│   ├── memory.go                   In-memory LRU with TTL
│   ├── redis.go                    Redis L2
│   ├── tiered.go                   TieredCache: L1 → L2 with promotion
│   ├── singleflight.go            Singleflight wrapper (from go-enriche)
│   └── cache_test.go
│
├── pipeline/
│   ├── smart.go                    SmartSearch pipeline (search → fetch → LLM)
│   ├── parallel.go                 ParallelFetch with semaphore
│   ├── output.go                   FormatOutput, BuildSearchOutput
│   └── pipeline_test.go
│
├── text/
│   ├── clean.go                    CleanHTML, CleanLines, Truncate, TruncateRunes
│   ├── classify.go                 DetectQueryType, DetectQueryDomain
│   └── text_test.go
│
├── metrics/
│   ├── registry.go                 Counter registry (atomic.Int64 map)
│   ├── format.go                   FormatMetrics, TrackOperation
│   └── metrics_test.go
│
├── sources/
│   ├── hackernews.go               HN Algolia search + comment fetch
│   ├── youtube.go                  YouTube InnerTube search
│   ├── youtube_transcript.go       YouTube transcript extraction
│   ├── context7.go                 Context7 library docs
│   ├── huggingface.go              HuggingFace model/dataset search
│   ├── wordpress.go                WordPress dev docs
│   ├── github.go                   GitHub URL helpers + raw content
│   └── sources_test.go
│
└── docs/
    ├── ROADMAP.md                  This file
    └── ARCHITECTURE.md             Detailed design (created in Phase 0)
```

### Core Interfaces

```go
// fetch/fetcher.go
type Fetcher interface {
    FetchBody(ctx context.Context, url string) ([]byte, error)
}

// extract/extractor.go
type Extractor interface {
    Extract(ctx context.Context, body []byte, pageURL *url.URL) (*ExtractResult, error)
}

type ExtractResult struct {
    Title       string
    Content     string      // clean text
    Markdown    string      // markdown (may be empty)
    Author      string
    Date        *time.Time
    Language    string
    SiteName    string
    Image       string
}

// search/engine.go
type SearchEngine interface {
    Search(ctx context.Context, q Query) ([]SearchResult, error)
    Name() string
}

// cache/cache.go
type Cache interface {
    Get(ctx context.Context, key string) ([]byte, bool)
    Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
}

// llm/client.go
type Client interface {
    Complete(ctx context.Context, system, user string) (string, error)
}
```

### Consumer Pattern

After migration, each service becomes a thin wrapper:

```go
// go-search/main.go
import (
    "github.com/anatolykoptev/go-engine/fetch"
    "github.com/anatolykoptev/go-engine/extract"
    "github.com/anatolykoptev/go-engine/search"
    "github.com/anatolykoptev/go-engine/cache"
    "github.com/anatolykoptev/go-engine/llm"
    "github.com/anatolykoptev/go-engine/pipeline"
    "github.com/anatolykoptev/go-engine/sources"
)

func initEngine() {
    pp, _ := proxypool.NewWebshare(os.Getenv("WEBSHARE_API_KEY"))
    fetcher := fetch.New(fetch.WithProxyPool(pp), fetch.WithTimeout(30*time.Second))
    extractor := extract.NewChain()  // trafilatura → goquery → regex
    c := cache.NewTiered(cache.NewMemory(), cache.NewRedis(redisURL))
    llmClient := llm.New(llm.WithAPIBase(llmURL), llm.WithAPIKey(key))
    // ... register MCP tools using these components
}
```

---

## Phases

### Phase 0: Project Scaffolding
**Scope:** go.mod, CI, lint, docs, empty package stubs
**Files:** go.mod, .golangci.yml, .pre-commit-config.yaml, Makefile, docs/ARCHITECTURE.md
**Deps:** none (stubs only)
**Tests:** `go build ./...` passes
**Consumers affected:** none

### Phase 1: text + metrics (zero external deps)
**Scope:** Extract pure-Go utilities that have no external dependencies.
**Extract from:** `textutil.go`, `detect.go`, `metrics.go` — identical across all 3 services.
**Packages:** `text/`, `metrics/`
**Tests:** Unit tests for CleanHTML, Truncate, DetectQueryType, counter registry
**Consumers affected:** none (not yet imported)
**LOC removed per service:** ~300

### Phase 2: cache
**Scope:** Extract tiered cache with singleflight.
**Extract from:** `cache.go` — near-identical across all 3 services.
**Packages:** `cache/`
**Deps:** `github.com/redis/go-redis/v9`, `golang.org/x/sync` (singleflight)
**Tests:** Unit tests with miniredis mock
**LOC removed per service:** ~250

### Phase 3: fetch + extract
**Scope:** Extract HTTP fetching (fetchBody, BrowserClient, retry) and content extraction (trafilatura chain).
**Extract from:** `fetch_http.go`, `fetch_html.go`, `httpclient.go`/`stealth.go`, `fetch_raw.go`
**Packages:** `fetch/`, `extract/`
**Deps:** `go-stealth`, `go-trafilatura`, `goquery`, `html-to-markdown`
**Tests:** Unit tests + integration test with httptest server
**Key fix:** Pre-compiled regex in extract/regex.go (fixes go-job regression)
**LOC removed per service:** ~400

### Phase 4: search + sources
**Scope:** Extract SearXNG integration, direct scrapers, and source implementations.
**Extract from:** `search.go`, `direct_ddg.go`, `direct_startpage.go`, `sources/*`
**Packages:** `search/`, `sources/`
**New:** URL normalization via purell for better dedup
**Tests:** Unit tests with mock SearXNG responses
**LOC removed per service:** ~1500 (go-search/go-job), ~200 (go-startup)

### Phase 5: llm + pipeline
**Scope:** Extract LLM client and SmartSearch pipeline.
**Extract from:** `llm.go`, `output.go`/`pipeline.go`
**Packages:** `llm/`, `pipeline/`
**Tests:** Unit tests with mock LLM responses
**LOC removed per service:** ~500

### Phase 6: Migrate go-search
**Scope:** Replace go-search's `internal/engine/` with go-engine imports.
**Pattern:** go-search keeps domain-specific tool handlers, imports go-engine for all shared logic.
**Verification:** All MCP tools work identically, `web_url_read` returns same content quality.
**Cleanup:** Delete `internal/engine/` (except config.go adapted to use go-engine types).

### Phase 7: Migrate go-job
**Scope:** Replace go-job's `internal/engine/` with go-engine imports.
**Fixes applied during migration:**
- Pre-compiled regex (from go-engine extract/regex.go)
- fetchWithFallback no longer re-fetches (uses body []byte)
- Named constants in sources (from go-engine sources/)
**Cleanup:** Delete `internal/engine/` except job-specific code.

### Phase 8: Migrate go-startup
**Scope:** Replace go-startup's `internal/engine/` with go-engine imports.
**Bonus fixes:**
- go-readability → go-trafilatura (via go-engine extract)
- Add proxy pool support (via go-engine fetch)
- Replace cenkalti/backoff with go-stealth retry (via go-engine fetch)
**Cleanup:** Delete `internal/engine/` except startup-specific config fields.

### Phase 9: Harden + Release v1.0.0
**Scope:** API stabilization, documentation, release.
**Tasks:**
- Comprehensive integration tests
- godoc comments on all exported types/functions
- ARCHITECTURE.md with diagrams
- Benchmark tests for hot paths (extract, cache, retry)
- Tag v1.0.0

---

## Migration Matrix

| Phase | Package | go-search | go-job | go-startup | go-content | go-enriche |
|:-----:|---------|:---------:|:------:|:----------:|:----------:|:----------:|
| 1 | text/ | replaces textutil.go, detect.go | same | same | — | — |
| 1 | metrics/ | replaces metrics.go | same | same | — | — |
| 2 | cache/ | replaces cache.go | same | same | — | may adopt |
| 3 | fetch/ | replaces fetch_http.go, httpclient.go | same | replaces fetch_http.go, stealth.go | — | may adopt |
| 3 | extract/ | replaces fetch_html.go | same | replaces fetch_html.go + upgrades to trafilatura | — | already has own |
| 4 | search/ | replaces search.go, direct_*.go | same | replaces search.go | — | — |
| 4 | sources/ | replaces sources/* | same | — | — | — |
| 5 | llm/ | replaces llm.go | same | replaces llm.go | may adopt | — |
| 5 | pipeline/ | replaces output.go | same | replaces output.go | — | — |

---

## Development Strategy

### Local Development with go.work

During development, use Go workspaces to avoid publishing after every change:

```bash
# ~/src/go.work (NOT committed)
go 1.25
use ./go-engine
use ./go-search
use ./go-job
use ./go-startup
```

### Docker Build

Services using go-engine need it available at build time. Two options:

**Option A: Published module** (preferred for production)
```dockerfile
# go-engine is a normal go.mod dependency, fetched by go mod download
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /app ./cmd/...
```

**Option B: Local copy** (during development before first release)
```dockerfile
COPY ../go-engine /go-engine
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /app ./cmd/...
```

### Versioning

- Start at **v0.x.x** — no compatibility guarantees during extraction
- Bump minor for each phase completion (v0.1.0 = Phase 1, v0.2.0 = Phase 2...)
- **v1.0.0** after all 3 services migrated and API stable (Phase 9)

---

## Success Criteria

| Metric | Before | After |
|--------|--------|-------|
| Duplicated engine LOC | ~3500 x 3 = ~10500 | 0 (single source) |
| Time to fix engine bug | 3 repos, 3 PRs, 3 deploys | 1 repo, 1 PR, 3 rebuilds |
| go-startup extraction quality | go-readability | go-trafilatura (3-tier) |
| go-startup proxy protection | None (exposes server IP) | Residential proxy via go-stealth |
| Regression risk on fork | High (manual sync) | Zero (shared module) |
| Test coverage of shared code | Per-service (sparse) | Centralized (comprehensive) |
