# Import and file mapping (`internal/core/importer`)

The grab lifecycle and the rules that decide which file in a payload is which
item. The root `CLAUDE.md` covers everything above this layer.

- **Grab lifecycle (`internal/core/importer`): every status but `grabbed` is
  settled.** `grabbed` → `imported`, `failed` (errored, or absent from the
  download client past the grace period — the item reverts to wanted), or
  `import_deferred`. The scan iterates **per info hash, not per row** (#126): a
  pack is a row per covered episode, and its payload only means anything
  examined as a whole. `collectPayloadFiles` walks it once and the pure
  `mapFiles` maps files onto the items the release covers, so
  `library.Target.Place` stays file-only while a pack imports episode by
  episode. `import_deferred` therefore narrows to "*this item's* file could not
  be picked out". Deferred grabs are never re-imported by the scan (the
  no-infinite-retry property) but stay in it for missing-from-client
  reconciliation, so a vanished payload still frees its item; only an explicit
  `RetryImport` reopens one, optionally naming the file.
- **The payload walk's extras filter yields to a sole video (#135).** A payload whose
  only video has an extras token is collected anyway — identity by
  construction again: one video and nothing to confuse it with means the token
  is a word in the title, and dropping it deferred the episode while its file
  was right there. `sampleTokens` is the exception that does not qualify (a
  sample is a truncated copy, never the episode) and so is excluded before the
  video is counted. Downstream the relaxation needs no special case: a
  one-item info-hash group takes it by the lone-file rule, a multi-item info-hash group leaves it
  over and defers.
- **Nothing unpacks an archive; the payload walk names one instead (#135).** Declined
  because there is no Usenet client here (qBittorrent only) and RAR
  packaging is a Usenet/scene convention that anime release groups do not use, so a
  decoder would be the first dependency in the import path and its tests would
  need committed binary fixtures. So `collectPayloadFiles` returns a `payload`
  whose `archives` are a separate field *beside* `[]candidate`, never inside it — `mapFiles`
  stays pure and a `.rar` is unassignable by construction rather than by a guard
  someone can miss. Volumes are grouped into sets keyed on **dir + stem**, so a
  12-volume set is one thing to extract and two discs sharing a naming scheme
  stay two; the deferral reason and the Fix import dialog then say what to
  extract, and re-importing after extracting in place already works with no new
  code. **An archive keeps its item deferred on every path**, including a retry
  clicked before extracting and a mixed payload whose loose file covers only some
  items — failing there would revert the item, blocklist the release and drop the
  row from the queue, while the episode was in the payload the whole time.
  Password-protected and corrupt archives are indistinguishable from healthy ones
  without the reader we declined, and all three defer identically — sound,
  because deferral is settled either way. A single-file `.rar` payload is an
  archive too: identity by construction stops here, since hardlinking it into
  the library as the episode is worse than deferring.
- **The mapping rules are narrow, because a wrong answer moves a
  file.** A lone file for a lone item is identity by construction (we chose this
  release); a file claims a number only when it names exactly one, with
  season-relative beating absolute when both land inside the release, matching
  decide's stance; among same-number claimants the higher `Version` wins and
  `Repack` breaks the tie, and an exact tie is a *conflict* rather than a coin
  flip, since taking either silently drops the other. Retry overrides are keyed
  on the payload-relative path and overrule every rule above — being wrong about
  a filename is the whole reason the escape hatch exists.
- **A covered item with no file splits by whether a human could fix it.** Files
  still loose in the payload → defer with the detail naming what is unmatched,
  fixable from the Activity queue. **An unextracted archive counts as still
  loose** — it contains the episode, so it is a human's to fix — which is why
  `settleGroup` takes the whole `payload` rather than its files. Nothing left
  over → `failGrab`, so the item reverts to wanted and the search sweep
  self-heals with a single; it flows through the same `remember()` grouping, so
  one payload counts as one failure toward the blocklist's escalating expiry. A file for an item the
  release never covered is placed too,
  guarded on the item existing, not being had, and having no unsettled grab,
  and **holding the `acquire` claim** (`TryClaimItems`/`ReleaseClaims`) so a
  concurrent grab cannot race a copy-mode `Place` that runs for minutes. One
  registry is the point. `ScanOnce`, `ListPayload` and `RetryImport` share the
  importer's mutex, which is why `main.go` builds one importer and passes it to
  both the job runner and `server.New`.
- **A movie's file is identified by size; numbering is never used (#210).**
  `mapMovie` takes the payload's largest surviving video, because a film is the
  biggest thing shipped with it — a property of the payload rather than of how a
  releaser named it, which is the same reason `decide` stopped mapping by number
  on the movie path (#218). The number-driven mapping was actively unsafe here:
  a movie's `covers` is always `{1}`, so a numbered extra (`Deleted Scene 1`)
  claimed the film's only item, hardlinked a clip as the movie and dropped the
  feature as a leftover — settled, held in the library, and self-healing never. Note the
  asymmetry it had: *two* claimants deferred safely as a conflict while *one*
  imported. Keyed on `domain.FormatMovie` and never on a one-item info-hash group, since a
  series' single grabbed episode is one too and its number is genuine identity
  there. The filter still runs first (a sample is never the feature, and is
  often the small file anyway, so size must not re-admit it), an exact size tie
  is a conflict rather than a coin flip, and a retry override still overrules
  everything. One consequence worth knowing: since size alone selects the file, the
  `unmatched file(s)` deferral is unreachable for a movie — a tie and an
  unextracted archive are the only deferrals it has.
- **User-facing copy follows the same rule, and only on paths a movie can take
  (#210).** The importer's settled reasons and the notification adapters word
  themselves off the item's kind (`itemLabel`, and the adapters' second condition
  on `Event.ItemKind`), so a film is never "episode 1"; every string only an episode
  can produce keeps its wording byte-identical, which is what the series
  assertions in `events_test.go` and `retry_test.go` pin. `Event.ItemKind` is
  display-only — `webhook.go` must never map it, because `item_number: 1` is
  *correct* for a movie and the payload is a contract (#207 broke it once,
  deliberately and with an upgrade note).
- **`failed` also means "this release is remembered" (`internal/core/blocklist`,
  #118).** Both `failed` paths record a per-title blocklist entry, because the
  grab row is per wanted item and the next attempt overwrites it — without that
  memory the search sweep re-derived the same ranking and re-grabbed the same doomed
  release forever. `decide` reads it through the existing `ineligibleReason`,
  so the search sweep's eligibility check, the Releases tab's reason column and manual
  grab's freedom from eligibility (PR #57) all hold unchanged. Two constants are
  deliberate rather than arbitrary: identity is the **info hash or the
  normalized release name** (`normalized_title`), because Torznab often omits the hash; and the expiry
  **escalates** (24h, 7d, then permanent) because the `failed` paths fire for
  environmental reasons that can fail many grabs at once, so permanent-on-first
  would blocklist a whole in-flight set on one qBit incident. Expired entries are
  filtered, never deleted — the row stores the failure count `blockDuration` reads.
  An *import* failure records nothing: it stays `grabbed` and
  retries, because its causes are path-mapping gaps rather than bad releases.
- **An absent torrent is not a verdict (#241).** `failed` settles two different
  things and an inference justifies only one of them: freeing the item is self-healing and
  stays automatic, while remembering the release as bad is a judgement that needs
  a cause. Only three things supply one — the client reporting `error` for one
  of its torrents, a payload we examined that lacked a covered item's file, and a
  download URL that could not be fetched or parsed (`acquire.AutoGrab`, #120).
  Absence supplies none (every cause is external: a hand-removed torrent, a reset
  client, other tooling, a hash the client never had), and neither does
  `missingFiles`, which is why it maps to its own `download.State` rather than
  sharing `StateError` — the data is gone, the release is not at fault, and a
  dropped mount would otherwise blocklist every release on it at once. **A blamed
  failure's two consequences always happen together**: the memory, and moving the
  item to the front of the search queue — so an unblamed failure gets neither.
  `record()`'s breaker branch (#120) already set the precedent: it skips the
  re-front exactly when it skips the blame, and a dropped mount would otherwise
  cause a second thundering herd in response to the first. `blame` is a required
  `failGrab` argument rather than a default, so every new failure path has to
  pass it explicitly. **Dropping the memory
  cost `data_missing` its only loop breaker**, which the blocklist entry had been
  supplying by accident: converging on a duplicate (`AddAlreadyExists`) assumes the
  torrent can still finish, and one whose data is gone never will, so the same release
  ranked first and "grabbed" every pass while the item stayed unacquirable. The
  adapter now returns `download.ErrDataMissing` for that add — not
  `ErrBadRelease`, which is the one `acquire.AutoGrab` blocklists — so the pass
  moves on to the next-best release instead. **Only the branch where the torrent
  demonstrably existed before our add returns that error**, which is the
  pre-check and never the post-failure re-check: there our own add may be what
  landed, so returning the error would leave a torrent no grab row references —
  #134's orphan, which that branch exists to prevent. It costs nothing to
  converge there, because the loop's steady state runs through the pre-check: a
  duplicate found by the re-check writes a grab row, fails unblamed, and gets the
  error on the next pass. One extra cycle, not a loop. Converging on a *healthy*
  duplicate is unchanged either way, and is what makes re-grabbing an in-flight
  torrent safe. A stalled torrent is *present* and takes none of these paths, so
  the doomed-release case #118 guards against cannot arise from absence. The posture behind it: this
  app disassociates a torrent from the library and never removes or deletes one
  on its own, because the download client is the user's disk and their ratio.
- **Both timers are the info-hash group's, not the row's (#247).** A pack is one
  torrent, so `sharedSince` gives every row of a group its earliest stamp and
  `stalled_since`/`missing_since` are stamped and cleared per group. Per-row
  clocks were the bug: a row a later add wrote (#241's converged duplicate) began
  its own clock, crossed the threshold in a *later* scan, and so fell outside
  `remember()`'s per-scan grouping — one incident, two `Record` calls, and since
  the upsert is keyed on `(title, normalized title)` that counts as `failures = 2`
  and escalates the expiry to 7d rather than writing a second row. The seam is here
  and not in `remember()`, whose per-scan grouping is #124's design and correct;
  widening *it* would need cross-scan state the design avoids. Three
  consequences. **Earliest, not `now`** — the clock belongs to the torrent, and
  taking `now` for the late row would reproduce the split; it also makes
  an install upgrading mid-stall converge rather than stay inconsistent. **The
  value is written, not just computed**, because the Activity queue renders
  `abandon_at` from each row's own column, so a divergent stamp would show one
  episode of a pack a countdown it will never be settled on; the write is guarded
  on the value differing, so a steady state costs nothing. And **an unreadable
  stamp is treated as an absent one** rather than restarting the group — the
  tolerance that unparseable data must not fail a grab is preserved at the group
  level, where only *no* row being readable waits another full period. Clearing
  needed no change: both clear conditions read the group's status and the global
  timeout, never the row.
