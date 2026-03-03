# go-engine Roadmap

> **Module:** `github.com/anatolykoptev/go-engine` | **Current:** v1.1.0 | **Updated:** 2026-03-03

**See also:** [ARCHITECTURE.md](ARCHITECTURE.md) | [COMPETITORS.md](COMPETITORS.md)

## Completed

| Version | What | Packages |
|---------|------|----------|
| v0.1.0 | Scaffolding, text utilities, metrics | `text/`, `metrics/` |
| v0.1.1 | Tiered cache (Memory L1 + Redis L2) | `cache/` |
| v0.7.0 | HTTP fetch + proxy, content extraction, search engines, LLM client, pipeline | `fetch/`, `extract/`, `search/`, `llm/`, `pipeline/`, `sources/` (stub) |
| v1.0.0 | Stable API: sources, strategy interfaces, pipeline orchestrator, singleflight cache | `sources/`, `extract/`, `text/`, `cache/`, `pipeline/` |
| v1.1.0 | Search quality: unified Result, rate limit detection, WRR fusion, dedup, categories, markdown | `search/`, `sources/`, `llm/`, `pipeline/` |

9 packages, 215 tests, benchmarks, Go 1.26. [v1.0.0 design](plans/2026-03-03-v1.0.0-design.md) | [v1.1.0 design](plans/2026-03-03-v1.1.0-design.md).

**v1.1.0 delivered:**
- [x] Unified `sources.Result` everywhere (deleted `search.Result`)
- [x] `ErrRateLimited` typed error + DDG captcha/403/429 detection
- [x] Startpage rate limit detection (HTML markers + HTTP status)
- [x] Per-engine `rate.Limiter` in `DirectConfig` (DDG, Startpage)
- [x] `FuseWRR()` — Weighted Reciprocal Rank fusion (k=60, configurable weights)
- [x] `DedupSnippets()` — BoW cosine similarity deduplication
- [x] `SearXNG.SearchQuery()` — `sources.Query` with `Extra["categories"]` support
- [x] `ResultsToMarkdown()` — numbered markdown for LLM consumption

---

## Next: v1.2.0 — Pipeline Robustness

*Sources: [Firecrawl](https://github.com/firecrawl/firecrawl), [Haystack](https://github.com/deepset-ai/haystack), [GoClaw](https://github.com/nextlevelbuilder/goclaw), [Crawl4ai](https://github.com/unclecode/crawl4ai)*

**Pipeline improvements:**
- [ ] **Fetch + immediate chunk** — chunk content right after fetch in goroutine, not as separate step. LLM_Web_search's `async_fetch_chunk_websites()` does fetch→chunk in one pass (`pipeline/`, ~30 LOC)
- [ ] **Auto-chunking detection** — only chunk if content > threshold. Crawl4ai's `_needs_chunking()` checks HTML size before splitting (`text/chunk.go`, ~15 LOC)
- [ ] **Token budget truncation** — cut extracted content to fit model context window before LLM. llm-scraper's `preprocess()` reduces token usage 50-80% (`llm/` or `text/`, ~50 LOC)

**Retry & observability:**
- [ ] **Retry hooks** — `RetryHookFunc(ctx, attempt, err)` callback injected via context for logging/metrics on each retry. GoClaw's `retryHookFromContext()` pattern (`fetch/`, ~20 LOC)
- [ ] **Retry with reset** — `retrySend(ctx, name, resetFn, fn)` — reset callback to clean up state before retry attempt. GoClaw's pattern at `internal/cron/retry.go:55` (`fetch/`, ~15 LOC)
- [ ] **Per-URL retry tracker** — track retry state per URL across calls, avoid retrying permanently broken URLs. Firecrawl's `retryTracker` (`fetch/`, ~50 LOC)

## v1.3.0 — Extraction Quality

*Sources: [Firecrawl](https://github.com/firecrawl/firecrawl), [Crawl4ai](https://github.com/unclecode/crawl4ai), [llm-scraper](https://github.com/mishushakov/llm-scraper)*

**Extraction chain:**
- [ ] **HTML pre-cleanup** — strip `<script>`, `<style>`, `<noscript>`, `<svg>`, hidden elements BEFORE extraction. llm-scraper's `cleanup.ts` (60 LOC) does this; trafilatura handles some but not all (`extract/`, ~40 LOC)
- [ ] **LLM-backed extraction fallback** — when trafilatura + goquery yield < N chars or low quality, send cleaned HTML to LLM for extraction. Firecrawl's `extractSmartScrape` is called only below quality threshold (`extract/`, ~150 LOC)
- [ ] **Dedicated markdown path** — separate HTML→Markdown conversion (html-to-markdown lib) from text extraction. Firecrawl has dedicated `html-to-markdown-client`; currently go-engine mixes text and markdown in trafilatura (`extract/`, ~60 LOC)
- [ ] **Content-type aware preprocessing** — detect content type (article, forum, docs, list) and use different extraction strategy per type. Crawl4ai's `markdown_generation_strategy.py` adjusts output by content shape (`extract/`, ~80 LOC)

**Filtering & ranking:**
- [ ] **Per-strategy retry** — retry individual extraction tiers, not the whole chain. Crawl4ai's `execute_with_retry()` wraps each strategy step (`extract/`, ~30 LOC)

**LLM client:**
- [ ] **LLM streaming** — `CompleteStream()` returning `io.Reader` or channel for long summarizations. llm-scraper's `stream()` and GoClaw both support streaming (`llm/`, ~80 LOC)
- [ ] **Configurable system prompt per task** — allow callers to set system prompt (summarize vs extract vs classify). Currently `Complete(prompt)` has no system prompt param; `CompleteParams` also lacks it (`llm/`, ~20 LOC)

---

## Future

Not scheduled. Evaluate after v1.3.0 based on consumer needs.

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
| Cron retry | GoClaw | `pipeline/` | Medium | Resubmit failed pipeline operations on schedule |

**Content processing:**

| Idea | Source | Package | Effort | Notes |
|------|--------|---------|--------|-------|
| Semantic chunking | Crawl4ai, LLM_Web_search | `text/` | High | Embedding cosine distances between sentences → split at topic boundaries |
| NER-based chunking | LLM_Web_search | `text/` | High | Token classification model detects semantic boundaries |
| PDF/DOCX extraction | Firecrawl | `extract/` | High | Non-HTML format support via external libs |
| Preprocessing modes | llm-scraper | `extract/` | Low | 6 modes: html, raw_html, markdown, text, image, custom — caller picks format |

---

## Versioning

| Version | Milestone |
|---------|-----------|
| v0.1.0 | Scaffolding + text/metrics |
| v0.1.1 | Cache |
| v0.7.0 | All core packages |
| v1.0.0 | Stable API, sources, strategy interfaces, pipeline orchestrator, 194 tests |
| **v1.1.0** | **Search quality: unified Result, rate limits, WRR fusion, dedup, categories, markdown — 215 tests** |
| v1.2.0 | Pipeline robustness (6 items) |
| v1.3.0 | Extraction quality (7 items) |
