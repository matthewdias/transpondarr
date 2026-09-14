# Search, feed and grab (`internal/core/acquire`)

The two automatic entry points and the one decision layer under them, plus what
a pass records about what it decided.

### Two entry points, one decision layer

- **Both entry points share one decision layer (#101).** The `feed-poll` job and
  the `wanted-search` search sweep both build a `Match` through
  `Service.evaluate` and act on it through `grabPass`. So the profile's minimum
  score, the blocklist, pinned-group delay and the coverage ranking are written
  once.
- **The feed is only a cheaper *trigger*.** Recent-feed polling (#101) inverts
  the search sweep's lookup: a release name needing a title, rather than a title
  needing releases. That is why it is title × feed entry, deliberately
  unoptimised. A feed page is ~100 feed entries, and the due query already drops
  any title with nothing wanted.
- **The feed writes no search cadence.** Nothing was searched, and a grab
  settles its item, so the search sweep's `EXISTS` drops the title anyway.
- **The one exception is a poll that detects a missing feed page**, from feed
  gap recovery (#140). It resets the search sweep for the titles that aired
  inside the gap. The reset is bounded to `titlesPerPass` per gap event, so a
  routine gap on a busy indexer queues no more searches than one pass can run.
- **The two entry points split the work by when a release was published.** The
  feed handles releases published while it is polling. The search sweep handles
  what already existed, plus everything when no feed is configured.
- **Cadence follows that division; grab scope deliberately does not.** If a search
  sweep turns up a current release, it still grabs it. The feed's dedupe
  is one-shot, so a feed entry seen before the title or item it matches existed
  never comes around again.
- **With a feed configured, `writeSearchState` drops two cadence rules.** It
  drops the aired-since reset and the next-broadcast clamp. Both are the
  scheduled search sweep's (#100) answer to "search a weekly show at air time",
  which the feed now handles. The pin-delay hold stays either way, since that
  release already exists.
- **`indexer.RecentFeed` is a type assertion, not a wider `Indexer`.** An
  indexer without one degrades to sweep-only, which is a supported configuration
  and logs at debug.
- **The feed's high-water mark lives in `settings` under
  `feed.seen.<indexer>`.** It stores the newest `pubDate` plus the ids sharing
  it, which is Sonarr's `LastRssSyncReleaseInfo` shape.
- **Two feed entry ids matter.** The GUID is unreliable across Torznab
  implementations (Sonarr keys on the download URL for that reason), and a feed
  publishing no dates dedupes on ids alone.

### Feed gap detection

- **A feed page proves coverage only by containing the mark's own instant.**
  Feed gap detection (#176) compares each feed page against the high-water mark.
  Coverage is a feed entry published at the mark's instant, or one whose id was
  recorded *for* it. Nothing else on the feed page is evidence.
- **Three things on a feed page are not evidence**, and each looked like
  evidence once:
  - **A feed entry merely *older* than the mark.** This is an aggregator's
    backfill, which is structural rather than exotic. Jackett's aggregate
    indexer returns partial results, so a member that timed out on one poll
    reappears on the next with its original dates.
  - **A sticky the feed never dated.** It is on every feed page, so treating it
    as coverage disables gap detection permanently and never self-corrects.
  - **A stale id the rewind path merged.** By construction it was published
    before the instant it would be counted as covering.
- **The mark stores two id sets that a reader must not merge back into one.**
  `IDs` is the dedupe set `unseenEntries` needs, deliberately the wider of the
  two. `LatestIDs` is coverage alone.
- **The id check was kept when coverage narrowed to the mark's instant (#176).**
  An aggregator rendering a tracker's relative date recomputes it every poll, so
  the feed entry at the mark comes back at a slightly different instant.
- **Reading a mere straggler as coverage is what masked the gap (#176).** The
  old signal was "every entry is fresh", and one backdated feed entry on the
  feed page defeated it.
- **Sonarr's `oldestReleaseDate < lastReleaseInfo.PublishDate` is that reading,
  so the precedent is one to understand and not to copy.** It is safe *there*
  only because Sonarr re-processes its whole page each sync and dedupes
  downstream, where `unseenEntries` skips what it dates before the mark.
- **A feed page with nothing fresh on it can still be a gap (#176).** So the
  recovery runs outside the quiet-feed shortcut rather than behind it.
- **A false gap recurs for as long as the feed page omits the mark.** An indexer
  stuck serving an older feed page reports one every poll. That is affordable
  only because feed gap recovery (#140) is bounded to `titlesPerPass` and
  reorders the search sweep queue rather than adding searches to it.

### Concurrent grabs

- **Concurrent grabs are serialized by an in-process claim over wanted-item
  ids** (`claims.go`). The two jobs are phase-locked: same interval, both
  `RunAtStart`, and the runner anchors both to startup. Both read grab state
  before either writes. Without the claim, a just-aired episode gets two adds.
  If they picked different releases, that leaves an orphan torrent no grab row
  references.
- **Automation `TryAcquire`s and yields; a manual grab `Acquire`s and never
  does.**

### Pass outcomes

- **A pass stores what it decided; the Missing page surfaces less than it stores
  (#181).** `walkCandidates` writes one `pass_outcomes` row per wanted item,
  upserted in place, so the table is bounded by `wanted_items` rather than by
  pass count.
- **The stored set is wider than the surfaced set.** Stored pass outcomes (#181)
  number eight, and five reach a row on the Missing page:
  - **`grabbed` exists only as the tombstone that invalidates an older
    refusal.** A listed item's grab plainly failed, and `grab_failed` is that
    row's reason.
  - **`contended` and `deferred` both mean a later pass will take the item.**
    Another grab has its items, or this pass took an overlapping release first.
    So neither adds anything the title group's own reason does not.
- **Item monitoring only *narrows* the write set, so the guard below still
  holds.** But it introduces a second, permanent staleness source. A pass never
  writes for an unmonitored item, and nothing later invalidates what it wrote
  before the toggle.
- **The read side suppresses that staleness rather than invalidating it.**
  `itemReason` returns `unmonitored` ahead of every other reason, so the stored
  answer returns intact when the item is monitored again.
- **Blame drops decide's coverage ranking** and re-ranks Pinned → Score →
  Seeders. Coverage improves grab efficiency in batch import (#126), and shows
  nothing about which release came closest *for one episode*. Inheriting it
  would let a wide low-scoring pack outrank a high-scoring single covering
  exactly the episode in question.
- **The read-side suppression guard is exactly equivalent to ranking on
  recency**, not an approximation of it. A pass only writes for a grabbable
  item, and an item is not grabbable while its grab is live, so a pass outcome
  can never be recorded between a grab being made and failing. Hence "older than
  `grabs.created_at` → dropped" needs no timestamp arithmetic and no index.
- **`covered` stays a separate map from the outcome set** (it runs per candidate
  on the feed's hot path). They agree by invariant, tested rather than merged.
- **Only a search sweep that ran to the end writes `no_match`.** A hard return
  never evaluated the rest of the candidates, and a feed poll read one feed
  page, not search results.

### Cutoff Unmet's parse cache

- **Cutoff Unmet caches the parse and never the score (#185).** Membership is
  re-derived per request so editing a profile moves the list. The scan uses its
  whole budget precisely when a library is healthy and nothing qualifies, so the
  scan cost had to come off without recording the answer.
- **The expensive half is the one no profile can change.** Measured, the split
  is lopsided: `parser.Parse` is ~103µs per held release against ~0.9µs for
  `decide.Score` plus `decide.UnmetGoals`. The cheap half is the one that would
  need invalidating.
- **`held_release_parses` stores the parse of a release name and nothing about
  what it is worth.** A `held_score` column is the regression this bullet exists
  to prevent. It would put a version counter on `quality_profiles` and a bump
  obligation on every profile mutation.
- **The row is keyed on the release name it parsed and on `parser.Version`**, so
  the read joins on both:
  - **A superseded release's parse cannot match.** That is why
    `SetWantedItemHeld`, the one writer of `held_release_title`, needs no
    reference to the table.
  - **A parse the current parser would no longer make cannot match either.** The release
    name alone could never express that, since a held release name does not
    change when the parser under it does.
- **Bump `parser.Version` whenever `Parse`, `Parsed` or anitogo can read the
  same release name differently.** That is the one obligation the design does
  not remove. Otherwise Cutoff Unmet scores the old reading forever while
  `decide.Match` parses fresh, and the two disagree. A stale row is replaced
  rather than migrated, so the bump is the whole of it.
- **The fill is a write from a read**, accepted for three reasons:
  - It is derived and idempotent (the worst a bad row costs is one re-parse).
  - It is bounded to once per held release ever.
  - It is unreachable any other way. SQL cannot run the parser, so no migration
    can backfill it. Filling at import time would never reach the healthy
    library that is the whole case, where nothing is ever re-imported.
- **The table stays Cutoff Unmet's alone.** Fixing the search sweep's re-parse
  of every held release (#266) proposed reusing it from `decide.Match`, and that
  was declined; the [`decide` guide](../decide/CLAUDE.md) explains why.

### The automation toggle

- **The automation toggle is three-state (#116): `off` / `notify_only` / `on`.**
  It is one settings key (`automation.enabled`) whose value domain widened, so
  legacy stored/env bools still parse.
- **Notify-only rehearses.** In notification-only mode (#116), both entry points
  run the real decision walk in `grabPass`, but every take dispatches a
  `rehearsal` notify event instead of grabbing.
- **In notify-only, the search sweep also reports the wanted items the walk left
  uncovered**, with the best refusal's reason. The feed stays silent there,
  because per-title silence is a feed page's normal state.
- **`AutomationEnabled()` stays the run/don't-run check (true in notify-only),
  and `NotifyOnly()` is the rehearse switch.**
- **A manual "Run now" bypasses only `off`.** So notify-only means nothing
  reaches the download client no matter who triggered the run.

### Switching from notify-only to `on`

- **A rehearsal rehearses the search cadence but not the grab-driven reset, so
  switching to `on` resets every title's cadence** (`ResetAllTitlesSearchState`,
  in the same transaction as the settings write).
- **A rehearsed pass returns a grab count of 0.** Nothing settled, and counting
  would-grabs would re-decide the same items every tick. Returning 0 means the
  pass takes the no-grab branch and doubles its backoff up to the daily cap.
- **Meanwhile the feed mark advances as usual**, so a rehearsed feed entry never
  comes around again. Not advancing it would make a 15-minute poll a repeating
  firehose.
- **Without the reset on resume, "flip to on and it grabs" would wait out a
  backoff the rehearsal accrued**, for releases the feed will not re-offer. With
  it, the search sweep re-searches and finds them.

### What the automation entry points filter on

- **The two cadence helpers filter on `monitored`, not on `grabbable`.** An
  unaired item is never grabbable by construction, so filtering `nextAiring` on
  grabbability would return the zero time always. That would silently delete the
  scheduled search sweep's (#100) next-broadcast clamp for every feedless
  install.
- **`airedSince` uses the same filter for a weaker reason.** The grabbable
  filter is merely over-broad there, and would also stop an in-flight grab
  resetting the backoff.
- **Neither automation entry point filters on format (automation includes
  movies, #211).** The search sweep's due query briefly did. Making a movie
  addable (#208) parked movies there, because decide could not match one. An
  unmatched movie's `next_search_at` would never advance, and the film would
  occupy a slot at the head of a LIMIT-ordered queue forever.
- **Format belongs in `decide` alone.** Movie mode in decide (#209) removed the
  reason, and automation including movies (#211) removed the clause, so a wanted
  movie is now due, searched and grabbed like any other title.
- **The feed's due query never had the filter**, which is why the feed acquired
  films before the search sweep could.
