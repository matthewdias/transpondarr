# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.0] — 2026-07-29

Quality profiles and airing awareness: releases are now scored against a
profile instead of ranked by seeders, and Transpondarr now knows when episodes
air — feeding a calendar, a seasonal discovery page, and a scheduled refresh
that keeps a releasing series growing on its own.

### Added

- **Quality profiles drive release selection.** Releases are scored against a
  profile — ranked release groups (dominant by construction), resolution
  order, and reward-only preferences over source, subtitle type, codec, and
  repack/version — instead of ranked by raw seeders. Hard excludes and an
  optional minimum-score floor let automation answer "nothing yet" rather
  than grab something unwanted.
- **Profile management UI and per-series assignment** — create, edit,
  reorder, and delete profiles under Settings → Quality profiles (deleting a
  profile in use prompts to migrate its series first), and assign a profile
  per series.
- **The Releases tab shows each release's score and its breakdown** — a
  per-axis tooltip explains the ranking, and a release outside the profile is
  shown with an amber score and the profile's reason, never hidden.
- **Profiles inform manual grabs; they gate only automation.** A manual grab
  always succeeds in one request — no confirm step; when the release falls
  outside the profile, the response carries `ineligible_reason` and the UI
  reports it after the fact.
- **Per-series pinned release group** — "for this show, this group's release
  is definitive." The pin is an absolute sort tier above profile scoring, so
  no stack of bonuses can outrank it — but it never bypasses eligibility: a
  pinned release that trips a hard exclude or the score floor stays refused.
- **Air dates from AniList** — wanted items now carry when they air, synced
  in the background off the job runner. Absence is normal, not an error:
  AniList's schedule coverage thins out before ~2015 and can skip episodes
  even on modern titles.
- **Calendar view** — upcoming episodes for monitored series
  (`GET /api/v1/calendar`), each carrying its acquisition status; monitored
  series with no schedule are listed as unscheduled instead of silently
  missing.
- **Discovery page** — a browse-and-add seasonal chart with format,
  airing-status, and genre filters, backed by a per-season cache; entries
  already in the library are marked as tracked.
- **A releasing series now grows on its own.** A scheduled metadata refresh
  adds newly-announced episodes as wanted items, and the airing sync creates
  the items its schedule names — covering long-runners whose total episode
  count AniList never publishes.
- **`GET /api/v1/system/jobs` reports background job status** — each job's
  interval, last run, how long it took, its last error, and when it runs next.
  "Did the refresh run, and did it fail?" was previously answerable only by
  reading server logs.

### Fixed

- **Dimension-form resolutions now count.** A release named with `1920x1080`
  kept the literal dimension string as its resolution, so it scored zero on
  the resolution axis and slipped past a `1080p` hard exclude; the parser now
  folds dimension forms to the height form profiles are written in.
- **Blocking a group in the profile editor no longer yanks the row away from
  the cursor.** Blocked groups still serialize last on save, but the list no
  longer re-sorts mid-edit.

### Security

- **Upgraded React Router to v8** to clear GHSA-qwww-vcr4-c8h2, a CSRF bypass
  in its unstable RSC APIs. The UI's declarative routing was never
  exploitable, but the only patched release line is 8.x.

### Internal

- **Background work runs on a named job runner** (`internal/core/jobs`) instead
  of a bare `go` per feature. Jobs are registered by name with an interval, a
  panicking job is contained to the run that caused it (its own loop and every
  other job survive), and shutdown drains in-flight runs before the store
  closes. Deliberately not a cron library and not a persistent queue: intervals
  only, nothing durable across restarts.
- **The importer now runs on the job runner** instead of its own goroutine, so
  it reports a real last run, duration, and error like every other job, and
  the entrypoint is down to one background mechanism and one drain.
- **Session cleanup no longer races the database close on shutdown.** It was
  started as an unawaited goroutine, so a sweep could be mid-`DELETE` when the
  store closed; it now drains with everything else inside the shutdown budget.
  The shutdown warning also names which worker overran instead of just
  reporting that something did.
- **Interval loops are now tested with `testing/synctest`**, so scheduling
  behaviour is asserted exactly against a virtual clock rather than polled
  against wall-clock deadlines.
- **`make test` now runs under the race detector.** The job runner's status
  fields are written by each job's goroutine and read by the HTTP handler, so a
  missing lock would be invisible without it. The existing suite was already
  race-clean; the whole run costs about a second more.
- **A fuzz target guards `parser.Parse` against panics.** Release titles come
  from external indexers through an unmaintained parsing dependency, so
  "never panics on arbitrary input" is now checked in rather than assumed.
- **The frontend suite is split into node and happy-dom vitest projects**, so
  pure-logic suites skip the ~350ms-per-file DOM build.
- **Local verification is scoped to what changed**, with CI named as the
  full-suite enforcement point — and the CI gates that stance relies on
  hardened.

## [0.2.1] — 2026-07-23

Frontend foundations: the web UI is now held to the same test/lint/format bar
as the Go backend, plus one History-tab fix.

### Fixed

- **The History tab no longer presents a failed grab as "Downloading".** A grab
  that settled as failed (the download errored in the client, or vanished past
  the missing-from-client grace period) rendered with the in-progress icon and
  verb; it now gets a distinct destructive-toned **Failed** row.

### Internal

- **The frontend now has a test runner** (Vitest), wired into `make test` — the
  web UI is no longer exempt from the repo's TDD process.
- **Formatting and linting are enforced end to end**: Prettier (checked in
  `make web-lint`), oxlint's `react/exhaustive-deps` and suspicious rule
  category, and a self-installing pre-commit hook (gofmt + prettier) so a quick
  manual edit can't reach CI unformatted.
- **CI now fails when the committed `api-types.ts` drifts** from the OpenAPI
  spec, keeping the typed frontend client honest.

## [0.2.0] — 2026-07-23

A reliability release: no download can appear stuck as "downloading" forever
anymore, and imports now survive crashes, power loss, and restarts.

### Fixed

- **Every permanently-stuck "downloading" state is gone.** The three ways a grab
  could wedge forever are each fixed or surfaced honestly:
  - A torrent removed from the download client out-of-band is now reconciled:
    after a 5-minute grace period the grab fails and the episode reverts to
    wanted (this closes the known limitation from 0.1.0).
  - A deferred batch/season-pack grab now shows a distinct **deferred** status
    instead of "downloading" forever. It stays honest — the bytes are on disk
    and seeding — and manually grabbing a single-episode release replaces the
    deferred grab cleanly.
  - An import that keeps failing (a qBittorrent path-mapping gap, library
    permissions, disk full) now surfaces as **stuck**, with the actual error
    shown on the grab, instead of retrying silently with the reason visible
    only in the logs.
- **Folder-wrapped downloads now import.** A single-episode torrent that
  delivers a directory payload is resolved to its one episode file at
  completion time; `import_deferred` is reserved for payloads that genuinely
  can't be disambiguated (real batches).
- **Imports are crash-safe.** Durability and shutdown ordering across the whole
  import path:
  - Copy-mode imports fsync the file before renaming it into the library —
    previously a power loss could land the rename before the data, leaving a
    truncated episode that was treated as already-imported forever.
  - Hardlink-mode imports fsync the linked inode (and directory entry) before
    the grab settles, closing the same window.
  - Truncated files left in the library by past crashes are detected and
    reclaimed for re-import instead of being invisible.
  - Graceful shutdown now waits for the importer: the store can no longer close
    mid-import, in-flight multi-GB copies abort promptly within the shutdown
    budget, and a completed `Place` always gets its have/status writes.
- **API failures in the web UI render as error states with retry**, not as
  misleading empty states ("No titles found" after a rate-limited AniList
  search). Abandoned searches are now aborted instead of left running, and an
  untrimmed search cache key no longer causes duplicate AniList queries.
- **Expired sessions are swept on a daily ticker**, not only at startup, so
  long-lived instances no longer accumulate expired session rows.

### Security

- **Rate-limited the change-password endpoint** (`POST /api/v1/auth/password`).
  It verifies the current password but was not throttled, so repeated wrong
  guesses were unmetered. This matters most under `TRANSPONDARR_AUTH_REQUIRED=local`,
  where any loopback/private-network client is admitted without a credential: the
  endpoint was an unauthenticated password-guessing oracle for anyone on the LAN.
  In the default `enabled` mode a valid session was already required, so there it
  is re-authentication hygiene. Unmetered argon2id verification was also a cheap
  CPU/memory exhaustion lever. Login and change-password now share a single
  per-client bucket (5 attempts per 15 minutes) rather than getting one each,
  since both verify the same admin password.

## [0.1.0] — 2026-07-22

The initial release: the full anime acquisition loop, end to end.

### Added

- **Acquisition pipeline** — add a series from AniList, search a Torznab/Prowlarr
  indexer, grab via qBittorrent, and hardlink the finished file into a
  Plex/Jellyfin-ready library (seeding-safe, correct `Season 01` naming).
- **Web UI** (embedded React SPA) — series list, add-series (live AniList search),
  per-episode wanted/downloading/have status, release matching + grab, and history.
- **Runtime settings UI** — edit qBittorrent / indexer / library integrations live,
  with changes applied without a restart.
- **Forms authentication** — first-run admin setup, session login for the web
  UI, and a separate machine API key (`X-Api-Key`) for dashboards and scripts.
- **AniList metadata** behind a status-aware read-through SQLite cache and a
  rate-limit-respecting client (min-interval limiter + 429/Retry-After backoff).
- **Content-agnostic core** keyed on `WantedItem`, with pluggable `Indexer` /
  `DownloadClient` / `LibraryTarget` interfaces.
- **Typed HTTP API** under `/api/v1` with an OpenAPI 3.1 spec
  (`transpondarrd openapi` prints it) and a public, unauthenticated health
  endpoint (`GET /api/v1/health`).
- **Docker runtime** — multi-arch distroless image that fixes `/config`
  ownership automatically: it starts as root, chowns the config volume, and
  drops to `PUID`/`PGID` (default `1000:1000`) before serving, so there's no
  pre-creating the bind-mount dir with `sudo chown` (pass `--user` to skip the
  root phase; an unwritable data dir fails fast with an actionable error).
  Ships with a built-in container `HEALTHCHECK` (the binary probes its own
  health endpoint — no shell or curl needed) and an example
  `docker-compose.yml`.
- **Packaging** — single static binary (`CGO_ENABLED=0`), GoReleaser +
  GitHub Actions CI/release, with SPDX SBOMs and build-provenance attestations
  for the release archives and the Docker image.

### Known limitations

- Indexing is via Torznab/Prowlarr only currently.
- Season-pack / batch releases are recognised but not yet importable (they are
  surfaced unmatched with a clear reason rather than grabbed).
- No remote path-mapping: qBittorrent and Transpondarr must resolve the same paths
  (satisfied by mounting the shared volume identically).
- A torrent removed from the client out-of-band is not yet reconciled (a torrent that
  _errors_ in the client is marked failed and the item becomes grabbable again).

[Unreleased]: https://github.com/matthewdias/transpondarr/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/matthewdias/transpondarr/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/matthewdias/transpondarr/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/matthewdias/transpondarr/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/matthewdias/transpondarr/releases/tag/v0.1.0
