package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// Errors the HTTP layer maps to status codes without huma leaking into core.
var (
	ErrGrabNotFound  = errors.New("importer: no such grab")
	ErrNotDeferred   = errors.New("importer: grab is not awaiting an import fix")
	ErrPayloadGone   = errors.New("importer: payload no longer available")
	ErrNoClient      = errors.New("importer: no download client or library configured")
	ErrBadAssignment = errors.New("importer: invalid assignment")
)

// PayloadFile is one video file found in a deferred grab's payload, with what
// its name parsed to, so a human can see why nothing mapped it.
type PayloadFile struct {
	Path            string // payload-relative; the identity an assignment names
	EpisodeStart    int
	EpisodeEnd      int
	AbsoluteEpisode int
	Batch           bool
	Version         int
	Repack          bool
	SuggestedItem   int // what the automatic mapping would claim; 0 when nothing
}

// PayloadItem is one grab row sharing the release, so the dialog can offer the
// episodes still unfilled.
type PayloadItem struct {
	GrabID     int64
	ItemNumber int
	Status     string
}

// PayloadInfo is a deferred release's payload as the retry dialog sees it.
type PayloadInfo struct {
	ReleaseTitle string
	InfoHash     string
	Items        []PayloadItem
	Files        []PayloadFile
}

// RetryResult is one row's outcome from a retry.
type RetryResult struct {
	ItemNumber int
	Outcome    string // imported, deferred, failed, or unchanged
	Detail     string
}

// ListPayload reports what a deferred grab's payload actually holds. It is the
// read behind the retry dialog: the scan already decided it could not map these
// files, so the only way forward is a human looking at them.
func (im *Importer) ListPayload(ctx context.Context, grabID int64) (PayloadInfo, error) {
	im.mu.Lock()
	defer im.mu.Unlock()

	group, st, err := im.deferredGroup(ctx, grabID)
	if err != nil {
		return PayloadInfo{}, err
	}
	files, err := collectPayloadFiles(st.ContentPath)
	if err != nil {
		return PayloadInfo{}, fmt.Errorf("%w: %w", ErrPayloadGone, err)
	}

	covers := make(map[int]bool, len(group))
	info := PayloadInfo{ReleaseTitle: group[0].ReleaseTitle, InfoHash: group[0].InfoHash}
	for _, g := range group {
		info.Items = append(info.Items, PayloadItem{
			GrabID: g.ID, ItemNumber: int(g.ItemNumber.Int64), Status: g.Status,
		})
		if g.Status == statusDeferred {
			covers[int(g.ItemNumber.Int64)] = true
		}
	}
	// Suggestions come from the same mapper the retry will run, so the dialog
	// preselects what an automatic re-map would do rather than a second opinion.
	res := mapFiles(files, covers, nil)
	suggested := make(map[string]int, len(res.assigned))
	for n, c := range res.assigned {
		suggested[c.rel] = n
	}
	for _, f := range files {
		info.Files = append(info.Files, PayloadFile{
			Path:            f.rel,
			EpisodeStart:    f.parsed.EpisodeStart,
			EpisodeEnd:      f.parsed.EpisodeEnd,
			AbsoluteEpisode: f.parsed.AbsoluteEpisode,
			Batch:           f.parsed.Batch,
			Version:         f.parsed.Version,
			Repack:          f.parsed.Repack,
			SuggestedItem:   suggested[f.rel],
		})
	}
	return info, nil
}

// RetryImport re-runs the mapping over a deferred release's deferred rows, with
// optional file-to-item assignments naming what a filename could not. It is the
// only way a deferral reopens: the scan never re-walks settled bytes.
func (im *Importer) RetryImport(ctx context.Context, grabID int64, assignments map[string]int) ([]RetryResult, error) {
	im.mu.Lock()
	defer im.mu.Unlock()

	group, st, err := im.deferredGroup(ctx, grabID)
	if err != nil {
		return nil, err
	}
	target := im.clients.Library()
	if target == nil {
		return nil, ErrNoClient
	}
	files, err := collectPayloadFiles(st.ContentPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPayloadGone, err)
	}

	// Grabbed rows stay the scan's business; a retry only reopens what settled.
	deferred := rowsWithStatus(group, statusDeferred)
	if err := im.validateAssignments(ctx, deferred, files, assignments); err != nil {
		return nil, err
	}
	im.remember(ctx, im.settleGroup(ctx, target, deferred, files, assignments))

	results := make([]RetryResult, 0, len(deferred))
	for _, g := range deferred {
		row, err := im.store.Q.GetGrabByID(ctx, g.ID)
		if err != nil {
			return nil, fmt.Errorf("read back grab %d: %w", g.ID, err)
		}
		outcome := row.Status
		if outcome == statusGrabbed {
			// Left open by a Place that failed; the scan picks it up on its own.
			outcome = "unchanged"
		}
		results = append(results, RetryResult{
			ItemNumber: int(row.ItemNumber.Int64),
			Outcome:    outcome,
			Detail:     row.LastError.String,
		})
	}
	return results, nil
}

// deferredGroup resolves a grab id to its whole release, insisting the target
// row is the one actually awaiting a fix and that the payload is still there.
func (im *Importer) deferredGroup(ctx context.Context, grabID int64) ([]db.ListGrabsByStatusRow, download.Status, error) {
	var none download.Status
	dl := im.clients.Download()
	if dl == nil || im.clients.Library() == nil {
		return nil, none, ErrNoClient
	}
	row, err := im.store.Q.GetGrabByID(ctx, grabID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, none, ErrGrabNotFound
	}
	if err != nil {
		return nil, none, fmt.Errorf("load grab %d: %w", grabID, err)
	}
	if row.Status != statusDeferred {
		return nil, none, fmt.Errorf("%w: it is %q", ErrNotDeferred, row.Status)
	}

	rows, err := im.store.Q.ListGrabsByInfoHash(ctx, row.InfoHash)
	if err != nil {
		return nil, none, fmt.Errorf("load release %q: %w", row.InfoHash, err)
	}
	group := make([]db.ListGrabsByStatusRow, 0, len(rows))
	for _, r := range rows {
		group = append(group, db.ListGrabsByStatusRow(r))
	}

	statuses, err := dl.Status(ctx, strings.ToLower(row.InfoHash))
	if err != nil {
		return nil, none, fmt.Errorf("%w: %w", ErrPayloadGone, err)
	}
	for _, s := range statuses {
		if !strings.EqualFold(s.Hash, row.InfoHash) {
			continue
		}
		if _, err := os.Stat(s.ContentPath); err != nil {
			return nil, none, fmt.Errorf("%w: %w", ErrPayloadGone, err)
		}
		return group, s, nil
	}
	return nil, none, fmt.Errorf("%w: the client no longer reports this torrent", ErrPayloadGone)
}

// validateAssignments refuses a retry that could not be carried out, rather than
// half-applying it: an unknown file, a duplicated item, or an item this release
// does not cover and the out-of-coverage guard would refuse anyway.
func (im *Importer) validateAssignments(ctx context.Context, deferred []db.ListGrabsByStatusRow, files []candidate, assignments map[string]int) error {
	if len(assignments) == 0 {
		return nil
	}
	if len(deferred) == 0 {
		return fmt.Errorf("%w: this release has nothing awaiting an import fix", ErrBadAssignment)
	}
	known := make(map[string]bool, len(files))
	for _, f := range files {
		known[f.rel] = true
	}
	covers := make(map[int]bool, len(deferred))
	for _, g := range deferred {
		covers[int(g.ItemNumber.Int64)] = true
	}
	taken := make(map[int]string, len(assignments))
	for path, n := range assignments {
		if !known[filepath.ToSlash(path)] {
			return fmt.Errorf("%w: %q is not in this payload", ErrBadAssignment, path)
		}
		if n <= 0 {
			return fmt.Errorf("%w: %q was assigned episode %d", ErrBadAssignment, path, n)
		}
		if other, dup := taken[n]; dup {
			return fmt.Errorf("%w: %q and %q were both assigned episode %d", ErrBadAssignment, other, path, n)
		}
		taken[n] = path
		if covers[n] {
			continue
		}
		if err := im.assignableOutsideRelease(ctx, deferred[0], n); err != nil {
			return err
		}
	}
	return nil
}

// assignableOutsideRelease applies the out-of-coverage guard up front, so a
// retry naming an episode this release never claimed is refused with a reason
// rather than silently doing nothing.
func (im *Importer) assignableOutsideRelease(ctx context.Context, g db.ListGrabsByStatusRow, number int) error {
	item, err := im.store.Q.GetWantedItemByNumber(ctx, db.GetWantedItemByNumberParams{
		SeriesID: g.SeriesID,
		Kind:     g.ItemKind,
		Number:   sql.NullInt64{Int64: int64(number), Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: this series has no episode %d", ErrBadAssignment, number)
	}
	if err != nil {
		return fmt.Errorf("look up item %d: %w", number, err)
	}
	if item.Have == 1 {
		return fmt.Errorf("%w: episode %d is already in the library", ErrBadAssignment, number)
	}
	if item.GrabStatus.Valid && item.GrabStatus.String != statusFailed {
		return fmt.Errorf("%w: episode %d has a grab of its own", ErrBadAssignment, number)
	}
	return nil
}
