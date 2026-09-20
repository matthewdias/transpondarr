# Import and file mapping (`internal/core/importer`)

The grab lifecycle and the rules that decide which file in a payload is which
item. The root `CLAUDE.md` covers everything above this layer.

## Grab lifecycle

- **Grab lifecycle (`internal/core/importer`): every status but `grabbed` is
  settled.** `grabbed` → `imported`, `failed` (errored, or absent from the
  download client past the grace period — the item reverts to wanted), or
  `import_deferred`.
- **The scan iterates per info hash, not per grab row** (batch and season-pack
  import, #126). A pack is a grab row per covered episode, and its payload only
  means anything examined as a whole. `collectPayloadFiles` walks it once, and
  the pure `mapFiles` maps files onto the items the release covers. So
  `library.Target.Place` stays file-only while a pack imports episode by
  episode.
- **`import_deferred` therefore narrows to "*this item's* file could not be
  picked out".**
- **Deferred grabs are never re-imported by the scan** (the no-infinite-retry
  property). They stay in the scan for missing-from-client reconciliation, so a
  vanished payload still frees its item. Only an explicit `RetryImport` reopens
  one, optionally naming the file.
- **A settled grab row's reason is stored only in `grab_events`.**
  `SetGrabStatus` writes `last_error = NULL` in the statement that settles the
  row, so reading `grabs.last_error` for a settled row returns the empty string.
  Three surfaces read the stored sentence, and changing what `settle()` records,
  or pruning `grab_events`, moves all three: a title's history
  (`ListTitleGrabEvents`), the Activity history page (`ListGrabEventsPage`), and
  the Missing screen's *Last grab failed* detail (`ListFailureDetailsByTitle`).
  Until #273 that third one read `grabs.last_error`, so it was empty on every
  install.
- **`RetryImport` is not one of those readers**, though `retry.go`'s comment
  about the reason being "the only text the toast has" reads as though it were.
  It takes the sentence from `settleGroup`'s return value in the same call, so it
  needs no query and is unaffected by what history stores.
- **`appendEvent` is best-effort, so a settled grab row can have no event.** It
  logs and returns, because history must never wedge the pipeline. So a reader
  scanning history for a settled row's reason has to check the event against
  `grabs.created_at`. A re-grab resets that column, and without the check the
  previous attempt's event is the newest one there is.

## The payload walk

- **The payload walk's extras filter yields to a sole video** (archive payloads,
  #135). A payload whose only video has an extras token is collected anyway —
  identity by construction again. One video and nothing to confuse it with means
  the token is a word in the title's name. Dropping that video deferred the
  episode while its file was right there.
- **`sampleTokens` is the exception that does not qualify** (a sample is a
  truncated copy, never the episode), and so is excluded before the video is
  counted.
- **Downstream, the sole-video relaxation needs no special case.** A one-item
  info-hash group takes the video by the lone-file rule; a multi-item info-hash
  group leaves it over and defers.

## Archives

- **Nothing unpacks an archive; the payload walk names one instead** (archive
  payloads, #135). We declined unpacking deliberately. There is no Usenet client
  here (qBittorrent only), and RAR packaging is a Usenet/scene convention that
  anime release groups do not use. So a decoder would be the first dependency in
  the import path, and its tests would need committed binary fixtures.
- **Archives sit beside the candidates, never among them.**
  `collectPayloadFiles` returns a `payload` whose `archives` are a separate
  field *beside* `[]candidate`, never inside it. `mapFiles` stays pure, and a
  `.rar` is unassignable by construction rather than by a guard someone can
  miss.
- **Volumes are grouped into sets keyed on dir + stem.** So a 12-volume set is
  one thing to extract, and two discs sharing a naming scheme stay two. The
  deferral reason and the Fix import dialog then say what to extract.
  Re-importing after extracting in place already works with no new code.
- **An archive keeps its item deferred on every path**, including a retry
  clicked before extracting and a mixed payload whose loose file covers only
  some items. Failing there would revert the item, blocklist the release and
  drop the grab row from the queue, while the episode was in the payload the
  whole time.
- **Password-protected and corrupt archives defer like healthy ones.** Without
  the reader we declined, the three are indistinguishable, and all three defer
  identically. That is sound, because deferral is settled either way.
- **A single-file `.rar` payload is an archive too.** Identity by construction
  stops here, since hardlinking it into the library as the episode is worse than
  deferring.

## Mapping files onto items

- **The mapping rules are narrow on purpose, because a wrong answer moves a
  file.**
- **A lone file for a lone item is identity by construction** (we chose this
  release).
- **A file claims a number only when it names exactly one.** Season-relative
  beats absolute when both land inside the release, matching decide's stance.
- **Among same-number claimants the higher `Version` wins, and `Repack` breaks
  the tie.** An exact tie is a *conflict* rather than a coin flip, since taking
  either silently drops the other.
- **Retry overrides are keyed on the payload-relative path and overrule every
  rule above.** Being wrong about a filename is the whole reason the escape
  hatch exists.

## A covered item with no file

- **A covered item with no file splits by whether a human could fix it.** Files
  still loose in the payload → defer, with the detail naming what is unmatched,
  fixable from the Activity queue.
- **An unextracted archive counts as still loose.** It contains the episode, so
  it is a human's to fix. That is why `settleGroup` takes the whole `payload`
  rather than its files.
- **Nothing left over → `failGrab`**, so the item reverts to wanted and the
  search sweep self-heals with a single. It flows through the same `remember()`
  grouping, so one payload counts as one failure toward the blocklist's
  escalating expiry.
- **A file for an item the release never covered is placed too.** It is guarded
  on the item existing, not being had, and having no unsettled grab. It also
  **holds the `acquire` claim** (`TryClaimItems`/`ReleaseClaims`), so a
  concurrent grab cannot race a copy-mode `Place` that runs for minutes. One
  registry is the point.
- **`ScanOnce`, `ListPayload` and `RetryImport` share the importer's mutex.**
  That is why `main.go` builds one importer and passes it to both the job runner
  and `server.New`.

## Movies

- **A movie's file is identified by size; numbering is never used** (movie
  payload import, #210). `mapMovie` takes the payload's largest surviving video,
  because a film is the biggest thing shipped with it. Size is a property of the
  payload rather than of how a releaser named it. The same reason made `decide`
  stop mapping by number on the movie path, in decide's movie mode (#218).
- **The number-driven mapping was actively unsafe for a movie.** A movie's
  `covers` is always `{1}`, so a numbered extra (`Deleted Scene 1`) claimed the
  film's only item. It hardlinked a clip as the movie and dropped the feature as
  a leftover — settled, held in the library, and self-healing never. Note the
  asymmetry it had: *two* claimants deferred safely as a conflict while *one*
  imported.
- **Movie mapping is keyed on `domain.FormatMovie`, never on a one-item
  info-hash group.** A series' single grabbed episode is a one-item info-hash
  group too, and its number is genuine identity there.
- **The filter still runs first** (a sample is never the feature, and is often
  the small file anyway, so size must not re-admit it). An exact size tie is a
  conflict rather than a coin flip, and a retry override still overrules
  everything.
- **A movie never gets the `unmatched file(s)` deferral.** Since size alone
  selects the file, that deferral is unreachable for a movie. A tie and an
  unextracted archive are the only deferrals it has.

## User-facing copy

- **User-facing copy keys on format as movie mapping does, and only on paths a
  movie can take** (movie payload import, #210). The importer's settled reasons
  and the notification adapters word themselves off the item kind (`itemLabel`,
  and the adapters' second condition on `Event.ItemKind`). So a film is never
  "episode 1".
- **Every string only an episode can produce keeps its wording byte-identical.**
  The series assertions in `events_test.go` and `retry_test.go` pin that.
- **`Event.ItemKind` is display-only — `webhook.go` must never map it.**
  `item_number: 1` is *correct* for a movie, and the payload is a contract. The
  REST contract rename from series to titles (#207) broke that contract once,
  deliberately and with an upgrade note.

## Failure memory

- **`failed` also means "this release is remembered" (`internal/core/blocklist`,
  #118).** Both `failed` paths record a per-title blocklist entry. The grab row
  is per wanted item, and the next attempt overwrites it. Without that memory,
  the search sweep re-derived the same ranking and re-grabbed the same doomed
  release forever.
- **`decide` reads the blocklist through the existing `ineligibleReason`.** So
  the search sweep's eligibility check, the Releases tab's reason column and
  manual grab's freedom from eligibility (PR #57, which surfaces ineligibility
  on the manual grab) all hold unchanged.
- **Blocklist identity is deliberately the info hash or the normalized release
  name** (`normalized_title`), because Torznab often omits the hash.
- **The expiry deliberately escalates: 24h, 7d, then permanent.** The `failed`
  paths fire for environmental reasons that can fail many grabs at once. So
  permanent-on-first would blocklist a whole in-flight set on one qBit incident.
- **Expired entries are filtered, never deleted.** The blocklist entry stores
  the failure count `blockDuration` reads.
- **An *import* failure deliberately records nothing.** It stays `grabbed` and
  retries, because its causes are path-mapping gaps rather than bad releases.

## Absent torrents

- **An absent torrent is not a verdict (#241).** `failed` settles two different
  things, and an inference justifies only one of them. Freeing the item is
  self-healing and stays automatic. Remembering the release as bad is a
  judgement that needs a cause.
- **Only three things supply a cause.** The client reporting `error` for one of
  its torrents, a payload we examined that lacked a covered item's file, and a
  download URL that could not be fetched or parsed (`acquire.AutoGrab`, from the
  failure-memory follow-up #120).
- **Absence supplies no cause.** Every cause of absence is external: a
  hand-removed torrent, a reset client, other tooling, a hash the client never
  had.
- **`missingFiles` supplies no cause either**, which is why it maps to its own
  `download.State` rather than sharing `StateError`. The data is gone and the
  release is not at fault. A dropped mount would otherwise blocklist every
  release on it at once.
- **A blamed failure's two consequences always happen together**: the memory,
  and moving the item to the front of the search queue. So an unblamed failure
  gets neither. `record()`'s breaker branch (#120) already set the precedent: it
  skips the re-front exactly when it skips the blame. A dropped mount would
  otherwise cause a second thundering herd in response to the first.
- **`blame` is a required `failGrab` argument rather than a default**, so every
  new failure path has to pass it explicitly.

## Data-missing duplicates

- **Dropping the memory cost `data_missing` its only loop breaker.** The
  blocklist entry had been supplying it by accident. Converging on a duplicate
  (`AddAlreadyExists`) assumes the torrent can still finish, and one whose data
  is gone never will. So the same release ranked first and "grabbed" every pass
  while the item stayed unacquirable.
- **The adapter now returns `download.ErrDataMissing` for that add** —
  deliberately not `ErrBadRelease`, which is the one `acquire.AutoGrab`
  blocklists. So the pass moves on to the next-best release instead.
- **Only the branch where the torrent demonstrably existed before our add
  returns that error.** That is the pre-check, never the post-failure re-check.
  At the re-check our own add may be what landed, so returning the error would
  leave a torrent no grab row references. That orphan is the one from
  recent-feed polling (#134), and the re-check branch exists to prevent it.
- **Converging at the re-check costs nothing**, because the loop's steady state
  runs through the pre-check. A duplicate found by the re-check writes a grab
  row, fails unblamed, and gets the error on the next pass. One extra cycle, not
  a loop.
- **Converging on a *healthy* duplicate is unchanged either way.** It is what
  makes re-grabbing an in-flight torrent safe.
- **A stalled torrent is *present* and takes none of these paths.** So the
  doomed-release case the blocklist (#118) guards against cannot arise from
  absence.
- **This app disassociates a torrent from the library and never removes or
  deletes one on its own.** That is the posture behind all of the above: the
  download client is the user's disk and their ratio.

## Timers per info-hash group

- **Both timers are the info-hash group's, not the grab row's (#247).** Per-row
  timers let a pack whose grab rows were created at different times escalate the
  blocklist expiry twice for one incident. A pack is one torrent. So
  `sharedSince` gives every grab row of an info-hash group its earliest stamp,
  and `stalled_since`/`missing_since` are stamped and cleared per info-hash
  group.
- **Per-row clocks split one incident into two `Record` calls.** A grab row that
  a later add wrote (a converged duplicate, from the absent-torrent change #241)
  began its own clock. It crossed the threshold in a *later* scan, and so fell
  outside `remember()`'s per-scan grouping. The upsert is keyed on `(series_id,
  normalized_title)`, so that counts as `failures = 2` and escalates the expiry
  to 7d rather than writing a second blocklist entry.
- **The fix is here and not in `remember()`.** Its per-scan grouping is the
  design of aggregating failed grab rows per release (#124), and correct.
  Widening *it* would need cross-scan state the design avoids.
- **Earliest, not `now`.** The clock belongs to the torrent, and taking `now`
  for the late grab row would reproduce the split. Earliest also makes an
  install upgrading mid-stall converge rather than stay inconsistent.
- **The value is written, not only computed.** The Activity queue renders
  `abandon_at` from each grab row's own column. So a divergent stamp would show
  one episode of a pack a countdown it will never be settled on. The write is
  guarded on the value differing, so a steady state costs nothing.
- **An unreadable stamp is treated as an absent one** rather than restarting the
  info-hash group. The tolerance that unparseable data must not fail a grab is
  preserved at the info-hash group level. There, only *no* grab row being
  readable waits another full period.
- **Clearing needed no change.** Both clear conditions read the info-hash
  group's status and the global timeout, never the grab row.
