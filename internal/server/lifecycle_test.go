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

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/importer"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
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
	im := importer.New(h.store, h.reg, discardLogger(), time.Second)
	im.ScanOnce(context.Background())

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
		if int(it.Number.Int64) == 3 && it.Have != 1 {
			t.Errorf("episode 3 have = %d, want 1 after import", it.Have)
		}
	}
}

// seriesDetailDTO mirrors the fields of the series detail response asserted on here.
type seriesDetailDTO struct {
	Items []struct {
		Number       int    `json:"number"`
		Status       string `json:"status"`
		ReleaseTitle string `json:"release_title"`
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
		MissingSince: sql.NullString{String: time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05"), Valid: true},
		ID:           grabs[0].ID,
	}); err != nil {
		t.Fatalf("stamp missing_since: %v", err)
	}

	importer.New(h.store, h.reg, discardLogger(), time.Second).ScanOnce(context.Background())

	if got := itemStatus(t, h, seriesID, 7); got != "wanted" {
		t.Errorf("episode 7 status = %q, want wanted after the torrent vanished", got)
	}
	grabs, _ = h.store.Q.ListGrabsBySeries(context.Background(), seriesID)
	if grabs[0].Status != "failed" {
		t.Errorf("grab status = %q, want failed", grabs[0].Status)
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
