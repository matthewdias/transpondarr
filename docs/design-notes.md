# Transpondarr — design notes

Design rationale too long to read as a bullet in `CLAUDE.md`. Each section answers
one question, so a reader with a specific question can find the paragraph that
answers it (rule 22 of [`style.md`](style.md)).

`CLAUDE.md`'s `## Conventions` has a one-line pointer to each of these; the
pointer is the summary, this file is the argument.

## A stall at exactly 0% is the one absence-shaped thing that is the release's fault (#242)

A `stalledDL` torrent is *present*, so neither `reconcileMissing` nor
`StateError` handled it, and its grab stayed open forever. That is the doomed
release the blocklist was built for (#118), and it never got a blocklist entry.

Progress is the discriminator, strictly `> 0`. A torrent that made any progress
proves a peer had the data, so those bytes are the user's to discard. A percentage
threshold would draw a line nothing supports.

This is `blameRelease`, unlike a torrent that has vanished (#241). Nobody
seeding a release we can see is a fact about that release, and without the
blocklist entry the search sweep re-picks the same first-ranked release and loops.

Two things bound how far a VPN drop can fan out. Every such failure runs through
`blocklist.Record`, so the failure breaker (#120) blames four items and suppresses
the rest. And only a torrent that never received a byte qualifies.

### Why `Progress`, and not a count of bytes received

`Progress` is the right predicate and must not be swapped for a bytes-received
count. The plausible "refinement" is a regression.

It is not piece-granular. `sessionimpl.cpp` calls `post_torrent_updates()` with no
arguments, so libtorrent's default `status_flags_t::all()` applies,
`query_accurate_download_counters` included, and that counts *partial blocks* in
`total_wanted_done`. `TorrentImpl::progress()` returns that unrounded, so progress
moves on the first 16 KiB block. Over a six-hour timeout that is under a byte per
second, which puts the slow-torrent false positive out of reach. The refinement
solves nothing that needed solving.

A second reason to distrust it is that it rests on reasoning instead of on traced
fact, and is flagged here as such. libtorrent documents `all_time_download` only
as an accumulated payload counter and describes `total_failed_bytes` separately,
so the documentation does not settle whether a byte counter excludes what a hash
check later discards. If it does not, a torrent failing every check would report
bytes received and `progress == 0` forever, and "has received nothing" would never
abandon it.

### The clock and the timeout

`stalled_since` mirrors `missing_since`: stamped on the first qualifying
observation, cleared the moment progress moves. The timeout is
`download.stall_hours`, which is client-agnostic policy and therefore not
`qbit.*`.

Setting it to 0 both disables the timeout and **clears the stamp**, so the two
ways of keeping a stalled download agree. Pausing and switching the timeout off both give
a fresh window instead of banking the wait.

### Which client states qualify (#246)

The trigger is the client reporting an *active* download, with progress 0. That
means `StateStalled` or `StateDownloading`, so `metaDL` and `forcedMetaDL` are
covered — the magnet stuck at "Downloading metadata", which #242's own wording
had to exclude.

`StatePaused` is deliberate user intent and `StateUnknown` is a gap in `mapState`,
so neither takes that branch. `queuedDL` is excluded by having its own
`StateQueued` instead of being folded into `StateDownloading`, because a queued
torrent is not an active download. Folding the two together is what made "widen the predicate"
and "never abandon a queued download" look like opposites.

`Status.StuckAtZero` names the predicate. `stalled_since` keeps a
name it has outgrown, because the clock did not change — it still mirrors
`missing_since`, and a migration for a column name is cosmetics.

The stamp-clearing loop and the switch's `StateStalled`/`StateDownloading` case
must read the one predicate. Widening only that case clears the clock every scan while `sharedSince` re-derives it from
the pre-clear rows. The database then keeps the cleared value, the timeout never
accumulates, and the grab stays open — the same bug, with its fix in place. It is a
mutation that lives, so `TestKeepsMetadataStallClockAcrossScans` exists to kill it.

### No fetching-metadata state, on purpose

Queued is near-universal: five of six surveyed clients have it, rTorrent has no
queue, and a missing download state degrades cleanly because an adapter never emits
it.

Fetching-metadata is one client's taxonomy. qBittorrent and Deluge are both
libtorrent and disagree in both directions, with qBit passing the metadata state
through while Deluge does not expose it. The two are disjoint by
construction in qBittorrent, whose `updateState()` tests `isQueued()` first inside
the `!hasMetadata()` branch.

If "Fetching metadata" is ever wanted in the UI it is a detail field beside
`State`, never a download state value. Sonarr renders it as a detail field, and
Transmission's `metadata_percent_complete` and rTorrent's `d.is_meta` are shaped
for the same use.

### An adapter maps the derived predicate, never a client's own "stalled" flag (#159)

Ours is qBittorrent's instantaneous `download_payload_rate == 0`. Transmission's
`is_stalled` is a 30-minute idle timer, so mapping that boolean would silently
stack `stall_hours` on top of it and make the threshold mean something different
per client. `stalled` stays instantaneous everywhere, and `stalled_since` is where
the duration lives.

### Absence wins over the stall clock, and what the queue shows

Between the two timers, absence wins by construction. The `!ok` branch
`continue`s before the download-state switch, so a torrent that goes missing is settled on
the 5-minute grace and the stall clock is never read.

The queue's `abandon_at` is the part `client_state` could not express — that we are
going to act, and when. It is therefore keyed on the *live* status as well as on
the stamp, which stays set for up to one scan after the stall ends.

Widened, it now shows on a healthy grab too: for the scan or two before its first
piece lands, and on a magnet for as long as metadata takes. That is accepted
instead of hidden below some fraction of the timeout, which would invent a second
threshold with nothing behind it.

Its countdown is stale for as long as the tab is open, not for one poll. A queue
of only stalled rows serializes byte-identically, so React Query's structural
sharing re-renders nothing. That is the class #144 named, and `activity.tsx` is a
third call site for that audit.

## The staging sweep deletes using rules instead of enumerating the library (#132)

Both transfer paths write a staging file beside the destination: `copyFile`
writes `.partial`, and `replace` makes an `.upgrade` hardlink. Only another
attempt at writing that destination will reclaim the staging files, so a file can
get left over if that never happens.

The predicate asserts the following clauses for the files it looks at:

1. not currently in flight
2. has one of our own two suffixes
3. sits over a known video extension
4. sits under a configured root
5. has an mtime older than 24 hours

`SweepStaging` only removes files it finds, so an unmounted root means it finds
nothing. It can't delete a library it doesn't find.

### What protects a live transfer

While file age is relevant for protecting `.partial` files, `.upgrade` files are
protected by the in-flight registry instead. `os.Link` shares the payload's
inode, so an `.upgrade` link to a payload gets that payload's mtime.

So the staging name is chosen in one place: `staged`, the helper both transfer
paths run their write inside. It builds the name from the destination, registers
it as in flight for the length of the write, and clears the registration when the
write returns. Both paths are given the name instead of computing it, which means
a staging file can't exist without being registered. `removeUnstaged` does the
check and the unlink under one lock, so a transfer can't register between the two.

The limitation we accepted on the other side: an `.upgrade` file orphaned by a
crash isn't swept until its *payload's* mtime passes 24 hours, so it can stay
on disk after it stops being useful. Late, never wrong.

### Resolving roots, and the order the staging sweep works in

Roots are resolved before the enumeration starts. `WalkDir` won't descend a
symlinked root, and `/media` pointing at `/mnt/user/media` is an ordinary NAS
layout. `staged` keys on a `canonical` path so that the registry doesn't silently
fail to match in that setup. Links inside the tree are still not followed, so the
enumeration stays within a root.

Every root is enumerated before anything is removed. `collectStale` builds the
whole list first, and a second loop deletes from it. If one root is inside the
other, walking the outer one already descends into the inner one, so the same
file lands in the list twice. The first delete succeeds and the second gets
`ErrNotExist`, which `SweepStaging` treats as ordinary instead of as a fault.

Deleting during the enumeration would hide the second occurrence. The outer walk
would remove the file, and the inner walk would never yield a path that no longer
exists. The tolerance would then only run during a real race, where an import
reclaims its own staging file while the sweep is running. Collecting first is what
makes it happen in an ordinary configuration, which keeps the tolerance reachable
and testable.

The de-duplication and the tolerance do different jobs. `stagingRoots` drops two
roots that resolve to the same path, which spares a second enumeration. It does
nothing about one root nested inside another, and tolerating `ErrNotExist` is
what keeps the sweep correct there.

### The videoExts dependency

The video-extension check narrows the predicate to *our* staging files instead of
any `.partial` on disk, and it is a third place that depends on `videoExts` being
the importer's list. If the two lists drift, the cost is a missed sweep and never
a wrong delete, and that is the only direction allowed to fail.

### Why it is an optional capability and its own job

`library.StagingSweeper` is an optional capability found by type assertion, so
`library.Target` is still `Name()` and `Place()`, with no method that
lists what a target currently contains. A target without the capability is a supported
configuration, not an error. That general read path is #170, and manual file
adoption (#157) and library drift detection (#171) both need it. The issue has to
settle the question this sweep's design already answered locally: whether listing belongs
on `Target` itself, or stays an optional capability like this one.

The sweep runs as its own slow job instead of inside the 15-second import scan,
because it enumerates every root.
