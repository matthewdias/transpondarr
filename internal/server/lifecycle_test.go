package server_test

import (
	"context"
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
