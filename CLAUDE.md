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
