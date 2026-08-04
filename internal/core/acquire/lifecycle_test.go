package acquire_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/blocklist"
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

	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{}, discardLogger(), nil)
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

	if err := importer.New(st, reg, discardLogger(), blocklist.New(st, nil), nil).ScanOnce(ctx); err != nil {
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

// The #118 loop, end to end: without failure memory the sweep re-derives the
// same ranking every pass, re-grabs the release that just failed, and hammers
// the indexer and the download client forever. With it, each failure demotes
// one release and the pass degrades to the next best, then to nothing.
func TestFailedGrabDegradesToTheNextBestReleaseThenToNothing(t *testing.T) {
	aired := time.Now().Add(-2 * time.Hour)
	top := indexer.Release{
		Title:       "[TopSubs] Placeholder Saga - 03 [1080p]",
		DownloadURL: "magnet:?xt=urn:btih:top03",
		Seeders:     500,
	}
	next := indexer.Release{
		Title:       "[NextSubs] Placeholder Saga - 03 [1080p]",
		DownloadURL: "magnet:?xt=urn:btih:next03",
		Seeders:     10,
	}
	st := coretest.NewStore(t)
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{top, next}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "failhash", Outcome: download.AddSuccess}}
	reg := clients.New()
	reg.SetIndexer(idx)
	reg.SetDownload(dl)
	reg.SetLibrary(&coretest.FakeLibrary{})

	ctx := context.Background()
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{}, discardLogger(), nil)
	im := importer.New(st, reg, discardLogger(), blocklist.New(st, nil), nil)
	id := seedSweep(t, st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &aired})

	// The client accepts every add and then reports the download as errored.
	dl.Statuses = []download.Status{{Hash: "failhash", State: download.StateError}}

	sweepAndFail := func(pass int) string {
		t.Helper()
		if err := svc.SweepOnce(ctx); err != nil {
			t.Fatalf("pass %d SweepOnce: %v", pass, err)
		}
		if err := im.ScanOnce(ctx); err != nil {
			t.Fatalf("pass %d ScanOnce: %v", pass, err)
		}
		grabs, err := st.Q.ListGrabsBySeries(ctx, id)
		if err != nil {
			t.Fatalf("list grabs: %v", err)
		}
		if len(grabs) != 1 {
			t.Fatalf("pass %d: grabs = %d, want 1", pass, len(grabs))
		}
		if grabs[0].Status != "failed" {
			t.Fatalf("pass %d: grab status = %q, want failed", pass, grabs[0].Status)
		}
		return grabs[0].ReleaseTitle
	}

	if got := sweepAndFail(1); got != top.Title {
		t.Fatalf("first pass grabbed %q, want the top-ranked release", got)
	}
	// The failure is remembered, so the second pass must not re-grab it.
	if got := sweepAndFail(2); got != next.Title {
		t.Fatalf("second pass grabbed %q, want the next-best release %q", got, next.Title)
	}
	if len(dl.Adds) != 2 {
		t.Fatalf("download adds = %d after two passes, want 2", len(dl.Adds))
	}

	// Both releases are now blocklisted: the pass finds nothing and does not fail.
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("third SweepOnce: %v", err)
	}
	if len(dl.Adds) != 2 {
		t.Errorf("download adds = %d after the third pass, want no further adds", len(dl.Adds))
	}
	entries, err := st.Q.ListBlocklistBySeries(ctx, id)
	if err != nil {
		t.Fatalf("list blocklist: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("blocklist entries = %d, want one per failed release", len(entries))
	}
	for _, e := range entries {
		if e.Failures != 1 {
			t.Errorf("entry %q failures = %d, want 1", e.ReleaseTitle, e.Failures)
		}
	}
}
