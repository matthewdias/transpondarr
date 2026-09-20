# Library placement (`internal/core/library`)

Where a placed file goes and what shape it takes there. Root (destination) and
layout (shape within a root) are deliberately different axes.

## Roots per format (#198)

- **The library has a root per format, and a missing one is an error, never a
  fallback (#198).** `mediaserver.Roots` splits Series from Movies because Plex
  and Jellyfin expect a Movies library separate from Shows.
- **`Place` picks between the roots on `Format` alone.** It is the same
  discriminator used everywhere else, so a one-item OVA files under Shows with
  the rest.
- **A movie with no movies root returns `ErrNoMoviesRoot` instead of falling back
  to the series root.** An import failure is the one settled-status exception:
  the grab row stays `grabbed` and retries. The error surfaces as `last_error` in
  the Activity queue plus one import-stuck notification. The next scan imports it
  once the root is set. A file already hardlinked into the wrong library would
  need hand cleanup instead.
- **Root and layout stay different axes.** The per-format library root (#198)
  defines the root, and the library layout options (#129) define the shape.

## Series layout (#129)

- **Layout parameterizes the shape inside a branch, never the branch itself
  (#129).** `library.series_layout` (`season_folders` default, `flat`) is read
  only by `destination`'s series branch, so format stays the sole discriminator.
  A one-item OVA loses its season folder along with every other series, and the
  movie shape is identical under either layout.
- **The setting is a string enum rather than a bool, for two reasons.**
  `libraryInput` is `omitempty` throughout, where a bool cannot distinguish
  "false" from "absent" (the trap `automationInput` documents). And per-format
  routing (#168) will need a value it can set per format.
- **The default is the *current* behaviour**, which is what lets an install that
  predates the key keep the layout its files are already in. `ParseLayout` maps
  both empty and unrecognized to `season_folders`. The settings layer normalizes
  through it, so what is stored, displayed and joined into a path agree.
- **Switching layouts moves nothing already placed**, so an upgrade writes the
  new shape beside the old file. The old file is the same orphan class as a
  refreshed title or year. It is repaired the same way, by placed-path memory
  (#213), never by deleting from a computed path.
- **Switched *to* flat, `heldElsewhere` is the only evidence.** The series folder
  still exists, so the missing-directory warning cannot fire.
- **The two warnings are independent `if`s, not a chain.** They detect different
  things, and one silencing the other is worse than either alone.
- **`heldElsewhere` matches a *video* at the exact stem rather than any
  stem-mate.** Otherwise an interrupted copy's `.partial` would report a layout
  switch that never happened and suppress the real warning.

## The copy fallback in auto import mode (#303)

- **A hardlink auto import mode turned into a copy is logged, because nothing else
  distinguishes the two.** `importer: imported` records the destination and not how
  the bytes got there. An install whose `PUID` can't hardlink qBittorrent's downloads
  copied every episode at twice the disk cost and wrote no line about it.
- **The first fallback of each kind warns; a repeat of that kind is `Info`.** An SMB
  or FUSE mount that can never hardlink would otherwise warn on every import, and
  `main.go` builds its handler with no level, so `Debug` is off and can't be turned
  on. A restart re-arms it, and so does any library settings save, since
  `settings.UpdateLibrary` rebuilds the target whether or not the values changed.
- **"Each kind" is the refusal, never the `Target`.** One target covers both library
  roots, so a Movies root on a second disk falls back on `EXDEV` for every film —
  permanent, benign, nothing to fix. Keyed on the target, that film would spend the
  one warning the Series root's fixable `EPERM` needed, and every later `EPERM` would
  be `Info` for the life of the process. `refusal` is therefore the errno plus
  whether the diagnosis fired, so two `EPERM`s still differ when only one of them
  names a cause.
- **`EPERM` still means "fall back to a copy", and the log line carries the
  diagnosis instead of the code acting on it.** `fs.protected_hardlinks` makes
  `link()` fail with `EPERM` for a file the caller neither owns nor can read and
  write. A user fixes that by changing `PUID` or qBittorrent's umask. SMB/CIFS and
  some FUSE mounts return the same `EPERM` when the filesystem has no hardlinks at
  all, which no user can fix. The file's owner and mode don't distinguish the two,
  because a FUSE mount can report any owner. So acting on one `EPERM` would stop
  imports for installs that work today, and `TRANSPONDARR_IMPORT_MODE=hardlink` is
  already the setting for anyone who wants a refused link to keep the grab row open.
- **`CAP_FOWNER` and `CAP_DAC_OVERRIDE` each make an `fs.protected_hardlinks`
  refusal impossible, so the diagnosis asks about capabilities before ownership.**
  `may_linkat()` accepts either an owner-or-capable caller or a source the caller can
  read and write, and `CAP_DAC_OVERRIDE` supplies the second on every file. Traced in
  Docker: `--cap-drop ALL --cap-add DAC_OVERRIDE` hardlinks a download owned by
  another user, while `--cap-add FOWNER` alone does not, since the directory write
  then fails first with `EACCES`. Bare-metal root under `TRANSPONDARR_PRIVDROP` and
  `docker run` as root both hold the pair, so an `EPERM` there is the mount and
  naming `PUID` would send a user after a fix that changes nothing.
- **Testing `l.euid == 0` instead would be the wrong reading.** It would also silence
  `PUID=0` under the example compose's `cap_drop: ALL`, which is root holding
  *neither* capability and the case #303 documents. So `boundByFileOwner` reads
  `CapEff` from `/proc/self/status`, and off Linux it is false outright, since
  `fs.protected_hardlinks` is the only thing the message names.
- **Neither result from `protectedHardlinkAttrs` is proof, which is why the field
  is named `likely` and the message starts "if the mount supports hardlinks".** The
  conditions it tests are necessary for an `fs.protected_hardlinks` refusal and not
  sufficient. An SMB mount holding a download qBittorrent owns satisfies all of them
  and cannot hardlink for an unrelated reason. Returning nothing is weaker still,
  since an unreadable owner produces it too.

## Other placement rules

- **`removeStemMates`' trailing dot is necessary and only a two- against
  three-digit pair tests it.** `seasonNumber` is hardcoded to 1, so every episode
  of an AniList entry already shares a directory. The flat layout therefore adds
  no neighbours to the one it scans, and the set of files it can remove is
  unchanged either way. But E03/E30 diverge at the first digit and pass with the
  guard removed; E10/E100 is the pair that catches it.
- **The staging sweep deletes using rules instead of enumerating the library
  (#132).** Long enough to need its own section — see
  [`docs/design-notes.md`](../../../docs/design-notes.md).
- **The library flag and the derived item status share one name, deliberately
  (#84): `in_library`.** `wanted_items.in_library` sources the status
  `deriveItemState` returns, so renaming either alone would hide the derivation.
- **The name `in_library` is mechanism-agnostic on purpose.** `imported` would
  name the importer as the only route into the library. Pre-existing-library
  import and hash identification (deferred, not rejected) would make that untrue
  in the API contract.
