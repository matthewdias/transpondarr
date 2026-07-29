# Contributing to Transpondarr

## Stack

- **Backend:** Go — `chi` + [Huma](https://huma.rocks/) (typed REST, OpenAPI 3.1),
  SQLite (`modernc.org/sqlite`, pure-Go) + `sqlc` + `goose`. Ships as a single
  binary with the frontend embedded.
- **Frontend:** React + TypeScript (Vite), embedded via `embed.FS`.
- **Packaging:** GoReleaser + distroless Docker (multi-arch).

## Toolchain

Transpondarr pins its toolchain so builds are reproducible. **mise is optional** —
it just installs and pins the versions of the same tools you'd otherwise install
yourself.

### With mise (recommended)

```sh
mise install
```

This reads `mise.toml` and installs Go, Node, and every dev tool at the pinned
versions.

### Without mise

Install these yourself (versions are what CI uses; the pinned versions live in
`mise.toml`):

| Tool                        | Version | Install                                                                      |
| --------------------------- | ------- | ---------------------------------------------------------------------------- |
| Go                          | 1.26+   | https://go.dev/dl/ (or the standard `GOTOOLCHAIN` from `go.mod`)             |
| Node                        | 24+     | https://nodejs.org (or nvm/asdf)                                             |
| sqlc                        | 1.31+   | `brew install sqlc` or `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest` |
| goose                       | 3.27+   | `go install github.com/pressly/goose/v3/cmd/goose@latest`                    |
| golangci-lint               | 2.12+   | `brew install golangci-lint`                                                 |
| goreleaser                  | 2.17+   | `brew install goreleaser`                                                    |
| air (optional, live reload) | 1.66+   | `go install github.com/air-verse/air@latest`                                 |

The `Makefile` is the canonical task interface and only assumes these are on your
`PATH`:

```sh
make build   # frontend + backend -> ./transpondarrd
make run     # build, then run the server on :9797
make gen     # regenerate sqlc code after editing internal/store/queries
make lint
make test
make dev     # live-reload API (air)
```

`make test` runs the Go suite under the race detector, which needs a C toolchain
(`gcc` or `clang`) because the race runtime links via cgo on Linux. The shipped
binary is still built `CGO_ENABLED=0` and stays pure Go.

The frontend suite is split into two vitest projects: `unit` runs the pure-logic
suites listed in `frontend/vite.config.ts` without a DOM, and `dom` runs
everything else under happy-dom. New suites land in `dom` by default; add a
pure-logic one to the list to keep it out of the slower lane.

`make build`, `lint`, or `test` installs a fast pre-commit hook (`git config
core.hooksPath .githooks`) that runs `gofmt` / `prettier --check` on staged files — the same
formatting CI enforces, caught before the commit instead of minutes later.
Bypass with `git commit --no-verify`.

## Local dev configuration

A `.env` file in the working directory is loaded on startup (see
[`.env.example`](.env.example)); real environment variables override it. Copy it
to `.env` to pin a dev API key and integration values.

## Architecture

- **Content-type-agnostic core** (`internal/core/domain`): the pipeline is keyed
  on `WantedItem`, so movies slot in later without a rewrite.
- **Pluggable interfaces:** `Indexer` (Torznab + other native integrations),
  `DownloadClient` (qBittorrent), `LibraryTarget` (media-server layout now; a
  drop-folder later).

## Layout

```
cmd/transpondarrd      server entrypoint
internal/config        env-based configuration
internal/server        chi + Huma API, forms auth (sessions) + machine API key, embedded SPA
internal/store         SQLite: goose migrations + sqlc query layer (internal/store/db)
internal/core/domain   content-type-agnostic model (Title / WantedItem)
internal/core/metadata Provider interface + anilist adapter + read-through cache
internal/core/indexer  Indexer interface + torznab
internal/core/download Download client interface + qbittorrent adapter
internal/core/library  LibraryTarget interface + mediaserver adapter
web/                   embeds frontend build output (web/dist)
frontend/              Vite + React + TypeScript source
```

## Database changes

1. Add a goose migration under `internal/store/migrations`.
2. Add/adjust queries in `internal/store/queries`.
3. Run `make gen` to regenerate `internal/store/db`.

## Conventions

- Keep new release sources / download clients / library targets behind their
  existing interfaces.
- Don't hardcode "episode" in the pipeline — use `domain.WantedItem`.
