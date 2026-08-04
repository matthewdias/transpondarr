# Transpondarr

An anime-focused PVR — Sonarr's job, built around anime-native tooling and
metadata. It monitors series, finds releases on anime indexers, drives a
download client, and organizes the results into a media library.

> **Status:** Beta. The acquisition loop runs end-to-end and unattended — add a
> series from AniList, and monitored episodes are searched, graded against your
> quality profile, grabbed via qBittorrent, and hardlinked into a
> Plex/Jellyfin-ready library. Automation ships off by default; flip it on in
> Settings, or set it to **notify-only** first to watch what it would grab
> without grabbing anything. Indexing is via Torznab/Prowlarr for now.

## Why not just use Sonarr?

Anime breaks Sonarr's assumptions: messy fansub filenames, absolute vs.
per-season numbering, release-group/dual-audio/sub preferences, and metadata
that lives on AniList/AniDB rather than TVDB.

## Features

**Today:**

- **AniList-native metadata** — add series from AniList search, browse a seasonal
  discovery chart, and see upcoming episodes on an airing calendar keyed to
  Japanese broadcast times.
- **Automated acquisition** — recent-feed polling grabs new releases within
  minutes of them appearing, and a scheduled sweep backs it up for everything
  that already existed; per-series monitoring and a global off / notify-only / on
  switch (off until you enable it). **Notify-only** rehearses the whole thing —
  real searches and real decisions, reported rather than grabbed.
- **Notifications and an activity feed** — Discord, generic webhook, and ntfy,
  with per-event toggles and a test button each; an Activity page collects the
  in-flight queue and the grab/import history across every series.
- **Anime-aware quality profiles** — release group is the dominant axis, then
  resolution/source, dual audio, and sub preferences, with a score floor and hard
  excludes. A per-series **pinned group** can also mean *wait for* — hold new
  episodes for the pinned group's release before settling for another.
- **Failure memory** — a failed release is blocklisted with escalating expiry
  instead of re-grabbed forever, an environmental-fault breaker stops one bad
  afternoon from blocklisting the library, and everything is visible and
  unblockable in the UI.
- **Manual control that's never refused** — search and grab any release by hand;
  profiles inform manual actions but only gate automation.
- **Seeding-safe library import** — hardlink (or copy) into Plex/Jellyfin-ready
  naming, without breaking the seeding torrent.
- **Self-hosted, single binary** — embedded web UI, login + API key auth, REST
  API with an OpenAPI spec, observable background jobs, and live-editable
  settings — no restarts.

**Planned** (tracked in the
[milestones](https://github.com/matthewdias/transpondarr/milestones)):

- Batch/season-pack import, quality upgrades, and episode-targeted search.
- Per-indexer category filtering, and feed-gap recovery for releases that scroll
  off between polls.
- Post-1.0: anime movies, AniList account sync (auto-monitor your Watching list),
  importing a pre-existing library, more indexers and download clients, and
  Sonarr-API compatibility for existing dashboard/mobile apps.
- Post-1.0: first-class handling for series whose releases aren't numbered the way
  AniList numbers them — continuously-airing long-runners, fan re-cuts, and a
  per-series override for when the automatic mapping is simply wrong.

## Install

Transpondarr ships as a single static binary with the web UI embedded — no
separate database or frontend to run.

### Docker

Pull the published multi-arch image (or build locally with `docker build -t
transpondarr .`):

```sh
docker pull ghcr.io/matthewdias/transpondarr:latest
docker run -p 9797:9797 -v ./config:/config ghcr.io/matthewdias/transpondarr:latest
```

> The container starts as root only to fix ownership of the mounted config dir,
> then drops to `PUID`/`PGID` (default `1000:1000`) before serving. To skip the
> root phase entirely, run with `--user "$(id -u):$(id -g)"` — the config dir
> must then already exist and be writable by that user.

For a real deployment alongside qBittorrent and a media server, see
[Docker deployment](#docker-deployment) below.

### Binary

Download the archive for your platform from the
[releases page](https://github.com/matthewdias/transpondarr/releases), extract
it, and run `./transpondarrd`.

To build from source instead, see [CONTRIBUTING.md](CONTRIBUTING.md).

## First run

The server listens on `:9797`. The **web UI uses a login** (username + password):
on first run you create an admin account, or set `TRANSPONDARR_AUTH_USERNAME`/
`_PASSWORD` to bootstrap one. A separate **API key** guards `/api/*` for machine
clients (dashboards, scripts, a future HA integration) via the `X-Api-Key` header
— it's generated and persisted on first run and shown in **Settings → API access**
(set `TRANSPONDARR_API_KEY` to pin one). Health check (public):

```sh
curl localhost:9797/api/v1/health
```

## Configuration

Integrations are set through `TRANSPONDARR_*` environment variables **or edited at
runtime in the Settings UI** (those DB overrides take precedence over the
environment and apply live, without a restart). Unset, unconfigured integrations
are simply disabled — the server still starts.

| Variable                                   | Default                   | Purpose                                                                                                   |
| ------------------------------------------ | ------------------------- | --------------------------------------------------------------------------------------------------------- |
| `TRANSPONDARR_API_KEY`                     | _(generated + persisted)_ | Machine-client key for `/api/*` (`X-Api-Key`). Auto-generated and saved in the DB; set to override.      |
| `TRANSPONDARR_AUTH_USERNAME` / `_PASSWORD` | —                         | Bootstrap the initial web-UI admin account on first run (otherwise use the setup screen).                 |
| `TRANSPONDARR_AUTH_REQUIRED`               | `enabled`                 | `enabled` (always require login) \| `local` (skip login for local/private addresses).                     |
| `TRANSPONDARR_ADDR`                        | `:9797`                   | Listen address.                                                                                           |
| `TRANSPONDARR_DATA_DIR`                    | `./data`                  | SQLite DB + state (`/config` in Docker).                                                                  |
| `TRANSPONDARR_DB`                          | `<DATA_DIR>/transpondarr.db` | SQLite DB file path. Override to relocate the DB independently of the data dir.                        |
| `TRANSPONDARR_QBIT_URL`                    | —                         | qBittorrent WebUI root; unset ⇒ no download client.                                                       |
| `TRANSPONDARR_QBIT_USER` / `_PASSWORD`     | —                         | qBittorrent credentials.                                                                                  |
| `TRANSPONDARR_QBIT_CATEGORY`               | `transpondarr`            | Category applied to grabbed torrents.                                                                     |
| `TRANSPONDARR_TORZNAB_URL`                 | —                         | Torznab feed (Prowlarr/Jackett); unset ⇒ no indexer.                                                      |
| `TRANSPONDARR_TORZNAB_APIKEY`              | —                         | Torznab API key.                                                                                          |
| `TRANSPONDARR_TORZNAB_NAME`                | `torznab`                 | Display name for the indexer.                                                                             |
| `TRANSPONDARR_LIBRARY_DIR`                 | —                         | Library root for imports; unset ⇒ import disabled.                                                        |
| `TRANSPONDARR_IMPORT_MODE`                 | `auto`                    | `auto` (hardlink, copy across filesystems) \| `hardlink` \| `copy`.                                       |
| `TRANSPONDARR_AUTOMATION_ENABLED`          | `false`                   | `off` \| `notify_only` \| `on` (bools also accepted). `notify_only` rehearses: it reports what automation would grab, without grabbing. |
| `TRANSPONDARR_PIN_DELAY_HOURS`             | `0`                       | Hours automation waits for a series' pinned group before taking another; per-series overrides in the UI.  |
| `PUID` / `PGID`                            | `1000` / `1000`           | Docker only: the uid:gid the container drops to after fixing `/config` ownership on start.                |

> **Auth & reverse proxies.** The `local` auth mode skips login only for requests
> from loopback/private addresses **with no forwarding headers**, so reverse-proxied
> requests (which set `X-Forwarded-For`) always require login — a same-host proxy
> can't turn local-bypass into open access. Session cookies are marked `Secure`
> automatically when the proxy sets `X-Forwarded-Proto: https`.

## Docker deployment

For a real deployment alongside qBittorrent and a media server, use
[`docker-compose.yml`](docker-compose.yml) as a template. Two things matter:

- **Imports hardlink from the path qBittorrent reports.** Mount your shared
  downloads/library volume into Transpondarr at the _same path_ qBittorrent uses,
  with both on one filesystem (a hardlink can't cross filesystems). The standard
  single-mount layout (`/data/torrents` + `/data/media`) satisfies this.
- **Ownership.** Set `PUID`/`PGID` to the UID:GID that owns your media volume —
  the container starts as root, fixes `/config` ownership, and drops to that user
  before serving, so hardlinks into the library land with the right ownership.
  Persist the `/config` volume (it holds the SQLite DB).

Verify a running deployment (the second call needs your API key):

```sh
curl -s http://localhost:9797/api/v1/health                      # {"status":"ok",...}
curl -s -X POST -H "X-Api-Key: <key>" http://localhost:9797/api/v1/download/test
#   {"status":"ok","client":"qbittorrent"}   (502 => qBit URL/creds wrong)
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the stack, toolchain, build-from-source
steps, and codebase layout.

## License

[Apache-2.0](LICENSE). Third-party dependency licenses and notices are
reproduced in [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md) (regenerate with
`make notices`).
