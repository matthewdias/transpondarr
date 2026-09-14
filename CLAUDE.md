# Transpondarr — Claude Code guide

Anime-focused PVR. Ships as a single Go binary
with an embedded React frontend. This file orients Claude and contributors on how
the codebase is built and organized. See `README.md` and `CONTRIBUTING.md` for
setup detail; the high-level product roadmap lives outside the repo.

## Stack

The manifests name the dependencies; two constraints they don't explain:

- **SQLite is `modernc.org/sqlite` (pure-Go) and builds are `CGO_ENABLED=0`** — the
  single static binary is the shipping constraint, so a cgo-linked driver is not a
  drop-in swap.
- Toolchain versions are pinned in `mise.toml`, but mise is optional: the `Makefile`
  works with plain tools on `PATH`.

## Build & run

The `Makefile` is the canonical interface — `make help` lists the targets. What the
list doesn't show:

- **`make notices` has a narrower trigger than "touched `go.mod`."** The file covers
  Go modules *linked into the binary* (`go version -m`, not the module graph) and
  frontend *production* deps, so a devDependency bump needs nothing. CI fails on
  drift, mirroring the `api-types.ts` rule.
- `make lint` and `make test` are the full suite CI runs; locally prefer the scoped
  commands below.

Server listens on `:9797`; on first run it logs a generated API key (set
`TRANSPONDARR_API_KEY` to persist one). Health check is public: `GET /api/v1/health`.

## Local verification — scope it, CI runs the rest

`make test` and `make lint` are whole-repo: the entire vitest suite plus
`go test -race ./...`, and oxlint + prettier + `golangci-lint` over everything. CI
(`.github/workflows/ci.yml`) already runs all of it on every push and PR, so don't
reproduce it locally — run what the change touched.

- **Go** — `go test ./internal/core/decide/...` for the touched package(s), `-run TestFoo`
  to narrow during red/green. Plain `go test`; add `-race` when the change touches shared
  mutable state (`internal/core/jobs`, the importer goroutine, the `clients` registry and
  the handlers reading it), which is what the suite's `-race` exists for.
- **Scoping is safe because the test cache is content-addressed through the dependency
  graph** — editing `internal/store` does re-run `internal/core/importer`'s tests, it
  won't return a stale pass. `(cached)` means the compiled inputs are
  unchanged (a comment-only edit to a dependency legitimately keeps the hit); reach for
  `-count=1` only to force a test whose result depends on state Go can't see.
- **Lint** — `golangci-lint run ./internal/core/decide/...` for Go,
  `./node_modules/.bin/oxlint src/components` for the frontend. Skip `make lint`;
  `.githooks/pre-commit` already blocks unformatted Go/TS and CI runs the rest.
- **Frontend** (from `frontend/`) — `./node_modules/.bin/vitest run src/lib/format.test.ts`
  filters by filename; `--project unit` / `--project dom` runs one project, `-t "name"`
  one test. Use the direct binary rather than `npx` throughout — `npx` adds ~1.5s to
  vitest and ~0.8s to oxlint, on commands you run dozens of times a session.
- **vitest does not typecheck** — it strips types, so a file with a hard type error runs
  green, and `make lint` doesn't check types either. Otherwise `tsc -b` runs only inside
  `npm run build`, which is why CI is normally the first to report the error. After a
  frontend type change run `make typecheck`; it depends on `web-deps`, so a fresh worktree
  installs `frontend/node_modules` first. Without that install neither spelling reports on
  this project's TypeScript: `./node_modules/.bin/tsc -b` exits 127 on the missing binary,
  and `npm run typecheck` falls back to whatever `tsc` is on `PATH`.
  It covers `*.test.ts(x)` too (`tsconfig.app.json` includes all of `src`), and re-checks
  the whole program every run (~4.5s; `noEmit` without `composite` makes the tsbuildinfo
  near-useless), so run it once before committing rather than per edit.
- **Before committing, run `go vet ./...`** — 0.6s idle, ~2s after a change to a core
  package, and it type-checks `_test.go` files too, so it catches a broken test in a
  package you never ran. Don't scope it; the packages you didn't touch are the whole
  point. Then push and let CI run the suite.
- **Still regenerate locally** — `make gen` / `make gen-api` / `make notices`, per the
  Build & run list above. CI fails on drift in all three, and a round-trip is the slow
  way to learn it.
- **Run the full `make test` anyway** for cross-cutting work: `domain` or `store`
  signature changes, dependency bumps, a refactor spanning packages, or a release tag.

## Architecture — the two boundaries

- **Content-agnostic core** (`internal/core/domain`): the pipeline
  (search → decide → grab → import) is keyed on `WantedItem`, never a hardcoded
  `Episode`. An episode is one item; a movie (a later `Format`) is a `Title` with a
  single item — so movies are additive, not a rewrite.
- **Pluggable interfaces**: `Indexer` (Torznab for breadth + native integrations
  later), `download.Client` (qBittorrent first), `library.Target`
  (media-server layout now; a drop-folder later). Add new sources/clients/
  targets behind these interfaces.
- **Optional provider capabilities are type assertions, not wider interfaces.**
  `metadata.AiringProvider` (broadcast schedules) sits alongside `Provider`
  because paging a schedule costs one request per 50 episodes and `GetTitle` is
  on the request path. Two rules follow: a decorator must forward the capability
  *conditionally* (`metadata.Cached` returns a schedule-carrying wrapper only
  when its inner provider has one, so the assertion is never wrong), and the caller
  treats a missing capability as a supported configuration, not an error.

## Development process — TDD, red/green

Behaviour changes are test-driven. Work red → green → refactor:

1. **Red** — write the failing test first, run it, and confirm it fails *for the
   right reason* (the missing behaviour, not a compile error or typo).
2. **Green** — write the minimum implementation that makes it pass.
3. **Refactor** — clean up with the touched packages green, then `go vet ./...` before
   committing. Scope every step to what changed (see *Local verification*).

- A bug fix starts with a test that reproduces the bug — it must fail on the old
  code before the fix lands.
- Never weaken or delete a failing test to get to green unless the test itself is
  wrong — and say so in the commit if it is.
- Use the shared `internal/coretest` harness (temp store + fake indexer/download/
  library) for pipeline-level tests instead of hand-rolling fixtures.
- **`internal/devdata` is keyed on vocabularies, so a new value in one of them
  needs a fixture (#184).** It is the other harness, and the two are easy to
  confuse: `coretest` builds a temp world per test, where `devdata` seeds the
  persistent one `make seed` leaves behind. What a screen can show is a closed set
  each time — `deriveItemState`'s five item statuses, the four grab statuses, the
  outcomes `passReason` surfaces, the two values `schedule_checked` takes — and
  the seed covers only the values whoever wrote it listed. Four of #271's review
  findings were one shape: a named constant that no fixture created, hidden
  because the test counted rows in the table a value is stored in instead of
  running the query the screen runs. So adding a value to one of those sets, or
  reading them in a new combination, means seeding a fixture and asserting
  through the real query. Two things follow. A fixture must not pair values the
  code cannot pair either: a `failed` grab row with a `last_error` is
  unreachable, because settling clears it, and that pairing is why #273 went
  unnoticed for a round. And where a value is left unseeded on purpose, say so
  where its fixture would have been, because an unstated decision is
  indistinguishable from an oversight — which is how these four findings got past
  a review and fourteen mutations.
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
  over more mounts.
- **Tests run in a pinned zone (`America/New_York`), set in
  `frontend/vite.config.ts` before the workers spawn.** Local-day logic needs a
  known zone, and setting `TZ` from *inside* a worker — `vi.stubEnv("TZ", ...)`
  — is silently ignored by the threads pool, so it fails as a one-hour
  arithmetic error. `src/lib/calendar.test.ts` opens with a guard asserting the
  offset, which names the cause before the real assertions misreport it.
- Pure mechanical changes (renames, generated code via `make gen`, docs) don't
  need a new test — everything that changes behaviour does.

## Conventions

Nine subsystems have their own rules in a nested `CLAUDE.md`, loaded when you work
under that directory — read the one you are in, not all nine:
[`decide`](internal/core/decide/CLAUDE.md) (release matching, eligibility, movies),
[`acquire`](internal/core/acquire/CLAUDE.md) (search sweep, feed, pass outcomes),
[`importer`](internal/core/importer/CLAUDE.md) (grab lifecycle, file mapping,
failure blame), [`library`](internal/core/library/CLAUDE.md) (placement, layout),
[`catalog`](internal/core/catalog/CLAUDE.md) (title identity, item counts,
monitoring), [`airing`](internal/core/airing/CLAUDE.md) (air dates, the calendar),
[`settings`](internal/core/settings/CLAUDE.md) (config precedence, stored
secrets), [`server`](internal/server/CLAUDE.md) (routes, the cross-origin write
guard, settings-body encoding) and [`store`](internal/store/CLAUDE.md) (migrations,
the sqlc layer). Two are security rationale and worth naming here so nobody
rediscovers them the hard way: **a write whose `Origin` names another origin is
rejected** (#269), and **a stored secret is only ever sent to the host it was saved
for** (#259).

What stays here is what applies before you know which package you are in.

- **A stall at exactly 0% is the one absence-shaped thing that is the release's
  fault (#242).** Long enough to need its own section — see
  [`docs/design-notes.md`](docs/design-notes.md).
- **Periodic work goes on the job runner (`internal/core/jobs`), not a bare
  `go`.** Register by name with an interval in `main.go`; the runner handles panic
  containment, the "log failures only when `ctx.Err() == nil`" rule, and the
  drained shutdown that keeps the store open until in-flight work finishes. It never cancels
  a job itself — `ctx` is the only shutdown signal, so work past a point of no
  return can still finish. A job closure must read its dependencies from the
  registry/service each run, not capture a snapshot, or live config edits stop
  applying. **The importer is deliberately still on its own goroutine** (its
  shutdown semantics predate the runner); migrating it is tracked separately.
- **`frontend/src/lib/api-types.ts` is generated and CI fails on drift**, so every
  backend schema change regenerates it and every concurrent branch conflicts there.
  Resolve by re-running `make gen-api` against the merged spec — never by hand-editing
  the conflict, which produces types that pass review and don't match the server.
- **Quality profiles inform manual actions; they restrict only automation.** A manual
  grab always succeeds in one request — the grab endpoint evaluates eligibility
  server-side at grab time and returns `ineligible_reason` on the 201, but never
  rejects the request (no confirm flag, no 422). Enforcement belongs to the scheduler's
  automatic choices; a manual grab is explicit user intent. Don't make a manual
  path reject an ineligible grab again (decided in PR #57).
- Don't hardcode "episode" in the pipeline — use `domain.WantedItem`.
- **Format is the discriminator everywhere; item count never is (#208).** Movie
  treatment — title+year matching (#209), movie naming (#198), no episodes table
  (#212) — keys on `domain.FormatMovie` alone, never on `len(items) == 1`. A
  single-episode OVA/ONA/special stays series-shaped, which is also what Plex and
  Jellyfin expect (OVAs file under Shows). The rule is enforced at the top of the
  funnel: `highestItem` returns `1` for a movie *before* reading `episodes`, so
  add and refresh agree by construction and a film whose three shorts ship as one
  AniList entry cannot create three items. Downstream, `domain.KindFor(Format)` is the one
  helper every create site writes `kind` through (catalog, refresh, airing) — and
  since it derives from the format frozen at add time, **any future
  `SetTitleFormat` must re-key the existing items**, exactly as `00022`'s
  backfill does: `idx_wanted_items_identity` is `(series_id, kind, number)`, so a
  stale `('episode', 1)` does not collide with `('movie', 1)` and the next refresh
  silently doubles the title instead of failing.
- **In Go and in the frontend, `series` now means the episodic format and
  nothing else (#215).** #207 renamed the contract and this renamed the
  identifiers behind it, so a tracked work is a `title` everywhere: `AddTitle`,
  `titleHandler`, `requireTitle`, `titleID`, the sqlc query names, the React
  Query key `["titles"]`. What deliberately kept the old word is the *other*
  meaning, which Movies made true rather than false — `mediaserver.Roots.Series`
  is the Shows root opposite Movies, `ErrNoSeriesRoot` and `seriesShape` are
  its branch of the path code, and `library.series_layout` (#129) shapes that
  branch alone. So
  `series` in a name is now a claim about format, and a reviewer should read it
  as one. Three things sit outside the rename by construction and must stay:
  the **`series` table and its columns** (SQLite has no DROP CONSTRAINT, and a
  `DROP TABLE series` cascade-deletes the library — so `db.Series` and a
  real-column `SeriesID` field are correct); the settings key
  `series_added` (`eventTitleAdded`), whose *value* a rename would turn into a
  silently re-enabled notification; and the `series_layout` settings key and
  API field. A SELECT alias is ours and renamed with the queries, which is why
  `s.title AS title_name` and a schema `SeriesID` now sit in the same struct.

## Comments

How to word a comment, a doc, a CHANGELOG entry or a PR body is
[`docs/style.md`](docs/style.md) — including the 15 words this codebase uses
twice, which prose has to qualify and identifiers already do. This section sets
the budget; that file covers the wording.

Code states the *what*; comments state only what the code cannot. Default to
none, and prefer a better name or a small helper over an explanation.

- **Budget:** one line. Exported declarations get the Go-standard one-line doc
  comment. Two lines is the ceiling for a subtle point; three or more
  needs a reason you could defend in review. Never a paragraph above a function.
- **Delete any comment that restates the next line.** `// Enrich the release with
  parsed attributes` above three assignments to `rel.ReleaseGroup/Resolution/
  DualAudio` is noise.
- **Keep only these:** a non-obvious *why* (a constraint, a rejected alternative,
  an external quirk), a deliberate limitation, or an invariant a reader would
  otherwise break.
- **Package doc comments** are the one place for design stance — a short
  paragraph, stated once, not repeated on the functions inside.
- Durable rationale (why AniList numbering degrades, why a payload conflict defers)
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
- **The indexer is the scheduled search sweep's scarce resource.** A pass costs one
  search per title (two when the zero-result title-variant fallback fires), so
  `titlesPerPass` × the job interval sets the whole search rate. Cost scales with
  titles carrying *unfilled* items, not library size: the due query's `EXISTS`
  drops a title as soon as nothing is wanted, so a complete library is free and a
  satisfied one leaves the queue instead of taking a second slot. To raise
  **back-catalog drain rate**, shorten the interval rather than widen the pass —
  the ratio sets throughput, the width sets peak burst, and a pass issues its
  searches back-to-back with no pacing. That is now the only thing the ratio
  controls: the feed sets acquisition latency for a current release.
- **The recent feed inverts that cost, which is why it is the hot path.** One
  request covers every title, so `feed-poll` is flat in library size while the
  search sweep is linear in due titles. That is what makes the sweep affordable as a
  safety net rather than the mechanism. Don't shorten `feedPollInterval` below
  15 minutes: indexer operators ask for it, and Sonarr — which sets the
  community's expectation here — defaults to 15 and rejects anything below 10.
- **Identification**: v1 relies on identity-by-construction (we chose the release);
  hash/AniDB identification and pre-existing-library import are deliberately
  out of v1's design.
