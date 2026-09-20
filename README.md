# Transpondarr

An anime-focused PVR — Sonarr's job, built around anime-native tooling and
metadata. It monitors anime series and films, finds releases on anime indexers,
drives a download client, and organizes the results into a media library.

> **Status:** Beta. The acquisition loop runs end-to-end and unattended. Add a
> series or a film from AniList, and what you monitor is searched, graded against
> your quality profile, grabbed via qBittorrent, and hardlinked into a
> Plex/Jellyfin-ready library. Automation ships off by default; flip it on in
> Settings, or set it to **notify-only** first to watch what it would grab
> without grabbing anything. Indexing is via Torznab/Prowlarr for now.

## Why not use Sonarr?

Anime breaks Sonarr's assumptions: messy fansub filenames, absolute vs.
per-season numbering, release-group/dual-audio/sub preferences, and metadata
that comes from AniList/AniDB rather than TVDB.

## Features

**Today:**

- **AniList-native metadata** — add series and films from AniList search, browse
  a seasonal discovery chart, and see upcoming episodes and film premieres on an
  airing calendar keyed to Japanese broadcast times.
- **Series and films, each handled as itself** — an episode is matched by number
  and filed under its show; a film is matched by its name and release year and
  filed into a movies library as `Placeholder Film (2019)/Placeholder Film
  (2019).mkv`. Plex and Jellyfin use separate libraries for films and shows, so
  each library gets its own root in Transpondarr. Until the movies root is set, a
  grabbed film stays in the Activity queue instead of being imported into the
  wrong library. Which handling a title gets depends on its format, never its
  episode count, so a one-episode OVA is a series and files with them. A film's
  year is what automation matches on, so automation doesn't grab a film until its
  year is published; searching and grabbing by hand work throughout.
- **Automated acquisition** — recent-feed polling grabs new releases within
  minutes of them appearing, and a scheduled search sweep queries the indexer for
  everything that already existed. Monitoring is per title **and per episode**:
  choose at add time whether to search for a whole back catalogue or only what
  airs next, and unmonitor anything you don't want searched for. Automation runs
  under a global off / notify-only / on switch (off until you enable it).
  **Notify-only** rehearses the whole pipeline — real searches and real
  decisions, reported instead of grabbed. Requests can be filtered to specific
  indexer categories, and if a feed poll misses a page, the series that aired
  inside the gap go back to the front of the search queue.
- **A Wanted queue that shows why** — everything still missing across the
  library, and everything you have that scores below its profile's cutoff, each
  with the reason it hasn't been grabbed: automation off, unmonitored, queued for
  search, blocklisted — or the release the last pass found and declined, and why.
- **Notifications and an activity feed** — Discord, generic webhook, and ntfy,
  with per-event toggles and a test button each; an Activity page collects the
  in-flight queue, the grab/import history across every title, and any download
  left in the client that no grab is linked to.
- **Anime-aware quality profiles** — release group is the dominant axis, then
  resolution/release source, dual audio, and sub preferences, with a minimum
  score and hard excludes. A profile is chosen when you add a title and can be
  reassigned from its page later. A per-title **pinned group** can also mean
  *wait for*: automation holds new episodes for the pinned release group's
  release before taking another group's. Opt a profile into **upgrades** and an
  episode you already have is re-grabbed while its file scores below the cutoff.
  Once the file meets the cutoff, automation takes only a v2 or repack of that
  release, from the same release group at the same resolution. **Still take v2s
  and repacks after cutoff** is on by default; turn it off to leave the file alone
  for good.
- **Failure memory** — a failed release is blocklisted with escalating expiry
  instead of re-grabbed forever. If many grabs fail within minutes of each other,
  a breaker treats the failures as an environmental fault and stops blocklisting,
  so one bad afternoon doesn't blocklist the library. Everything is visible in
  the UI, and a blocklisted release can be unblocked there.
- **Manual control** — search and grab by hand, with an episode's Search opening
  the release list focused on that episode. Your quality profile never blocks a
  manual grab: profiles are advisory on manual actions and enforced only on
  automation. A release that doesn't match any of the title's episodes, or isn't
  the film, is still refused (eg. another show's release, or an episode number
  past the title's last episode).
- **Seeding-safe library import** — hardlink (or copy) into Plex/Jellyfin-ready
  naming, without breaking the seeding torrent. Episodes file into season
  folders or flat, whichever suits your media server's scanner. Season packs
  import episode by episode, so a back catalogue arrives in one grab, and
  anything the importer can't place can be fixed by hand from the Activity
  queue. Archived payloads aren't unpacked: a RAR-set download is deferred with
  a reason listing what to extract, and extracting it in place then retrying
  from **Fix import** completes the import.
- **Self-hosted, single binary** — embedded web UI, login + API key auth, REST
  API with an OpenAPI spec, observable background jobs, and live-editable
  settings — no restarts.

**Planned** (tracked in the
[milestones](https://github.com/matthewdias/transpondarr/milestones)):

- Post-1.0: AniList account sync (auto-monitor your Watching list), adopting a
  pre-existing library and detecting when it changes on disk, more indexers and
  download clients with per-title routing between them, and Sonarr-API
  compatibility for existing dashboard/mobile apps.
- Post-1.0: first-class handling for series whose releases aren't numbered the way
  AniList numbers them — continuously-airing long-runners, fan re-cuts, and a
  per-series override for when the automatic mapping is wrong.

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
> root phase, run with `--user "$(id -u):$(id -g)"` — the config dir
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
`_PASSWORD` to bootstrap one. Machine clients (dashboards, scripts, a future Home
Assistant integration) authenticate to `/api/*` with a separate **API key**, sent
in the `X-Api-Key` header. The key is generated and persisted on first run and
shown in **Settings → API access** (set `TRANSPONDARR_API_KEY` to pin one).
Health check (public):

```sh
curl localhost:9797/api/v1/health
```

## Configuration

Integrations are set through `TRANSPONDARR_*` environment variables **or edited at
runtime in the Settings UI**. A Settings UI edit is stored in the DB, takes
precedence over the environment, and applies live, without a restart. An
integration left unconfigured is disabled, and the server still starts.

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
| `TRANSPONDARR_STALL_TIMEOUT_HOURS`         | `6`                       | Hours a download may go without transferring anything before its grab is failed and the release remembered; `0` disables the timeout. Covers a download the client reports as stalled and one still fetching a magnet's metadata. A download with any progress is never abandoned. |
| `TRANSPONDARR_TORZNAB_URL`                 | —                         | Torznab feed (Prowlarr/Jackett); unset ⇒ no indexer.                                                      |
| `TRANSPONDARR_TORZNAB_APIKEY`              | —                         | Torznab API key.                                                                                          |
| `TRANSPONDARR_TORZNAB_NAME`                | `torznab`                 | Display name for the indexer.                                                                             |
| `TRANSPONDARR_TORZNAB_CATEGORIES`          | —                         | Comma-separated Newznab category IDs sent as `cat=` on every search and the recent feed (anime is usually `5070`); unset ⇒ no filter. |
| `TRANSPONDARR_LIBRARY_DIR`                 | —                         | Library root episodes import into; unset ⇒ episodes do not import.                                                        |
| `TRANSPONDARR_LIBRARY_MOVIES_DIR`          | —                         | Library root films are placed into, separate from `TRANSPONDARR_LIBRARY_DIR`; unset ⇒ a grabbed film stays in the Activity queue instead of importing. |
| `TRANSPONDARR_LIBRARY_SERIES_LAYOUT`       | `season_folders`          | Path shape inside the series root: `season_folders` \| `flat`. Films are unaffected, and switching applies to future imports only. |
| `TRANSPONDARR_IMPORT_MODE`                 | `auto`                    | `auto` (hardlink, copy across filesystems) \| `hardlink` \| `copy`.                                       |
| `TRANSPONDARR_AUTOMATION_ENABLED`          | `false`                   | `off` \| `notify_only` \| `on` (bools also accepted). `notify_only` rehearses: it reports what automation would grab, without grabbing. |
| `TRANSPONDARR_PIN_DELAY_HOURS`             | `0`                       | Hours automation holds a grab for a series' pinned release group before taking another group's release; per-series overrides in the UI. |
| `PUID` / `PGID`                            | `1000` / `1000`           | Docker only: the uid:gid the container drops to after fixing `/config` ownership on start.                |

> **Auth & reverse proxies.** The `local` auth mode skips login only for requests
> from loopback/private addresses **with no forwarding headers**. A reverse proxy
> sets `X-Forwarded-For`, so reverse-proxied requests always require login, and a
> same-host proxy can't turn the `local` bypass into open access. Session cookies
> are marked `Secure` automatically when the proxy sets `X-Forwarded-Proto: https`.

## Docker deployment

For a real deployment alongside qBittorrent and a media server, use
[`docker-compose.yml`](docker-compose.yml) as a template. Five things matter:

- **Imports hardlink from the path qBittorrent reports.** Mount your shared
  downloads/library volume into Transpondarr at the _same path_ qBittorrent uses,
  with both on one filesystem (a hardlink can't cross filesystems). The standard
  single-mount layout (`/data/torrents` + `/data/media`) satisfies both
  requirements. The movies root is one more directory under the same mount, not
  a second mount.
- **Run Transpondarr as qBittorrent's user.** Set `PUID`/`PGID` to the UID:GID
  qBittorrent runs as. That user owns the downloads, so it can hardlink them (see
  the next point). It also needs to write into both library roots (`/data/media`
  and `/data/media-movies` in the example), including the folders already in them,
  so check that separately. If an import can't write into a folder, it fails with
  "permission denied" and waits in the Activity queue; `auto` import mode doesn't
  copy instead.
  If you change `PUID` on an existing install, the folders the previous user
  created are writable only by that user, so the next episode of a title already
  in the library fails with "permission denied". Give the new user those folders,
  running this against the library roots' host paths:
  `find /data/media /data/media-movies -type d -exec chown <PUID>:<PGID> {} +`.
  Change only the folders: a file in a library root may be a hardlink, and
  changing its owner changes the download's owner too.
- **On Linux, `PUID` needs permission to hardlink qBittorrent's downloads.** The
  kernel setting `fs.protected_hardlinks` is on by default on most distributions
  and in Docker Desktop. With it on, a hardlink to a file fails with "operation not
  permitted" unless the user making it owns the file or can both read and write it.
  qBittorrent normally saves downloads writable only by its own user, so a
  different `PUID` can't hardlink them. In `auto` import mode, Transpondarr copies
  every file instead, using twice the disk space, and logs the link error on each
  import. The first import to hit a given failure logs it as a warning, and a
  restart or any save under Settings → Library arms that warning again. In
  `hardlink` import mode, the grab waits in the Activity queue with an "operation
  not permitted" error, and the importer retries it every 15 seconds until the
  permissions change. `PUID=0` is affected too: `cap_drop: ALL` in
  [`docker-compose.yml`](docker-compose.yml) leaves root subject to the same check
  as anyone else, and it can't write into a library folder another user created
  either — see [SECURITY.md](SECURITY.md).
- **To run as a `PUID` other than qBittorrent's, share a group instead.** Put both
  containers in the same group and make both library roots writable by it. `PGID`
  does that, except under `PUID=0`: that skips the privilege drop, which is what
  applies `PGID`, so set the group on the container itself (`user: "0:1000"`, or
  `group_add`). Then set qBittorrent's umask to `002` (eg. `UMASK=002` in the
  linuxserver image), so new downloads are group-writable. Files downloaded before
  the umask change stay read-only to the group until you `chmod g+w` them.
- **Who owns imported files.** The container starts as root, fixes `/config`
  ownership, and drops to `PUID`/`PGID` before serving. The folders Transpondarr
  creates in a library root, and any file it copies there (`copy` import mode, or
  `auto` when a hardlink isn't possible), are owned by that user. A hardlink is the
  downloaded file under a second name, so it has the same owner as the download.

Persist the `/config` volume (it contains the SQLite DB).

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
