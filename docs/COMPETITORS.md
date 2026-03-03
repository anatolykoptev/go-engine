# go-engine: Competitor Analysis

> Deep code analysis of competitors across 5 niches.
> Date: 2026-03-03 | Tool: go-code `repo_analyze`

## 1. Search Aggregation

### Djarvur/ddg-search (Go, 7 stars)

- **Repo:** https://github.com/Djarvur/ddg-search
- **Scope:** DDG scraper + Perplexity API client + page-dump (URL→markdown)
- **Files:** 18 Go files, clean architecture (`internal/search`, `internal/perplexity`, `internal/dump`)

**Key patterns:**

| Pattern | Implementation | go-engine gap |
|---------|---------------|---------------|
| Rate limit detection | `Parser.IsRateLimitPage()` — checks DDG HTML for captcha/403 markers before parsing | No detection — captcha pages silently return empty results |
| Exponential backoff + jitter | `calculateDelay()` with cap + `randomJitter()` via `crypto/rand` | Delegated to go-stealth `RetryDo`, no DDG-specific rate limit logic |
| Configurable retry | `RetryOptions{MaxRetries, InitialDelay, MaxDelay, BackoffFactor, Jitter}` — full config | `RetryConfig` exists but no DDG-specific retry options |
| Markdown output | `SearchMarkdown()` — results formatted as markdown for LLM consumption | Only structured `[]Result`, consumers format themselves |
| Perplexity fallback | Separate client for Perplexity API as second-tier search | Only DDG/Startpage/SearXNG |
| Debug logging | `debugf()` controlled by `DDG_DEBUG` env var | slog.Debug — always on |

**Code highlights:**
- `internal/search/parser.go:76` — `IsRateLimitPage()` checks for DDG CAPTCHA patterns
- `internal/search/client.go:62` — `isRateLimited()` detects 429 + 403 + CAPTCHA in response body
- `internal/perplexity/client.go:239` — `calculateDelay()` with `crypto/rand` jitter (not `math/rand`)

### mamei16/LLM_Web_search (Python, 276 stars)

- **Repo:** https://github.com/mamei16/LLM_Web_search
- **Scope:** DDG + SearXNG search backend for local LLMs, with hybrid retrieval
- **Files:** 15 Python files, retrieval pipeline focus

**Key patterns:**

| Pattern | Implementation | go-engine gap |
|---------|---------------|---------------|
| Hybrid retrieval | FAISS (embedding) + BM25 + SPLADE — three retrievers combined | No retrieval — raw results passed to LLM |
| Weighted Reciprocal Rank | `weighted_reciprocal_rank()` — fuses results from multiple retrievers with weights | Multi-source results concatenated, no fusion |
| Semantic chunking | `BoundedSemanticChunker` — splits by embedding cosine distances between sentences | No chunking at all |
| Deduplication | `filter_similar_embeddings()` + `bow_filter_similar_texts()` — embedding + BoW dedup | URL dedup only |
| Async parallel fetch | `async_fetch_chunk_websites()` — fetch + immediate chunking | `ParallelFetch` — fetch without chunking |
| SearXNG categories | `retrieve_from_searxng()` supports `categories` param (general/news/science) | SearXNG search without category support |

**Code highlights:**
- `retrieval.py:246` — WRR fusion algorithm (portable to Go)
- `chunkers/semantic_chunker.py:131` — semantic chunking with breakpoint detection
- `retrieval.py:98` — `retrieve_from_webpages()` async pipeline: fetch → chunk → embed → rank

---

## 2. Content Extraction

### firecrawl/firecrawl (TypeScript, 87K stars)

- **Repo:** https://github.com/firecrawl/firecrawl
- **Scope:** Web → LLM-ready markdown/JSON, full SaaS with API
- **Files:** 390 TS files, complex monorepo (`apps/api/src/`)

**Key patterns:**

| Pattern | Implementation | go-engine gap |
|---------|---------------|---------------|
| Queue-based pipeline | `queue-service` + `queue-jobs` via Redis/BullMQ with priorities | `pipeline/` is synchronous, no queues |
| Per-team rate limiter | `services/rate-limiter` — rate limiting per API key/team | Rate limiting only external (DDG), no internal |
| Multi-tier extraction | `extract/` → `fire-0/` → `extractSmartScrape` (LLM-backed fallback) | trafilatura→goquery→regex, no LLM fallback |
| Concurrency reconciler | `concurrency-queue-reconciler` — ensures N parallel scrapes don't overload target | `ParallelFetch` has no concurrency limit (unbounded goroutines) |
| PDF/DOCX extraction | Non-HTML format parsing | HTML only |
| GCS PDF cache | `gcs-pdf-cache` — cloud cache for heavy resources | Memory + Redis only |
| HTML-to-Markdown client | Dedicated `html-to-markdown-client` with API | go-trafilatura does text+markdown, no dedicated markdown path |
| Retry tracker | `scraper/scrapeURL/lib/retryTracker` — per-URL retry state | Retry is per-request, no per-URL state |

**Architecture insight:** Firecrawl's `extract/` package has two generations (`extract/` and `fire-0/`), showing evolution from rule-based to LLM-backed extraction. The LLM fallback (`extractSmartScrape`) is called only when rule-based extraction quality is below threshold.

### unclecode/crawl4ai (Python, 61K stars)

- **Repo:** https://github.com/unclecode/crawl4ai
- **Scope:** LLM-friendly web crawler, strategy pattern everywhere
- **Files:** 79 Python files, strategy-driven design

**Key patterns:**

| Pattern | Implementation | go-engine gap |
|---------|---------------|---------------|
| Strategy pattern | 7 swappable strategies: proxy, chunking, markdown, content_filter, content_scraping, extraction, deep_crawling | Hardcoded chains, no strategy interface |
| Proxy strategy | `RoundRobinProxyStrategy` + session pinning — proxy bound to session, rotated round-robin | go-stealth ProxyPool exists but no session pinning |
| Chunking strategies | 4 types: character, semantic, NER, regex (`chunking_strategy.py`) | No chunking at all |
| Markdown generation | `MarkdownGenerationStrategy` — content-type-aware markdown with metadata | trafilatura markdown without content-type awareness |
| Content filtering | BM25 + Pruning + Relevance — filters irrelevant content BEFORE sending to LLM | No pre-LLM filtering |
| Auto-chunking detection | `_needs_chunking()` — automatically detects if HTML needs chunking by size | Everything goes to LLM as-is |
| Per-strategy retry | `execute_with_retry()` — retry wrapper for each strategy step | Retry only at fetch level |
| Deep crawling | BFS/DFS/BFF strategies for multi-page crawling | Single-page only |

**Code highlights:**
- `crawl4ai/proxy_strategy.py:225` — `get_proxy_for_session()` with session ID binding
- `crawl4ai/content_filter_strategy.py` — 1082 lines of content filtering (BM25, pruning, relevance)
- `crawl4ai/extraction_strategy.py` — 2214 lines, LLM-based + cosine + JSON CSS extraction

### mishushakov/llm-scraper (TypeScript, 6.2K stars)

- **Repo:** https://github.com/mishushakov/llm-scraper
- **Scope:** Structured data extraction from any webpage via LLM
- **Files:** 12 TS files, minimal but powerful

**Key patterns:**

| Pattern | Implementation | go-engine gap |
|---------|---------------|---------------|
| Schema-based extraction | Zod schema → LLM structured output → typed result | `llm.StructuredOutput` hardcoded (Answer+Facts) |
| Preprocessing modes | 6 modes: `html`, `raw_html`, `markdown`, `text`, `image`, `custom` | Only text and markdown |
| Streaming | `stream()` — streaming LLM output | No streaming support |
| Code generation | `generate()` — LLM generates scraper code | Not applicable |
| HTML cleanup | `cleanup.ts` — removes scripts, styles, noscript, SVGs before extraction | No pre-extraction cleanup (trafilatura handles internally) |

**Architecture insight:** Entire library is ~200 LOC. The key insight is the `preprocess()` function that converts page to the optimal format BEFORE sending to LLM, reducing token usage by 50-80%.

---

## 3. LLM Orchestration & Pipeline

### nextlevelbuilder/goclaw (Go, 402 stars)

- **Repo:** https://github.com/nextlevelbuilder/goclaw
- **Scope:** Multi-agent AI gateway, Go single binary
- **Files:** 661 Go files, production-grade architecture

**Key patterns:**

| Pattern | Implementation | go-engine gap |
|---------|---------------|---------------|
| Retry with hooks | `RetryHookFunc` — callback on every retry (for logging/metrics) | `RetryDo` without hooks |
| LLM span tracing | `emitLLMSpan()` — OpenTelemetry-compatible tracing per LLM call with timing, model, messages | Only `metrics.Incr("llm_calls")` counter |
| Cron retry | `cron/retry.go` — resubmit failed jobs on schedule | Fail = permanent fail |
| Multi-provider LLM | 13+ providers with routing logic | Single endpoint via CLIProxyAPI |
| Retry with reset | `retrySend()` — retry + reset function (state cleanup before retry) | No reset callback |
| Listener retry states | `buildListenerRetryStates()` — per-listener retry configuration | Global retry config |

**Code highlights:**
- `internal/providers/retry.go:76` — `Do()` with `RetryHookFunc` context injection
- `internal/providers/retry.go:137` — generic `retrySend()` with reset callback pattern
- `internal/cron/retry.go` — cron-based job retry (separate from HTTP retry)

### deepset-ai/haystack (Python, 18K stars)

- **Repo:** https://github.com/deepset-ai/haystack
- **Scope:** Pipeline orchestration framework for search + LLM
- **Files:** 162 Python files, mature component architecture

**Key patterns:**

| Pattern | Implementation | go-engine gap |
|---------|---------------|---------------|
| Component protocol | `@component` decorator → `run()` + typed inputs/outputs → auto-wiring | Manual composition in consumer code |
| Pipeline as DAG | Topological sort, parallel execution of independent nodes | Linear fetch→extract→summarize |
| Cache component | `CacheChecker` — checks DocumentStore before processing | Cache only at fetch level |
| Router component | Conditional routing by content/metadata | No routing |
| Typed errors | Per-component error types (`SearchApiError`, `SerperDevError`) | Generic `error` everywhere |
| Connectors | `connectors/` — standardized external service integration | Ad-hoc integration per source |

**Architecture insight:** Haystack's pipeline model is the most mature. Components declare input/output types, and the framework auto-validates connections at build time. This prevents runtime type mismatches. go-engine could benefit from a lightweight version of this pattern.

---

## 4. Summary: Actionable Improvements

### Priority 1 — Low effort, high impact

| # | Improvement | Source | Package | Effort |
|---|------------|--------|---------|--------|
| 1 | DDG rate limit detection (captcha/403 check before parse) | Djarvur/ddg-search | `search/ddg.go` | ~30 LOC |
| 2 | Semaphore in `ParallelFetch` (maxConcurrency param) | Firecrawl | `pipeline/output.go` | ~15 LOC |
| 3 | Error collection in `ParallelFetch` (return `[]error` not silently skip) | Haystack | `pipeline/output.go` | ~20 LOC |
| 4 | Retry hooks callback (`RetryHookFunc` on each retry) | GoClaw | `fetch/fetcher.go` | ~20 LOC |

### Priority 2 — Medium effort, high impact

| # | Improvement | Source | Package | Effort |
|---|------------|--------|---------|--------|
| 5 | Content chunking (character-based with overlap) | Crawl4ai, LLM_Web_search | new `text/chunk.go` | ~100 LOC |
| 6 | Result dedup + WRR fusion for multi-source search | LLM_Web_search | `search/filter.go` | ~80 LOC |
| 7 | LLM streaming support (`CompleteStream` method) | llm-scraper | `llm/client.go` | ~80 LOC |
| 8 | Pre-LLM content truncation by token budget | Firecrawl, llm-scraper | `text/` or `llm/` | ~50 LOC |

### Priority 3 — High effort, architectural

| # | Improvement | Source | Package | Effort |
|---|------------|--------|---------|--------|
| 9 | LLM-backed extraction fallback (when rule-based < threshold) | Firecrawl | `extract/` | ~150 LOC |
| 10 | Content relevance filtering (BM25 pre-filter before LLM) | Crawl4ai | `text/` or `extract/` | ~200 LOC |
| 11 | Composable pipeline (Component interface with typed I/O) | Haystack | `pipeline/` | ~300 LOC |
| 12 | Session-pinned proxies for multi-page operations | Crawl4ai | go-stealth (upstream) | ~100 LOC |

### Patterns to NOT adopt

| Pattern | Source | Why skip |
|---------|--------|----------|
| Queue-based pipeline (BullMQ) | Firecrawl | Overkill — go-engine is a library, queues belong in consumer services |
| Multi-provider LLM routing | GoClaw | CLIProxyAPI already handles provider routing |
| Deep crawling (BFS/DFS) | Crawl4ai | Out of scope — go-engine is single-page focused |
| Schema-based LLM extraction | llm-scraper | Zod/schema approach is TS-specific; Go consumers define their own JSON structs |
| PDF/DOCX parsing | Firecrawl | Low priority — MCP tools deal with web HTML, not documents |

---

## 5. Competitor Matrix

| Capability | go-engine | Djarvur/ddg-search | Firecrawl | Crawl4ai | LLM_Web_search | llm-scraper | GoClaw | Haystack |
|-----------|:---------:|:--:|:--:|:--:|:--:|:--:|:--:|:--:|
| **Lang** | Go | Go | TS | Python | Python | TS | Go | Python |
| **Search engines** | DDG+SP+SearXNG | DDG+Perplexity | - | - | DDG+SearXNG | - | - | Serper+SearchAPI |
| **Proxy/TLS** | go-stealth | - | Built-in | Multi-strategy | - | - | - | - |
| **HTML extraction** | 3-tier fallback | - | LLM-backed | 7 strategies | readability | LLM-only | - | - |
| **Markdown** | trafilatura | html-to-md | Dedicated | Strategy | - | Turndown | - | - |
| **Chunking** | - | - | - | 4 strategies | 3 strategies | - | - | - |
| **Content filter** | - | - | - | BM25+Prune | Embedding+BM25 | - | - | - |
| **LLM client** | OpenAI-compat | - | Multi-provider | Multi-provider | - | AI SDK | 13 providers | - |
| **Streaming** | - | - | Yes | - | - | Yes | Yes | - |
| **Cache** | Memory+Redis tiered | - | Redis+GCS | - | - | - | - | DocumentStore |
| **Pipeline** | Linear | - | Queue-based | Strategy-based | - | - | Agent loop | DAG |
| **Rate limit detect** | - | Yes | Per-team | - | - | - | Per-listener | - |
| **Retry hooks** | - | - | Tracker | Per-strategy | - | - | Hook callback | - |
| **Observability** | Counters | debug env | - | - | - | - | OTel spans | - |
| **Stars** | - | 7 | 87K | 61K | 276 | 6.2K | 402 | 18K |
