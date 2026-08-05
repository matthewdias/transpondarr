# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Wanted is a real page: every missing episode across the library, and why it
  is still missing.** Two tabs. *Missing* lists every episode still worth
  acquiring — the scheduled search's own definition of wanted, so an episode
  already downloading is Activity's row, not this one's — newest broadcast
  first, with the back catalogue after it in episode order. Unaired episodes are
  hidden behind a toggle, since the Calendar owns the forward-looking view, and
  an unmonitored toggle mirrors the Calendar's. *Cutoff Unmet* lists the
  episodes you hold that score below their profile's cutoff, with the held
  release name and its score against that cutoff; it only ever lists series on a
  profile with upgrades enabled, and re-scores from the stored release name, so
  editing a profile moves the list immediately. Every row's Search opens the
  Releases tab already focused on that episode, where the manual grab is
  unchanged. Each row states the one thing most explaining why it is still
  missing — not aired, unmonitored, no indexer, automation off or rehearsing,
  the last grab failed (with the error), releases blocklisted, or where its
  series sits in the search queue — all read from stored state when you look, so
  a reason is never a stale record of an earlier pass. "Search selected" and
  "Search all" put those series back at the front of the search queue and start
  a run rather than firing one indexer request per series: the per-pass limit
  that protects your indexer still applies, so a large library drains over
  several passes, and the confirmation says queued rather than done. Under
  notify-only it says the run will rehearse and grab nothing. Both lists page as
  you scroll rather than loading whole, so a fresh library add of several
  hundred episodes stays responsive. An episode's Releases focus is now
  addressable as `#/series/<id>?item=<n>`.

- **An episode's Search button now searches for that episode.** It used to hand
  you the series-wide result list and leave you to scan it for the row whose
  Match badge said E7. The Releases tab now opens pre-filtered to the candidates
  that cover the episode you asked about — a single or the batch that contains
  it — with a dismissible chip naming the focus and a count of what is hidden.
  Nothing covering it says so, and offers the full list in one click. The search
  itself is unchanged and still series-wide: *Search all wanted* and clicking
  the Releases tab directly both stay that way, and a grab is still decided by
  the server.

- **An episode you already have can be re-grabbed when a better release turns
  up.** Off by default and enabled per quality profile: *Upgrade until cutoff*
  in the profile editor, with the cutoff picked from the same landmarks your
  score weights compose ("top group, best resolution") rather than typed as a
  bare number. It is a cutoff, not a chase — while what you hold scores below
  it, any strictly better release replaces it; once it reaches the cutoff, the
  episode is left alone forever. A second switch keeps taking the same group's
  v2 or repack of the very file you hold even above the cutoff, since that is a
  fix for a broken release rather than a better one. Upgrades ride the recent
  feed, which costs one request for the whole library; the scheduled search
  never spends a search on a complete series, though a series it searched anyway
  takes an upgrade it happens to find. A manual search now offers releases for
  episodes you already have too, and grabs them without asking about the cutoff
  — profiles inform manual actions and gate only automation. The library file is
  replaced in place, with anything the superseded release left under the same
  episode name (a different container, a sidecar) cleared with it; a failed or
  unresolvable upgrade leaves the episode exactly as it was. Nothing removes the
  superseded torrent from your download client. Adds migration 00017
  (`upgrades_enabled`, `cutoff_score`, `upgrade_v2_above_cutoff` on quality
  profiles, and the release each held episode is holding, backfilled from its
  import).

- **The indexer can be pointed at specific Newznab categories.** A new
  *Categories* field on the indexer settings (env
  `TRANSPONDARR_TORZNAB_CATEGORIES`) takes a comma-separated list of IDs — anime
  is usually `5070` — and sends it as `cat=` on every search and on the recent
  feed. It matters most for the feed: one page is about 100 entries covering the
  whole endpoint, so on a general-purpose indexer music, books and software eat
  the window an anime release needs to be seen in. Empty is the default and
  keeps today's behaviour exactly — every request unfiltered — so nothing
  changes until you fill it in. A `cat` you baked into a Prowlarr or Jackett
  feed URL is left alone while the field is empty.

- **Season packs and batches now import episode by episode, and automation
  prefers them.** A multi-episode payload used to settle every grab row it
  covered as "downloaded (batch)" — terminal, with the bytes on disk and the
  filing left to you — and automation was deliberately kept away from packs for
  exactly that reason. The importer now walks a completed payload once and maps
  its files onto the episodes the release covered, placing each on its own, so
  the refusal is gone: a back-catalog series that has both singles and a pack
  takes the pack, one grab instead of one per episode. Ranking gained a coverage
  tier to make that happen deliberately rather than by seeder count, below a
  pinned group, and a weekly single-episode release still ranks purely on your
  profile score. A pack whose numbering runs past the entry — a `01-48` pack
  against a 12-episode season — is now refused as a possible absolute/season
  mismatch instead of silently claiming episodes 1-12.
- **A payload file for an episode the release never claimed is imported too**,
  when that episode exists, is still wanted, and has no download of its own —
  the release titled `03` that ships `03` and `04`.
- **A stuck import can be fixed by hand from the Activity queue.** "Needs import
  fix" rows get a *Fix import* action listing every file in the payload with
  what its name parsed to, and a per-file episode picker preseeded with the
  importer's own suggestion. Use it when a filename is unreadable and nothing
  could be matched to it. Automation never reopens a settled import on its own.

### Changed

- **The Calendar's "Unmonitored" control is now a filter chip rather than a
  switch.** A switch says you are changing a stored preference — which is what
  Monitored on a series is — and using the same control for a view filter made
  both mean less. It now reads "Include: Unmonitored" as a pill that lights up
  when on, matching the new Wanted page. Same behaviour, same keyboard and
  screen-reader semantics.

- **An episode you have with a download in flight now reads as "downloading"**
  rather than as simply had — the state an upgrade in progress puts it in.

- **One import notification per release, not one per episode.** A pack landing
  six episodes now sends a single "Import succeeded" naming the range
  (`Episodes 1-3, 5`). The generic webhook's payload gains an `items` array
  carrying the raw numbers — always present, empty for a single-episode import,
  with `item_number` then `0`.
- **"Downloaded (batch)" is now "Needs import fix"**, and says which episode had
  no file and how many files were left unmatched. It means one file could not be
  picked out of a payload, not that a whole batch was refused. When *nothing* in
  the payload matched an episode, that episode now goes back to wanted and the
  release is remembered, so the next search picks a different one instead of
  parking the item forever.

- **A download that holds only an archive now says so, and can be finished by
  hand.** Transpondarr does not unpack archives — a scene-style RAR set used to
  settle with a flat "the payload holds no video file", and *Fix import* then
  opened on an empty list, which was a dead end. The deferral now names the
  archive and how many volumes it spans (a 12-volume set counts as one thing to
  extract, not twelve), and the dialog lists it with what to do: extract it into
  the download folder and retry, which imports the extracted episode. An archive
  is listed beside the payload's files and can never be assigned to an episode.
  An episode an archive holds stays waiting for you on every path — retrying
  before you have extracted anything tells you so and leaves the download where
  it was, and a download holding one loose episode plus an archived one keeps
  the second waiting instead of giving up on it.
  Password-protected and corrupt archives are indistinguishable from healthy
  ones without an unpacker, so all three defer the same way.

### Fixed

- **A single-file `.rar` download is no longer filed into the library as the
  episode.** A payload that is one plain file was taken as the episode whatever
  its extension, so a torrent containing a lone archive was hardlinked into
  place under an episode's name — a file no player can open, with the episode
  marked as had. Such a payload now defers as the archive it is.

- **A downloaded episode whose filename happens to contain an extras word no
  longer parks itself.** The payload resolver drops files marked `preview`,
  `promo`, `nc`, `ncop` and similar so a bundled creditless opening is never
  mistaken for the episode. When those words appear in the episode's *own* title,
  a folder holding exactly one video filtered to nothing and the grab settled as
  deferred — settled for good, with the file sitting right there. A payload whose
  sole video carries such a word is now imported. A payload where several videos
  all carry one still defers, since there is nothing to distinguish them, and a
  file marked `sample` is still never imported however alone it is: it is a
  truncated copy of the episode, not a title that happens to read that way.

- **A releasing title whose episode count AniList never publishes now adds with
  its episodes.** Such a title previously created a series with *no* wanted
  items and sat at `0 / 0` until a background pass caught up, because the add
  asked only for the episode count — precisely the field that is null. AniList
  exposes the broadcast schedule as a field on the title rather than a separate
  query, so one page of it now rides along in the request the add already makes:
  zero extra API calls, and the item set arrives immediately. Separately, the
  schedule *skips* an episode when two share a broadcast slot — it reads 1, 3,
  4 — so both the add and the background sync now fill the gaps rather than
  transcribing, which is the only thing that creates an episode nothing else
  ever claimed should exist. A filled-in episode has no air date (a normal state
  here) and resets the series' search cadence, so it is looked for on the next
  pass instead of waiting out an accumulated backoff.

- **The feed poll no longer spends AniList requests on title variants.** Every
  poll resolved each wanted series' name variants through the metadata provider,
  and currently-airing titles — exactly the ones carrying wanted items — expire
  from the cache every 6 hours, so a library of airing shows could blow AniList's
  budget on a single 15-minute tick. The poll now reads variants from the local
  metadata cache only, serving even a stale snapshot (names don't go bad the way
  episode counts do); a series with no snapshot still matches on its stored
  title, and the bounded sweep covers it with full variants.

- **An episode that fell through a gap in the recent feed is now searched
  within the next sweep, not up to a day later.** When a busy indexer publishes
  more than one page of releases between two polls, the poll recognises nothing
  on the page it fetches and knows its coverage was broken — but it only warned,
  leaving whatever aired in the meantime to the scheduled search, which with a
  feed configured no longer aims at broadcast times and can be a full day away.
  Such a poll now puts the affected series back at the front of the search
  queue: monitored series still missing an episode that aired inside the gap
  (plus an hour of slack before it, since a rip is published after it airs).
  The reset is deliberately capped at five series per gap event — the same
  number one scheduled pass searches — and skips series already due, so a
  routine gap on a high-volume indexer never queues more searching than the
  schedule can spend.

### Upgrade notes

- **A long-running series already in your library will gain its full back
  catalogue.** This affects monitored series whose AniList entry publishes no
  episode count — in practice the very long ones (One Piece, Detective Conan,
  Pokémon, Crayon Shin-chan). AniList keeps only a *recent window* of broadcast
  records for these (One Piece's first page starts at episode 1123), so until
  now everything below that window was created by nothing at all and the series
  quietly tracked only its recent tail. On the next metadata refresh or airing
  sync each one materializes its whole run — for One Piece, about 1,170 wanted
  items in place of roughly 25.

  Nothing is grabbed by that write, but the new items are wanted and searchable
  immediately, so expect the series to read as far from complete and to stay in
  the search queue while it drains. It costs no additional AniList requests, and
  no additional indexer load per episode either: a sweep spends one search per
  *series*, whatever its item count. If you would rather not track a long-runner's
  back catalogue, unmonitor the series before upgrading — an unmonitored series is
  skipped by the metadata refresh and the airing sync, which are the two passes
  that would create the items, and by the search sweep that would go looking for
  them.

## [0.5.0] — 2026-08-02

Visibility: the release where unattended acquisition stops being silent. v0.4.0
made Transpondarr act on its own; this one tells you what it did — pushed to
Discord, a webhook, or ntfy, and collected in an Activity feed — and lets you
rehearse the whole thing first. Turn automation to **notify-only** and watch a
week of real decisions across your library without a single byte reaching the
download client.

### Added

- **Notifications, with Discord, generic-webhook, and ntfy adapters.** Once
  acquisition runs unattended, silence is the default failure mode: a stuck
  import, a failed grab, an episode landing — all of it previously visible only
  by opening the UI. A new `Notifier` seam carries one structured event that
  each adapter flattens natively: Discord to a colored embed with per-field
  detail, the generic webhook to a documented JSON contract you can script
  against, ntfy to title/priority/tags. ntfy is first-class rather than reached
  through the generic webhook because its priority mapping *is* the feature —
  a stuck import buzzes at high priority, an episode landing does not. Six
  event kinds (grabbed, imported, import stuck, grab failed, series added, and
  rehearsal), per-adapter per-event toggles, and a **test button per adapter**
  so a config can be verified without waiting for a real event. Delivery is
  fire-and-forget: a failing notifier logs and never blocks or fails the
  pipeline that triggered it. A *manual* grab stays deliberately silent — you
  were there.
- **Notification-only mode: rehearse the sweep without grabbing.** The global
  automation toggle grew a third state — off / notify-only / on. In notify-only
  the sweep and feed poll run for real (search, decide, cadence, pinned-group
  holds) and report what they *would* have taken, without recording a grab or
  touching the download client. The negative half reports too, which is the
  more useful half: no eligible candidate, held for a pinned group, refused by
  the season-pack guard — each with its reason. A rehearsal is a firehose by
  design, so ntfy maps it to low priority. Switching to `on` afterwards resets
  every series' search cadence, because a rehearsal settles nothing and would
  otherwise leave the library backed off for releases the feed has already
  consumed.
- **Activity page: the global download and import feed.** The placeholder is
  now the answer to "what did automation do while I wasn't looking?" — a
  **queue** of every in-flight grab across the library, joined with live
  client-reported state, and a paginated **history feed** of grab and import
  events, newest first. The queue surfaces paused, stalled, and checking per
  row, which the derived status vocabulary could not express: a paused torrent
  previously read as "Downloading" forever. With the download client
  unconfigured or unreachable it degrades to grab state rather than erroring.
- **Per-job "Run now" from the Background jobs card.** The runner's trigger has
  been implemented and tested since v0.3.0 with no caller; it now has a button.
  A manual run bypasses the automation kill switch on the same precedent that
  governs manual search and grab — an explicit action is intent, not something
  to gate — so the card confirms in words when a hand-triggered sweep will grab
  for real. Every eligibility rule still applies, and a run in notify-only
  rehearses like any other, so the mode's guarantee holds no matter who
  triggered it. The trigger only queues, so the button never pretends to wait
  for a result.
- **Delete a series.** Anything added previously stayed forever, occupying the
  library list and keeping the sweep searching. `DELETE /api/v1/series/{id}`
  removes the series with its episodes, grab history, and blocklist memory in
  one transaction, behind a confirmation that names what goes and what stays.
  **Library files are never touched** — a decision, not an omission: deleting
  media is a bigger call than deleting a tracking row. An optional flag also
  clears the series' torrents from the download client; without it they are
  left seeding.

### Changed

- **Breaking:** `PUT /api/v1/settings/automation` now takes `mode`
  (`off` | `notify_only` | `on`) in place of the `enabled` boolean, and
  `GET /api/v1/settings` reports it the same way — an old client body is
  rejected. Stored values and `TRANSPONDARR_AUTOMATION_ENABLED` still accept
  the old booleans, so no configuration needs migrating.
- **A re-grab no longer erases the attempt before it.** The per-series History
  tab now reads an append-only event log instead of the single current grab
  row, so a completed lifecycle shows both "Grabbed" and "Imported" and a
  replaced release keeps its record. "Import blocked" leaves history in the
  process — it is live state, owned by the Episodes tab and the Activity
  queue, not an event that happened at a point in time.

### Fixed

- **The Discovery page built 88 hidden year options on every render.** Radix
  keeps a closed `Select`'s items mounted so the trigger can resolve its own
  text, so the year picker's full 1940-onward range was constructed on every
  render — about half the page's mount cost, for a dropdown that is rarely
  opened. The items now render only while the menu is open.

### Internal

- **An append-only `grab_events` table** records the lifecycle the `grabs`
  table structurally cannot: one row per wanted item, overwritten by the next
  attempt. Events are written at the two convergence points — inside the grab
  transaction, and as the importer settles — with the importer's writes
  best-effort, because history must never wedge the pipeline. The migration
  backfills from surviving grab rows so an upgrade does not start empty.
- **The API's first pagination**, keyset-cursor over `(created_at, id)` with an
  opaque cursor, set as the precedent for the endpoints that follow.
- **The notification dispatcher fans out under `context.WithoutCancel`** with
  its own timeout, so neither a request-scoped context nor a shutdown cancels
  an in-flight send, and a hung endpoint cannot leak a goroutine.

## [0.4.0] — 2026-08-01

Monitoring and scheduled search: the release where the monitored flag starts
doing real work. Add a series, touch nothing, and episodes arrive — searched on
a broadcast-aware cadence, graded against your quality profile, grabbed,
imported, and remembered when they fail. Automation ships **off by default**;
enable it under Settings → Automation.

### Added

- **Scheduled search sweep.** A `wanted-search` job periodically searches
  monitored series with outstanding wanted items once their episodes have aired
  (an unknown air date counts as searchable — absence is normal, not an error),
  runs the same match-and-score path as manual search, and grabs only a fully
  eligible release: matched, clear of excludes, over the profile floor. No
  eligible candidate means grab nothing — "nothing yet" is a correct outcome.
  Empty passes back off exponentially from an hour toward a day, clamped so a
  weekly show is still searched at broadcast however many empty passes came
  before; a new episode airing resets the clock, and integration edits in
  Settings apply on the next pass without a restart.
- **Recent-feed polling.** The sweep costs one indexer query per series, so its
  interval must be long; the Torznab recent feed inverts that — one request
  returns the newest releases across the endpoint, matched against everything
  wanted at once. The feed is the hot path, polled on a short interval, and the
  sweep becomes the safety net. A new release for a wanted, aired episode is
  grabbed on the next poll, not the next sweep. An indexer without a feed
  degrades to sweep-only as a supported configuration, and a quiet poll costs
  one request.
- **A pinned group can now mean "wait for," not just "win when present."** The
  sweep holds another group's release for a delay window measured from the
  episode's broadcast, so the pinned group's slower release gets its chance
  before automation settles. Global default plus a per-series override; a
  manual grab is never held.
- **Automation controls.** A global auto-search toggle and the default pin
  delay, persisted and editable live in Settings → Automation — flipping the
  switch takes effect at the next tick, off or on, without a restart. Off
  until you enable it, deliberately: an install that has never configured an
  indexer must not start grabbing on its own. A monitored series page says so
  when automation is globally off, since monitored now means "will be grabbed
  automatically," not just "appears on the calendar."
- **Failure memory: a release that fails is not chosen again.** The sweep's
  determinism made its own bug — a failed download reverted its item to wanted,
  the next pass re-derived the same ranking, and the same release won forever.
  Failed releases are now blocklisted per series with an escalating expiry
  (24 hours, then 7 days, then permanent): repeat failure of the *same* release
  is the only signal separating a dead release from a bad day, so first
  failures stay recoverable while a proven-dead release converges to permanent.
  Identity is info hash *and* normalized title, because Torznab feeds often
  omit the hash. Automation degrades to the next-best eligible release;
  blocked releases appear in the series History tab with the reason, an
  unblock, and bulk clear actions — and a *manual* grab is never refused and
  never leaves a block behind.
- **An environmental-fault breaker guards the failure memory itself.** A full
  disk or a client reaping torrents fails a *different* release every pass —
  the escalation ladder never triggers, and the candidate pool drains into the
  blocklist one entry at a time. Failures across distinct items library-wide
  now trip a breaker that suppresses *recording* (never grabbing) until the
  window passes; Settings says so in words, and clearing the library blocklist
  also closes the breaker so a fixed fault doesn't wait out its window.
  Refused adds are remembered too, when the client can attribute the failure
  to the release itself — an unreachable client blocklists nothing.
- **Background jobs status in Settings.** Each runner job's last run, duration,
  and last error (styled as one), refreshed live — "did automation actually
  run, and did it fail?" no longer requires reading server logs.
- **Season packs are matched honestly, and automation declines them.** Packs
  previously surfaced as unmatched with a reason; they now match their items
  and are declined by *automation* with an explicit ineligible reason instead —
  the importer can only defer a multi-episode payload, and unattended grabbing
  must not volunteer for a state it cannot finish. A manual grab of a pack
  still succeeds, landing in the deferred flow as before. Per-file batch
  import is tracked for a later release.

### Fixed

- **Search terms with unicode punctuation found nothing.** AniList titles carry
  typography that never appears in release names (`×`, `☆`, `・`), so a
  HUNTER×HUNTER-class title searched verbatim died before matching ever ran —
  and unattended search would have inherited the silence. Queries are now
  sanitized (`×` → `x`, separators to spaces), zero results fall back to the
  English then native title variants, and matching transliterates instead of
  deleting, so the romaji variant matches real releases on its own.
- **Dual-titled scene releases misparsed the alternate title as the release
  group.** The trailing `(Romaji Title, Multi-Audio, Multi-Subs)` parenthetical
  became the group, so group ranking, blocking, and pinning silently never
  applied to a whole common release family. An implausible group is now
  distrusted and the real scene group recovered from the codec-dash pattern.
  The same family's no-episode-title variant also lost its source and codec to
  a misfiled tag run; both now survive.
- **Saving any Settings section no longer blanks the Authentication card.** The
  section writers returned a settings body without the auth block and the UI
  cached it whole; all writers now share one complete response.

### Internal

- **Search, decide, and grab moved into one core package** (`internal/core/
  acquire`), shared by the manual routes, the sweep, and the feed poll — one
  matcher, one eligibility path, three entry points. Recording a grab is now a
  single transaction per release, so a multi-episode grab can no longer be
  half-recorded by a mid-write failure.
- **Per-series search cadence lives in the store** (last searched, backoff,
  next due) with an epoch guard so a reset landing mid-sweep — a series
  growing, or being re-monitored — beats the sweep's stale write.
- **The recent-feed capability is a type assertion, not a wider interface**,
  per the repo's optional-capability rule: an indexer that lacks it is a
  supported configuration, and decorators forward it only when the inner
  source really has one.

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

[Unreleased]: https://github.com/matthewdias/transpondarr/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/matthewdias/transpondarr/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/matthewdias/transpondarr/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/matthewdias/transpondarr/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/matthewdias/transpondarr/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/matthewdias/transpondarr/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/matthewdias/transpondarr/releases/tag/v0.1.0
