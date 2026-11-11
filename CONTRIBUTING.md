# Contributing to go-engine

Thanks for helping. go-engine is a Go library; a change is welcome when it keeps the packages
small, composable and covered by tests.

## Development

Requires Go 1.26 and, for linting, golangci-lint v2.9.0 or newer.

```sh
make test     # go test -race -count=1 ./...
make lint     # golangci-lint run ./...
make build    # go build ./...
```

CI runs exactly these three (`.github/workflows/ci.yml`: `lint`, `test`, `build`). If they pass
locally they pass there. A pull request that changes only documents skips them.

Dependencies are vendored. After changing `go.mod` run `go mod vendor` and commit `vendor/`.
Set `GOWORK=off` if you have a Go workspace. The optional pre-commit config
(`.pre-commit-config.yaml`) runs whitespace checks and golangci-lint.

## Commits

Conventional Commits: `feat:`, `fix:`, `perf:`, `refactor:`, `docs:`, `deps:`, `revert:`, and the
hidden types `chore:`, `ci:`, `test:`, `build:`, `style:`. release-please reads them to choose the
next version and write [CHANGELOG.md](CHANGELOG.md); do not edit the changelog or tag by hand.

## Pull requests

- Branch from `main`, keep the change focused, and fill in the pull-request template.
- Add or update tests for behaviour changes; the suite runs with the race detector.
- Update [docs/INDEX.md](docs/INDEX.md) when you add or rename a document.
- Do not add secrets, hostnames or personal data to any file; the repository is public.

## Reporting problems

Bugs and feature requests go through the issue templates. Report vulnerabilities privately as
described in [SECURITY.md](SECURITY.md).
