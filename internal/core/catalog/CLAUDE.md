# Titles, items and monitoring (`internal/core/catalog`)

How a title enters the app, what identifies it, and how many items it gets.

### A missing episode count

- **A missing episode count is a normal state (the missing AniList episode
  count, #151).** AniList publishes `episodes: null` for a releasing title, for
  long-runners, and permanently for a scattering of older OVAs. If AniList also
  has no schedule, the title materializes *no* items. The search sweep's `EXISTS`
  then drops it, the airing stamp stops it being re-requested, and nothing else
  can create an item.
- **A missing count's two consequences are handled separately.** Two rules follow, the item
  count and the refresh cadence below, and each is deliberately not the other's
  fix.

#### The item count

- **The count is human-set and never inferred.** `catalog.SetItemCount`
  materializes `1..N` one-shot, storing nothing. Refresh only ever adds items, so
  there is no override to clobber.
- **`SetItemCount` is deliberately *not* prefilled from a release search.**
  `maxItem` is the very bound `decide` uses to reject a release's numbering, so
  letting release names set it makes the absolute-numbering guard inert.
- **`SetItemCount` is guarded to a title with zero items** (409 otherwise).
  Raising `maxItem` on a healthy title is the same hazard, human-triggered. A
  numberless pack would then cover every item in the inflated range.
- **The manual-grab eligibility doctrine (PR #57) does not apply to
  `SetItemCount`.** That doctrine is about *eligibility* checks on a grab, not
  about creating items.
- **A title with a *partial* schedule stays unfixable.** That limit is the scope
  of the missing-episode-count fix (#151), and that scope can broaden
  additively.

#### The refresh cadence

- **The cadence keys on the count, not the item count.** `TTLFor(status,
  countKnown)` gives a FINISHED/CANCELLED title with a null count a 7d TTL,
  between the 6h and 30d ones.
- **The 7d TTL is one rule in two halves, each mutated separately.** It applies
  to *both* `fresh()` and `ListTitlesDueMetadataRefresh`'s CASE. Each half must be
  mutated separately, which is the lesson of the feed-gap detection fix (#176).
- **Reading the item count was the defect the mirror mutation exposes.** A
  FINISHED title whose *schedule* filled items but whose count was null got 30d
  from `fresh()` and 6h from the SQL. So the two halves of one rule disagreed.
- **The 7d TTL deliberately never applies to the airing sync.** The airing sync
  passes `countKnown` true always. Its own CASE keys on status alone, because
  aired times are immutable.

### Title identity

- **A title's identity is `(provider, provider_id)`, and the two are never
  separated (provider-generic title identity, #74).** The pair is what the
  `series` table is keyed on, what `catalog.AddTitle` dedupes on, and what the API
  takes and emits.
- **The API requires `provider` instead of defaulting it**, because a default
  hides which id space the caller meant.
- **The provider name is read, never written as a literal.** `Provider.Name()` is
  the source, so `metadata_cache` joins on the title's own `provider` column and
  the DTOs include `ProviderName()`.
- **The remaining provider literals describe the schema.** They are the
  `enum:"anilist"` request tags and the migration's backfill. Both are statements
  about the *schema*, not lookups.
- **AniList is still the only provider.** `catalog.AddTitle` rejects a pair it
  cannot fetch, so nothing persists a `series` row keyed on an unreachable id
  space.
- **The pair does *not* mean a title can have two ids.** Deduping the same title
  across id spaces needs the cross-reference layer for cross-provider id mapping
  (#189). Until that layer lands, reaching for a `tmdb_id` column is the
  regression it exists to prevent.

### Monitor decisions

- **A monitor decision is stored as a numeric cut, not as a monitor mode.** The
  add-time choice (`all` / `future` / `none`) maps to `series.monitor_new_from`.
  It is read everywhere through the one helper `store.MonitorNew`.
- **A monitor mode could not work for items created later.** The airing gap-fill
  and refresh growth both create items through `UpsertWantedItem`, which
  deliberately writes no `airs_at`. So `future` would have nothing to test there.
- **A number records the decision as *taken* rather than as a policy to
  re-derive.** A re-evaluated `future` would unmonitor episodes someone has been
  waiting weeks for.
- **The numeric cut is self-maintaining.** Episode 1051 created six months later
  is monitored with no follow-up write.
- **`CreateTitle` leaves `monitor_new_from` to its schema default.**
  `SetTitleMonitorNewFrom` then narrows it. An omitted sqlc params field writes
  NULL, which means "monitor nothing new". So the insert is the one place that
  must not be able to write NULL.
- **Both later create sites also split the reset counter.** The airing gap-fill
  and refresh growth each keep an unmonitored fill from putting a narrowed
  long-runner back at the front of the search queue on every sync.
- **Refresh still clears the airing stamp on *any* insert**, since the air-date
  sync ignores monitoring.
