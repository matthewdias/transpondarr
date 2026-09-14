# Library placement (`internal/core/library`)

Where a placed file goes and what shape it takes there. Root (destination) and
layout (shape within a root) are deliberately different axes.

- **The library has a root per format, and a missing one is an error, never a
  fallback (#198).** `mediaserver.Roots` splits Series from Movies because Plex
  and Jellyfin expect a Movies library separate from Shows, and `Place` picks
  between them on `Format` alone — the same discriminator, so a one-item OVA
  files under Shows with the rest. Placing a movie with no movies root returns
  `ErrNoMoviesRoot` rather than falling back to the series root: an import
  failure is the one settled-status exception (it stays `grabbed` and retries),
  so the grab row stays `grabbed`, the error surfaces as `last_error` in the Activity queue
  plus one import-stuck notification, and the next scan imports it once the root
  is set — where a file already hardlinked into the wrong library would need
  hand cleanup. Root (destination) and layout (shape within a root) stay
  different axes: #198 defines the root, #129 the shape.
- **Layout parameterizes the shape inside a branch, never the branch itself
  (#129).** `library.series_layout` (`season_folders` default, `flat`) is read
  only by `destination`'s series branch, so format stays the sole discriminator and
  a one-item OVA loses its season folder along with every other series — the
  movie shape is identical under either layout. It is a string enum rather than
  a bool for two reasons: `libraryInput` is `omitempty` throughout, where a bool
  cannot distinguish "false" from "absent" (the trap `automationInput` documents),
  and #168's per-format routing will need a value it can set per format. The
  default is the *current* behaviour, which is what lets an install that predates
  the key keep the layout its files are already in — `ParseLayout` maps both
  empty and unrecognized to `season_folders`, and the settings layer normalizes
  through it so what is stored, displayed and joined into a path agree.
  **Switching layouts moves nothing already placed**, so an upgrade writes the
  new shape beside the old file. That is the same orphan class as a refreshed
  title or year and is repaired the same way (#213's placed-path memory), never
  by deleting from a computed path; but switched *to* flat the series folder
  still exists, so the missing-directory warning cannot fire and `heldElsewhere`
  is the only evidence. The two warnings are **independent `if`s, not a chain** —
  they detect different things, and one silencing the other is worse than
  either alone. `heldElsewhere` therefore matches a *video* at the exact stem
  rather than any stem-mate, or an interrupted copy's `.partial` would report a
  layout switch that never happened and suppress the real warning.
- **`removeStemMates`' trailing dot is necessary and only a two- against
  three-digit pair tests it.** `seasonNumber` is hardcoded to 1, so every episode
  of an AniList entry already shares a directory and the flat layout adds no neighbours
  to the one it scans — the set of files it can remove is unchanged either way. But E03/E30
  diverge at the first digit and pass with the guard removed; E10/E100 is the
  pair that catches it.
- **The staging sweep deletes using rules instead of enumerating the library
  (#132).** Long enough to need its own section — see
  [`docs/design-notes.md`](../../../docs/design-notes.md).
- **The library flag and the derived item status share one name, deliberately
  (#84): `in_library`.** `wanted_items.in_library` sources the status
  `deriveItemState` returns, so renaming either alone would hide the derivation.
  The name is mechanism-agnostic on purpose — `imported` would name the importer
  as the only route into the library, which pre-existing-library import and hash
  identification (deferred, not rejected) would make untrue in the API contract.
