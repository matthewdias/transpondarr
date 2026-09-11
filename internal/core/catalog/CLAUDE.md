# Titles, items and monitoring (`internal/core/catalog`)

How a title enters the app, what identifies it, and how many items it gets.

- **A missing episode count is a normal state, and its two consequences are
  handled separately (#151).** AniList publishes `episodes: null` for a
  releasing title, for long-runners, and permanently for a scattering of older
  OVAs; when it also has no schedule the title materializes *no* items, and the
  sweep's `EXISTS` then drops it, the airing stamp stops it being re-requested, and
  nothing else can create one. Two rules follow, and each is deliberately not
  the other's fix. **The count is human-set and never inferred**:
  `catalog.SetItemCount` materializes `1..N` one-shot, storing nothing (refresh
  only ever adds, so there is no override to clobber), and it is deliberately
  *not* prefilled from a release search — `maxItem` is the very bound `decide`
  uses to reject a release's numbering, so letting release names set it makes
  the absolute-numbering guard inert. That is also why it is **guarded to a
  title with zero items** (409 otherwise): raising `maxItem` on a healthy title
  is the same hazard human-triggered, and a numberless pack would then claim the
  inflated range. PR #57 does not apply to it — that doctrine is about *eligibility*
  gating a grab, not about creating items — and a title with a *partial*
  schedule stays unfixable, which is the issue's scope and broadens additively.
  **The cadence keys on the count, not the item count**: `TTLFor(status,
  countKnown)` gives a FINISHED/CANCELLED title with a null count a middle 7d
  tier, applied to *both* `fresh()` and `ListTitlesDueMetadataRefresh`'s CASE,
  which is one two-armed rule and must be mutated arm by arm (#176's lesson).
  Reading the item count was itself the defect the mirror exposes: a FINISHED
  title whose *schedule* filled items but whose count was null got 30d from
  `fresh()` and 6h from the SQL, so the two halves of one rule disagreed. The
  airing sync passes `countKnown` true always — its own CASE keys on status
  alone, because aired times are immutable — so the tier deliberately never
  applies to it.
- **A title's identity is `(provider, provider_id)`, and the two are never
  separated (#74).** The pair is what the `series` table is keyed on, what `catalog.AddTitle`
  dedupes on, and what the API takes and emits — `provider` required rather than
  defaulted, because a default hides which id space the caller meant. Two rules
  follow. **The provider name is read, never written as a literal**:
  `Provider.Name()` is the source, so `metadata_cache` joins on the title's own
  `provider` column and the DTOs include `ProviderName()`; the remaining literals
  are the `enum:"anilist"` request tags and the migration's backfill, both of
  which are statements about the *schema*, not lookups. And **AniList is still
  the only provider**: `catalog.AddTitle` rejects a pair it cannot fetch, so
  nothing persists a row keyed on an unreachable id space. The pair does *not*
  mean a title can have two ids — deduping the same title across id spaces
  needs the cross-reference layer (#189), and until it lands, reaching for a
  `tmdb_id` column is the regression it exists to prevent.
- **A monitor decision is stored as a numeric cut, not as a mode.** The add-time
  choice (`all` / `future` / `none`) maps to `series.monitor_new_from`, read
  everywhere through the one helper `store.MonitorNew`. A mode could not work:
  the airing gap-fill and refresh growth both create items through
  `UpsertWantedItem`, which deliberately writes no `airs_at`, so `future` would
  have nothing to test there. A number also records the decision as *taken*
  rather than as a policy to re-derive — a re-evaluated `future` would unmonitor
  episodes someone has been waiting weeks for — and it is self-maintaining, so
  episode 1051 created six months later is monitored with no follow-up write.
  `monitor_new_from` is left to its schema default in `CreateTitle` and narrowed
  by `SetTitleMonitorNewFrom`: an omitted sqlc params field writes NULL, which
  means "monitor nothing new", so the insert is the one place that must not be
  able to write it. Both later create sites also **split the reset counter** — an
  unmonitored fill must not put a narrowed long-runner back at the front of the
  search queue on every sync — while refresh still clears the airing stamp on
  *any* insert, since the air-date sync ignores monitoring.
