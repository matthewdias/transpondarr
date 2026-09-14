# Air dates and the schedule (`internal/core/airing`)

The only writer of `wanted_items.airs_at`, and what an absent date means.
AniList's coverage is partial by design, so absence is a normal state here.

- **Air dates are nullable everywhere, by design.** AniList's schedule coverage
  thins out badly before ~2015 and can skip episodes even for a modern title (it
  lists no schedule entry for a multi-episode premiere block), so `wanted_items.airs_at`
  is null for real titles in normal operation — never treat its absence as an
  error. `internal/core/airing` syncs it in the background off the job runner and
  stamps `series.airing_synced_at` even when the provider returns nothing, which
  is what stops an unschedulable title being re-requested every tick. Aired times are
  immutable, so only a never-synced title pages full history; a resync passes
  `notYetAired` and fetches the tail.
- **A schedule is densified, never transcribed (#152).** `airingSchedule` is a
  field on `Media`, not a root query, so one page of it plus
  `nextAiringEpisode.episode` are fetched in `titleQuery` for zero extra requests, and a
  null-count add returns its items immediately instead of showing `0 / 0` for
  an `airingSyncInterval`. Both that schedule page and the background sync then create
  `1..max(known number)` rather than transcribing, leaving `airs_at` null on the
  filled-in ones — a schedule listing 1, 3, 4 means episode 2 shared a broadcast
  slot, and with a null count nothing else would ever create it. Over-creating
  leaves an item permanently wanted that no release matches (a search sweep slot, and a
  title that shows as incomplete) but cannot cause a wrong grab, since `decide`
  refuses anything numbered past `maxItem` regardless; under-creating loses an
  episode nobody notices is missing. Three bounds, each measured against the live
  API rather than assumed:
  - **A published count wins outright** over the two minimums, the schedule page's highest number and
    `nextAiringEpisode.episode`. Roughly 1 counted AniList entry
    in 15 has a schedule reaching *past* its count (a 12-episode show whose
    schedule runs 2..13), which unconditional `max` would turn into a phantom item.
  - **A full fetch fills from 1, never from the schedule's own minimum.** In the
    wild a minimum above 1 means AniList lost the early records — a 24-episode
    entry whose schedule starts at 23, a 16-episode one starting at 14 — *not* an
    offset season: sampled sequel entries restart their numbering at 1 (24 of
    25). Filling from the minimum would silently drop the run below it.
  - **A tail fetch fills only inside its own span**, being a partial view of the
    numbering, so it does not re-derive a back catalogue every pass.
- **The in-band schedule page is bounded; the next-broadcast minimum is not.** AniList keeps
  only a recent *window* of schedule records for a long-runner — its first page
  starts in the middle of the run, not at episode 1 — so a null-count long-runner
  materializes its whole run in the add's transaction. That is deliberate: it
  is the same set the sync would reach for the tail, plus a back catalogue
  *nothing* creates today, and it costs no extra AniList requests, because the
  sweep runs one search per *title* regardless of item count. Numbers only —
  passing dates through the add would pull in `ItemMeta`, `CreateWantedItem` and
  the sqlc layer for a column `internal/core/airing` already writes. A gap-filled
  item does reset the search cadence (`ResetTitleSearchState`, as `refresh`
  does): it has no air date, so `airedSince` never selects it.
- **Monitoring limits what automation *acquires*, not what the app *looks up*
  (#183).** `series.monitored = 0` withholds a title from the search sweep and the feed
  — the paths that grab releases and move files — but never from the two
  background jobs that only *look up* data about it. It used to exclude a title from all four, and
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
  together**: nothing else calls the provider's `GetTitle` for a title (the
  title-detail page reads `store.Q.GetTitle`, the DB), so an unmonitored title's
  cached status freezes at its add-time value — and since the airing query picks
  its TTL from that status, opening it alone would leave a title added
  while `RELEASING` on the 6h TTL permanently, never graduating to 30d. Fixing
  one query and not both would have turned a bounded one-off cost into a
  recurring one. Reaching instead for a per-job TTL override was declined:
  `TTLFor` is deliberately one policy shared by both jobs, which is #151's rule
  about the two halves not disagreeing.
- **"We asked and got nothing" and "we have not asked" are different absences,
  and the calendar footer states which (#183).** `internal/core/airing` is the
  *only* writer of `wanted_items.airs_at` — `catalog` never writes one, since
  #152's in-band schedule page carries episode numbers alone — so **every** title is
  briefly undated between being added and its first sync, and the footer was
  telling the user AniList publishes no air dates for titles nobody had asked
  AniList about. That predated unmonitored titles being synced at all and was
  merely widened by it. `ListUnscheduledTitles` therefore selects
  `airing_synced_at IS NOT NULL AS schedule_checked` and the calendar page renders two
  notes off it, because the sync stamps that column **even when the provider
  returns nothing** — which is exactly what makes it the discriminator rather
  than a proxy for one. Only the checked half may state a verdict; the unchecked
  half says the lookup is still pending, so a wrong claim degrades into a
  temporary one. The footer is *not* the place to hide either: dropping the
  unchecked titles would restore the silent omission the footer exists to end.
