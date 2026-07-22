// Package importer is the final pipeline stage: it watches the download client
// for grabs Transpondarr initiated (rows in the grabs table) and, once a torrent
// completes, hands the file to a library.Target and marks the item had.
//
// It only ever imports our own grabs — every torrent it touches has an
// authoritative info_hash -> wanted_item mapping, so it never has to identify an
// arbitrary release. Adopting externally-added torrents is a later phase (it
// needs the deferred identification layer).
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
	statusDeferred = "import_deferred" // completed, but its payload holds no single identifiable episode (batch)
	statusFailed   = "failed"          // the download errored or vanished in the client (terminal)
)

// missingGracePeriod is how long a grabbed torrent may be absent from the
// download client's report before the grab is failed and the item becomes
// wanted (and so re-grabbable) again.
//
// A torrent the client no longer knows about is simply omitted from its
// response, which is indistinguishable from a torrent it has not finished
// loading yet — so absence is only acted on once it has persisted. An
// unreachable client is a different case entirely: Status returns an error and
// the whole scan is skipped, so nothing is stamped. That leaves the window to
// cover a client that answers before its torrent list is fully back (a restart,
// a session reset). Five minutes is ~20 polls at the 15s interval: far longer
// than any restart, far shorter than a user's patience with a dead
// "downloading".
const missingGracePeriod = 5 * time.Minute

// missingSinceLayout matches SQLite's datetime('now') output (UTC, no zone),
// which is the format grabs.missing_since is stored in.
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

// Run polls until ctx is cancelled. It scans once immediately, then every interval.
func (im *Importer) Run(ctx context.Context) {
	im.ScanOnce(ctx)
	t := time.NewTicker(im.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			im.ScanOnce(ctx)
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
			// Not currently reported by the client (removed out-of-band, or the
			// client has not finished loading its torrent list). Reconcile against
			// the grace window rather than failing on a single absence.
			im.reconcileMissing(ctx, g, now)
			continue
		}
		if g.MissingSince.Valid {
			// Reported again: forget the absence entirely, so a client that came
			// back does not carry a half-spent grace window into its next blip.
			im.setMissingSince(ctx, g.ID, sql.NullString{})
		}
		switch st.State {
		case download.StateError:
			// The download errored in the client (error / missing files). Mark the
			// grab failed so the item stops showing "downloading" forever, becomes
			// grabbable again, and the failure is visible in history. This applies to
			// a deferred grab too: its payload is gone, so there is nothing left to
			// import by hand and the item is better off wanted again.
			im.log.Warn("importer: download failed in client", "release", g.ReleaseTitle, "hash", g.InfoHash)
			im.setStatus(ctx, g.ID, statusFailed)
		case download.StateComplete:
			if g.Status == statusDeferred {
				// Deferred grabs ride along only for reconciliation (above): their
				// payload was already examined and found unresolvable, and the same
				// bytes will not resolve differently on a later tick.
				continue
			}
			im.importGrab(ctx, target, g, st)
		default:
			continue // still downloading / stalled / paused
		}
	}
}

// openGrabs returns every grab whose fate is not yet settled: those still
// downloading, plus those deferred at import.
//
// A deferred grab is not re-imported — the payload was examined once and found
// to hold no single identifiable episode — but it is still an outstanding
// torrent in the client, so it must keep participating in missing-from-client
// reconciliation. Otherwise a deferred grab whose torrent is later removed stays
// deferred forever and its item shows "downloading" with no path back to wanted.
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

// reconcileMissing handles a grabbed torrent the download client no longer
// reports. The first absence is only recorded (missing_since); the grab is
// failed once the absence has outlived the grace period, at which point the item
// reverts to wanted — re-searchable and re-grabbable — with the failure visible
// in the grabs history.
func (im *Importer) reconcileMissing(ctx context.Context, g db.ListGrabsByStatusRow, now time.Time) {
	if !g.MissingSince.Valid {
		im.log.Info("importer: download not reported by client; watching", "release", g.ReleaseTitle, "hash", g.InfoHash)
		im.setMissingSince(ctx, g.ID, sql.NullString{String: now.Format(missingSinceLayout), Valid: true})
		return
	}

	since, err := time.Parse(missingSinceLayout, g.MissingSince.String)
	if err != nil {
		// An unreadable stamp would otherwise decide the grab's fate; restamp so
		// the grace window is measured from something we can reason about.
		im.log.Warn("importer: unreadable missing_since; restamping", "hash", g.InfoHash, "value", g.MissingSince.String, "err", err)
		im.setMissingSince(ctx, g.ID, sql.NullString{String: now.Format(missingSinceLayout), Valid: true})
		return
	}
	if now.Sub(since) < missingGracePeriod {
		return // still inside the grace window
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

	// A directory payload is usually still one episode, wrapped with subtitles, an
	// nfo, a sample or a screenshots folder. Resolve it down to that one file so
	// the library target keeps receiving a file; only a payload that genuinely
	// holds more than one episode is deferred.
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
