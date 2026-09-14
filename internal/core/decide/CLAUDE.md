# Matching and eligibility (`internal/core/decide`)

Which release is offered for which item, and which is refused. `decide` is
pure: it reads what a release name says and what the profile allows, and it
never touches the client, the library or the store.

### Batch releases (#126)

- **A batch is matched, eligible, and preferred on coverage (#126).** The sweep
  guard against auto-grabbing a batch (#125) refused a pack in
  `ineligibleReason`, because the importer could only *defer* a multi-episode
  payload. Per-file batch import (#126) removed the reason, so the refusal is
  gone. Both entry points, rehearsal included, inherit the three changes below
  through the one decision layer.
- **Coverage ranks between Pinned and Score.** The comparator gained a
  **coverage ranking** on `Items` descending. Lifting the refusal alone would
  leave the winner between a pack and a single arbitrary on score and seeders; a
  pack covering six wanted items is one grab instead of N. Weekly singles tie at
  1 and fall through to score unchanged.
- **The pin stays *above* coverage deliberately.** A pin is per-title knowledge
  ("this release group is definitive"), so coverage only breaks ties among
  equally pinned candidates.
- **An explicit range past `maxItem` is now unmatched.** `batchItems` gained the
  guard it never had, and returns the single-episode path's
  absolute/season-mismatch reason. So a `01-48` pack no longer matches items
  1-12 of a 12-item AniList entry. A numberless pack has no range to check and
  still fills the AniList entry, because that is what a season pack is.

### Parsing once per poll (#266)

- **The automation loop parses once per poll, not once per title — and not at
  all for a held release no profile will rate (#266).** Measured, reusing
  `held_release_parses` from `decide.Match` was the smallest of three effects in
  the one loop. It was also the only one costing a `decide.Item` change. So the
  two we took are both about **not doing the work** rather than caching it.
- **A feed poll shares one parse map across titles.** A feed poll matches one
  ~100-entry feed page against *every* due title, so `MatchOpts.Parses`
  contains the feed page's parses, built once in `pageParses`. The map is
  **read-only inside `decide`**, which is what makes it shareable across titles
  without a lock. A miss is parsed normally, so the map is a cache and never a
  filter. The search sweep deliberately passes nil: it searches per title, so
  its releases differ and there is nothing to share.
- **The feed half's cost is untestable by construction.** The memo agrees with a
  fresh parse, so dropping it changes no behaviour and only an allocation
  assertion could catch it.
- **With upgrades off, `Match` stores a held item's membership without its
  parse.** `profile.UpgradesEnabled` is false by schema default. Every read of
  the parsed value is guarded by that flag, while the reason wording at the
  batch, single and movie branches only checks `_, ok := held[n]`.
- **The membership write is necessary.** Leaving `held` empty instead trips
  `applyUpgradePolicy`'s `len(held) == 0` early return. Covered items are then
  never marked `UpgradeBlocked` and stay in `TakeItems()`, so the search sweep
  grabs an upgrade on a profile that has upgrades switched off. Deleting that
  one line is the mutation that proves it.
- **The `UpgradesEnabled` check is hoisted into `applyUpgradePolicy` on
  purpose.** The only path reading a `heldRelease`'s parse and score is then the
  one that computed them. Pushing the check back down into `upgradeRefusal` is
  behaviour-preserving today and makes the zero value reachable by the next
  edit.

### Items: numbering basis and candidacy

- **`decide.Match`'s `items` is the numbering basis, not only the candidate
  set.** `maxItem` spans every item passed (grabbable or not) and drives
  absolute-numbering detection. So narrowing the slice to scope a search
  silently misreports every release outside that range. Scope with
  `decide.Item.Grabbable` instead. The scoped search is tracked as
  episode-specific search (#105).
- **Candidacy and possession are two fields, not one.**
  `decide.Item.Grabbable` is "worth offering a release for";
  `domain.WantedItem.InLibrary` is "a file is in the library". They coincide
  only on the manual path, because the search sweep withholds in-flight and
  unaired items that plainly aren't in the library. Only `acquire.passItem` may
  derive one field from the other, and it is where each entry point states its
  own.
- **Quality upgrades (#97) need both fields.** The upgrade path is an item
  already in the library that is grabbable anyway, which is the case the old
  single field could not express.

### Item monitoring (#188)

- **Per-episode monitoring (#188) is one more input to `Grabbable`, which is why
  it costs `decide` and the importer nothing.** `wanted_items.monitored` is
  conjoined into `loadSweepItems`' single grabbability line. So it applies to
  search-sweep searches, feed grabs and the upgrade pool at once. `maxItem`
  still spans the item, and a pack covering an unmonitored episode still matches
  the rest.
- **Monitoring never restricts a manual path**: search, grab, and later file
  adoption (#157). The manual paths sit deliberately outside the grabbability
  condition, which generalises the manual grab's freedom from eligibility
  (PR #57) rather than enumerating the paths that exist today.
- **The importer deliberately doesn't check monitoring.** A pack grabbed for its
  monitored neighbours still places every file it contains. The bytes are
  already spent, and a hardlink costs no disk. The hole is transitional:
  `unclaimedItem` already excludes an item already in the library, so
  file adoption (#157) closes it with no importer change.

### Movies: batch tokens (#211)

- **A batch token on a movie release is an eligibility rule, not a matching one
  (#211, automation including movies).** Movie mode's two numeric checks both
  read what a release *names*. A numberless pack names neither an episode nor a
  year, so `[Grp] Placeholder Saga (Complete Series)` matched the film
  `Placeholder Saga: The Final` eligibly. The search sweep grabbed it, and the
  importer placed one of the *series'* episodes into the Movies root under the
  film's name.
- **The refusal is movie-specific.** On the series path a numberless pack
  filling the AniList entry is what a season pack *is*.
- **The check is in `ineligibleReason` rather than `movieCandidate`.** A genuine
  multi-part film release is indistinguishable from a parent series' pack.
  Refusing it as unmatched would 422 the manual grab and make it ungrabbable
  without renaming, so the precision risk goes on the supervised path alone, as
  the null-year rule splits it.
- **The reason ranks above the null-year reason and below the profile rules.**
  It ranks **above** the null-year reason because it is per release, so it
  discriminates between releases. It ranks **below** the profile rules because
  the user set those deliberately.
- **An explicit range stays a *matching* refusal, unchanged**: a film cannot
  span episodes however it is packaged.

### Movies: the null-year rule (#208)

- **The null-year rule is one rule split by actor, not two (#208, a movie is
  addable).** `series.year` is `0`, never NULL, for "no year on record".
  **Naming** drops the ` (Year)` suffix (#198, the per-format library root).
  **Matching** stays free for manual search and grab, per the manual grab's
  freedom from eligibility (#57). It still gets an ineligible reason, so
  automation never grabs a null-year movie (#209, movie mode).
- **The precision risk sits on the supervised path, the availability cost on the
  unsupervised one.** A null year correlates with an unreleased title, which is
  when every candidate a search returns is wrong.
- **The stored year is refresh-maintained rather than an add-time snapshot.**
  `SetTitleYear` guards `? > 0` **in SQL**, so no caller can let a transient
  upstream null erase one.
- **A movie's path is keyed on that refresh-maintained year**, so a 0 -> N fill
  after an import orphans the year-less folder the next upgrade replaces into.
  The orphan is the same class as an AniList title edit orphaning
  `<root>/<Old Name>/`, which the series branch has always had. Placed-path
  memory (#213) repairs it, never enumerating the library: `Place` only warns
  that its naming inputs moved.

### Movies: reading a release's year (#209)

- **A year is read the same way whichever form names it, and both are decided
  against the variants (#209, movie mode).** anitogo fills `AnimeYear` only from
  a *bracket-isolated* token, so `[Grp] Film (2019)` yields a year. The scene
  form `Film.2019.1080p.x264-GRP` glues the year into the anime title and
  reports none. That would have left the year check inert on the naming form
  films most often ship in.
- **`parser.Parsed.Year` stays narrow, and `decide.releaseYear` reads either
  source.** `parser.Parsed.Year` keeps anitogo's reading, since the parser
  deliberately ignores identity. `decide.releaseYear` takes the isolated token,
  else the **rightmost** in-range four-digit token in the release name, never
  the first. Unrecognized scene tags trail the year (`Sample Film 2021 REPACK`),
  while a leading number is the film naming itself.
- **One variant check runs on the result, not on either source.** A year that
  appears in an accepted variant is part of the film's name
  (`Placeholder Legend 1979`), not a release year. Checking only the scanned
  source made the two forms of one release disagree, and it was the bracketed
  one that got refused.
- **A collision reports *no* year, so the year check passes instead of
  refusing.** That is deliberate, because a wrong year is a **matching** refusal
  and an unmatched release is `grabRelease`'s 422. Over-reading a year would
  block the manual grab PR #57 protects.
- **The null-year *title* is the other half, and is never a refusal.** It is a
  reason in `ineligibleReason`, **last** in that chain. It is a title-level fact
  identical on every release, while every rule above it discriminates between
  releases.

### Movies: a release's number (#209, #211)

- **Movie mode ignores a release's number for *mapping* and reads it for
  *identity* (#209, movie mode)** — not the same thing, and conflating them was
  a bug. Every episodic token appears on movie names (`Sample Film 2 (2021)`
  parses as episode 2, `(Complete)` sets `Batch`), so none may map onto a film.
  `movieCandidate` never calls the episode-mapping code, which is not the same
  as never reading the number.
- **The number rules out a long-runner.** `titleBelongs` is fuzzy containment,
  so a long-runner sharing a name prefix takes the movie path with episode 250,
  and refusing *that* is the number's remaining job. The number disqualifies the
  release unless reattaching it to the parsed title matches a variant. That is
  the same check against the variants that the year rule makes. The number check
  includes padded widths, since a release writes `0080` where anitogo returns
  80. An explicit range never qualifies.
- **That variant match is exact, and the asymmetry with `titleBelongs` is the
  point (#211, automation including movies).** Containment proves nothing about
  a name we assembled: any variant prefixing the parsed title is contained by
  construction, so the fuzzy branch only ever returns true. The match shipped
  fuzzy, and the guard was inert whenever the film's title was *not* longer than
  the release name — `Sample Film` matched `Sample Film Chronicles - 250`
  unattended. `titleBelongs` compares a name the releaser wrote and stays fuzzy,
  while the variant match compares one we built.
- **The exact match's cost is accepted knowingly.** A film whose variant renders
  its number differently (`Sample Film 2` against `Sample Film 2nd Movie`) goes
  unmatched. Being a *matching* refusal, it 422s the manual grab too. A named
  test pins it, so the strictness is not read as an oversight and loosened back.
