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
| sqlc                        | 1.31.1  | `go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1`                       |
| goose                       | 3.27+   | `go install github.com/pressly/goose/v3/cmd/goose@latest`                    |
| golangci-lint               | 2.12+   | `brew install golangci-lint`                                                 |
| goreleaser                  | 2.17+   | `brew install goreleaser`                                                    |
| air (optional, live reload) | 1.66+   | `go install github.com/air-verse/air@latest`                                 |

sqlc is the one exact pin: its generated output is committed and CI diffs it, so
`make gen` refuses to run on any other version rather than produce drift.

The `Makefile` is the canonical task interface and only assumes these are on your
`PATH`:

```sh
make build   # frontend + backend -> ./transpondarrd
make run     # build, then run the server on :9797
make gen     # regenerate sqlc code after editing internal/store/queries
make gen-api # regenerate frontend/src/lib/api-types.ts from the OpenAPI spec
make notices # regenerate THIRD-PARTY-NOTICES.md after a dependency change
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

CI regenerates every committed generated file and fails if any differs from what
it produces — `internal/store/db` (`make gen`), `frontend/src/lib/api-types.ts`
(`make gen-api`), and `THIRD-PARTY-NOTICES.md` (`make notices`). Run the matching
target before you push.

`make notices` has the least obvious trigger, and it is narrower than "touched
`go.mod`": the file covers Go modules actually linked into the binary (not the
full module graph) and frontend *production* dependencies, so a devDependency
bump needs nothing.

## Local dev configuration

A `.env` file in the working directory is loaded on startup (see
[`.env.example`](.env.example)); real environment variables override it. Copy it
to `.env` to pin a dev API key and integration values.

`.env.local` is read first and so outranks `.env`. It is the per-checkout tier:
put anything true of *this* working copy alone there — a port, a stub endpoint —
and keep `.env` for what every checkout shares. Neither file is committed. This
matters most in a git worktree, where `.env` is often shared with the main
checkout, so editing it would change every checkout at once.

## Seeding a dev database

A library that has been running for weeks is the population that shows layout
bugs; two hand-added titles and a lot of empty states is not. `make seed` builds
one, and serves the stubs the two live-fetching screens need:

```
make seed                      # seed ./data and serve the stubs until ctrl-c
go run ./cmd/devseed --reset   # wipe and reseed an existing database
go run ./cmd/devseed --seed-only
```

It prints the endpoints its stubs bound to — they take port 0, so several
worktrees can run their own at once — along with the environment lines that
point the server at them. Put those in `.env.local`, or pass
`--write-env-local` to have the command write the file itself. Then run the
server as usual; every screen including Releases and Discovery has something on
it, with no network access and no real credentials.

The fixtures live in `internal/devdata`. The seeder and both stubs read one set,
so a search for a seeded title returns release names that fit its run; those
release names are synthetic. Three further titles are served by the stubs and
deliberately not seeded, so the add dialog still has something to add offline.

There is deliberately no fake download client. The seeded grab rows produce
every status the Activity queue derives — downloading, stuck and deferred — and
every status the History tab lists. They can't produce the queue's live columns:
progress, client state and the abandon countdown are all read from a download
client, so those stay empty. For the same reason the printed environment block
sets `TRANSPONDARR_QBIT_URL` to nothing. If the importer connected to a real
qBittorrent it would find none of the seeded info hashes and fail every seeded
grab row after five minutes. If your `.env.local` already sets that variable,
blank it yourself — devseed won't overwrite an existing `.env.local`.

Seeding refuses to write over an existing database unless you pass `--reset`,
and `--reset` refuses a database outside the working directory unless you also
pass `--force`.

One seeded state doesn't survive the server starting. The calendar footer
separates a title nobody has asked the provider about from one the provider
publishes no dates for, and `airing-sync` runs at startup and stamps the first
kind, so within a tick every seeded title reads as asked. That's the job doing
its work, not a gap in the fixtures: `--seed-only` leaves the unasked title in
place. That is where `TestSeedProducesBothCalendarAbsences` reads it.

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

## Changelog

`CHANGELOG.md` is not a summary written at release time — it **is** the release
notes. `scripts/release-notes.sh` extracts the tag's section verbatim for the
GitHub Release, and the release workflow fails a tag with no matching section.
That check proves a section *exists*; only you can make it complete.

So add the entry in the PR that changes the behaviour, under `[Unreleased]`:

- **Which section:** `Added` / `Changed` / `Fixed` / `Security` for anything a
  user would notice; `Internal` for work that changes no observable behaviour.
  Refactors, test-only changes and doc edits usually need no entry at all.
- **Write it for someone who did not read the diff.** Lead with the symptom or
  the capability, not the mechanism, and say *why* where the reason isn't
  obvious. Match the surrounding entries: a bolded opening sentence, then the
  detail.
- **Add an `Upgrade notes` section** when an existing install changes on its own
  after upgrading — a migration that rewrites rows, a default that flips, a
  background pass that now creates data it didn't before. Say what changes, how
  it will look, what it costs, and how to opt out. These are the entries people
  are angriest to find out about afterwards.

Before tagging a release, read the whole `[Unreleased]` section back:

1. Check it against `git log` since the last tag — a behaviour change that
   landed without an entry is invisible from here.
2. Merge entries that describe one user-visible change across several PRs, and
   drop anything that turned out to be internal.
3. Rename the heading to the version and date, and open the release with a short
   paragraph on the theme — see the `0.5.0` and `0.4.0` entries.

## Conventions

- Keep new release sources / download clients / library targets behind their
  existing interfaces.
- Don't hardcode "episode" in the pipeline — use `domain.WantedItem`.
