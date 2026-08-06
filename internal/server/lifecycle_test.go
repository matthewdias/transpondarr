package server_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/blocklist"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/importer"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// TestGrabThenImportLifecycle exercises the whole item lifecycle across the two
// halves that were previously only tested in isolation: a grab recorded by the
// HTTP handler (status "grabbed", have=false) is picked up by the importer once
// its download completes, placed in the library, and the item becomes had. The
// importer reads the very registry the server writes to, so this is the real
// wiring, not a reconstructed mapping.
func TestGrabThenImportLifecycle(t *testing.T) {
	const matchURL = "magnet:?xt=urn:btih:0000000000000000000000000000000000000003"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga S1E03 [1080p]", DownloadURL: matchURL, Seeders: 100},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hash3", Outcome: download.AddSuccess}}

	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	// Grab episode 3 through the API (records the grab in "grabbed").
	var grabOut struct {
		InfoHash string `json:"infohash"`
		Items    []int  `json:"items"`
	}
	if code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": matchURL}, &grabOut); code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201", code)
	}

	// The download now completes: report the hash as complete with a real file for
	// the importer to stat and place.
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl.Statuses = []download.Status{{Hash: "hash3", State: download.StateComplete, ContentPath: src}}

	// Run one importer scan over the same registry the server uses.
	im := importer.New(h.store, h.reg, discardLogger(), blocklist.New(h.store, nil), nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// The library received exactly the episode-3 file of this series.
	if len(h.lib.Placed) != 1 {
		t.Fatalf("library Place called %d times, want 1", len(h.lib.Placed))
	}
	req := h.lib.Placed[0]
	if req.Item.Number != 3 || req.Title.Name != "Placeholder Saga" {
		t.Errorf("placed %+v, want episode 3 of Placeholder Saga", req)
	}
	if req.SourcePath != src {
		t.Errorf("placed source = %q, want %q", req.SourcePath, src)
	}

	// The grab is now imported and the wanted item is had.
	grabs, _ := h.store.Q.ListGrabsBySeries(context.Background(), seriesID)
	if len(grabs) != 1 || grabs[0].Status != "imported" {
		t.Fatalf("grab status = %+v, want one imported", grabs)
	}
	items, _ := h.store.Q.ListWantedItems(context.Background(), seriesID)
	for _, it := range items {
		if int(it.Number.Int64) == 3 && it.InLibrary != 1 {
			t.Errorf("episode 3 have = %d, want 1 after import", it.InLibrary)
		}
	}
}

// seriesDetailDTO mirrors the fields of the series detail response asserted on here.
type seriesDetailDTO struct {
	Items []struct {
		Number       int    `json:"number"`
		Status       string `json:"status"`
		ReleaseTitle string `json:"release_title"`
		ImportError  string `json:"import_error"`
	} `json:"items"`
}

// TestVanishedTorrentRevertsItemToWanted checks reconciliation from the API's
// point of view: item status is derived from the grab, not stored.
func TestVanishedTorrentRevertsItemToWanted(t *testing.T) {
	const matchURL = "magnet:?xt=urn:btih:0000000000000000000000000000000000000007"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga S1E07 [1080p]", DownloadURL: matchURL, Seeders: 100},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hash7", Outcome: download.AddSuccess}}

	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	if code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": matchURL}, nil); code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201", code)
	}

	if got := itemStatus(t, h, seriesID, 7); got != "downloading" {
		t.Fatalf("episode 7 status = %q, want downloading right after the grab", got)
	}

	// Removed in the client, with the absence already past the grace period.
	dl.Statuses = nil
	grabs, _ := h.store.Q.ListGrabsBySeries(context.Background(), seriesID)
	if len(grabs) != 1 {
		t.Fatalf("got %d grabs, want 1", len(grabs))
	}
	if err := h.store.Q.SetGrabMissingSince(context.Background(), db.SetGrabMissingSinceParams{
		MissingSince: sql.NullString{String: store.FormatTimestamp(time.Now().Add(-time.Hour)), Valid: true},
		ID:           grabs[0].ID,
	}); err != nil {
		t.Fatalf("stamp missing_since: %v", err)
	}

	if err := importer.New(h.store, h.reg, discardLogger(), blocklist.New(h.store, nil), nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if got := itemStatus(t, h, seriesID, 7); got != "wanted" {
		t.Errorf("episode 7 status = %q, want wanted after the torrent vanished", got)
	}
	grabs, _ = h.store.Q.ListGrabsBySeries(context.Background(), seriesID)
	if grabs[0].Status != "failed" {
		t.Errorf("grab status = %q, want failed", grabs[0].Status)
	}
}

// TestAmbiguousPayloadShowsDeferred: a grab settled as import_deferred must not
// present as "downloading" forever — the detail endpoint reports it distinctly.
func TestAmbiguousPayloadShowsDeferred(t *testing.T) {
	const matchURL = "magnet:?xt=urn:btih:0000000000000000000000000000000000000009"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga S1E09 [1080p]", DownloadURL: matchURL, Seeders: 100},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hash9", Outcome: download.AddSuccess}}

	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	if code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": matchURL}, nil); code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201", code)
	}

	// The download completes with two files claiming episode 9 and nothing to
	// tell them apart, so the importer defers rather than guessing.
	dir := t.TempDir()
	for _, name := range []string{
		"[ExampleSubs] Placeholder Saga - 09 [1080p].mkv",
		"[OtherSubs] Placeholder Saga - 09 [720p].mkv",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dl.Statuses = []download.Status{{Hash: "hash9", State: download.StateComplete, ContentPath: dir}}

	if err := importer.New(h.store, h.reg, discardLogger(), blocklist.New(h.store, nil), nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if got := itemStatus(t, h, seriesID, 9); got != "deferred" {
		t.Errorf("episode 9 status = %q, want deferred after a batch payload", got)
	}
}

// TestRegrabReplacesDeferredGrab pins the way out of "deferred" the UI offers:
// the item still matches a search, grabbing a single-episode release replaces
// the deferred grab, and the new download imports normally.
func TestRegrabReplacesDeferredGrab(t *testing.T) {
	const batchURL = "magnet:?xt=urn:btih:000000000000000000000000000000000000000a"
	const singleURL = "magnet:?xt=urn:btih:000000000000000000000000000000000000000b"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga S1E09 [1080p]", DownloadURL: batchURL, Seeders: 100},
		{Title: "[OtherSubs] Placeholder Saga S1E09 [1080p]", DownloadURL: singleURL, Seeders: 50},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hashA", Outcome: download.AddSuccess}}

	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	// First grab lands a payload nothing can disambiguate; the importer defers it.
	if code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": batchURL}, nil); code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201", code)
	}
	dir := t.TempDir()
	for _, name := range []string{
		"[ExampleSubs] Placeholder Saga - 09 [1080p].mkv",
		"[OtherSubs] Placeholder Saga - 09 [720p].mkv",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dl.Statuses = []download.Status{{Hash: "hashA", State: download.StateComplete, ContentPath: dir}}
	if err := importer.New(h.store, h.reg, discardLogger(), blocklist.New(h.store, nil), nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := itemStatus(t, h, seriesID, 9); got != "deferred" {
		t.Fatalf("episode 9 status = %q, want deferred before the re-grab", got)
	}

	// The deferred item still matches a search, and grabbing the alternative
	// release replaces the deferred grab rather than stacking a second one.
	dl.Result = download.AddResult{Hash: "hashB", Outcome: download.AddSuccess}
	if code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": singleURL}, nil); code != http.StatusCreated {
		t.Fatalf("re-grab status = %d, want 201", code)
	}
	if got := itemStatus(t, h, seriesID, 9); got != "downloading" {
		t.Errorf("episode 9 status = %q, want downloading after the re-grab", got)
	}
	grabs, _ := h.store.Q.ListGrabsBySeries(context.Background(), seriesID)
	if len(grabs) != 1 || grabs[0].InfoHash != "hashB" || grabs[0].Status != "grabbed" {
		t.Fatalf("grabs = %+v, want one grabbed row for hashB", grabs)
	}

	// The replacement download completes as a single file and imports normally.
	src := filepath.Join(t.TempDir(), "[OtherSubs] Placeholder Saga - 09 [1080p].mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl.Statuses = []download.Status{{Hash: "hashB", State: download.StateComplete, ContentPath: src}}
	if err := importer.New(h.store, h.reg, discardLogger(), blocklist.New(h.store, nil), nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if got := itemStatus(t, h, seriesID, 9); got != "in_library" {
		t.Errorf("episode 9 status = %q, want in_library after the replacement import", got)
	}
	if len(h.lib.Placed) != 1 || h.lib.Placed[0].SourcePath != src {
		t.Errorf("library placed %+v, want exactly the replacement file", h.lib.Placed)
	}
}

// TestStuckImportShowsReason (#37): an import failing on source access must
// surface as a distinct "stuck" status with the reason — not blend into
// "downloading" — and recover to "in_library" once the path works.
func TestStuckImportShowsReason(t *testing.T) {
	const matchURL = "magnet:?xt=urn:btih:000000000000000000000000000000000000000c"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga S1E04 [1080p]", DownloadURL: matchURL, Seeders: 100},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hashC", Outcome: download.AddSuccess}}

	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	if code := h.postJSON(t, fmt.Sprintf("/api/v1/series/%d/grab", seriesID),
		map[string]any{"download_url": matchURL}, nil); code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201", code)
	}

	// Complete, but at a path Transpondarr cannot see (a path-mapping gap).
	dl.Statuses = []download.Status{{Hash: "hashC", State: download.StateComplete,
		ContentPath: filepath.Join(t.TempDir(), "unmapped", "raw.mkv")}}
	if err := importer.New(h.store, h.reg, discardLogger(), blocklist.New(h.store, nil), nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	var out seriesDetailDTO
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d", seriesID), &out); code != http.StatusOK {
		t.Fatalf("GET series detail = %d, want 200", code)
	}
	found := false
	for _, it := range out.Items {
		if it.Number != 4 {
			continue
		}
		found = true
		if it.Status != "stuck" {
			t.Errorf("episode 4 status = %q, want stuck while the import cannot proceed", it.Status)
		}
		if it.ImportError == "" {
			t.Error("episode 4 import_error is empty, want the recorded reason")
		}
	}
	if !found {
		t.Fatal("episode 4 not in series detail")
	}

	// History records lifecycle moments, not live trouble: an import failure
	// stays out of it (#111), owned by the stuck status and the queue instead.
	var hist struct {
		Events []struct {
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"events"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d/grabs", seriesID), &hist); code != http.StatusOK {
		t.Fatalf("GET grabs = %d, want 200", code)
	}
	if len(hist.Events) != 1 || hist.Events[0].Status != "grabbed" {
		t.Errorf("grab events = %+v, want only the grabbed event while stuck", hist.Events)
	}

	// The path-mapping gap is fixed: the same scan now imports and the reason clears.
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl.Statuses = []download.Status{{Hash: "hashC", State: download.StateComplete, ContentPath: src}}
	if err := importer.New(h.store, h.reg, discardLogger(), blocklist.New(h.store, nil), nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if got := itemStatus(t, h, seriesID, 4); got != "in_library" {
		t.Errorf("episode 4 status = %q, want in_library after the path is reachable", got)
	}
	grabs, _ := h.store.Q.ListGrabsBySeries(context.Background(), seriesID)
	if len(grabs) != 1 || grabs[0].LastError.Valid {
		t.Errorf("grabs = %+v, want one row with last_error cleared", grabs)
	}
}

// TestImportErrorOnlyReportedWhileStuck: import_error is part of the stuck
// contract, so a status that is not stuck must never carry one. Since #97 an
// item that is had with an open grab reads as an upgrade in flight, which is
// what this shape -- a failed status write after a successful Place -- now
// looks like from outside.
func TestImportErrorOnlyReportedWhileStuck(t *testing.T) {
	h := newHarness(t, &coretest.FakeIndexer{}, &coretest.FakeDownload{})
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 12)

	ctx := context.Background()
	items, err := h.store.Q.ListWantedItems(ctx, seriesID)
	if err != nil {
		t.Fatalf("list wanted items: %v", err)
	}
	var itemID int64
	for _, it := range items {
		if it.Number.Int64 == 4 {
			itemID = it.ID
		}
	}
	grab, err := h.store.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: itemID, InfoHash: "hashD", ReleaseTitle: "rel", Status: "grabbed",
	})
	if err != nil {
		t.Fatalf("upsert grab: %v", err)
	}
	if err := h.store.Q.SetGrabLastError(ctx, db.SetGrabLastErrorParams{
		LastError: sql.NullString{String: "import failed: disk full", Valid: true}, ID: grab.ID,
	}); err != nil {
		t.Fatalf("set last_error: %v", err)
	}
	if err := h.store.Q.SetWantedItemInLibrary(ctx, db.SetWantedItemInLibraryParams{InLibrary: 1, ID: itemID}); err != nil {
		t.Fatalf("set have: %v", err)
	}

	var out seriesDetailDTO
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d", seriesID), &out); code != http.StatusOK {
		t.Fatalf("GET series detail = %d, want 200", code)
	}
	for _, it := range out.Items {
		if it.Number != 4 {
			continue
		}
		if it.Status != "downloading" {
			t.Errorf("episode 4 status = %q, want downloading", it.Status)
		}
		if it.ImportError != "" {
			t.Errorf("episode 4 import_error = %q, want empty on a status that is not stuck", it.ImportError)
		}
	}
}

// itemStatus reads one episode's derived status off the series detail endpoint.
func itemStatus(t *testing.T, h *harness, seriesID int64, number int) string {
	t.Helper()
	var out seriesDetailDTO
	if code := h.get(t, fmt.Sprintf("/api/v1/series/%d", seriesID), &out); code != http.StatusOK {
		t.Fatalf("GET series detail = %d, want 200", code)
	}
	for _, it := range out.Items {
		if it.Number == number {
			return it.Status
		}
	}
	t.Fatalf("episode %d not in series detail", number)
	return ""
}
