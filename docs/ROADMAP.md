# go-engine Roadmap

> **Module:** `github.com/anatolykoptev/go-engine` | **Current:** v1.3.0 | **Updated:** 2026-03-03

**See also:** [ARCHITECTURE.md](ARCHITECTURE.md) | [COMPETITORS.md](COMPETITORS.md)

## Completed

| Version | What | Packages |
|---------|------|----------|
| v0.1.0 | Scaffolding, text utilities, metrics | `text/`, `metrics/` |
| v0.1.1 | Tiered cache (Memory L1 + Redis L2) | `cache/` |
| v0.7.0 | HTTP fetch + proxy, content extraction, search engines, LLM client, pipeline | `fetch/`, `extract/`, `search/`, `llm/`, `pipeline/`, `sources/` (stub) |
| v1.0.0 | Stable API: sources, strategy interfaces, pipeline orchestrator, singleflight cache | `sources/`, `extract/`, `text/`, `cache/`, `pipeline/` |
| v1.1.0 | Search quality: unified Result, rate limit detection, WRR fusion, dedup, categories, markdown | `search/`, `sources/`, `llm/`, `pipeline/` |
| v1.2.0 | Pipeline robustness: fetch+chunk, auto-chunking, token budget, retry hooks/reset/tracker | `text/`, `llm/`, `pipeline/`, `fetch/` |
| v1.3.0 | Extraction quality: format enum, LLM fallback, cleanup, system prompts | `extract/`, `llm/`, `pipeline/` |

9 packages, 280 tests, benchmarks, Go 1.26. [v1.0.0 design](plans/2026-03-03-v1.0.0-design.md) | [v1.1.0 design](plans/2026-03-03-v1.1.0-design.md).

**v1.3.0 delivered:**
- [x] **Configurable system prompt** — `CompleteWithSystem(ctx, system, prompt)` for task-specific LLM instructions (`llm/client.go`)
- [x] **Enhanced Tier 2 cleanup** — extended goquery selectors with ARIA roles, ad patterns, hidden elements, attribute stripping (`extract/goquery.go`)
- [x] **Tokenizer fast-path** — `StripScriptStyle` using `html.NewTokenizer` for large bodies >500KB (`extract/cleanup.go`)
- [x] **Context in Strategy interface** — `Strategy.Extract` gains `context.Context` parameter (breaking change) (`extract/strategy.go`)
- [x] **Format enum** — `FormatText`/`FormatMarkdown`/`FormatHTML` with `WithFormat` option; `Result.Markdown` removed (breaking change) (`extract/`)
- [x] **LLM fallback extraction** — `WithLLMFallback` + `WithMinExtractChars` for thin content recovery (`extract/extractor.go`)
- [x] **Pipeline integration** — ctx flows through pipeline to LLM fallback; verified with integration test (`pipeline/`)

---

## Next: v1.4.0

Not yet scoped. Candidates from backlog below, prioritized by consumer needs.

---

## Future

Not scheduled. Evaluate based on consumer needs.

**Architecture:**

| Idea | Source | Package | Effort | Notes |
|------|--------|---------|--------|-------|
| Composable pipeline | Haystack | `pipeline/` | High | `Component` interface with typed I/O, DAG execution with topological sort, auto-validation of connections at build time |
| Typed errors per package | Haystack | all | Medium | `SearchError`, `FetchError`, `ExtractError` instead of generic `error`; enables `errors.As` pattern in consumers |
| Router component | Haystack | `pipeline/` | Medium | Conditional routing by content type, query domain, language — dispatch to different extraction/LLM chains |
| Pipeline-level cache | Haystack | `pipeline/` | Low | Cache checker before processing: skip fetch+extract if cached result exists for query |

**Search & sources:**

| Idea | Source | Package | Effort | Notes |
|------|--------|---------|--------|-------|
| Perplexity API as search source | Djarvur/ddg-search | `search/` | Low | Fallback search via Perplexity Sonar API when DDG/SearXNG fail |
| Hybrid retrieval (embedding + BM25) | LLM_Web_search | `search/` | High | FAISS + BM25 + SPLADE fusion — needs local embedding model |
| Internal rate limiter | Firecrawl | `pipeline/` | Medium | Per-consumer rate limiting: cap requests/sec per API key or team |

**Fetch & proxy:**

| Idea | Source | Package | Effort | Notes |
|------|--------|---------|--------|-------|
| Session-pinned proxies | Crawl4ai | go-stealth | Medium | `get_proxy_for_session(session_id)` — bind proxy to session for multi-page crawls |
| Per-listener retry states | GoClaw | `fetch/` | Medium | Different retry configs per target domain or operation type |

**LLM & observability:**

| Idea | Source | Package | Effort | Notes |
|------|--------|---------|--------|-------|
| OTel span tracing | GoClaw | `llm/`, `metrics/` | Medium | `emitLLMSpan()` with timing, model, token count, error — replace simple counters |
| Configurable output schema | llm-scraper | `llm/` | Medium | Zod-like schema → `generateObject` pattern; consumers define expected JSON structure |
| LLM streaming | llm-scraper | `llm/` | Medium | `CompleteStream()` returning `io.Reader` or channel for long summarizations |
| Cron retry | GoClaw | `pipeline/` | Medium | Resubmit failed pipeline operations on schedule |

**Content processing:**

| Idea | Source | Package | Effort | Notes |
|------|--------|---------|--------|-------|
| Semantic chunking | Crawl4ai, LLM_Web_search | `text/` | High | Embedding cosine distances between sentences → split at topic boundaries |
| NER-based chunking | LLM_Web_search | `text/` | High | Token classification model detects semantic boundaries |
| PDF/DOCX extraction | Firecrawl | `extract/` | High | Non-HTML format support via external libs |
| Content-type aware preprocessing | Crawl4ai | `extract/` | Medium | Detect content type (article, forum, docs, list) and adjust extraction strategy |
| Per-strategy retry | Crawl4ai | `extract/` | Low | Retry individual extraction tiers, not the whole chain |

---

## Versioning

| Version | Milestone |
|---------|-----------|
| v0.1.0 | Scaffolding + text/metrics |
| v0.1.1 | Cache |
| v0.7.0 | All core packages |
| v1.0.0 | Stable API, sources, strategy interfaces, pipeline orchestrator, 194 tests |
| v1.1.0 | Search quality: unified Result, rate limits, WRR fusion, dedup, categories, markdown — 215 tests |
| v1.2.0 | Pipeline robustness: fetch+chunk, auto-chunking, token budget, retry hooks/reset/tracker — 264 tests |
| **v1.3.0** | **Extraction quality: format enum, LLM fallback, cleanup, system prompts — 280 tests** |
