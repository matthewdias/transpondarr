// Package importer is the final pipeline stage: it watches the download client
// for grabs Transpondarr initiated (rows in the grabs table) and, once a torrent
// completes, hands the file to a library.Target and marks the item had.
//
// It only ever imports our own grabs — every torrent it touches has an
// authoritative info_hash -> wanted_item mapping, so it never has to identify an
// arbitrary release. Adopting externally-added torrents is a later phase (it
// needs the deferred identification layer).
//
// Every grab status but "grabbed" is settled. A completed payload is walked once
// and its files mapped onto the items the release claimed, so a season pack
// imports episode by episode (#126). "import_deferred" therefore means a covered
// item's file could not be picked out — nothing matched it, or two files claimed
// it — and nothing re-walks the same bytes on a later tick; only an explicit
// retry, optionally naming the file, reopens one. Deferred grabs stay in the scan
// for missing-from-client reconciliation, so a vanished payload still frees its item.
package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/library"
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// Grab status values the importer transitions between.
const (
	statusGrabbed  = "grabbed"         // downloading / awaiting completion
	statusImported = "imported"        // placed in the library, item marked had
	statusDeferred = "import_deferred" // completed, but no single episode file could be resolved
	statusFailed   = "failed"          // the download errored or vanished in the client (terminal)
)

// missingGracePeriod need only cover a client answering before its torrent list
// is fully back; an unreachable client errors out of the scan instead.
const missingGracePeriod = 5 * time.Minute

// ClientSource supplies the current download client, library target, and
// notification dispatcher. It is read on every scan so a runtime settings
// change (which swaps the clients) is picked up on the next poll without
// restarting the importer.
type ClientSource interface {
	Download() download.Client
	Library() library.Target
	Notify() *notify.Dispatcher
}

// Recorder remembers a release that failed, so the sweep stops re-deriving it
// (#118). Narrow on purpose; false means its breaker refused to blame the
// release (#120).
type Recorder interface {
	Record(ctx context.Context, seriesID int64, itemIDs []int64, infoHash, releaseTitle, reason string) (bool, error)
}

// ItemClaims is the in-flight claim registry (satisfied by *acquire.Service).
// The importer takes a claim only to place a payload file for an item no grab
// row claimed, so a concurrent grab cannot race a copy-mode Place that runs for
// minutes. A nil registry means nothing else can be claiming, which is what a
// unit test wiring the importer alone has.
type ItemClaims interface {
	TryClaimItems(ids []int64) bool
	ReleaseClaims(ids []int64)
}

// Importer scans the download client for completed grabs and imports them. It
// runs as a job on the runner, which owns the polling interval.
type Importer struct {
	store     *store.Store
	clients   ClientSource
	log       *slog.Logger
	blocklist Recorder
	claims    ItemClaims

	// mu serializes the 15s scan against a manual retry: both walk the same
	// payload and settle the same rows, and a retry that reopened one mid-scan
	// would have it settled twice.
	mu sync.Mutex
}

// New builds an Importer. The download client and library target are read from
// src each scan, so either being unconfigured (nil) simply skips that scan.
func New(st *store.Store, src ClientSource, log *slog.Logger, blocklist Recorder, claims ItemClaims) *Importer {
	return &Importer{store: st, clients: src, log: log, blocklist: blocklist, claims: claims}
}

// ScanOnce imports every outstanding grab whose torrent has completed. It reads
// the current clients from the source; if either the download client or the
// library is unconfigured, there is nothing to do this tick.
func (im *Importer) ScanOnce(ctx context.Context) error {
	im.mu.Lock()
	defer im.mu.Unlock()

	dl := im.clients.Download()
	target := im.clients.Library()
	if dl == nil || target == nil {
		return nil
	}

	grabs, err := im.openGrabs(ctx)
	if err != nil {
		return fmt.Errorf("list grabs: %w", err)
	}
	if len(grabs) == 0 {
		return nil
	}

	statuses, err := dl.Status(ctx, uniqueHashes(grabs)...)
	if err != nil {
		return fmt.Errorf("download status: %w", err)
	}
	byHash := make(map[string]download.Status, len(statuses))
	for _, s := range statuses {
		byHash[strings.ToLower(s.Hash)] = s
	}

	now := time.Now().UTC()
	var failed []failedGrab
	for _, group := range groupByHash(grabs) {
		if ctx.Err() != nil {
			// Settled rows are not retryable next run, so what this scan already
			// failed is remembered before the shutdown completes.
			im.remember(context.WithoutCancel(ctx), failed)
			return ctx.Err() // the rest is retryable next run
		}
		st, ok := byHash[group.hash]
		if !ok {
			for _, g := range group.rows {
				failed = append(failed, im.reconcileMissing(ctx, g, now)...)
			}
			continue
		}
		for _, g := range group.rows {
			if g.MissingSince.Valid {
				// Clear it, so a later absence is measured from itself, not from this one.
				im.setMissingSince(ctx, g.ID, sql.NullString{})
			}
		}
		switch st.State {
		case download.StateError:
			// Failing frees the items back to wanted, with the failure kept in history.
			im.log.Warn("importer: download failed in client", "release", group.rows[0].ReleaseTitle, "hash", group.hash)
			for _, g := range group.rows {
				failed = append(failed, im.failGrab(ctx, g, "the download client reported an error"))
			}
		case download.StateComplete:
			// Deferred rows were already examined; the same bytes won't resolve
			// differently without a human naming the file.
			active := rowsWithStatus(group.rows, statusGrabbed)
			if len(active) == 0 {
				continue
			}
			failed = append(failed, im.importGroup(ctx, target, active, st)...)
		default:
			continue // still downloading / stalled / paused
		}
	}
	im.remember(ctx, failed)
	return nil
}

// grabGroup is one release's rows. A pack is a row per covered episode sharing
// an info hash, and its payload only means anything examined as a whole.
type grabGroup struct {
	hash string
	rows []db.ListGrabsByStatusRow
}

// groupByHash buckets the already-fetched rows, preserving first-seen order so a
// scan's work is still deterministic. Series is part of the key because a group
// is the unit of numbering and attribution both, and one torrent can back grabs
// for two series — a manual grab answers to no eligibility rule.
func groupByHash(grabs []db.ListGrabsByStatusRow) []grabGroup {
	type key struct {
		series int64
		hash   string
	}
	at := make(map[key]int, len(grabs))
	out := make([]grabGroup, 0, len(grabs))
	for _, g := range grabs {
		k := key{g.SeriesID, strings.ToLower(g.InfoHash)}
		i, seen := at[k]
		if !seen {
			i = len(out)
			at[k] = i
			out = append(out, grabGroup{hash: k.hash})
		}
		out[i].rows = append(out[i].rows, g)
	}
	return out
}

// rowsWithStatus filters a group to one status, in item-number order so a pack
// imports front to back.
func rowsWithStatus(rows []db.ListGrabsByStatusRow, status string) []db.ListGrabsByStatusRow {
	out := make([]db.ListGrabsByStatusRow, 0, len(rows))
	for _, g := range rows {
		if g.Status == status {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ItemNumber.Int64 < out[b].ItemNumber.Int64 })
	return out
}

// notify dispatches one event through the current dispatcher, if any.
func (im *Importer) notify(ctx context.Context, ev notify.Event) {
	if d := im.clients.Notify(); d != nil {
		d.Dispatch(ctx, ev)
	}
}

// failedGrab is one row this scan settled as failed, held until the scan can
// group them by the release they came from.
type failedGrab struct {
	seriesID     int64
	seriesTitle  string
	itemID       int64
	itemNumber   int
	infoHash     string
	releaseTitle string
	reason       string
}

// remember reports and records one entry per failed release, not per failed
// row: a batch is a row per episode, and recording each walked the whole ladder
// in one incident — 24h, 7d, permanent — on a release that had failed once
// (#124). The grab_failed notification groups the same way: one per incident.
func (im *Importer) remember(ctx context.Context, failed []failedGrab) {
	if len(failed) == 0 {
		return
	}
	// Keyed like the blocklist: the hash we derived at grab time, or the title
	// when an indexer omitted one.
	type release struct {
		seriesID int64
		ident    string
	}
	order := make([]release, 0, len(failed))
	byRelease := make(map[release][]failedGrab, len(failed))
	for _, f := range failed {
		ident := strings.ToLower(f.infoHash)
		if ident == "" {
			ident = f.releaseTitle
		}
		key := release{seriesID: f.seriesID, ident: ident}
		if _, seen := byRelease[key]; !seen {
			order = append(order, key)
		}
		byRelease[key] = append(byRelease[key], f)
	}

	for _, key := range order {
		rows := byRelease[key]
		item := 0
		if len(rows) == 1 {
			item = rows[0].itemNumber
		}
		im.notify(ctx, notify.Event{
			Kind:         notify.KindGrabFailed,
			SeriesTitle:  rows[0].seriesTitle,
			ItemNumber:   item,
			ReleaseTitle: rows[0].releaseTitle,
			Error:        rows[0].reason,
		})
		if im.blocklist == nil {
			continue
		}
		items := make([]int64, 0, len(rows))
		for _, r := range rows {
			items = append(items, r.itemID)
		}
		im.record(ctx, rows[0], items)
	}
}

// record writes one release's failure memory and, unless the breaker refused it,
// puts the series back at the front of the search queue.
func (im *Importer) record(ctx context.Context, f failedGrab, itemIDs []int64) {
	recorded, err := im.blocklist.Record(ctx, f.seriesID, itemIDs, f.infoHash, f.releaseTitle, f.reason)
	if err != nil {
		im.log.Error("importer: record blocklist entry", "release", f.releaseTitle, "err", err)
		return
	}
	// A suppressed record means the breaker blamed the environment, and
	// re-fronting the series would only tighten a loop around the same fault.
	if !recorded {
		return
	}
	// A failure is new information, so the series is searched again promptly with
	// the next-best release rather than sitting behind accumulated backoff.
	if err := im.store.Q.ResetSeriesSearchState(ctx, f.seriesID); err != nil {
		im.log.Error("importer: reset series search state", "series", f.seriesID, "err", err)
	}
}

// openGrabs returns the grabs still worth scanning: downloading, plus deferred
// ones, which are never re-imported but still need reconciliation.
func (im *Importer) openGrabs(ctx context.Context) ([]db.ListGrabsByStatusRow, error) {
	grabbed, err := im.store.Q.ListGrabsByStatus(ctx, statusGrabbed)
	if err != nil {
		return nil, err
	}
	deferred, err := im.store.Q.ListGrabsByStatus(ctx, statusDeferred)
	if err != nil {
		return nil, err
	}
	return append(grabbed, deferred...), nil
}

// reconcileMissing fails a grab whose torrent the client has stopped reporting
// for longer than missingGracePeriod; a single absence is only recorded. It
// returns what it failed, empty when the grab is only being watched.
func (im *Importer) reconcileMissing(ctx context.Context, g db.ListGrabsByStatusRow, now time.Time) []failedGrab {
	if !g.MissingSince.Valid {
		im.log.Info("importer: download not reported by client; watching", "release", g.ReleaseTitle, "hash", g.InfoHash)
		im.setMissingSince(ctx, g.ID, sql.NullString{String: store.FormatTimestamp(now), Valid: true})
		return nil
	}

	since, err := store.ParseTimestamp(g.MissingSince.String)
	if err != nil {
		// A value we cannot parse must not fail the grab, so wait another full period.
		im.log.Warn("importer: missing_since could not be parsed; setting it to now", "hash", g.InfoHash, "value", g.MissingSince.String, "err", err)
		im.setMissingSince(ctx, g.ID, sql.NullString{String: store.FormatTimestamp(now), Valid: true})
		return nil
	}
	if now.Sub(since) < missingGracePeriod {
		return nil
	}

	im.log.Warn("importer: download gone from client; failing grab",
		"release", g.ReleaseTitle, "hash", g.InfoHash, "missing_for", now.Sub(since).Round(time.Second))
	return []failedGrab{im.failGrab(ctx, g, "the download vanished from the client")}
}

// failGrab settles one grab as failed and reports it for the scan to remember
// (#118). Settling before recording is load-bearing: a grab left in "grabbed"
// would never free its item.
func (im *Importer) failGrab(ctx context.Context, g db.ListGrabsByStatusRow, reason string) failedGrab {
	im.settle(ctx, g, statusFailed, reason)
	return failedGrab{
		seriesID:     g.SeriesID,
		seriesTitle:  g.SeriesTitle,
		itemID:       g.WantedItemID,
		itemNumber:   int(g.ItemNumber.Int64),
		infoHash:     g.InfoHash,
		releaseTitle: g.ReleaseTitle,
		reason:       reason,
	}
}

func (im *Importer) setMissingSince(ctx context.Context, id int64, v sql.NullString) {
	if err := im.store.Q.SetGrabMissingSince(ctx, db.SetGrabMissingSinceParams{MissingSince: v, ID: id}); err != nil {
		im.log.Error("importer: set grab missing_since", "err", err)
	}
}

// importGroup walks one completed release's payload and imports it file by file.
// It returns what it failed, for the scan to remember as one incident.
func (im *Importer) importGroup(ctx context.Context, target library.Target, active []db.ListGrabsByStatusRow, st download.Status) []failedGrab {
	if _, err := os.Stat(st.ContentPath); err != nil {
		// Source not reachable from here — commonly a path-mapping gap when the
		// client runs elsewhere. Leave the rows grabbed and retry next tick.
		im.log.Warn("importer: source not accessible", "hash", st.Hash, "path", st.ContentPath, "err", err)
		im.setLastErrors(ctx, active, "source not accessible: "+err.Error())
		return nil
	}
	p, err := collectPayloadFiles(st.ContentPath)
	if err != nil {
		im.log.Warn("importer: payload could not be walked", "hash", st.Hash, "path", st.ContentPath, "err", err)
		im.setLastErrors(ctx, active, "payload could not be read: "+err.Error())
		return nil
	}
	if len(p.files) == 0 {
		// No unpacker ships, so only a human extracting it moves this on.
		for _, g := range active {
			im.settle(ctx, g, statusDeferred, noVideoReason(p.archives))
		}
		return nil
	}
	failed, _ := im.settleGroup(ctx, target, active, p, nil)
	return failed
}

// settleGroup places what the mapping matched and settles every row it did not,
// shared by the scan and the manual retry (which supplies overrides). It reports
// the reason it settled each row by, which only the event table otherwise keeps.
func (im *Importer) settleGroup(ctx context.Context, target library.Target, active []db.ListGrabsByStatusRow, p payload, overrides map[string]int) ([]failedGrab, map[int64]string) {
	covers := make(map[int]bool, len(active))
	for _, g := range active {
		covers[int(g.ItemNumber.Int64)] = true
	}
	res := mapFiles(p.files, covers, overrides)

	imported := make(map[int]string, len(active))
	touched := make(map[int64]bool, len(active))
	stopped := false
	for _, g := range active {
		if ctx.Err() != nil {
			stopped = true
			break
		}
		c, ok := res.assigned[int(g.ItemNumber.Int64)]
		if !ok {
			continue
		}
		touched[g.ID] = true
		final, err := im.place(ctx, target, c.path, g)
		if err != nil {
			continue // stays grabbed; the remaining rows re-form a smaller group next tick
		}
		imported[int(g.ItemNumber.Int64)] = final
	}

	leftovers := res.leftovers
	if !stopped {
		leftovers = im.placeUnclaimed(ctx, target, active[0], leftovers, imported)
	}
	im.notifyImported(ctx, active[0], imported)
	if stopped {
		return nil, nil
	}

	var failed []failedGrab
	details := make(map[int64]string, len(active))
	settle := func(g db.ListGrabsByStatusRow, detail string) {
		im.settle(ctx, g, statusDeferred, detail)
		details[g.ID] = detail
	}
	for _, g := range active {
		if touched[g.ID] {
			continue
		}
		n := int(g.ItemNumber.Int64)
		if detail, ok := res.conflicts[n]; ok {
			settle(g, detail)
			continue
		}
		// A file nothing could map is fixable by hand; a payload with nothing left
		// in it never will be, so the item goes back to wanted and the sweep
		// self-heals with a single. An unextracted archive still holds the episode,
		// so it counts as something left rather than as an empty payload.
		if len(leftovers) > 0 {
			settle(g, fmt.Sprintf("no file matched episode %d; %d unmatched file(s) in the payload", n, len(leftovers)))
			continue
		}
		if len(p.archives) > 0 {
			settle(g, fmt.Sprintf("no file matched episode %d; %s is still packed, so %s",
				n, archiveSummary(p.archives), extractAdvice(p.archives)))
			continue
		}
		failed = append(failed, im.failGrab(ctx, g, "the payload held no file for this episode"))
	}
	return failed, details
}

// place puts one payload file in the library and settles its grab as imported.
func (im *Importer) place(ctx context.Context, target library.Target, source string, g db.ListGrabsByStatusRow) (string, error) {
	final, err := target.Place(ctx, library.ImportRequest{
		SourcePath: source,
		Title:      domain.Title{Name: g.SeriesTitle, Format: domain.Format(g.SeriesFormat)},
		Item: domain.WantedItem{
			ID:     g.WantedItemID,
			Kind:   domain.WantedKind(g.ItemKind),
			Number: int(g.ItemNumber.Int64),
		},
	})
	if err != nil {
		if ctx.Err() == nil {
			im.log.Warn("importer: place failed", "release", g.ReleaseTitle, "err", err)
			im.setLastError(ctx, g, "import failed: "+err.Error())
		}
		return "", err // transient — retry next tick
	}

	// Past Place is the point of no return: the writes run detached so a shutdown
	// signal cannot leave the placed file still marked grabbed.
	ctx = context.WithoutCancel(ctx)

	// Mark the item had before flipping the grab status. The file is already in the
	// library, so "had" is the true state; if the status write then fails, the grab
	// stays 'grabbed' and retries, and Place is idempotent, so the retry is a no-op
	// rather than an inconsistency.
	if err := im.store.Q.SetWantedItemHave(ctx, db.SetWantedItemHaveParams{Have: 1, ID: g.WantedItemID}); err != nil {
		im.log.Error("importer: set have", "err", err)
		return "", err
	}
	im.settle(ctx, g, statusImported, "")
	im.log.Info("importer: imported", "release", g.ReleaseTitle, "item", int(g.ItemNumber.Int64), "dest", final)
	return final, nil
}

// placeUnclaimed imports payload files for items this release never claimed —
// the release titled 03 that ships 03 and 04 — and returns what is still loose.
// A file is only taken when the item exists, is not had, and carries no
// unsettled grab of its own; anything else is left alone rather than guessed at.
func (im *Importer) placeUnclaimed(ctx context.Context, target library.Target, g db.ListGrabsByStatusRow, leftovers []fileClaim, imported map[int]string) []fileClaim {
	var loose []fileClaim
	for _, lo := range leftovers {
		if lo.number <= 0 {
			loose = append(loose, lo)
			continue
		}
		item, free, err := im.unclaimedItem(ctx, g, lo.number)
		if err != nil {
			loose = append(loose, lo) // no such item: nothing to place it against
			continue
		}
		// Had, or already spoken for: the file is redundant rather than unmatched,
		// so it must not be counted as a loose end that defers another row.
		if !free {
			continue
		}
		if !im.claim([]int64{item.ID}) {
			loose = append(loose, lo) // a grab is in flight for it; don't race a copy
			continue
		}
		// A grab can take the item and release again inside the gap since that read,
		// so re-check under the claim — the same window acquire's AutoGrab closes.
		if _, free, err := im.unclaimedItem(ctx, g, lo.number); err != nil || !free {
			im.release([]int64{item.ID})
			if err != nil {
				loose = append(loose, lo)
			}
			continue
		}
		final, err := im.placeUnclaimedFile(ctx, target, g, item, lo)
		im.release([]int64{item.ID})
		if err != nil {
			loose = append(loose, lo)
			continue
		}
		imported[lo.number] = final
	}
	return loose
}

// unclaimedItem reads the item a loose file claims and reports whether it is free
// to take: not had, and carrying no unsettled grab of its own.
func (im *Importer) unclaimedItem(ctx context.Context, g db.ListGrabsByStatusRow, number int) (db.GetWantedItemByNumberRow, bool, error) {
	item, err := im.store.Q.GetWantedItemByNumber(ctx, db.GetWantedItemByNumberParams{
		SeriesID: g.SeriesID,
		Kind:     g.ItemKind,
		Number:   sql.NullInt64{Int64: int64(number), Valid: true},
	})
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			im.log.Error("importer: look up an item for a payload extra", "item", number, "err", err)
		}
		return item, false, err
	}
	return item, item.Have != 1 && (!item.GrabStatus.Valid || item.GrabStatus.String == statusFailed), nil
}

// placeUnclaimedFile places one such file and writes its grab row after the
// fact, so the item reads as grabbed-and-imported from this release like any other.
func (im *Importer) placeUnclaimedFile(ctx context.Context, target library.Target, g db.ListGrabsByStatusRow, item db.GetWantedItemByNumberRow, lo fileClaim) (string, error) {
	final, err := target.Place(ctx, library.ImportRequest{
		SourcePath: lo.file.path,
		Title:      domain.Title{Name: g.SeriesTitle, Format: domain.Format(g.SeriesFormat)},
		Item:       domain.WantedItem{ID: item.ID, Kind: domain.WantedKind(item.Kind), Number: lo.number},
	})
	if err != nil {
		if ctx.Err() == nil {
			im.log.Warn("importer: place of an unclaimed payload file failed",
				"release", g.ReleaseTitle, "item", lo.number, "err", err)
		}
		return "", err
	}
	ctx = context.WithoutCancel(ctx)
	if err := im.store.Q.SetWantedItemHave(ctx, db.SetWantedItemHaveParams{Have: 1, ID: item.ID}); err != nil {
		im.log.Error("importer: set have for an unclaimed payload file", "err", err)
		return "", err
	}
	if _, err := im.store.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: item.ID, InfoHash: g.InfoHash,
		ReleaseTitle: g.ReleaseTitle, Status: statusImported,
	}); err != nil {
		im.log.Error("importer: record a grab for an unclaimed payload file", "err", err)
		return final, nil
	}
	im.appendEvent(ctx, g, item.ID, lo.number, item.Kind, statusGrabbed, "recovered from another grab's payload")
	im.appendEvent(ctx, g, item.ID, lo.number, item.Kind, statusImported, "")
	im.log.Info("importer: imported an unclaimed payload file",
		"release", g.ReleaseTitle, "item", lo.number, "dest", final)
	return final, nil
}

// notifyImported reports one release's import as one event: a pack landing six
// episodes is one arrival, not six.
func (im *Importer) notifyImported(ctx context.Context, g db.ListGrabsByStatusRow, imported map[int]string) {
	if len(imported) == 0 {
		return
	}
	nums := make([]int, 0, len(imported))
	for n := range imported {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	ev := notify.Event{
		Kind:         notify.KindImported,
		SeriesTitle:  g.SeriesTitle,
		ReleaseTitle: g.ReleaseTitle,
	}
	if len(nums) == 1 {
		ev.ItemNumber, ev.Path = nums[0], imported[nums[0]]
	} else {
		ev.Items, ev.Path = nums, filepath.Dir(imported[nums[0]])
	}
	im.notify(ctx, ev)
}

// claim takes the in-flight claim for ids, reporting whether it got them. A nil
// registry means nothing else can be holding them.
func (im *Importer) claim(ids []int64) bool {
	return im.claims == nil || im.claims.TryClaimItems(ids)
}

func (im *Importer) release(ids []int64) {
	if im.claims != nil {
		im.claims.ReleaseClaims(ids)
	}
}

func (im *Importer) setLastErrors(ctx context.Context, rows []db.ListGrabsByStatusRow, msg string) {
	for _, g := range rows {
		im.setLastError(ctx, g, msg)
	}
}

// setLastError records why this attempt could not import (a status transition
// clears it). Skips the write when the reason is unchanged since last tick.
func (im *Importer) setLastError(ctx context.Context, g db.ListGrabsByStatusRow, msg string) {
	if g.LastError.Valid && g.LastError.String == msg {
		return
	}
	if err := im.store.Q.SetGrabLastError(ctx, db.SetGrabLastErrorParams{
		LastError: sql.NullString{String: msg, Valid: true}, ID: g.ID,
	}); err != nil {
		im.log.Error("importer: set grab last_error", "err", err)
		return
	}
	// Notify only on the no-error -> error transition: one per stuck incident, so
	// an alternating reason cannot flap (last_error clears only when it settles).
	if g.LastError.Valid && g.LastError.String != "" {
		return
	}
	im.notify(ctx, notify.Event{
		Kind:         notify.KindImportStuck,
		SeriesTitle:  g.SeriesTitle,
		ItemNumber:   int(g.ItemNumber.Int64),
		ReleaseTitle: g.ReleaseTitle,
		Error:        msg,
	})
}

// settle writes a grab's terminal status and appends the matching history event.
// The event is best-effort: history must never wedge the pipeline.
func (im *Importer) settle(ctx context.Context, g db.ListGrabsByStatusRow, status, detail string) {
	if err := im.store.Q.SetGrabStatus(ctx, db.SetGrabStatusParams{Status: status, ID: g.ID}); err != nil {
		im.log.Error("importer: set grab status", "status", status, "err", err)
	}
	im.appendEvent(ctx, g, g.WantedItemID, int(g.ItemNumber.Int64), g.ItemKind, status, detail)
}

// appendEvent records one history row against a release. The item is passed
// separately because a payload file can land on an item the release never claimed.
func (im *Importer) appendEvent(ctx context.Context, g db.ListGrabsByStatusRow, itemID int64, number int, kind, event, detail string) {
	if err := im.store.Q.AppendGrabEvent(ctx, db.AppendGrabEventParams{
		SeriesID:     g.SeriesID,
		WantedItemID: itemID,
		ItemNumber:   int64(number),
		ItemKind:     kind,
		InfoHash:     g.InfoHash,
		ReleaseTitle: g.ReleaseTitle,
		Event:        event,
		Detail:       detail,
	}); err != nil {
		im.log.Error("importer: append grab event", "event", event, "err", err)
	}
}

func uniqueHashes(grabs []db.ListGrabsByStatusRow) []string {
	seen := make(map[string]bool, len(grabs))
	var hashes []string
	for _, g := range grabs {
		h := strings.ToLower(g.InfoHash)
		if !seen[h] {
			seen[h] = true
			hashes = append(hashes, h)
		}
	}
	return hashes
}
