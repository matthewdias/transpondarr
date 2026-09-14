# Air dates and the schedule (`internal/core/airing`)

The only writer of `wanted_items.airs_at`, and what an absent date means.
AniList's coverage is partial by design, so absence is a normal state here.

### Absent air dates

- **Air dates are nullable everywhere, by design.** AniList's schedule coverage
  thins out badly before ~2015. It can skip episodes even for a modern title (it
  lists no schedule entry for a multi-episode premiere block). So
  `wanted_items.airs_at` is null for real titles in normal operation — never
  treat its absence as an error.
- **The sync stamps a title even when AniList returns nothing.**
  `internal/core/airing` syncs `airs_at` in the background off the job runner. It
  stamps `series.airing_synced_at` even when the provider returns nothing. That
  stamp is what stops an unschedulable title being re-requested every tick.
- **Only a never-synced title pages full history.** Aired times are immutable, so
  a resync passes `notYetAired` and fetches the tail.

### Densifying a schedule

- **The first schedule page arrives with the title (the in-band schedule fetch
  for null-count titles, #152).** `airingSchedule` is a field on `Media`, not a
  root query. So one schedule page plus `nextAiringEpisode.episode` are fetched
  in `titleQuery` for zero extra requests. A null-count add therefore returns its
  items immediately instead of showing `0 / 0` for an `airingSyncInterval`.
- **A schedule is densified, never transcribed.** Both that schedule page and the
  background sync create `1..max(known number)` instead of transcribing. They
  leave `airs_at` null on the filled-in items. A schedule listing 1, 3, 4 means
  episode 2 shared a broadcast slot, and with a null count nothing else would
  ever create episode 2.
- **Over-creating is the safe mistake; under-creating is not.** Over-creating
  leaves an item permanently wanted that no release matches (a search sweep
  slot, and a title that shows as incomplete). It cannot cause a wrong grab,
  since `decide` refuses anything numbered past `maxItem` regardless.
  Under-creating loses an episode nobody notices is missing.
- **Densifying has three bounds**, each measured against the live API rather than
  assumed:
  - **A published count wins outright** over the two minimums, the schedule
    page's highest number and `nextAiringEpisode.episode`. Roughly 1 counted
    AniList entry in 15 has a schedule reaching *past* its count (a 12-episode
    show whose schedule runs 2..13). Unconditional `max` would turn that into a
    phantom item.
  - **A full fetch fills from 1, never from the schedule's own minimum.** In the
    wild a minimum above 1 means AniList lost the early records (a 24-episode
    AniList entry whose schedule starts at 23, a 16-episode one starting at 14).
    It does *not* mean an offset season: sampled sequel AniList entries restart
    their numbering at 1 (24 of 25). Filling from the minimum would silently drop
    the run below it.
  - **A tail fetch fills only inside its own span.** A tail fetch is a partial
    view of the numbering, so it does not re-derive a back catalogue every pass.

### What an add creates for a long-runner

- **The in-band schedule page is bounded; the next-broadcast minimum is not.**
  AniList keeps only a recent *window* of schedule records for a long-runner, so
  its first schedule page starts in the middle of the run, not at episode 1. A
  null-count long-runner therefore materializes its whole run in the add's
  transaction.
- **Materializing the whole run at add time is deliberate.** The whole run is the
  same set the sync would reach for the tail, plus a back catalogue *nothing*
  creates today. It costs no extra AniList requests, because the search sweep
  runs one search per *title* regardless of item count.
- **The add writes episode numbers only.** Passing dates through the add would
  pull in `ItemMeta`, `CreateWantedItem` and the sqlc layer for a column
  `internal/core/airing` already writes.
- **A gap-filled item does reset the search cadence** (`ResetTitleSearchState`,
  as `refresh` does). A gap-filled item has no air date, so `airedSince` never
  selects it.

### Monitoring and the lookup jobs

- **Monitoring limits what automation *acquires*, not what the app *looks up*
  (unmonitored titles getting no air dates, #183).** `series.monitored = 0`
  withholds a title from the search sweep and the feed, the paths that grab
  releases and move files. It never withholds a title from the two background
  jobs that only *look up* data about it.
- **Excluding unmonitored titles from lookups hid them from the Calendar.**
  Monitoring used to exclude a title from all four paths, and three predicates
  then composed into a hole. An unmonitored title got no `airs_at`, so
  `ListCalendarItems` dropped its rows before the Calendar's own monitored check
  could include them. `ListUnscheduledTitles` then filtered it out of the very
  footer that exists to explain such absences.
- **The old exclusion defeated the toggle's purpose.** The toggle could only ever
  reveal a title that had been monitored, synced, and unmonitored *afterwards*.
  That is the opposite of the case the toggle is for, since a title you are still
  deciding about is one you unmonitored early.
- **The two due queries order on `s.monitored = 0` instead of filtering on it.**
  They order it ahead of their never-synced key. So a monitored title still takes
  every slot before an unmonitored one gets any, and the request budget's
  priority is unchanged.
- **`GivesMonitoredTitlesEverySlot` proves the unmonitored tail is reached.** The
  second pass in each `GivesMonitoredTitlesEverySlot` test pins that the
  unmonitored tail is reached *at all*. Without that second pass, ordering last and starving
  forever are indistinguishable.
- **Both due queries had to move together.** Nothing else calls the provider's
  `GetTitle` for a title (the title-detail page reads `store.Q.GetTitle`, the
  DB). So an unmonitored title's cached status freezes at its add-time value. The
  airing query picks its TTL from that status. Opening the airing query alone
  would leave a title added while `RELEASING` on the 6h TTL permanently, never
  graduating to 30d. Fixing one query and not both would have turned a bounded
  one-off cost into a recurring one.
- **A per-job TTL override was declined.** `TTLFor` is deliberately one policy
  shared by both jobs. That is the rule from the missing-episode-count fix (#151)
  about the two halves not disagreeing.

### The calendar footer's two absences

- **"We asked and got nothing" and "we have not asked" are different absences,
  and the calendar footer states which (unmonitored titles getting no air dates,
  #183).**
  `ListUnscheduledTitles` selects
  `airing_synced_at IS NOT NULL AS schedule_checked`, and the calendar page
  renders two notes off it.
- **`schedule_checked` is the discriminator, not a proxy for one.** The sync
  stamps `airing_synced_at` **even when the provider returns nothing**.
- **Every title is briefly undated after it is added.** `internal/core/airing` is
  the *only* writer of `wanted_items.airs_at`. `catalog` never writes one, since
  the in-band schedule page (#152) carries episode numbers alone. So **every**
  title is undated between being added and its first sync.
- **The footer used to blame AniList for titles nobody had asked about.** The footer
  told the user AniList publishes no air dates for titles nobody had asked AniList
  about. That predated unmonitored titles being synced, which merely widened it.
- **Only the checked half may state a verdict.** The unchecked half says the
  lookup is still pending, so a wrong claim degrades into a temporary one.
- **The footer is *not* the place to hide either half.** Dropping the unchecked
  titles would restore the silent omission the footer exists to end.
