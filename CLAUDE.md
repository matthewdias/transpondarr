# Transpondarr — Claude Code guide

Anime-focused PVR. Ships as a single Go binary
with an embedded React frontend. This file orients Claude and contributors on how
the codebase is built and organized. See `README.md` and `CONTRIBUTING.md` for
setup detail; the high-level product roadmap lives outside the repo.

## Stack

- **Go**: `chi` + [Huma](https://huma.rocks/) (typed REST, OpenAPI 3.1); SQLite via
  `modernc.org/sqlite` (pure-Go) + `sqlc` + `goose`.
- **Frontend**: React + TypeScript (Vite), embedded via `embed.FS`.
- Toolchain pinned in `mise.toml` (mise is optional). Builds are `CGO_ENABLED=0`.

## Build & run

The `Makefile` is the canonical interface (works with plain tools on `PATH`; mise
just pins their versions):

- `make build` — frontend (`web/dist`) + backend → `./transpondarrd`
- `make dev` — live-reload API (`air`)
- `make gen` — regenerate the sqlc layer after editing `internal/store/queries`
- `make gen-api` — regenerate `frontend/src/lib/api-types.ts` from the OpenAPI spec
- `make lint`, `make test`

Server listens on `:9797`; on first run it logs a generated API key (set
`TRANSPONDARR_API_KEY` to persist one). Health check is public: `GET /api/v1/health`.

## Layout

```
cmd/transpondarrd      entrypoint (graceful shutdown)
internal/config        env config (TRANSPONDARR_*)
internal/server        chi + Huma API, API-key auth, embedded SPA
internal/store         SQLite: goose migrations + sqlc layer (internal/store/db)
internal/core/domain   content-agnostic model (Title / WantedItem)
internal/core/indexer  Indexer iface + torznab adapter
internal/core/download  DownloadClient iface + qbittorrent adapter
internal/core/library  LibraryTarget iface + mediaserver adapter
web/                   embeds web/dist; frontend/ is the Vite source
```

## Architecture — the two boundaries

- **Content-agnostic core** (`internal/core/domain`): the pipeline
  (search → decide → grab → import) is keyed on `WantedItem`, never a hardcoded
  `Episode`. An episode is one item; a movie (a later `Format`) is a `Title` with a
  single item — so movies are additive, not a rewrite.
- **Pluggable interfaces**: `Indexer` (Torznab for breadth + native integrations
  later), `DownloadClient` (qBittorrent first), `LibraryTarget`
  (media-server layout now; a drop-folder later). Add new sources/clients/
  targets behind these interfaces.
- **Optional provider capabilities are type assertions, not wider interfaces.**
  `metadata.AiringProvider` (broadcast schedules) sits alongside `Provider`
  because paging a schedule costs one request per 50 episodes and `GetTitle` is
  on the request path. Two rules follow: a decorator must forward the capability
  *conditionally* (`metadata.Cached` returns a schedule-carrying wrapper only
  when its inner provider has one, so the assertion never lies), and the caller
  treats a missing capability as a supported configuration, not an error.

## Development process — TDD, red/green

Behaviour changes are test-driven. Work red → green → refactor:

1. **Red** — write the failing test first, run it, and confirm it fails *for the
   right reason* (the missing behaviour, not a compile error or typo).
2. **Green** — write the minimum implementation that makes it pass.
3. **Refactor** — clean up with the suite green; run `make test` (and `make lint`)
   before moving on.

- A bug fix starts with a test that reproduces the bug — it must fail on the old
  code before the fix lands.
- Never weaken or delete a failing test to get to green unless the test itself is
  wrong — and say so in the commit if it is.
- Use the shared `internal/coretest` harness (temp store + fake indexer/download/
  library) for pipeline-level tests instead of hand-rolling fixtures.
- **Test interval loops with `testing/synctest`, not sleeps** (see
  `internal/core/jobs/jobs_test.go`). Inside a bubble the clock is virtual, so
  "the first run waits a full interval" and "the schedule does not drift" become
  exact assertions instead of tolerant polls. Two gotchas: a pending
  `synctest.Wait()` takes priority over advancing the clock, so the test
  goroutine must `time.Sleep` to let a job's own sleep elapse (the `advance`
  helper does both); and bubbles forbid real I/O, so store- or network-backed
  tests stay on the real clock and synchronise on a channel.
- **The frontend suite runs in two vitest projects** (`frontend/vite.config.ts`):
  `unit` (node, no setup file) for the pure-logic suites named in `unitTests`, and
  `dom` (happy-dom + `src/test/setup.ts`) for everything else, which is the
  default — a new pure-logic suite runs correctly but slowly until it is added to
  the list. Building a DOM costs ~350ms per file, and a page test's cost is its
  renders: `render()` of a page is 300-400ms, so prefer asserting more per mount
  over more mounts. Note `vi.stubEnv("TZ", ...)` (`src/lib/calendar.test.ts`)
  only works under the default forks pool; worker threads ignore it.
- Pure mechanical changes (renames, generated code via `make gen`, docs) don't
  need a new test — everything that changes behaviour does.

## Conventions

- **Grab lifecycle (`internal/core/importer`): every status but `grabbed` is
  settled.** `grabbed` → `imported`, `failed` (errored, or absent from the
  download client past the grace period — the item reverts to wanted), or
  `import_deferred`. A directory payload is *not* automatically deferred:
  `resolvePayloadFile` resolves it to one episode file at completion time, so
  `library.Target.Place` stays file-only and `import_deferred` means "we looked
  and could not pick one file" — a real batch, or a payload nothing could
  disambiguate. Deferred grabs are never re-imported (the
  no-infinite-retry property) but stay in the scan for missing-from-client
  reconciliation, so a vanished payload still frees its item.
- **Periodic work goes on the job runner (`internal/core/jobs`), not a bare
  `go`.** Register by name with an interval in `main.go`; the runner owns panic
  containment, the "log failures only when `ctx.Err() == nil`" rule, and the
  drained shutdown that lets the store outlive in-flight work. It never cancels
  a job itself — `ctx` is the only shutdown signal, so work past a point of no
  return can still finish. A job closure must read its dependencies from the
  registry/service each run, not capture a snapshot, or live config edits stop
  applying. **The importer is deliberately still on its own goroutine** (its
  shutdown semantics predate the runner); migrating it is tracked separately.
- **Air dates are nullable everywhere, by design.** AniList's schedule coverage
  thins out badly before ~2015 and can skip episodes even for a modern title (it
  lists no entry for a multi-episode premiere block), so `wanted_items.airs_at`
  is null for real titles in normal operation — never treat its absence as an
  error. `internal/core/airing` syncs it in the background off the job runner and
  stamps `series.airing_synced_at` even when the provider returns nothing, which
  is what stops an unschedulable title being re-asked every tick. Aired times are
  immutable, so only a never-synced series pages full history; a resync passes
  `notYetAired` and fetches the tail.
- **Auth is forms-based** (`internal/core/auth`): the web UI logs in (username +
  argon2id password) and gets an httpOnly session cookie; the **API key** is for
  machine clients only (`X-Api-Key`). A request to `/api/*` is authorized by a
  valid session cookie, a valid API key, or — in `local` required-mode — a
  loopback/private request with no forwarding headers. The key is resolved as
  `TRANSPONDARR_API_KEY` env → DB-persisted → generate-and-persist (`resolveAPIKey`
  in `cmd/transpondarrd`); it survives restarts.
- **Config precedence: env (or a dev `.env`) → DB `settings` overrides → defaults.**
  Integrations (qBit/indexer/library) are editable live via the Settings UI;
  `internal/core/settings.Service` persists the change, rebuilds the client, and
  swaps it into `internal/core/clients.Registry`, which handlers and the importer
  read through — so edits apply without a restart.
- A DB change = a goose migration under `internal/store/migrations` + queries in
  `internal/store/queries` + `make gen`.
  - **Keep comments in `internal/store/queries/*.sql` ASCII-only.** sqlc's sqlite
    codegen miscounts byte vs. rune offsets: a doc comment between `-- name:` and
    the SQL body containing a multi-byte character — an em dash, which this repo's
    prose style uses everywhere — silently truncates the *emitted* SQL by that many
    bytes. The result compiles, `make gen` reports no error, and the query fails
    only at runtime. Also note `sqlc.arg(name)` is rejected by this dialect
    (`extraneous input '?1'`) — use positional `?` params.
  - **Migration numbers are a shared sequence — check `main` before claiming one.**
    Two branches that each add `000NN_*.sql` merge without a git conflict (different
    filenames) and leave a migration set goose sees as a duplicate version. Renumber
    on rebase; never merge past a collision.
- **`frontend/src/lib/api-types.ts` is generated and CI fails on drift**, so every
  backend schema change regenerates it and every concurrent branch conflicts there.
  Resolve by re-running `make gen-api` against the merged spec — never by hand-editing
  the conflict, which produces types that pass review and disagree with the server.
- **Quality profiles inform manual actions; they gate only automation.** A manual
  grab always succeeds in one request — the grab endpoint evaluates eligibility
  server-side at grab time and returns `ineligible_reason` on the 201, but never
  refuses (no confirm flag, no 422). Enforcement belongs to the scheduler's
  automatic choices; a manual grab is explicit user intent. Don't reintroduce a
  gate on manual paths (decided in PR #57).
- Don't hardcode "episode" in the pipeline — use `domain.WantedItem`.
- **Route handlers: group by resource; use a receiver when it earns its keep.**
  Each resource gets a `*_routes.go` file with a `register<Resource>Routes(api,
deps)` function; `registerRoutes` in `internal/server/routes.go` is the manifest.
  Multi-route resources that share deps/helpers (series, settings) hang handlers
  off a per-resource receiver struct (`seriesHandler`) built via
  `new<Resource>Handler(deps)`, with shared logic as methods (e.g. `requireSeries`,
  `matchReleases`); single-route groups (system, download, metadata, indexer) keep
  inline closures. The receiver earns its keep around 3+ routes or shared
  helpers/state. Handlers stay thin — push business logic into `internal/core`.
  Auth endpoints are plain-chi, not Huma.

## Comments

Code carries the *what*; comments carry only what the code cannot. Default to
none, and prefer a better name or a small helper over an explanation.

- **Budget:** one line. Exported declarations get the Go-standard one-line doc
  comment. Two lines is the ceiling for a genuinely subtle point; three or more
  needs a reason you could defend in review. Never a paragraph above a function.
- **Delete any comment that restates the next line.** `// Enrich the release with
  parsed attributes` above three assignments to `rel.ReleaseGroup/Resolution/
  DualAudio` is noise.
- **Keep only these:** a non-obvious *why* (a constraint, a rejected alternative,
  an external quirk), a deliberate limitation, or a load-bearing invariant a
  reader would otherwise break.
- **Package doc comments** are the one place for design stance — a short
  paragraph, stated once, not repeated on the functions inside.
- Durable rationale (why AniList numbering degrades, why batch import is deferred)
  belongs in this file, a commit message, or an issue — not stacked above a func.

Concretely, in `internal/core/decide`:

```go
// No — a paragraph explaining what the parameters already say.
// itemSet holds the numbers still worth grabbing. Already-had items are excluded
// so a fully-downloaded episode is not re-matched and re-grabbed; maxItem still
// spans every item (had or not) so absolute-numbering detection below is unaffected.

// Yes — the part the code can't say: maxItem intentionally counts had items.
// maxItem spans had items too, so absolute-numbering detection stays correct.
```

## External realities that shape the design

- **AniList**: ~30 req/min (degraded state, not the documented 90) and **no
  per-episode metadata** — cache aggressively, and degrade to absolute numbering
  rather than depending on TVDB.
- **Identification**: v1 relies on identity-by-construction (we chose the release);
  hash/AniDB identification and pre-existing-library import are deliberately
  out of v1's design.
