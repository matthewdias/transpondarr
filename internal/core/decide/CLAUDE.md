# Matching and eligibility (`internal/core/decide`)

Which release is offered for which item, and which is refused. `decide` is
pure: it reads what a release name says and what the profile allows, and it
never touches the client, the library or the store.

- **A batch is matched, eligible, and preferred on coverage (#126).** #125
  refused a pack in `ineligibleReason` because the importer could only *defer* a
  multi-episode payload; per-file import removed the reason, so the refusal is
  gone. Three things follow. The comparator gained a **coverage ranking** — `Items`
  descending, between Pinned and Score — because lifting the refusal alone would
  make the winner between a pack and a single score- and seeder-arbitrary; a
  pack covering six wanted items is one grab instead of N. Weekly singles tie at
  1 and fall through to score unchanged, and the pin stays *above* coverage
  deliberately: a pin is per-series knowledge ("this group is definitive"), so
  coverage only breaks ties among equally pinned candidates. And `batchItems` gained
  the guard it never had: an explicit range past `maxItem` is now **unmatched**
  with the single-episode path's absolute/season-mismatch reason, so a `01-48`
  pack no longer claims a 12-item entry's items 1-12. A numberless pack has
  no range to check and still fills the entry — that is what a season pack is.
  Both entry points inherit all of it through the one decision layer, rehearsal
  included.
- **The automation loop parses once per poll, not once per title — and not at all
  for a held release no profile will rate (#266).** Measured, reusing
  `held_release_parses` from `decide.Match` was the smallest of three effects in
  the one loop and the only one costing a `decide.Item` change, so the two that
  were taken are both about **not doing the work** rather than caching it. A
  feed poll matches one ~100-entry page against *every* due title, so
  `MatchOpts.Parses` contains the feed page's parses, built once in `pageParses`; it is
  **read-only inside `decide`**, which is what makes one map shareable across
  titles without a lock, and a miss is parsed normally so it is a cache and never
  a filter. The sweep deliberately passes nil — it searches per title, so its
  releases differ and there is nothing to share. And `Match` stores a held item's
  **membership without its parse** when `profile.UpgradesEnabled` is false (the
  schema default), because every read of the parsed value is guarded by that flag
  while the reason wording at the batch, single and movie branches only ever checks
  `_, ok := held[n]`. Two things a reader would otherwise break. **The membership
  write is necessary**: leaving `held` empty instead trips
  `applyUpgradePolicy`'s `len(held) == 0` early return, so covered items are never
  marked `UpgradeBlocked`, stay in `TakeItems()`, and the sweep grabs an upgrade on
  a profile that has upgrades switched off — deleting that one line is the
  mutation that proves it. And the `UpgradesEnabled` check is **hoisted into
  `applyUpgradePolicy` on purpose**, so the only path reading a `heldRelease`'s
  parse and score is the one that computed them; pushing it back down into
  `upgradeRefusal` is behaviour-preserving today and makes the zero value
  reachable by the next edit. The cost of the feed half is untestable by
  construction — the memo agrees with a fresh parse, so dropping it changes no
  behaviour and only an allocation assertion could catch it.
- **`decide.Match`'s `items` is the numbering basis, not just the candidate set.**
  `maxItem` spans every item passed (grabbable or not) and drives absolute-numbering
  detection, so narrowing the slice to scope a search silently misreports every
  release outside that range. Scope with `decide.Item.Grabbable` instead (#105
  tracks the scoped search).
- **Candidacy and possession are two fields, not one.** `decide.Item.Grabbable`
  is "worth offering a release for"; `domain.WantedItem.InLibrary` is "a
  file is in the library". They coincide only on the manual path — the sweep
  withholds in-flight and unaired items that plainly aren't in the library — so nothing may
  derive one from the other outside `acquire.passItem`, which is where each entry
  point states its own. #97's upgrade path is a held item that is grabbable
  anyway, which is exactly the case the old single field could not express.
  **Item monitoring (#188) is one more input to `Grabbable`, which is why it
  costs `decide` and the importer nothing**: `wanted_items.monitored` is
  conjoined into `loadSweepItems`' single grabbability line, so it applies to sweep
  search, feed grab and the upgrade pool at once, while `maxItem` still spans
  the item and a pack covering an unmonitored episode still matches the rest.
  Two things sit deliberately outside that condition. **Monitoring never
  restricts a manual path** — search, grab, and later file adoption (#157) — which
  generalises PR #57 rather than enumerating the paths that exist today; and the
  importer doesn't check monitoring at all, so a pack grabbed for its monitored neighbours
  still places every file it contains. The bytes are already spent, a hardlink
  costs no disk, and the hole is transitional: `unclaimedItem` already excludes a
  had item, so adoption closes it with no importer change.
- **A batch token on a movie release is an eligibility rule, not a matching one
  (#211).** Movie mode's two numeric checks both read what a release *names*, and
  a numberless pack names neither an episode nor a year — so `[Grp] Placeholder
  Saga (Complete Series)` matched the film `Placeholder Saga: The Final`
  eligibly, the sweep grabbed it, and the importer placed one of the *series'*
  episodes into the Movies root under the film's name. This is movie-specific: on the
  series path a numberless pack filling the entry is what a season pack *is*.
  The check is in `ineligibleReason` rather than `movieCandidate` because a
  genuine multi-part film release is indistinguishable from a parent series'
  pack — unmatched would 422 the manual grab and make it ungrabbable without
  renaming, so the precision risk goes on the supervised path alone, exactly as
  the null-year rule splits it. It ranks **above** the null-year reason (per
  release, so it discriminates between rows) and **below** the profile rules
  (which the user set deliberately). An explicit range stays a *matching*
  refusal, unchanged: a film cannot span episodes however it is packaged.
- **The null-year rule is one rule split by actor, not two (#208).** `series.year`
  is `0`, never NULL, for "no year on record", and the split is: **naming** drops
  the ` (Year)` suffix (#198), while **matching** stays free for manual search and
  grab — the #57 doctrine — but gets an ineligible reason so automation never
  grabs a null-year movie (#209). The precision risk sits on the supervised path,
  the availability cost on the unsupervised one, because a null year correlates
  with an unreleased title, which is exactly when every candidate a search returns
  is wrong. The stored year is refresh-maintained rather than an add-time
  snapshot, and `SetTitleYear` guards `? > 0` **in SQL** so no caller can let a
  transient upstream null erase one. **A movie's path is keyed on that
  refresh-maintained year**, so a 0 -> N fill after an import orphans the
  year-less folder the next upgrade replaces into — the same class as an AniList
  title edit orphaning `<root>/<Old Name>/`, which the series branch has always
  had. Repaired by #213's placed-path memory, never by enumerating the library:
  `Place` only warns that its naming inputs moved.
- **A year is read the same way whichever form names it, and both are decided
  against the variants (#209).** anitogo fills `AnimeYear` only from a
  *bracket-isolated* token, so `[Grp] Film (2019)` yields a year while the scene
  form `Film.2019.1080p.x264-GRP` glues it into the anime title and reports
  none — which would have left the year check inert on the naming form films most
  often ship in. `parser.Parsed.Year` keeps that narrow reading, since the parser
  deliberately ignores identity, and `decide.releaseYear` derives from
  either source — the isolated token, else the **rightmost** in-range four-digit
  token in the title, never the first, because unrecognized scene tags trail the
  year (`Sample Film 2021 REPACK`) while a leading number is the film naming
  itself. Then **one** variant check, on the result rather than on one source: a
  year that appears in an accepted variant is part of the film's name
  (`Placeholder Legend 1979`), not a release year. Checking only the scanned source made the two forms of one
  release disagree, and it was the bracketed one that got refused. A collision
  reports *no* year, so the year check passes rather than refuses — deliberate, because
  a wrong year is a **matching** refusal and an unmatched release is
  `grabRelease`'s 422, so over-reading a year would block the manual grab PR #57
  protects. The null-year *title* is the other half and is never a refusal — it
  is a reason in `ineligibleReason`, **last** in that chain, because it is a title-level
  fact identical on every row while every rule above it discriminates between
  them.
- **Movie mode ignores a release's number for *mapping* and reads it for
  *identity* (#209)** — not the same thing, and conflating them was a bug. Every
  episodic token appears on movie names (`Sample Film 2 (2021)` parses as episode
  2, `(Complete)` sets `Batch`), so none may map onto a film; but `titleBelongs`
  is fuzzy containment, so a long-runner sharing a name prefix takes the movie
  path with episode 250, and refusing *that* is the number's remaining job.
  It is disqualifying unless reattaching it to the parsed title matches a
  variant — the same check against the variants that the year rule makes, padded widths
  included, since a release writes `0080` where anitogo returns 80; an
  explicit range never qualifies. So `movieCandidate` never calls the
  episode-mapping code, which is not the same as never reading the number.
  **That variant match is exact, and the asymmetry with `titleBelongs` is the
  point (#211).** Containment proves nothing about a name we assembled: any
  variant prefixing the parsed title is contained by construction, so the fuzzy
  branch only ever returns true. It shipped fuzzy and the guard was inert
  whenever the film's title was *not* longer than the release's — `Sample Film`
  matched `Sample Film Chronicles - 250` unattended. `titleBelongs` compares a name
  the releaser wrote and stays fuzzy; this compares one we built. The cost is
  accepted knowingly: a film whose variant renders its number differently
  (`Sample Film 2` against `Sample Film 2nd Movie`) goes unmatched, and being a
  *matching* refusal that 422s the manual grab too — pinned by a named test so
  the strictness is not read as an oversight and loosened back.
