# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/matthewdias/transpondarr/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/matthewdias/transpondarr/releases/tag/v0.1.0
