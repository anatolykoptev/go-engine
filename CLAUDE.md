# CLAUDE.md

Notes for coding agents working in go-engine: a Go library (module
`github.com/anatolykoptev/go-engine`, Go 1.26) that other services import to search the web,
fetch pages, extract text, chunk and rank it. Every document is indexed in
[docs/INDEX.md](docs/INDEX.md). Contribution rules: [CONTRIBUTING.md](CONTRIBUTING.md).

## Layout

One directory per package, no `cmd/`: `extract`, `fetch`, `imagesearch`, `llm`, `metrics`,
`pipeline`, `search`, `sources` (+ `sources/gosearch`), `text`, `webtext`, `websearch`. The
`cache` and `webcache` packages were removed (#21); `docs/ARCHITECTURE.md` and
`docs/ROADMAP.md` still describe the v1.3 shape, so trust the code over them.

`search` (direct fan-out, pacing, block cache) and `websearch` (provider adapters) both exist on
purpose; `imagesearch` re-declares `BrowserDoer` instead of importing `websearch` to keep the two
leaf packages independent.

## Gate

CI (`.github/workflows/ci.yml`) runs three jobs on `ubuntu-latest`, and `make` has the same names:

```sh
golangci-lint run ./...        # CI pins golangci-lint v2.9.0, the first build made with go1.26
go test -race -count=1 ./...
go build ./...
```

A docs-only PR (Markdown, `docs/`, licence, issue and PR templates) skips all three through the
`docs-scope` job in the same workflow. That block is canonical across the fleet and is compared
byte for byte, so do not edit it here.

Dependencies are **vendored** (`vendor/` is tracked). After any `go.mod` change run
`go mod vendor` and commit `vendor/`, or `go build` fails on an inconsistent `vendor/modules.txt`.
Use `GOWORK=off`. Versions come from release-please (Conventional Commits); never tag or edit
`CHANGELOG.md` or `.release-please-manifest.json` by hand.

## Invariants that are easy to break

- **The direct fetch tier keeps its SSRF guard.** `fetch` redirects to loopback must stay blocked
  and the policy comes from go-kit, not from the HTTP client library's weaker floor (see
  `fetch/fetcher_ssrf_direct_test.go`).
- **Per-source timeouts must not poison the block cache.** A parent deadline must not mark a host
  blocked; a per-source timeout does. `search/direct_timeout_test.go` pins this.
- **`DirectStats` keeps `Attempted == OK + Empty + Failed`**, with `Shed` counted separately so
  rationing alone never trips degraded mode (`search/direct_stats_test.go`).
- **Strategy and `Extract` take a `context.Context`** and `extract.Format` is an enum; both were
  breaking changes in v1.3. Keep the three-tier fallback order trafilatura, goquery, regex.
- **This repository is public.** No hostnames, operator paths, account names, tokens or personal
  data in any tracked file, tests and fixtures included.
