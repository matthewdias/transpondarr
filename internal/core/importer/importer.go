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

// missingSinceLayout is SQLite's datetime('now') format, which missing_since is stored in.
const missingSinceLayout = "2006-01-02 15:04:05"

// ClientSource supplies the current download client and library target. It is
// read on every scan so a runtime settings change (which swaps the clients) is
// picked up on the next poll without restarting the importer.
type ClientSource interface {
	Download() download.Client
	Library() library.Target
}

// Importer polls the download client and imports completed grabs.
type Importer struct {
	store    *store.Store
	clients  ClientSource
	log      *slog.Logger
	interval time.Duration
}

// New builds an Importer. interval is how often the download client is polled.
// The download client and library target are read from src each scan, so either
// being unconfigured (nil) simply skips that scan.
func New(st *store.Store, src ClientSource, log *slog.Logger, interval time.Duration) *Importer {
	return &Importer{store: st, clients: src, log: log, interval: interval}
}

// Run scans once immediately, then every interval, and returns once ctx is
// cancelled and any in-flight scan has finished. Callers wait on that return
// before closing the store, under their own deadline.
func (im *Importer) Run(ctx context.Context) {
	// Scans run detached from ctx: cancelling one mid-import would fail the writes
	// after a completed Place, leaving a file in the library still marked grabbed.
	scanCtx := context.WithoutCancel(ctx)
	t := time.NewTicker(im.interval)
	defer t.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		im.ScanOnce(scanCtx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ScanOnce imports every outstanding grab whose torrent has completed. It reads
// the current clients from the source; if either the download client or the
// library is unconfigured, there is nothing to do this tick.
func (im *Importer) ScanOnce(ctx context.Context) {
	dl := im.clients.Download()
	target := im.clients.Library()
	if dl == nil || target == nil {
		return
	}

	grabs, err := im.openGrabs(ctx)
	if err != nil {
		im.log.Error("importer: list grabs", "err", err)
		return
	}
	if len(grabs) == 0 {
		return
	}

	statuses, err := dl.Status(ctx, uniqueHashes(grabs)...)
	if err != nil {
		im.log.Error("importer: status", "err", err)
		return
	}
	byHash := make(map[string]download.Status, len(statuses))
	for _, s := range statuses {
		byHash[strings.ToLower(s.Hash)] = s
	}

	now := time.Now().UTC()
	for _, g := range grabs {
		st, ok := byHash[strings.ToLower(g.InfoHash)]
		if !ok {
			im.reconcileMissing(ctx, g, now)
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
			im.setStatus(ctx, g.ID, statusFailed)
		case download.StateComplete:
			if g.Status == statusDeferred {
				continue // already examined; the same bytes won't resolve differently
			}
			im.importGrab(ctx, target, g, st)
		default:
			continue // still downloading / stalled / paused
		}
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
// for longer than missingGracePeriod; a single absence is only recorded.
func (im *Importer) reconcileMissing(ctx context.Context, g db.ListGrabsByStatusRow, now time.Time) {
	if !g.MissingSince.Valid {
		im.log.Info("importer: download not reported by client; watching", "release", g.ReleaseTitle, "hash", g.InfoHash)
		im.setMissingSince(ctx, g.ID, sql.NullString{String: now.Format(missingSinceLayout), Valid: true})
		return
	}

	since, err := time.Parse(missingSinceLayout, g.MissingSince.String)
	if err != nil {
		// A value we cannot parse must not fail the grab, so wait another full period.
		im.log.Warn("importer: missing_since could not be parsed; setting it to now", "hash", g.InfoHash, "value", g.MissingSince.String, "err", err)
		im.setMissingSince(ctx, g.ID, sql.NullString{String: now.Format(missingSinceLayout), Valid: true})
		return
	}
	if now.Sub(since) < missingGracePeriod {
		return
	}

	im.log.Warn("importer: download gone from client; failing grab",
		"release", g.ReleaseTitle, "hash", g.InfoHash, "missing_for", now.Sub(since).Round(time.Second))
	im.setStatus(ctx, g.ID, statusFailed)
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
		im.log.Warn("importer: place failed", "release", g.ReleaseTitle, "err", err)
		return // transient — retry next tick
	}

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
