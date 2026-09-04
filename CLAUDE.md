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
- `make notices` — regenerate `THIRD-PARTY-NOTICES.md` after a dependency change; CI
  fails on drift, mirroring the `api-types.ts` rule. The trigger is narrower than
  "touched `go.mod`": the file covers Go modules *linked into the binary*
  (`go version -m`, not the module graph) and frontend *production* deps, so a
  devDependency bump needs nothing.
- `make lint`, `make test` — the full suite CI runs; locally prefer the scoped
  commands below

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
  won't hand back a stale pass. `(cached)` means the compiled inputs really are
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
  green. `tsc -b` runs only inside `npm run build`, leaving the error invisible to vitest
  and `make lint` both. After a frontend type change run `./node_modules/.bin/tsc -b`
  from `frontend/`. It covers `*.test.ts(x)` too
  (`tsconfig.app.json` includes all of `src`), and re-checks the whole program every run
  (~4.5s; `noEmit` without `composite` makes the tsbuildinfo near-useless), so run it
  once before committing rather than per edit.
- **Before committing, run `go vet ./...`** — 0.6s idle, ~2s after a change to a core
  package, and it type-checks `_test.go` files too, so it catches a broken test in a
  package you never ran. Don't scope it; the packages you didn't touch are the whole
  point. Then push and let CI run the suite.
- **Still regenerate locally** — `make gen` / `make gen-api` / `make notices`, per the
  Build & run list above. CI fails on drift in all three, and a round-trip is the slow
  way to learn it.
- **Run the full `make test` anyway** for cross-cutting work: `domain` or `store`
  signature changes, dependency bumps, a refactor spanning packages, or a release tag.

## Layout

```
cmd/transpondarrd      entrypoint (graceful shutdown)
internal/config        env config (TRANSPONDARR_*)
internal/server        chi + Huma API, API-key auth, embedded SPA
internal/store         SQLite: goose migrations + sqlc layer (internal/store/db)
internal/core/domain   content-agnostic model (Title / WantedItem)
internal/core/indexer  Indexer iface + torznab adapter
internal/core/download  download.Client iface + qbittorrent adapter
internal/core/library  library.Target iface + mediaserver adapter
web/                   embeds web/dist; frontend/ is the Vite source
```

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
  when its inner provider has one, so the assertion never lies), and the caller
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

- **Grab lifecycle (`internal/core/importer`): every status but `grabbed` is
  settled.** `grabbed` → `imported`, `failed` (errored, or absent from the
  download client past the grace period — the item reverts to wanted), or
  `import_deferred`. The scan iterates **per info hash, not per row** (#126): a
  pack is a row per covered episode, and its payload only means anything
  examined as a whole. `collectPayloadFiles` walks it once and the pure
  `mapFiles` maps files onto the items the release claimed, so
  `library.Target.Place` stays file-only while a pack imports episode by
  episode. `import_deferred` therefore narrows to "*this item's* file could not
  be picked out". Deferred grabs are never re-imported by the scan (the
  no-infinite-retry property) but stay in it for missing-from-client
  reconciliation, so a vanished payload still frees its item; only an explicit
  `RetryImport` reopens one, optionally naming the file.
- **The walk's extras filter yields to a sole video (#135).** A payload whose
  only video carries an extras token is collected anyway — identity by
  construction again: one video and nothing to confuse it with means the token
  is a word in the title, and dropping it parked the episode with the file
  sitting right there. `sampleTokens` is the exception that does not qualify (a
  sample is a truncated copy, never the episode) and so is excluded before the
  video is counted at all. Downstream the relaxation needs no special case: a
  one-item group takes it by the lone-file rule, a multi-item group leaves it
  over and defers.
- **Nothing unpacks an archive; the walk names one instead (#135).** Declined
  deliberately: there is no Usenet client here (qBittorrent only) and RAR
  packaging is a Usenet/scene convention that anime groups do not use, so a
  decoder would be the first dependency in the import path and its tests would
  need committed binary fixtures. So `collectPayloadFiles` returns a `payload`
  whose `archives` ride *beside* `[]candidate`, never inside it — `mapFiles`
  stays pure and a `.rar` is unassignable by construction rather than by a guard
  someone can miss. Volumes are grouped into sets keyed on **dir + stem**, so a
  12-volume set is one thing to extract and two discs sharing a naming scheme
  stay two; the deferral reason and the Fix import dialog then say what to
  extract, and re-importing after extracting in place already works with no new
  code. **An archive keeps its item deferred on every path**, including a retry
  clicked before extracting and a mixed payload whose loose file covers only some
  items — failing there would revert the item, blocklist the release and drop the
  row from the queue, with the episode sitting in the payload the whole time.
  Password-protected and corrupt archives are indistinguishable from healthy ones
  without the reader we declined, and all three defer identically — sound,
  because deferral is settled either way. A single-file `.rar` payload is an
  archive too: identity by construction stops here, since hardlinking it into
  the library as the episode is worse than deferring.
- **The mapping rules are narrow on purpose, because a wrong answer moves a
  file.** A lone file for a lone item is identity by construction (we chose this
  release); a file claims a number only when it names exactly one, with
  season-relative beating absolute when both land inside the release, matching
  decide's stance; among same-number claimants the higher `Version` wins and
  `Repack` breaks the tie, and an exact tie is a *conflict* rather than a coin
  flip, since taking either silently drops the other. Retry overrides are keyed
  on the payload-relative path and overrule every rule above — being wrong about
  a filename is the whole reason the escape hatch exists.
- **A covered item with no file splits by whether a human could fix it.** Files
  still loose in the payload → defer with the detail naming what is unmatched,
  fixable from the Activity queue. **An unextracted archive counts as still
  loose** — it holds the episode, so it is a human's to fix — which is why
  `settleGroup` takes the whole `payload` rather than its files. Nothing left
  over at all → `failGrab`, so the item reverts to wanted and the sweep
  self-heals with a single; it flows through the same `remember()` grouping, so
  one payload is one step on the blocklist ladder. A file for an item the
  release never claimed is placed too,
  guarded on the item existing, not being had, and carrying no unsettled grab,
  and **holding the `acquire` claim** (`TryClaimItems`/`ReleaseClaims`) so a
  concurrent grab cannot race a copy-mode `Place` that runs for minutes. One
  registry is the point. `ScanOnce`, `ListPayload` and `RetryImport` share the
  importer's mutex, which is why `main.go` builds one importer and hands it to
  both the job runner and `server.New`.
- **`failed` also means "this release is remembered" (`internal/core/blocklist`,
  #118).** Both `failed` paths record a per-series blocklist entry, because the
  grab row is per wanted item and the next attempt overwrites it — without that
  memory the sweep re-derived the same ranking and re-grabbed the same doomed
  release forever. `decide` consults it through the existing `ineligibleReason`,
  so the sweep's eligibility gate, the Releases tab's reason column and manual
  grab's freedom from eligibility (PR #57) all hold unchanged. Two constants are
  load-bearing rather than arbitrary: identity is the **info hash or the
  normalized title**, because Torznab often omits the hash; and the expiry
  **escalates** (24h, 7d, then permanent) because the `failed` paths fire for
  environmental reasons that can fail many grabs at once, so permanent-on-first
  would blocklist a whole in-flight set on one qBit incident. Expired entries are
  filtered, never deleted — the row carries the failure count the ladder reads.
  An *import* failure deliberately records nothing: it stays `grabbed` and
  retries, because its causes are path-mapping gaps rather than bad releases.
- **An absent torrent is not a verdict (#241).** `failed` settles two different
  things and only one survives an inference: freeing the item is self-healing and
  stays automatic, while remembering the release as bad is a judgement that needs
  a cause. Only three things supply one — the client reporting `error` for a
  torrent it holds, a payload we examined that lacked what it claimed, and a
  download URL that could not be fetched or parsed (`acquire.AutoGrab`, #120).
  Absence supplies none (every cause is external: a hand-removed torrent, a reset
  client, other tooling, a hash the client never had), and neither does
  `missingFiles`, which is why it maps to its own `download.State` rather than
  sharing `StateError` — the data is gone, the release is not at fault, and a
  dropped mount would otherwise blocklist every release on it at once. **A blamed
  failure's two consequences travel together**: the memory, and the re-fronting of
  the search queue — so an unblamed failure takes neither. `record()`'s breaker
  arm (#120) was already the precedent, declining to re-front exactly when it
  declines to blame, and a dropped mount would otherwise answer one thundering
  herd with another. `blame` is a required `failGrab` argument rather than a
  default so a new failure path has to state its answer. **Dropping the memory
  cost `data_missing` its only loop breaker**, which the blocklist entry had been
  supplying by accident: converging on a duplicate (`AddAlreadyExists`) assumes it
  can still deliver, and one whose data is gone never will, so the same release
  ranked first and "grabbed" every pass while the item stayed unacquirable. The
  adapter now refuses that add with `download.ErrDataMissing` — deliberately not
  `ErrBadRelease`, which is the one `acquire.AutoGrab` blocklists — so the pass
  reaches the next-best release instead. **The refusal belongs to the arm where
  the torrent demonstrably pre-existed our add**, which is the pre-check and never
  the post-failure re-check: there our own add may be what landed, so refusing
  would leave a torrent no grab row references — #134's orphan, which that arm
  exists to prevent. It costs nothing to converge there, because the loop's steady
  state runs through the pre-check: a duplicate reached by the re-check writes a
  grab row, fails unblamed, and is refused on the next pass. One extra cycle, not
  a loop. Converging on a *healthy* duplicate is unchanged either way, and is what
  makes re-grabbing an in-flight torrent safe. A stalled torrent is
  *present* and reaches none of these paths, so the doomed-release case #118
  defends against cannot arise from absence at all. The posture behind it: this
  app disassociates a torrent from the library and never removes or deletes one
  on its own, because the download client is the user's disk and their ratio.
- **A stall at exactly 0% is the one absence-shaped thing that *is* the
  release's (#242).** A `stalledDL` torrent is *present*, so it reached neither
  `reconcileMissing` nor `StateError` and sat open forever — the doomed release
  #118 built the blocklist for, unreachable by it. **Progress is the
  discriminator, strictly `> 0`**: a torrent that moved at all proves a peer had
  the data, so those bytes are the user's to discard, where a percentage
  threshold would draw a line nothing supports. **`Progress` is the right
  predicate and must not be swapped for a bytes-received count** — the plausible
  "refinement" is a regression. It is not piece-granular: `sessionimpl.cpp` calls
  `post_torrent_updates()` with no arguments, so libtorrent's default
  `status_flags_t::all()` applies, `query_accurate_download_counters` included,
  which counts *partial blocks* in `total_wanted_done`; `TorrentImpl::progress()`
  returns that unrounded, so progress moves on the first 16 KiB block — under a
  byte per second over a six-hour timeout, which puts the slow-torrent false
  positive out of reach, which is the whole of the case: the refinement solves
  nothing that needed solving. A second reason to distrust it is *reasoning
  rather than traced fact*, and is flagged as such — libtorrent documents
  `all_time_download` only as an accumulated payload counter and describes
  `total_failed_bytes` separately, so whether a byte counter excludes what a hash
  check later discards is not settled by the documentation; if it does not, a
  torrent failing every check would report bytes received and `progress == 0`
  forever, and "has received nothing" would never abandon it.
  `stalled_since` mirrors
  `missing_since` (stamped on the first qualifying observation, cleared the
  moment progress moves), the timeout is `download.stall_hours` — client-agnostic
  policy, hence not `qbit.*` — and 0 both disables it and **clears the stamp**,
  so the two ways of holding a download deliberately agree: pausing and switching
  the timeout off both give a fresh window rather than banking the wait. It is
  `blameRelease`, unlike #241's absence: nobody seeding a release we can see is a
  fact about it, and without the memory the sweep re-picks the same first-ranked
  release and loops. Two things bound the fan-out a VPN drop causes, and both are
  load-bearing: every such failure runs through `blocklist.Record`, so #120's
  breaker blames four items and suppresses the rest, and only a torrent that
  never received a byte qualifies at all.
  **The trigger is the client saying it is *trying*, with progress 0 (#246).**
  `StateStalled` or `StateDownloading` — so `metaDL` and `forcedMetaDL` are
  covered, which is the magnet parked at "Downloading metadata" that #242's own
  wording had to exclude. `StatePaused` is deliberate user intent and
  `StateUnknown` is a gap in `mapState`, so neither reaches the arm, and
  `queuedDL` is now excluded by **having its own `StateQueued`** rather than by
  hiding inside `StateDownloading`: a client holding a torrent back is not
  trying, and folding the two together is what made "widen the predicate" and
  "never abandon a queued download" look like opposites. `Status.StuckAtZero`
  therefore names the predicate and `stalled_since` deliberately keeps a name it
  has outgrown — the clock did not change, it still mirrors `missing_since`, and
  a migration for a column name is cosmetics. **The stamp-clearing loop and the
  switch arm must read the one predicate**: widening only the arm clears the
  clock every scan while `sharedSince` re-derives it from the pre-clear rows, so
  the DB keeps the cleared value, the timeout never accumulates, and the grab
  sits open — the bug, surviving its own fix. It is a mutation that lives, so
  `TestKeepsMetadataStallClockAcrossScans` exists to kill it.
  **No fetching-metadata state, on purpose.** Queued is near-universal (five of
  six surveyed clients; rTorrent has no queue, and a missing state degrades
  cleanly because an adapter simply never emits it), while fetching-metadata is
  one client's taxonomy: qBittorrent and Deluge are both libtorrent and disagree
  in both directions, qBit passing the metadata state through while Deluge does
  not expose it at all. The two are disjoint by construction in qBittorrent,
  whose `updateState()` tests `isQueued()` first inside the `!hasMetadata()`
  branch. If "Fetching metadata" is ever wanted in the UI it is a detail field
  beside `State` — what Sonarr does, and what Transmission's
  `metadata_percent_complete` and rTorrent's `d.is_meta` are shaped for — never a
  state value.
  **An adapter maps the derived predicate, never a client's own "stalled"
  flag.** Ours is qBittorrent's instantaneous `download_payload_rate == 0`;
  Transmission's `is_stalled` is a 30-minute idle timer, so mapping that boolean
  would silently stack `stall_hours` on top of it and make the threshold mean
  something different per client. `stalled` stays instantaneous everywhere and
  `stalled_since` owns the duration (#159).
  **Between the two timers absence wins by construction**: the
  `!ok` branch `continue`s before the state switch, so a torrent that goes
  missing is settled on the 5-minute grace and the stall clock never gets a say.
  The queue's `abandon_at` is the part `client_state` could not say — that we are
  going to act, and when — and it is therefore keyed on the *live* status as well
  as the stamp, which outlives the stall by up to one scan. Widened, it now shows
  on a healthy grab too, for the scan or two before its first piece lands and on
  a magnet for as long as metadata takes: accepted rather than hidden below some
  fraction of the timeout, which would invent a second threshold with nothing
  behind it. Its
  countdown is stale for as long as the tab is open, not for one poll: a queue of
  only stalled rows serializes byte-identically, so React Query's structural
  sharing re-renders nothing (#144's class, and `activity.tsx` is a third call
  site for that audit).
- **Both timers are the info-hash group's, not the row's (#247).** A pack is one
  torrent, so `sharedSince` gives every row of a group its earliest stamp and
  `stalled_since`/`missing_since` are stamped and cleared per group. Per-row
  clocks were the bug: a row a later add wrote (#241's converged duplicate) began
  its own clock, crossed the threshold in a *later* scan, and so escaped
  `remember()`'s per-scan grouping — one incident, two `Record` calls, and since
  the upsert is keyed on `(title, normalized title)` that reads as `failures = 2`
  and jumps the ladder to 7d rather than writing a second row. The seam is here
  and not in `remember()`, whose per-scan grouping is #124's design and correct;
  widening *it* would need cross-scan memory the design avoids. Three
  consequences. **Earliest, not `now`** — the clock belongs to the torrent, and
  taking `now` for the late row would reproduce the split exactly; it also makes
  an install upgrading mid-stall converge rather than stay inconsistent. **The
  value is written, not just computed**, because the Activity queue renders
  `abandon_at` from each row's own column, so a divergent stamp would show one
  episode of a pack a countdown it will never be settled on; the write is guarded
  on the value differing, so a steady state costs nothing. And **an unreadable
  stamp is treated as an absent one** rather than restarting the group — the
  tolerance that unparseable data must not fail a grab is preserved at the group
  level, where only *no* row being readable waits another full period. Clearing
  needed no change: both clear conditions read the group's status and the global
  timeout, never the row.
- **A batch is matched, eligible, and preferred on coverage (#126).** #125
  refused a pack in `ineligibleReason` because the importer could only *defer* a
  multi-episode payload; per-file import removed the reason, so the refusal is
  gone. Three things follow. The comparator gained a **coverage tier** — `Items`
  descending, between Pinned and Score — because lifting the refusal alone would
  make the winner between a pack and a single score- and seeder-arbitrary; a
  pack covering six wanted items is one grab instead of N. Weekly singles tie at
  1 and fall through to score unchanged, and the pin stays *above* coverage
  deliberately: a pin is per-series knowledge ("this group is definitive"), so
  coverage only decides among equally pinned candidates. And `batchItems` gained
  the guard it never had: an explicit range past `maxItem` is now **unmatched**
  with the single-episode path's absolute/season-mismatch reason, so a `01-48`
  pack no longer claims a 12-item entry's items 1-12. A numberless pack carries
  no range to check and still fills the entry — that is what a season pack is.
  Both entry points inherit all of it through the one decision layer, rehearsal
  included.
- **Two entry points, one decision layer (#101).** The `feed-poll` job and the
  `wanted-search` sweep both build a `Match` through `Service.evaluate` and act
  on it through `grabPass`, so profile floor, blocklist, pinned-group delay and
  the coverage ranking are written once. The feed is only a cheaper *trigger*: it
  inverts the sweep's lookup (a release title needing a series, rather than a
  series needing releases), which is why it is series × entry — deliberately
  unoptimised, since a page is ~100 entries and the due query already drops any
  series with nothing wanted. It writes no search cadence: nothing was searched,
  and a grab settles its item, so the sweep's `EXISTS` drops the series anyway.
  The one exception is a poll that catches itself missing a page (#140): it
  resets the sweep for the series that aired inside the gap, bounded to
  `titlesPerPass` per gap event so a routine gap on a busy indexer queues no more
  searches than one pass can spend.
  **A page proves coverage only by showing the mark's own instant (#176)** — an
  entry published at it, or one whose id was remembered *for* it. Nothing else on
  the page is evidence, and the three things that are not are worth naming because
  each looked like evidence once: an entry merely *older* than the mark (an
  aggregator's backfill, which is structural rather than exotic — Jackett's
  aggregate indexer returns partial results, so a member that timed out on one
  poll reappears on the next carrying its original dates); a **sticky the feed
  never dated**, which sits on every page, so treating it as coverage disables gap
  detection permanently and never self-corrects; and a **stale id the rewind path
  merged**, which by construction was published before the instant it would be
  claiming to reach. Hence the mark carries two id sets that a reader must not
  merge back into one: `IDs` is the dedupe set `unseenEntries` needs, deliberately
  the wider of the two, and `LatestIDs` is coverage alone. The id arm survives the
  narrowing rather than being dropped because an aggregator rendering a tracker's
  relative date recomputes it every poll, so the entry carrying the mark comes back
  at a slightly different instant.
  Reading a mere straggler as coverage is what masked the gap: the old signal was
  "every entry is fresh", and one backdated entry on the page defeated it. Sonarr's
  `oldestReleaseDate < lastReleaseInfo.PublishDate` is exactly that reading, and is
  safe *there* only because Sonarr re-processes its whole page each sync and
  dedupes downstream, where `unseenEntries` skips what it dates before the mark —
  so the precedent is one to understand and not to copy. Two consequences: a page
  with nothing fresh on it can still be a gap, so the recovery sits outside the
  quiet-feed shortcut rather than behind it; and a false gap **recurs for as long
  as the page hides the mark** — an indexer stuck serving an older page reports one
  every poll — which is affordable only because the recovery is bounded to
  `titlesPerPass` and reorders the sweep queue rather than adding searches to it.
  They divide by what they can see: the feed owns releases published while we
  watch, the sweep owns what already existed and everything when no feed is
  configured. **Cadence follows that division; grab scope deliberately does
  not** — a sweep search that turns up a current release still takes it, because
  the feed's dedupe is one-shot and an entry seen before its series or item
  existed never comes around again. Concretely, `writeSearchState` drops the
  aired-since reset and the next-broadcast clamp when a feed exists (both are
  #100's answer to "search a weekly show at air time", which the feed now owns);
  the pin-delay hold stays either way, since that release already exists.
  **Concurrent grabs are serialized by an in-process claim over wanted-item ids**
  (`claims.go`). The two jobs are phase-locked — same interval, both
  `RunAtStart`, the runner anchors both to startup — and both read grab state
  before either writes, so without it a just-aired episode gets two adds and, if
  they picked different releases, an orphan torrent no grab row references.
  Automation `TryAcquire`s and yields; a manual grab `Acquire`s and never does.
  `indexer.RecentFeed` is a type assertion, not a wider `Indexer` — a source
  without one degrades to sweep-only, which is a supported configuration and logs
  at debug. The high-water mark lives in `settings` under `feed.seen.<indexer>`:
  newest `pubDate` plus the ids sharing it, which is Sonarr's
  `LastRssSyncReleaseInfo` shape. Two entry ids matter — the GUID is unreliable
  across Torznab implementations (Sonarr keys on the download URL for exactly
  that reason), and a feed publishing no dates dedupes on ids alone.
- **A pass stores what it decided; the page surfaces less than it stores
  (#181).** `walkCandidates` writes one `pass_outcomes` row per wanted item,
  upserted in place, so the table is bounded by `wanted_items` rather than by
  pass count. Three constants are load-bearing. **The stored set is wider than
  the surfaced set** — seven outcomes stored, five reach a row: `grabbed` exists
  only as the tombstone that invalidates an older refusal (a listed item's grab
  plainly did not hold, and `grab_failed` owns that row), and `contended`'s
  honest message is "the queue is working", which the group tier already says.
  Item monitoring only *narrows* the write set, so the guard below still holds —
  but it introduces a second, permanent staleness source, since a pass never
  writes for an unmonitored item and nothing later invalidates what it wrote
  before the toggle. That one the read side **suppresses** (`itemReason` returns
  `unmonitored` above every other tier) rather than invalidating, so the stored
  answer returns intact when the item is monitored again.
  **Blame drops decide's coverage tier** and re-ranks Pinned → Score → Seeders,
  because coverage buys grab efficiency (#126) and says nothing about which
  release came closest *for one episode* — inheriting it would let a wide
  low-scoring pack outrank a high-scoring single covering exactly the episode
  asked about. And **the read-side suppression guard is exactly equivalent to
  ranking on recency**, not an approximation of it: a pass only writes for a
  grabbable item and an item is not grabbable while its grab is live, so an
  outcome can never be recorded between a grab being made and failing — hence
  "older than `grabs.created_at` → dropped" needs no timestamp arithmetic and no
  index. `covered` stays a separate map from the outcome set (it runs per
  candidate on the feed's hot path); they agree by invariant, tested rather than
  merged. Only a sweep that ran to the end writes `no_match`: a hard return never
  saw the rest of the candidates, and a feed poll saw a page, not a search.
- **Cutoff Unmet caches the parse and never the score (#185).** Membership is
  re-derived per request so editing a profile moves the list, and the scan walks
  its whole budget precisely when a library is healthy and nothing qualifies —
  so the cost had to come off without recording the answer. Measured, the split
  is lopsided: `parser.Parse` is ~103µs per held release against ~0.9µs for
  `decide.Score` plus `decide.UnmetGoals`, so **the expensive half is the one no
  profile can change** and the cheap half is the one that would need
  invalidating. Hence `held_release_parses` stores what a release name said and
  nothing about what it is worth; a `held_score` column is the regression this
  bullet exists to prevent, since it would put a version counter on
  `quality_profiles` and a bump obligation on every profile mutation. Two things
  follow. The row is **keyed on the release title it parsed and on
  `parser.Version`**, so the read joins on both: a superseded release's parse
  cannot match, which is why `SetWantedItemHeld`, the one writer of
  `held_release_title`, needs no knowledge of the table — and neither can a
  parse the current parser would no longer make, which the title alone could
  never express, since a held title does not change when the parser under it
  does. That is the one obligation the design does not remove: bump
  `parser.Version` whenever `Parse`, `Parsed` or anitogo can read the same title
  differently, or Cutoff Unmet scores the old reading forever while
  `decide.Match` parses fresh, and the two disagree. A stale row is replaced
  rather than migrated, so the bump is the whole of it. And the fill is a
  **write from a read**, accepted because it is derived and idempotent (the
  worst a bad row costs is one re-parse), bounded to once per held release ever,
  and unreachable any other way: SQL cannot run the parser, so no migration can
  backfill it, and filling at import time would never reach the healthy library
  that is the whole case, where nothing is ever re-imported. The table stays
  Cutoff Unmet's alone: #266 proposed reusing it from `decide.Match` and that was
  declined (see below).
- **The automation loop parses once per poll, not once per title — and not at all
  for a held release no profile will rate (#266).** Measured, reusing
  `held_release_parses` from `decide.Match` was the smallest of three effects in
  the one loop and the only one costing a `decide.Item` change, so the two that
  were taken are both about **not doing the work** rather than remembering it. A
  feed poll matches one ~100-entry page against *every* due title, so
  `MatchOpts.Parses` carries the page's parses, built once in `pageParses`; it is
  **read-only inside `decide`**, which is what makes one map shareable across
  titles without a lock, and a miss is parsed normally so it is a cache and never
  a filter. The sweep deliberately passes nil — it searches per title, so its
  releases differ and there is nothing to share. And `Match` stores a held item's
  **membership without its parse** when `profile.UpgradesEnabled` is false (the
  schema default), because every read of the parsed value sits behind that flag
  while the reason wording at the batch, single and movie arms only ever asks
  `_, ok := held[n]`. Two things a reader would otherwise break. **The membership
  write is load-bearing**: leaving `held` empty instead trips
  `applyUpgradePolicy`'s `len(held) == 0` early return, so covered items never
  reach `UpgradeBlocked`, stay in `TakeItems()`, and the sweep grabs an upgrade on
  a profile that has upgrades switched off — deleting that one line is the
  mutation that proves it. And the `UpgradesEnabled` check is **hoisted into
  `applyUpgradePolicy` on purpose**, so the only path reading a `heldRelease`'s
  parse and score is the one that computed them; pushing it back down into
  `upgradeRefusal` is behaviour-preserving today and makes the zero value
  reachable by the next edit. The cost of the feed half is untestable by
  construction — the memo agrees with a fresh parse, so dropping it changes no
  behaviour and only an allocation assertion could catch it.
- **The automation toggle is three-state (#116): `off` / `notify_only` / `on` —
  one settings key (`automation.enabled`) whose value domain widened, so legacy
  stored/env bools still parse.** Notify-only rehearses: both entry points run
  the real decision walk in `grabPass`, but every take dispatches a `rehearsal`
  notify event instead of grabbing, and the sweep also reports the wanted items
  the walk left uncovered, with the best refusal's reason (the feed stays silent
  there — per-series silence is a feed page's normal state).
  `AutomationEnabled()` stays the run/don't-run gate (true in notify-only) and
  `NotifyOnly()` is the rehearse switch; a manual "Run now" bypasses only `off`,
  so notify-only means nothing reaches the download client no matter who
  triggered the run.
- **A rehearsal rehearses the search cadence but not the grab-driven reset, so
  switching to `on` resets every series' cadence** (`ResetAllTitlesSearchState`,
  in the same transaction as the settings write). A rehearsed pass returns a grab
  count of 0 — nothing settled, so counting would-grabs would re-decide the same
  items every tick — which means it takes the empty-handed branch and climbs the
  backoff ladder to its daily cap. Meanwhile the feed mark advances as usual (not
  advancing it would make a 15-minute poll a repeating firehose), so a rehearsed
  entry never comes around again. Without the reset on resume, "flip to on and it
  grabs" would wait out a backoff the rehearsal itself accrued, for releases the
  feed will not re-offer; with it, the sweep re-searches and finds them.
- **Periodic work goes on the job runner (`internal/core/jobs`), not a bare
  `go`.** Register by name with an interval in `main.go`; the runner owns panic
  containment, the "log failures only when `ctx.Err() == nil`" rule, and the
  drained shutdown that lets the store outlive in-flight work. It never cancels
  a job itself — `ctx` is the only shutdown signal, so work past a point of no
  return can still finish. A job closure must read its dependencies from the
  registry/service each run, not capture a snapshot, or live config edits stop
  applying. **The importer is deliberately still on its own goroutine** (its
  shutdown semantics predate the runner); migrating it is tracked separately.
- **A missing episode count is a normal state, and its two consequences are
  answered separately (#151).** AniList publishes `episodes: null` for a
  releasing title, for long-runners, and permanently for a scattering of older
  OVAs; when it also has no schedule the title materializes *no* items, and the
  sweep's `EXISTS` then drops it, the airing stamp stops it being re-asked, and
  nothing else can create one. Two rules follow, and each is deliberately not
  the other's fix. **The count is human-set and never inferred**:
  `catalog.SetItemCount` materializes `1..N` one-shot, storing nothing (refresh
  only ever adds, so there is no override to clobber), and it is deliberately
  *not* prefilled from a release search — `maxItem` is the very bound `decide`
  uses to distrust a release's numbering, so letting release names set it makes
  the absolute-numbering guard inert. That is also why it is **guarded to a
  title with zero items** (409 otherwise): raising `maxItem` on a healthy title
  is the same hazard human-triggered, and a numberless pack would then claim the
  inflated range. PR #57 does not reach it — that doctrine is about *eligibility*
  gating a grab, not about creating items — and a title with a *partial*
  schedule stays unfixable, which is the issue's scope and broadens additively.
  **The cadence keys on the count, not the item count**: `TTLFor(status,
  countKnown)` gives a FINISHED/CANCELLED title with a null count a middle 7d
  tier, applied to *both* `fresh()` and `ListTitlesDueMetadataRefresh`'s CASE,
  which is one two-armed rule and must be mutated arm by arm (#176's lesson).
  Reading the item count was itself the defect the mirror exposes: a FINISHED
  title whose *schedule* filled items but whose count was null got 30d from
  `fresh()` and 6h from the SQL, so the two halves of one rule disagreed. The
  airing sync passes `countKnown` true always — its own CASE keys on status
  alone, because aired times are immutable — so the tier deliberately never
  reaches it.
- **Air dates are nullable everywhere, by design.** AniList's schedule coverage
  thins out badly before ~2015 and can skip episodes even for a modern title (it
  lists no entry for a multi-episode premiere block), so `wanted_items.airs_at`
  is null for real titles in normal operation — never treat its absence as an
  error. `internal/core/airing` syncs it in the background off the job runner and
  stamps `series.airing_synced_at` even when the provider returns nothing, which
  is what stops an unschedulable title being re-asked every tick. Aired times are
  immutable, so only a never-synced series pages full history; a resync passes
  `notYetAired` and fetches the tail.
- **A schedule is densified, never transcribed (#152).** `airingSchedule` is a
  field on `Media`, not a root query, so one page of it plus
  `nextAiringEpisode.episode` ride in `titleQuery` for zero extra requests, and a
  null-count add returns its items immediately instead of sitting at `0 / 0` for
  an `airingSyncInterval`. Both that page and the background sync then create
  `1..max(known number)` rather than transcribing, leaving `airs_at` null on the
  filled-in ones — a schedule reading 1, 3, 4 means episode 2 shared a broadcast
  slot, and with a null count nothing else would ever create it. Over-creating
  leaves an item permanently wanted that no release matches (a sweep slot, and a
  series that reads incomplete) but cannot cause a wrong grab, since `decide`
  refuses anything numbered past `maxItem` regardless; under-creating loses an
  episode nobody notices is missing. Three bounds, each measured against the live
  API rather than assumed:
  - **A published count wins outright** over both floors. Roughly 1 counted entry
    in 15 has a schedule reaching *past* its count (a 12-episode show whose
    schedule runs 2..13), which unconditional `max` would turn into a phantom item.
  - **A full fetch fills from 1, never from the schedule's own minimum.** In the
    wild a minimum above 1 means AniList lost the early records — a 24-episode
    entry whose schedule starts at 23, a 16-episode one starting at 14 — *not* an
    offset season: sampled sequel entries restart their numbering at 1 (24 of
    25). Filling from the minimum would silently drop the run below it.
  - **A tail fetch fills only inside its own span**, being a partial view of the
    numbering, so it does not re-derive a back catalogue every pass.
- **The in-band page is bounded; the next-broadcast floor is not.** AniList keeps
  only a recent *window* of schedule records for a long-runner — its first page
  starts in the middle of the run, not at episode 1 — so a null-count long-runner
  materializes its whole run in the add's transaction. That is deliberate: it
  is the same set the sync would reach for the tail, plus a back catalogue
  *nothing* creates today, and it costs no extra AniList requests, because the
  sweep spends one search per *series* regardless of item count. Numbers only —
  carrying dates through the add would pull in `ItemMeta`, `CreateWantedItem` and
  the sqlc layer for a column `internal/core/airing` already owns. A gap-filled
  item does reset the search cadence (`ResetTitleSearchState`, as `refresh`
  does): it carries no air date, so it is exactly what `airedSince` cannot see.
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
- **A stored secret only ever goes to the host it was saved for (#259).** Three
  `Test*` and three `Update*` paths fill a blank secret field from storage — which is
  what lets the Settings **Test** button work without retyping a password the read
  path deliberately redacts — and then connect to the URL the *caller* supplied, so
  the substitution handed the secret to whatever host a request named.
  `inheritSecret` is now the single way a stored secret is read back, and it refuses
  rather than substituting when the destination differs. Three constants are
  load-bearing. The comparison is **scheme + host + port**, because the host is what
  receives the secret: a path edit (a different Jackett indexer on the same Jackett)
  must not cost a retype, and a default port written out explicitly is not a move.
  **An empty destination inherits rather than refusing** — nothing is ever connected
  to, so clearing a URL to disable an integration keeps the secret instead of wiping
  it or 422-ing. And **ntfy applies its defaults before the comparison, never
  after**: a blank server means the public ntfy.sh, so defaulting afterwards makes it
  read as "no destination", takes that empty-destination branch, and hands a custom
  server's token to ntfy.sh — a live mutation, which is why
  `TestBlankNtfyServerDoesNotInheritACustomServersToken` exists. **The guard is
  per-request, so a request it lets through must not move the baseline the next one
  is compared against**: ntfy is the one integration whose disable signal (topic) is
  a *different field* from its destination (server), so a blank-topic save persisting
  the caller's server would rebind the inherited token to it and the follow-up would
  match — #259 reinstated in two ordinary requests. `UpdateNotify` therefore keeps the
  stored server on that branch, and only while a stored token is actually being
  carried forward, so staging a server before choosing a topic still saves. Download
  and indexer cannot have this shape: their disable signal *is* the destination, so
  clearing the URL stores an empty one and `sameDestination`'s hostless fallback
  refuses every later host. The **save** paths
  carry the rule too, not just the tests: a save rebuilds the live client against the
  new URL and it authenticates on the next poll, so fixing only the tests would leave
  the same exfiltration one `PUT` away. Deliberately **not** an access-control fix —
  the cross-origin hole that makes it reachable without a credential is #269, and in
  `enabled` mode the caller is authenticated anyway. What it protects is the secret
  *leaving* the app (an indexer key is a private-tracker account credential, a qBit
  password is often reused) and the coherence of the `GET /settings` redaction, which
  asserts that API access does not hand you the secrets.
- A DB change = a goose migration under `internal/store/migrations` + queries in
  `internal/store/queries` + `make gen`.
  - **`make gen` refuses to run on any sqlc but the `mise.toml` pin.** The generated
    layer is committed and CI diffs it, so a mismatched sqlc lands as drift blamed on
    your change rather than on the toolchain.
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
  - **A table rebuild is one `-- +goose StatementBegin` block, never loose
    statements** (`00020_provider_identity.sql` is the only one, and the recipe).
    SQLite has no DROP CONSTRAINT, so changing one means create-copy-drop-rename —
    and `DROP TABLE series` with foreign keys on **cascade-deletes the user's whole
    library**, since `db.go` enables them in the DSN for every pooled connection.
    `PRAGMA foreign_keys` is a silent no-op inside a transaction (hence `-- +goose
    NO TRANSACTION`) *and* is per-connection, so the pragma and the DROP must meet
    on the same connection. One statement block is one `Exec` is one pooled
    connection — that is the guarantee; loose statements have none. Wrap the DDL in
    an explicit `BEGIN`/`COMMIT` so a failure rolls back, and restore
    `PRAGMA foreign_keys = on` inside the same block, because the DSN pragma is
    applied only at connection open. A migration test seeding every cascade child
    and asserting it survives is the acceptance criterion, not a nicety.
    - **Re-check the keys before `COMMIT`, and make the check able to fail.** With
      enforcement off, a mis-copied id orphans children silently. A bare `PRAGMA
      foreign_key_check` cannot catch it — it *returns* offending rows, and `Exec`
      discards them — so land the count somewhere that rejects it:
      `CREATE TABLE fk_violations (n INTEGER NOT NULL CHECK (n = 0)); INSERT INTO
      fk_violations (n) SELECT count(*) FROM pragma_foreign_key_check; DROP TABLE
      fk_violations;`.
    - **A failure mid-block returns a connection to the pool with foreign keys
      off**, since the restoring pragma never runs. Contained today only because
      `store.Open` propagates the error and the process exits — do not build
      anything that keeps running past a failed migration.
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
- **A title's identity is `(provider, provider_id)`, and the pair travels
  together (#74).** The pair is what the `series` table is keyed on, what `catalog.AddTitle`
  dedupes on, and what the API takes and emits — `provider` required rather than
  defaulted, because a default hides which id space the caller meant. Two rules
  follow. **The provider name is read, never written as a literal**:
  `Provider.Name()` is the source, so `metadata_cache` joins on the title's own
  `provider` column and the DTOs carry `ProviderName()`; the surviving literals
  are the `enum:"anilist"` request tags and the migration's backfill, both of
  which are statements about the *schema*, not lookups. And **AniList is still
  the only provider**: `catalog.AddTitle` refuses a pair it cannot read, so
  nothing persists a row keyed on an unreachable id space. The pair does *not*
  mean a title can hold two ids — deduping the same title across id spaces
  needs the cross-reference layer (#189), and until it lands, reaching for a
  `tmdb_id` column is the regression it exists to prevent.
- **`decide.Match`'s `items` is the numbering basis, not just the candidate set.**
  `maxItem` spans every item passed (grabbable or not) and drives absolute-numbering
  detection, so narrowing the slice to scope a search silently misreports every
  release outside that range. Scope with `decide.Item.Grabbable` instead (#105
  tracks the scoped search).
- **Candidacy and possession are two fields, not one.** `decide.Item.Grabbable`
  is "worth offering a release for"; `domain.WantedItem.InLibrary` is "the
  library holds a file". They coincide only on the manual path — the sweep
  withholds in-flight and unaired items it plainly does not hold — so nothing may
  derive one from the other outside `acquire.passItem`, which is where each entry
  point states its own. #97's upgrade path is a held item that is grabbable
  anyway, which is exactly the case the old single field could not express.
  **Item monitoring (#188) is one more input to `Grabbable`, which is why it
  costs `decide` and the importer nothing**: `wanted_items.monitored` is
  conjoined into `loadSweepItems`' single grabbability line, so it gates sweep
  search, feed grab and the upgrade pool at once, while `maxItem` still spans
  the item and a pack covering an unmonitored episode still matches the rest.
  Two things sit deliberately outside that gate. **Monitoring never gates a
  manual path** — search, grab, and later file adoption (#157) — which
  generalises PR #57 rather than enumerating the paths that exist today; and the
  importer keeps no gate at all, so a pack grabbed for its monitored neighbours
  still places every file it carries. The bytes are already spent, a hardlink
  costs no disk, and the hole is transitional: `unclaimedItem` already refuses a
  had item, so adoption closes it with no importer change.
- **The two cadence helpers gate on `monitored`, not on `grabbable`.** An unaired
  item is never grabbable by construction, so filtering `nextAiring` on
  grabbability would return the zero time always and silently delete #100's
  next-broadcast clamp for every feedless install. `airedSince` takes the same
  gate for a weaker reason — the grabbable filter is merely over-broad there, and
  would also stop an in-flight grab resetting the ladder.
- **A monitor decision is stored as a numeric cut, not as a mode.** The add-time
  choice (`all` / `future` / `none`) maps to `series.monitor_new_from`, read
  everywhere through the one helper `store.MonitorNew`. A mode could not work:
  the airing gap-fill and refresh growth both create items through
  `UpsertWantedItem`, which deliberately writes no `airs_at`, so `future` would
  have nothing to test there. A number also records the decision as *taken*
  rather than as a policy to re-derive — a re-evaluated `future` would unmonitor
  episodes someone has been waiting weeks for — and it is self-maintaining, so
  episode 1051 created six months later is monitored with no follow-up write.
  `monitor_new_from` is left to its schema default in `CreateTitle` and narrowed
  by `SetTitleMonitorNewFrom`: an omitted sqlc params field writes NULL, which
  reads as "monitor nothing new", so the insert is the one place that must not be
  able to say it. Both later create sites also **split the reset counter** — an
  unmonitored fill must not put a narrowed long-runner back at the front of the
  search queue on every sync — while refresh still clears the airing stamp on
  *any* insert, since the air-date sync ignores monitoring.
- **Monitoring gates what automation *pursues*, not what the app *knows*
  (#183).** `series.monitored = 0` withholds a title from the sweep and the feed
  — the paths that spend money and move files — but never from the two
  background jobs that only *learn* about it. It used to gate all four, and
  three predicates then composed into a hole: an unmonitored title got no
  `airs_at`, so `ListCalendarItems` dropped its rows before the Calendar's own
  monitored check could include them, and `ListUnscheduledTitles` filtered it
  out of the very footer that exists to explain such absences. So the toggle
  could only ever reveal a title that had been monitored, synced, and
  unmonitored *afterwards* — the opposite of the case it is for, since a title
  you are still deciding about is one you unmonitored early. The two due queries
  therefore **order `s.monitored = 0` ahead of their never-synced key rather
  than filtering on it**, so a monitored title still takes every slot before an
  unmonitored one gets any and the request budget's priority is unchanged; that
  the unmonitored tail is reached *at all* is what the second pass in each
  `GivesMonitoredTitlesEverySlot` test pins, since ordering last and starving
  forever are otherwise indistinguishable. **Both queries had to move
  together**: nothing else calls the provider's `GetTitle` for a series (the
  title-detail page reads `store.Q.GetTitle`, the DB), so an unmonitored title's
  cached status freezes at its add-time value — and since the airing query picks
  its TTL tier from that status, opening it alone would leave a title added
  while `RELEASING` on the 6h tier permanently, never graduating to 30d. Fixing
  the arm and not the class would have turned a bounded one-off cost into a
  recurring one. Reaching instead for a per-job TTL override was declined:
  `TTLFor` is deliberately one policy shared by both jobs, which is #151's rule
  about the two halves not disagreeing.
- **"We asked and got nothing" and "we have not asked" are different absences,
  and the calendar footer states which (#183).** `internal/core/airing` is the
  *only* writer of `wanted_items.airs_at` — `catalog` never writes one, since
  #152's in-band page carries episode numbers alone — so **every** title is
  briefly undated between being added and its first sync, and the footer was
  telling the user AniList publishes no air dates for titles nobody had asked
  AniList about. That predated unmonitored titles being synced at all and was
  merely widened by it. `ListUnscheduledTitles` therefore selects
  `airing_synced_at IS NOT NULL AS schedule_checked` and the page renders two
  notes off it, because the sync stamps that column **even when the provider
  returns nothing** — which is exactly what makes it the discriminator rather
  than a proxy for one. Only the checked half may state a verdict; the unchecked
  half says the lookup is still pending, so a wrong claim degrades into a
  temporary one. The footer is *not* the place to hide either: dropping the
  unchecked titles would restore the silent omission the footer exists to end.
- **Format is the discriminator everywhere; item count never is (#208).** Movie
  treatment — title+year matching (#209), movie naming (#198), no episodes table
  (#212) — keys on `domain.FormatMovie` alone, never on `len(items) == 1`. A
  single-episode OVA/ONA/special stays series-shaped, which is also what Plex and
  Jellyfin expect (OVAs file under Shows). The rule is enforced at the top of the
  funnel: `highestItem` returns `1` for a movie *before* reading `episodes`, so
  add and refresh agree by construction and a film whose three shorts ship as one
  entry cannot create three items. Downstream, `domain.KindFor(Format)` is the one
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
  its path arm, and `library.series_layout` (#129) shapes that arm alone. So
  `series` in a name is now a claim about format, and a reviewer should read it
  as one. Three things sit outside the rename by construction and must stay:
  the **`series` table and its columns** (SQLite has no DROP CONSTRAINT, and a
  `DROP TABLE series` cascade-deletes the library — so `db.Series` and a
  real-column `SeriesID` field are correct); the settings key
  `series_added` (`eventTitleAdded`), whose *value* a rename would turn into a
  silently re-enabled notification; and the `series_layout` settings key and
  API field. A SELECT alias is ours and renamed with the queries, which is why
  `s.title AS title_name` and a schema `SeriesID` now sit in the same struct.
- **A movie's file is identified by size; numbering never gets a say (#210).**
  `mapMovie` takes the payload's largest surviving video, because a film is the
  biggest thing shipped with it — a property of the payload rather than of how a
  releaser named it, which is the same reason `decide` stopped trusting numbers
  on the movie path (#218). The number-driven mapping was actively unsafe here:
  a movie's `covers` is always `{1}`, so a numbered extra (`Deleted Scene 1`)
  claimed the film's only item, hardlinked a clip as the movie and dropped the
  feature as a leftover — settled, held, and self-healing never. Note the
  asymmetry it had: *two* claimants deferred safely as a conflict while *one*
  imported. Keyed on `domain.FormatMovie` and never on a one-item group, since a
  series' single grabbed episode is one too and its number is genuine identity
  there. The filter still runs first (a sample is never the feature, and is
  often the small file anyway, so size must not re-admit it), an exact size tie
  is a conflict rather than a coin flip, and a retry override still overrules
  everything. One consequence worth knowing: with size always deciding, the
  `unmatched file(s)` deferral is unreachable for a movie — a tie and an
  unextracted archive are the only deferrals it has.
- **User-facing copy follows the same rule, and only where a movie reaches it
  (#210).** The importer's settled reasons and the notification adapters word
  themselves off the item's kind (`itemLabel`, and the adapters' second condition
  on `Event.ItemKind`), so a film is never "episode 1"; every string an episode
  alone can reach keeps its wording byte-identical, which is what the series
  assertions in `events_test.go` and `retry_test.go` pin. `Event.ItemKind` is
  display-only — `webhook.go` must never map it, because `item_number: 1` is
  *correct* for a movie and the payload is a contract (#207 broke it once,
  deliberately and with an upgrade note).
- **A batch token on a movie release is an eligibility rule, not a matching one
  (#211).** Movie mode's two numeric gates both read what a release *names*, and
  a numberless pack names neither an episode nor a year — so `[Grp] Placeholder
  Saga (Complete Series)` matched the film `Placeholder Saga: The Final`
  eligibly, the sweep grabbed it, and the importer placed one of the *series'*
  episodes into the Movies root under the film's name. This is movie-specific: on the
  series path a numberless pack filling the entry is what a season pack *is*.
  The refusal sits in `ineligibleReason` rather than `movieCandidate` because a
  genuine multi-part film release is indistinguishable from a parent series'
  pack — unmatched would 422 the manual grab and make it ungrabbable without
  renaming, so the precision risk goes on the supervised path alone, exactly as
  the null-year rule splits it. It ranks **above** the null-year reason (per
  release, so it discriminates between rows) and **below** the profile rules
  (which the user set deliberately). An explicit range stays a *matching*
  refusal, unchanged: a film cannot span episodes however it is packaged.
- **Neither automation entry point filters on format (#211).** The sweep's due
  query briefly did — #208 parked movies there because decide could not match one,
  so `next_search_at` would never advance and the film would hold a slot at the
  head of a LIMIT-ordered queue forever. #209 removed the reason and #211 the
  clause, so a wanted movie is now due, searched and grabbed like any other
  title, and format belongs in `decide` alone. The feed's due query never carried
  the stop, which is why the feed acquired films before the sweep could.
- **The null-year rule is one rule split by actor, not two (#208).** `series.year`
  is `0`, never NULL, for "no year on record", and the split is: **naming** drops
  the ` (Year)` suffix (#198), while **matching** stays free for manual search and
  grab — the #57 doctrine — but carries an ineligible reason so automation never
  grabs a null-year movie (#209). The precision risk sits on the supervised path,
  the availability cost on the unsupervised one, because a null year correlates
  with an unreleased title, which is exactly when every candidate a search returns
  is wrong. The stored year is refresh-maintained rather than an add-time
  snapshot, and `SetTitleYear` guards `? > 0` **in SQL** so no caller can let a
  transient upstream null erase one. **A movie's path is keyed on that
  refresh-maintained year**, so a 0 -> N fill after an import orphans the
  year-less folder the next upgrade replaces into — the same class as an AniList
  title edit orphaning `<root>/<Old Name>/`, which the series branch has always
  had. Repaired by #213's placed-path memory, never by enumerating the library:
  `Place` only warns that its naming inputs moved.
- **The library has a root per format, and a missing one is an error, never a
  fallback (#198).** `mediaserver.Roots` splits Series from Movies because Plex
  and Jellyfin want a Movies library separate from Shows, and `Place` picks
  between them on `Format` alone — the same discriminator, so a one-item OVA
  files under Shows with the rest. Placing a movie with no movies root returns
  `ErrNoMoviesRoot` rather than falling back to the series root: an import
  failure is the one settled-status exception (it stays `grabbed` and retries),
  so the error holds the grab, surfaces as `last_error` in the Activity queue
  plus one import-stuck notification, and the next scan imports it once the root
  is set — where a file already hardlinked into the wrong library would need
  hand cleanup. Root (destination) and layout (shape within a root) stay
  different axes: #198 owns the root, #129 the shape.
- **Layout parameterizes the shape inside a branch, never the branch itself
  (#129).** `library.series_layout` (`season_folders` default, `flat`) is read
  only by `destination`'s series arm, so format stays the sole discriminator and
  a one-item OVA loses its season folder along with every other series — the
  movie shape is identical under either layout. It is a string enum rather than
  a bool for two reasons: `libraryInput` is `omitempty` throughout, where a bool
  cannot distinguish "false" from "absent" (the trap `automationInput` documents),
  and #168's per-format routing will need a value it can carry per format. The
  default is the *current* behaviour, which is what lets an install that predates
  the key keep the layout its files are already in — `ParseLayout` maps both
  empty and unrecognized to `season_folders`, and the settings layer normalizes
  through it so what is stored, displayed and joined into a path agree.
  **Switching layouts moves nothing already placed**, so an upgrade writes the
  new shape beside the old file. That is the same orphan class as a refreshed
  title or year and is repaired the same way (#213's placed-path memory), never
  by deleting from a computed path; but switched *to* flat the series folder
  still exists, so the missing-directory warning cannot fire and `heldElsewhere`
  is the only evidence. The two warnings are **independent `if`s, not a chain** —
  they answer different questions, and one silencing the other is worse than
  either alone. `heldElsewhere` therefore matches a *video* at the exact stem
  rather than any stem-mate, or an interrupted copy's `.partial` would report a
  layout switch that never happened and suppress the real warning.
- **`removeStemMates`' trailing dot is load-bearing and only a two- against
  three-digit pair tests it.** `seasonNumber` is hardcoded to 1, so every episode
  of an entry already shares a directory and the flat layout adds no neighbours
  to the one it scans — the blast radius is unchanged either way. But E03/E30
  diverge at the first digit and pass with the guard removed; E10/E100 is the
  pair that catches it.
- **The staging sweep deletes on a predicate, never on a walk (#132).** Both
  transfer paths stage beside the destination — `copyFile`'s `.partial`,
  `replace`'s `.upgrade` link — and both are reclaimed by the next attempt at
  that destination, so what survives is exactly the orphan whose destination is
  never written again: one shape twice, and therefore one sweep. Every clause of
  the conjunction is load-bearing — **not in flight, our own suffix, over a known
  video extension, under a configured root, past a 24h mtime** — and the sweep
  removes only what it finds, so an unmounted root reads empty rather than
  condemning a library.
  **What protects a live transfer is the in-flight registry, not the age.**
  `os.Link` shares the payload's inode, so an `.upgrade` link to a week-old
  payload reads as a week old the instant it exists — age is a margin on
  `.partial` alone. Hence `staged` *owns* the staging name (both paths receive it
  rather than compute it, so a staging file cannot exist unregistered) and
  `removeUnstaged` takes the check and the unlink under one lock, or a transfer
  could register between them. The deliberate limitation on the other side:
  an `.upgrade` orphaned by a crash is only swept once its *payload's* mtime
  passes the threshold, so it can outlive its usefulness — late, never wrong.
  Two more things a reader would otherwise break. **Roots are resolved before
  walking**, since `WalkDir` will not descend a symlinked root and `/media ->
  /mnt/user/media` is an ordinary NAS shape — which is also why `staged` keys on
  a `canonical` path, or the registry would silently fail to match in exactly
  that config. Inside the tree links are still not followed, so the sweep cannot
  escape a root. And **every root is walked before anything is removed**: a root
  nested in the other enumerates one path twice, and the second sighting reaching
  the removal already gone is what makes the `ErrNotExist` tolerance reachable
  and testable. That tolerance is the mechanism; `stagingRoots`' de-dupe is only a
  spared walk.
  The video-extension check is what makes it *our* staging name rather than any
  `.partial`, and is a third thing leaning on `videoExts` being importer's list
  again: drift there costs a **missed** sweep and never a wrong delete, which is
  the only direction that may fail. `library.StagingSweeper` is an optional
  capability by type assertion, so `library.Target` stays write-only and
  enumerating a library remains #170's question; a target without it is a
  supported configuration, not an error. It is its own slow job rather than a
  rider on the 15s import scan because it walks the roots.
- **A year is read the same way whichever form names it, and both are decided
  against the variants (#209).** anitogo fills `AnimeYear` only from a
  *bracket-isolated* token, so `[Grp] Film (2019)` yields a year while the scene
  form `Film.2019.1080p.x264-GRP` glues it into the anime title and reports
  none — which would have left the year gate inert on the naming form films most
  often ship in. `parser.Parsed.Year` keeps that dumb reading, since the parser
  is deliberately dumb about identity, and `decide.releaseYear` derives from
  either source — the isolated token, else the **rightmost** in-range four-digit
  token in the title, never the first, because unrecognized scene tags trail the
  year (`Sample Film 2021 REPACK`) while a leading number is the film naming
  itself. Then **one** variant check, on the result rather than on one source: a
  year an accepted variant carries names the film (`Placeholder Legend 1979`),
  not the release. Checking only the scanned source made the two forms of one
  release disagree, and it was the bracketed one that refused. A collision
  reports *no* year, so the gate passes rather than refuses — deliberate, because
  a wrong year is a **matching** refusal and an unmatched release is
  `grabRelease`'s 422, so over-reading a year would block the manual grab PR #57
  protects. The null-year *title* is the other half and is never a refusal — it
  rides `ineligibleReason`, **last** in that chain, because it is a title-level
  fact identical on every row while every rule above it discriminates between
  them.
- **Movie mode ignores a release's number for *mapping* and reads it for
  *identity* (#209)** — not the same thing, and conflating them was a bug. Every
  episodic token appears on movie names (`Sample Film 2 (2021)` parses as episode
  2, `(Complete)` sets `Batch`), so none may map onto a film; but `titleBelongs`
  is fuzzy containment, so a long-runner sharing a name prefix reaches the movie
  path carrying episode 250, and refusing *that* is the number's remaining job.
  It is disqualifying unless reattaching it to the parsed title matches a
  variant — the same "ask the variants" move the year rule makes, padded widths
  included, since a release writes `0080` where anitogo hands back 80; an
  explicit range never qualifies. So `movieCandidate` never reaches the
  episode-mapping apparatus, which is not the same as never reading the number.
  **That variant match is exact, and the asymmetry with `titleBelongs` is the
  point (#211).** Containment proves nothing about a name we assembled: any
  variant prefixing the parsed title is contained by construction, so the fuzzy
  branch only ever answers yes. It shipped fuzzy and the guard was inert
  whenever the film's title was *not* longer than the release's — `Sample Film`
  took `Sample Film Chronicles - 250` unattended. `titleBelongs` compares a name
  the releaser wrote and stays fuzzy; this compares one we built. The cost is
  accepted knowingly: a film whose variant renders its number differently
  (`Sample Film 2` against `Sample Film 2nd Movie`) goes unmatched, and being a
  *matching* refusal that 422s the manual grab too — pinned by a named test so
  the strictness is not read as an oversight and loosened back.
- **The library flag and the derived item status share one name, deliberately
  (#84): `in_library`.** `wanted_items.in_library` sources the status
  `deriveItemState` returns, so renaming either alone would hide the derivation.
  The name is mechanism-agnostic on purpose — `imported` would name the importer
  as the only route into the library, which pre-existing-library import and hash
  identification (deferred, not rejected) would make a lie in the API contract.
- **A settings body is its section's whole state, so `omitempty` is an argument
  rather than a default (#227).** A field the service would fill in with a
  default is required — the library mode and layout, the qBit category, the
  stall hours, the ntfy server, every notify toggle — because omitting it
  *selects* that default instead of leaving it alone, invisibly to the sender:
  #129's flat library reverted to season folders on a save that never mentioned
  the layout, and the DB row then outranked the env var permanently. A field is
  `omitempty` only where absent and empty are the same instruction — a blank
  secret keeps the stored one, a blank URL, root or topic switches that piece
  off. Sending a required field empty still takes the default, and that *is* the
  distinction: the client said so — **except where the field carries an `enum`
  tag**, which refuses an empty value outright, so `mode`, `series_layout` and
  automation's `mode` are 422 either way and the handler's `ValidImportMode` /
  `ValidSeriesLayout` guards are unreachable defence in depth. The rule is about
  the encoding, not about Huma, so it reaches the hand-rolled bodies too —
  `POST /api/v1/auth/mode` validates the mode itself, where the service would
  otherwise read an absent one as `enabled` and lock a `local` install out. It
  validates *exactly*, matching those enums: `auth.ValidRequired` refuses a case
  variant that `normalizeRequired` would have accepted, because that one reads
  what a stored value or an env var may hold, not what a client sent.
  `TestSettingsInputsRequireEveryDefaultedField` is the audit in runnable form:
  a field moved back to `omitempty` fails it unless someone also takes it out of
  the table, which is where the argument has to be made.
  **The quality-profile body takes the same rule, and being one body for create
  and update is why it has to.** A create that omitted a field did not take the
  column's default — `CreateQualityProfile` writes every column explicitly, so
  it wrote the *zero* over `resolution_order`'s three tiers and
  `upgrade_v2_above_cutoff`'s on, which is the opposite of what both the schema
  and the editor present as a new profile's starting point. So the usual "POST
  defaults what it omits" idiom was never true here, and splitting create from
  update would have meant inventing those defaults in Go to match the ones SQLite
  already states. What stays `omitempty` is where empty is the value: no
  preference, no excludes, no ranked groups. `blocked` on a group row is
  required for the plain reason — under `omitempty` no client can say "not
  blocked", which this repo's own Go and TypeScript test fixtures both
  demonstrated by being unable to.
- **Route handlers: group by resource; use a receiver when it earns its keep.**
  Each resource gets a `*_routes.go` file with a `register<Resource>Routes(api,
deps)` function; `registerRoutes` in `internal/server/routes.go` is the manifest.
  Multi-route resources that share deps/helpers (titles, settings) hang handlers
  off a per-resource receiver struct (`titleHandler`) built via
  `new<Resource>Handler(deps)`, with shared logic as methods (e.g. `requireTitle`,
  `respond`); single-route groups (system, download, metadata, indexer) keep
  inline closures. The receiver earns its keep around 3+ routes or shared
  helpers/state. Handlers stay thin — push business logic into `internal/core`.
  Auth endpoints are plain-chi, not Huma.

## Comments

How to word a comment, a doc, a CHANGELOG entry or a PR body is
[`docs/style.md`](docs/style.md) — including the 15 words this codebase uses
twice, which prose has to qualify and identifiers already do. This section owns
the budget; that file owns the English.

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
- **The indexer is the scheduled sweep's scarce resource.** A pass costs one
  search per series (two when the zero-result title-variant fallback fires), so
  `titlesPerPass` × the job interval sets the whole search rate. Cost scales with
  series carrying *unfilled* items, not library size: the due query's `EXISTS`
  drops a series as soon as nothing is wanted, so a complete library is free and a
  satisfied one leaves the queue instead of taking a second slot. To raise
  **back-catalog drain rate**, shorten the interval rather than widen the pass —
  the ratio sets throughput, the width sets peak burst, and a pass issues its
  searches back-to-back with no pacing. That is now the only thing the ratio
  buys: acquisition latency for a current release belongs to the feed.
- **The recent feed inverts that cost, which is why it is the hot path.** One
  request covers every series, so `feed-poll` is flat in library size while the
  sweep is linear in due series. That is what makes the sweep affordable as a
  safety net rather than the mechanism. Don't shorten `feedPollInterval` below
  15 minutes: indexers ask for it, and Sonarr — which sets the community's
  expectation here — defaults to 15 and refuses below 10.
- **Identification**: v1 relies on identity-by-construction (we chose the release);
  hash/AniDB identification and pre-existing-library import are deliberately
  out of v1's design.
