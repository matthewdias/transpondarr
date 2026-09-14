# Search, feed and grab (`internal/core/acquire`)

The two automatic entry points and the one decision layer under them, plus what
a pass records about what it decided.

- **Two entry points, one decision layer (#101).** The `feed-poll` job and the
  `wanted-search` sweep both build a `Match` through `Service.evaluate` and act
  on it through `grabPass`, so the profile's minimum score, the blocklist, pinned-group delay and
  the coverage ranking are written once. The feed is only a cheaper *trigger*: it
  inverts the sweep's lookup (a release name needing a title, rather than a
  title needing releases), which is why it is title × entry — deliberately
  unoptimised, since a page is ~100 entries and the due query already drops any
  title with nothing wanted. It writes no search cadence: nothing was searched,
  and a grab settles its item, so the sweep's `EXISTS` drops the title anyway.
  The one exception is a poll that detects a missing feed page (#140): it
  resets the sweep for the titles that aired inside the gap, bounded to
  `titlesPerPass` per gap event so a routine gap on a busy indexer queues no more
  searches than one pass can run.
  **A feed page proves coverage only by containing the mark's own instant
  (#176)** — an entry published at it, or one whose id was recorded *for* it. Nothing else on
  the page is evidence, and the three things that are not are worth naming because
  each looked like evidence once: an entry merely *older* than the mark (an
  aggregator's backfill, which is structural rather than exotic — Jackett's
  aggregate indexer returns partial results, so a member that timed out on one
  poll reappears on the next with its original dates); a **sticky the feed
  never dated**, which is on every page, so treating it as coverage disables gap
  detection permanently and never self-corrects; and a **stale id the rewind path
  merged**, which by construction was published before the instant it would be
  counted as covering. Hence the mark stores two id sets that a reader must not
  merge back into one: `IDs` is the dedupe set `unseenEntries` needs, deliberately
  the wider of the two, and `LatestIDs` is coverage alone. The id check was kept
  through the narrowing because an aggregator rendering a tracker's
  relative date recomputes it every poll, so the entry at the mark comes back
  at a slightly different instant.
  Reading a mere straggler as coverage is what masked the gap: the old signal was
  "every entry is fresh", and one backdated entry on the page defeated it. Sonarr's
  `oldestReleaseDate < lastReleaseInfo.PublishDate` is that reading, and is
  safe *there* only because Sonarr re-processes its whole page each sync and
  dedupes downstream, where `unseenEntries` skips what it dates before the mark —
  so the precedent is one to understand and not to copy. Two consequences: a page
  with nothing fresh on it can still be a gap, so the recovery runs outside the
  quiet-feed shortcut rather than behind it; and a false gap **recurs for as long
  as the feed page omits the mark** — an indexer stuck serving an older page reports one
  every poll — which is affordable only because the recovery is bounded to
  `titlesPerPass` and reorders the sweep queue rather than adding searches to it.
  They split the work by when a release was published: the feed handles
  releases published while it is polling, and the sweep handles what already
  existed, plus everything when no feed is configured. **Cadence follows that division; grab scope deliberately does
  not** — a sweep search that turns up a current release still grabs it, because
  the feed's dedupe is one-shot and an entry seen before the title or item it matches
  existed never comes around again. Concretely, `writeSearchState` drops the
  aired-since reset and the next-broadcast clamp when a feed exists (both are
  #100's answer to "search a weekly show at air time", which the feed now handles);
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
  across Torznab implementations (Sonarr keys on the download URL for
  that reason), and a feed publishing no dates dedupes on ids alone.
- **A pass stores what it decided; the Missing page surfaces less than it stores
  (#181).** `walkCandidates` writes one `pass_outcomes` row per wanted item,
  upserted in place, so the table is bounded by `wanted_items` rather than by
  pass count. **The stored set is wider than
  the surfaced set** — eight outcomes stored, five reach a row: `grabbed` exists
  only as the tombstone that invalidates an older refusal (a listed item's grab
  plainly failed, and `grab_failed` is that row's reason), while `contended` and
  `deferred` both mean a later pass will take the item — another grab has its
  items, or this pass took an overlapping release first — so neither adds
  anything the title group's own reason does not.
  Item monitoring only *narrows* the write set, so the guard below still holds —
  but it introduces a second, permanent staleness source, since a pass never
  writes for an unmonitored item and nothing later invalidates what it wrote
  before the toggle. That one the read side **suppresses** (`itemReason` returns
  `unmonitored` ahead of every other reason) rather than invalidating, so the stored
  answer returns intact when the item is monitored again.
  **Blame drops decide's coverage ranking** and re-ranks Pinned → Score → Seeders,
  because coverage improves grab efficiency (#126) and shows nothing about which
  release came closest *for one episode* — inheriting it would let a wide
  low-scoring pack outrank a high-scoring single covering exactly the episode
  in question. And **the read-side suppression guard is exactly equivalent to
  ranking on recency**, not an approximation of it: a pass only writes for a
  grabbable item and an item is not grabbable while its grab is live, so an
  outcome can never be recorded between a grab being made and failing — hence
  "older than `grabs.created_at` → dropped" needs no timestamp arithmetic and no
  index. `covered` stays a separate map from the outcome set (it runs per
  candidate on the feed's hot path); they agree by invariant, tested rather than
  merged. Only a search sweep that ran to the end writes `no_match`: a hard return never
  evaluated the rest of the candidates, and a feed poll read one feed page, not
  search results.
- **Cutoff Unmet caches the parse and never the score (#185).** Membership is
  re-derived per request so editing a profile moves the list, and the scan uses
  its whole budget precisely when a library is healthy and nothing qualifies —
  so the cost had to come off without recording the answer. Measured, the split
  is lopsided: `parser.Parse` is ~103µs per held release against ~0.9µs for
  `decide.Score` plus `decide.UnmetGoals`, so **the expensive half is the one no
  profile can change** and the cheap half is the one that would need
  invalidating. Hence `held_release_parses` stores the parse of a release name and
  nothing about what it is worth; a `held_score` column is the regression this
  bullet exists to prevent, since it would put a version counter on
  `quality_profiles` and a bump obligation on every profile mutation. Two things
  follow. The row is **keyed on the release title it parsed and on
  `parser.Version`**, so the read joins on both: a superseded release's parse
  cannot match, which is why `SetWantedItemHeld`, the one writer of
  `held_release_title`, needs no reference to the table — and neither can a
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
- **The automation toggle is three-state (#116): `off` / `notify_only` / `on` —
  one settings key (`automation.enabled`) whose value domain widened, so legacy
  stored/env bools still parse.** Notify-only rehearses: both entry points run
  the real decision walk in `grabPass`, but every take dispatches a `rehearsal`
  notify event instead of grabbing, and the sweep also reports the wanted items
  the walk left uncovered, with the best refusal's reason (the feed stays silent
  there — per-title silence is a feed page's normal state).
  `AutomationEnabled()` stays the run/don't-run check (true in notify-only) and
  `NotifyOnly()` is the rehearse switch; a manual "Run now" bypasses only `off`,
  so notify-only means nothing reaches the download client no matter who
  triggered the run.
- **A rehearsal rehearses the search cadence but not the grab-driven reset, so
  switching to `on` resets every title's cadence** (`ResetAllTitlesSearchState`,
  in the same transaction as the settings write). A rehearsed pass returns a grab
  count of 0 — nothing settled, so counting would-grabs would re-decide the same
  items every tick — which means it takes the no-grab branch and doubles its
  backoff up to the daily cap. Meanwhile the feed mark advances as usual (not
  advancing it would make a 15-minute poll a repeating firehose), so a rehearsed
  entry never comes around again. Without the reset on resume, "flip to on and it
  grabs" would wait out a backoff the rehearsal accrued, for releases the
  feed will not re-offer; with it, the sweep re-searches and finds them.
- **The two cadence helpers filter on `monitored`, not on `grabbable`.** An unaired
  item is never grabbable by construction, so filtering `nextAiring` on
  grabbability would return the zero time always and silently delete #100's
  next-broadcast clamp for every feedless install. `airedSince` uses the same
  filter for a weaker reason — the grabbable filter is merely over-broad there, and
  would also stop an in-flight grab resetting the backoff.
- **Neither automation entry point filters on format (#211).** The sweep's due
  query briefly did — #208 parked movies there because decide could not match one,
  so `next_search_at` would never advance and the film would occupy a slot at the
  head of a LIMIT-ordered queue forever. #209 removed the reason and #211 the
  clause, so a wanted movie is now due, searched and grabbed like any other
  title, and format belongs in `decide` alone. The feed's due query never had
  the filter, which is why the feed acquired films before the sweep could.
