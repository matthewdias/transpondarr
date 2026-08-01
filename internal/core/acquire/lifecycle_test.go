package acquire_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/importer"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

// A swept grab is an ordinary grab: the importer sees no difference between one
// the sweep made and one a user clicked, so an unattended episode goes all the
// way to the library.
func TestSweepThenImportLifecycle(t *testing.T) {
	aired := time.Now().Add(-2 * time.Hour)
	st := coretest.NewStore(t)
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{episodeRelease("Placeholder Saga", 3)}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "swepthash", Outcome: download.AddSuccess}}
	lib := &coretest.FakeLibrary{}
	reg := clients.New()
	reg.SetIndexer(idx)
	reg.SetDownload(dl)
	reg.SetLibrary(lib)

	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{}, discardLogger())
	id := seedSweep(t, st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})

	ctx := context.Background()
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	// The download completes with a real file for the importer to place.
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl.Statuses = []download.Status{{Hash: "swepthash", State: download.StateComplete, ContentPath: src}}

	if err := importer.New(st, reg, discardLogger()).ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce: %v", err)
	}

	if len(lib.Placed) != 1 || lib.Placed[0].Item.Number != 3 {
		t.Fatalf("library placed %+v, want episode 3", lib.Placed)
	}
	grabs, _ := st.Q.ListGrabsBySeries(ctx, id)
	if len(grabs) != 1 || grabs[0].Status != "imported" {
		t.Fatalf("grabs = %+v, want one imported", grabs)
	}
	items, _ := st.Q.ListWantedItems(ctx, id)
	for _, it := range items {
		if int(it.Number.Int64) == 3 && it.Have != 1 {
			t.Errorf("episode 3 have = %d, want 1 after the import", it.Have)
		}
	}
}
