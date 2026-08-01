// Package importer is the final pipeline stage: it watches the download client
// for grabs Transpondarr initiated (rows in the grabs table) and, once a torrent
// completes, hands the file to a library.Target and marks the item had.
//
// It only ever imports our own grabs — every torrent it touches has an
// authoritative info_hash -> wanted_item mapping, so it never has to identify an
// arbitrary release. Adopting externally-added torrents is a later phase (it
// needs the deferred identification layer).
//
// Every grab status but "grabbed" is settled. A directory payload is resolved to
// a single episode file at completion time, so "import_deferred" means the
// payload was examined and no one file could be chosen — a real batch, or a
// payload nothing could disambiguate — and nothing re-walks the same bytes on a
// later tick. Deferred grabs stay in the scan for missing-from-client
// reconciliation, so a vanished payload still frees its item.
package importer

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/library"
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

// ClientSource supplies the current download client and library target. It is
// read on every scan so a runtime settings change (which swaps the clients) is
// picked up on the next poll without restarting the importer.
type ClientSource interface {
	Download() download.Client
	Library() library.Target
}

// Recorder remembers a release that failed, so the sweep stops re-deriving it
// (#118). Narrow on purpose: the importer needs no more of blocklist.Service.
// It reports whether it recorded — false means too many distinct items are
// failing at once for the release to be the cause (#120).
type Recorder interface {
	Record(ctx context.Context, seriesID int64, itemIDs []int64, infoHash, releaseTitle, reason string) (bool, error)
}

// Importer scans the download client for completed grabs and imports them. It
// runs as a job on the runner, which owns the polling interval.
type Importer struct {
	store     *store.Store
	clients   ClientSource
	log       *slog.Logger
	blocklist Recorder
}

// New builds an Importer. The download client and library target are read from
// src each scan, so either being unconfigured (nil) simply skips that scan.
func New(st *store.Store, src ClientSource, log *slog.Logger, blocklist Recorder) *Importer {
	return &Importer{store: st, clients: src, log: log, blocklist: blocklist}
}

// ScanOnce imports every outstanding grab whose torrent has completed. It reads
// the current clients from the source; if either the download client or the
// library is unconfigured, there is nothing to do this tick.
func (im *Importer) ScanOnce(ctx context.Context) error {
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
	for _, g := range grabs {
		if ctx.Err() != nil {
			// Settled rows are not retryable next run, so what this scan already
			// failed is remembered before the shutdown completes.
			im.remember(context.WithoutCancel(ctx), failed)
			return ctx.Err() // the rest is retryable next run
		}
		st, ok := byHash[strings.ToLower(g.InfoHash)]
		if !ok {
			failed = append(failed, im.reconcileMissing(ctx, g, now)...)
			continue
		}
		if g.MissingSince.Valid {
			// Clear it, so a later absence is measured from itself, not from this one.
			im.setMissingSince(ctx, g.ID, sql.NullString{})
		}
		switch st.State {
		case download.StateError:
			// Failing frees the item back to wanted, with the failure kept in history.
			im.log.Warn("importer: download failed in client", "release", g.ReleaseTitle, "hash", g.InfoHash)
			failed = append(failed, im.failGrab(ctx, g, "the download client reported an error"))
		case download.StateComplete:
			if g.Status == statusDeferred {
				continue // already examined; the same bytes won't resolve differently
			}
			im.importGrab(ctx, target, g, st)
		default:
			continue // still downloading / stalled / paused
		}
	}
	im.remember(ctx, failed)
	return nil
}

// failedGrab is one row this scan settled as failed, held until the scan can
// group them by the release they came from.
type failedGrab struct {
	seriesID     int64
	itemID       int64
	infoHash     string
	releaseTitle string
	reason       string
}

// remember records one blocklist entry per failed *release*, not per failed row.
// A batch is one grab row per covered episode, so recording each separately made
// a single incident walk the whole escalation ladder — 24h, 7d, permanent — and
// blocklist a healthy three-episode release forever on one client hiccup (#124).
// It is also what keeps a call boundary equal to a release boundary on this
// path, matching the sweep's.
func (im *Importer) remember(ctx context.Context, failed []failedGrab) {
	if im.blocklist == nil || len(failed) == 0 {
		return
	}
	// Grab rows carry the hash we derived at grab time; the title is the fallback
	// the blocklist itself falls back to when an indexer omits the hash.
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
	// The reset below is justified by the failure being new information about this
	// release. A suppressed record means the breaker judged it evidence about the
	// environment instead, so re-fronting the series would only tighten a loop
	// around the same fault.
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
	im.setStatus(ctx, g.ID, statusFailed)
	return failedGrab{
		seriesID:     g.SeriesID,
		itemID:       g.WantedItemID,
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

// importGrab places a single completed grab's file and marks the item had.
func (im *Importer) importGrab(ctx context.Context, target library.Target, g db.ListGrabsByStatusRow, st download.Status) {
	info, err := os.Stat(st.ContentPath)
	if err != nil {
		// Source not reachable from here — commonly a path-mapping gap when the
		// client runs elsewhere. Leave it grabbed and retry next tick.
		im.log.Warn("importer: source not accessible", "hash", g.InfoHash, "path", st.ContentPath, "err", err)
		im.setLastError(ctx, g, "source not accessible: "+err.Error())
		return
	}

	// A directory is not a batch by itself: most hold one episode plus extra files.
	source := st.ContentPath
	if info.IsDir() {
		source, err = resolvePayloadFile(st.ContentPath, int(g.ItemNumber.Int64))
		if err != nil {
			im.log.Info("importer: cannot resolve a single episode from payload; deferring",
				"release", g.ReleaseTitle, "path", st.ContentPath, "reason", err)
			im.setStatus(ctx, g.ID, statusDeferred)
			return
		}
		im.log.Info("importer: resolved folder-wrapped episode", "release", g.ReleaseTitle, "file", source)
	}

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
		return // transient — retry next tick
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
		return
	}
	im.setStatus(ctx, g.ID, statusImported)
	im.log.Info("importer: imported", "release", g.ReleaseTitle, "item", int(g.ItemNumber.Int64), "dest", final)
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
	}
}

func (im *Importer) setStatus(ctx context.Context, id int64, status string) {
	if err := im.store.Q.SetGrabStatus(ctx, db.SetGrabStatusParams{Status: status, ID: id}); err != nil {
		im.log.Error("importer: set grab status", "status", status, "err", err)
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
