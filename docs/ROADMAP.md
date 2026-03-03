# go-engine Roadmap

> **Module:** `github.com/anatolykoptev/go-engine` | **Current:** v0.7.0 | **Updated:** 2026-03-03

**See also:** [ARCHITECTURE.md](ARCHITECTURE.md) | [COMPETITORS.md](COMPETITORS.md)

## Completed

| Version | What | Packages |
|---------|------|----------|
| v0.1.0 | Scaffolding, text utilities, metrics | `text/`, `metrics/` |
| v0.1.1 | Tiered cache (Memory L1 + Redis L2) | `cache/` |
| v0.7.0 | HTTP fetch + proxy, content extraction, search engines, LLM client, pipeline | `fetch/`, `extract/`, `search/`, `llm/`, `pipeline/`, `sources/` (stub) |

9 packages, all tests passing, Go 1.26, quality grade A (88/100).

---

## Next: v1.0.0 — Architecture Upgrade

API freeze + composable framework. [Design doc](plans/2026-03-03-v1.0.0-design.md).

**`sources/` — Source interface + API client:**
- [ ] `Source` interface (`Name`, `Search`) + `Query`/`Result` types
- [ ] `APIClient` — generic JSON API helper with auth, rate limit, retry
- [ ] `AuthMethod` interface (`BearerAuth`, `NoAuthMethod`)

**Strategy interfaces + implementations:**
- [ ] `extract.Strategy` interface (existing `Extractor` satisfies it)
- [ ] `text.Chunker` interface + `CharacterChunker` (split with overlap, UTF-8 safe)
- [ ] `text.Filter` interface + `BM25Filter` (score chunks against query, return top-K)

**Pipeline upgrade:**
- [ ] `ParallelFetch` — bounded concurrency (semaphore), `[]FetchResult` with errors (breaking change)
- [ ] `pipeline.Pipeline` — orchestrator: sources → fetch → extract → chunk → filter → llm

**Cache:**
- [ ] `Tiered.GetOrFetch` — singleflight wrapper to prevent thundering herd

**Tests:**
- [ ] Integration tests — cross-package pipeline with httptest + mocks
- [ ] Benchmark tests — extract, cache, chunker, filter hot paths

---

## v1.1.0 — Search Quality

*Sources: [Djarvur/ddg-search](https://github.com/Djarvur/ddg-search), [LLM_Web_search](https://github.com/mamei16/LLM_Web_search), [karust/openserp](https://github.com/karust/openserp)*

**Rate limiting & anti-detection:**
- [ ] **DDG rate limit detection** — `IsRateLimitPage()`: check HTML for captcha/403 markers before parsing, avoid silent empty results. Djarvur checks body text for CAPTCHA patterns + HTTP 429/403 (`search/ddg.go`, ~30 LOC)
- [ ] **Startpage rate limit detection** — same for Startpage: detect "rate limited" pages before parsing (`search/startpage.go`, ~20 LOC)
- [ ] **crypto/rand jitter** — replace `math/rand` in retry delays with `crypto/rand` for unpredictable intervals. Djarvur uses `randomJitter()` via `crypto/rand.Read` + `binary.BigEndian` (`fetch/`, ~15 LOC)
- [ ] **Per-engine rate limiter** — `rate.Limiter` per search engine to avoid hammering one source. openserp does this per-engine with `golang.org/x/time/rate` (`search/`, ~40 LOC)

**Result quality:**
- [ ] **WRR result fusion** — `WeightedReciprocalRank()`: when combining DDG + SearXNG + Startpage, rank by weighted reciprocal position instead of naive concat. LLM_Web_search has portable implementation at `retrieval.py:246` (`search/filter.go`, ~80 LOC)
- [ ] **Snippet deduplication** — beyond URL dedup: BoW cosine on snippets to remove near-duplicates from different engines. LLM_Web_search has `bow_filter_similar_texts()` + `filter_similar_embeddings()` (`search/filter.go`, ~60 LOC)
- [ ] **SearXNG categories** — pass `categories` param (general/news/science/it) for targeted search. LLM_Web_search passes categories in SearXNG params (`search/searxng.go`, ~10 LOC)
- [ ] **Markdown output format** — `ResultsToMarkdown()`: format search results as numbered markdown for direct LLM consumption. Djarvur's `SearchMarkdown()` outputs `## [Title](URL)\nSnippet` format (`search/result.go`, ~30 LOC)

## v1.2.0 — Pipeline Robustness

*Sources: [Firecrawl](https://github.com/firecrawl/firecrawl), [Haystack](https://github.com/deepset-ai/haystack), [GoClaw](https://github.com/nextlevelbuilder/goclaw), [Crawl4ai](https://github.com/unclecode/crawl4ai)*

**ParallelFetch improvements:**
- [ ] **Bounded concurrency** — `maxConcurrency` semaphore via `chan struct{}` to prevent target overload and IP bans. Firecrawl's `concurrency-queue-reconciler` guarantees N parallel scrapes max (`pipeline/`, ~15 LOC)
- [ ] **Error propagation** — return `map[string]error` per-URL instead of silently skipping failures. Haystack has typed errors per component (`ComponentError`) (`pipeline/`, ~20 LOC)
- [ ] **Fetch + immediate chunk** — chunk content right after fetch in goroutine, not as separate step. LLM_Web_search's `async_fetch_chunk_websites()` does fetch→chunk in one pass (`pipeline/`, ~30 LOC)

**Retry & observability:**
- [ ] **Retry hooks** — `RetryHookFunc(ctx, attempt, err)` callback injected via context for logging/metrics on each retry. GoClaw's `retryHookFromContext()` pattern (`fetch/`, ~20 LOC)
- [ ] **Retry with reset** — `retrySend(ctx, name, resetFn, fn)` — reset callback to clean up state before retry attempt. GoClaw's pattern at `internal/cron/retry.go:55` (`fetch/`, ~15 LOC)
- [ ] **Per-URL retry tracker** — track retry state per URL across calls, avoid retrying permanently broken URLs. Firecrawl's `retryTracker` (`fetch/`, ~50 LOC)

**Content processing:**
- [ ] **Character chunking with overlap** — split large texts into chunks with configurable size + overlap for LLM. Crawl4ai has 4 strategies; start with character-based (`RecursiveCharacterTextSplitter` from LLM_Web_search) (new `text/chunk.go`, ~100 LOC)
- [ ] **Auto-chunking detection** — only chunk if content > threshold. Crawl4ai's `_needs_chunking()` checks HTML size before splitting (`text/chunk.go`, ~15 LOC)
- [ ] **Token budget truncation** — cut extracted content to fit model context window before LLM. llm-scraper's `preprocess()` reduces token usage 50-80% (`llm/` or `text/`, ~50 LOC)

## v1.3.0 — Extraction Quality

*Sources: [Firecrawl](https://github.com/firecrawl/firecrawl), [Crawl4ai](https://github.com/unclecode/crawl4ai), [llm-scraper](https://github.com/mishushakov/llm-scraper)*

**Extraction chain:**
- [ ] **HTML pre-cleanup** — strip `<script>`, `<style>`, `<noscript>`, `<svg>`, hidden elements BEFORE extraction. llm-scraper's `cleanup.ts` (60 LOC) does this; trafilatura handles some but not all (`extract/`, ~40 LOC)
- [ ] **LLM-backed extraction fallback** — when trafilatura + goquery yield < N chars or low quality, send cleaned HTML to LLM for extraction. Firecrawl's `extractSmartScrape` is called only below quality threshold (`extract/`, ~150 LOC)
- [ ] **Dedicated markdown path** — separate HTML→Markdown conversion (html-to-markdown lib) from text extraction. Firecrawl has dedicated `html-to-markdown-client`; currently go-engine mixes text and markdown in trafilatura (`extract/`, ~60 LOC)
- [ ] **Content-type aware preprocessing** — detect content type (article, forum, docs, list) and use different extraction strategy per type. Crawl4ai's `markdown_generation_strategy.py` adjusts output by content shape (`extract/`, ~80 LOC)

**Filtering & ranking:**
- [ ] **BM25 content relevance filter** — score extracted chunks against user query, send only top-K to LLM. Crawl4ai's `content_filter_strategy.py` (1082 LOC) does BM25 + pruning; we need a lightweight Go port (`text/relevance.go`, ~200 LOC)
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
| Strategy pattern for extraction | Crawl4ai | `extract/` | Medium | `ExtractionStrategy` interface → swap implementations (rule-based, LLM, hybrid) per call |

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
| **v1.0.0** | **Stable API, sources, tests, docs** |
| v1.1.0 | Search quality (8 items) |
| v1.2.0 | Pipeline robustness (12 items) |
| v1.3.0 | Extraction quality (8 items) |
