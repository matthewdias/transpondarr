package importer

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/library"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// --- fakes ------------------------------------------------------------------

// fakeSource supplies fixed clients to the importer (stands in for the registry).
// The download/library fakes themselves come from coretest.
type fakeSource struct {
	dl  download.Client
	lib library.Target
}

func (f fakeSource) Download() download.Client { return f.dl }
func (f fakeSource) Library() library.Target   { return f.lib }

// --- helpers ----------------------------------------------------------------

// seedGrab creates a series + one wanted item + a grab, returning the item id.
func seedGrab(t *testing.T, st *store.Store, hash string) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: "Placeholder Saga", Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	item, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
		SeriesID: s.ID, Kind: "episode", Number: sql.NullInt64{Int64: 5, Valid: true},
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: item.ID, InfoHash: hash, ReleaseTitle: "rel", Status: "grabbed",
	}); err != nil {
		t.Fatalf("upsert grab: %v", err)
	}
	return item.ID
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// --- tests ------------------------------------------------------------------

func TestImportsCompletedGrab(t *testing.T) {
	st := coretest.NewStore(t)
	itemID := seedGrab(t, st, "abc")

	// A real completed file for the importer to stat and place.
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: src},
	}}
	target := &coretest.FakeLibrary{}
	im := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second)

	im.ScanOnce(context.Background())

	if len(target.Placed) != 1 {
		t.Fatalf("Place called %d times, want 1", len(target.Placed))
	}
	if target.Placed[0].Item.Number != 5 || target.Placed[0].Title.Name != "Placeholder Saga" {
		t.Errorf("ImportRequest = %+v, want item 5 of Placeholder Saga", target.Placed[0])
	}

	// Grab marked imported, item marked had.
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	if len(rows) != 1 || rows[0].Status != "imported" {
		t.Errorf("grab status = %v, want imported", rows)
	}
	items, _ := st.Q.ListWantedItems(context.Background(), rowsSeriesID(t, st, itemID))
	if items[0].Have != 1 {
		t.Errorf("have = %d, want 1", items[0].Have)
	}
}

func TestSkipsIncompleteGrab(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateDownloading, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	if len(target.Placed) != 0 {
		t.Errorf("Place called for an incomplete download")
	}
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	if rows[0].Status != "grabbed" {
		t.Errorf("status = %q, want still grabbed", rows[0].Status)
	}
}

func TestDefersBatchDirectory(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dir := t.TempDir() // a directory content path == batch

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	if len(target.Placed) != 0 {
		t.Error("batch directory should not be placed")
	}
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	if rows[0].Status != "import_deferred" {
		t.Errorf("status = %q, want import_deferred", rows[0].Status)
	}
}

func TestFailsErroredGrab(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	if len(target.Placed) != 0 {
		t.Error("an errored download should not be placed")
	}
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	if rows[0].Status != "failed" {
		t.Errorf("status = %q, want failed", rows[0].Status)
	}
}

// TestLeavesGrabWhenHashAbsentFromClient pins the known grab-state
// reconciliation gap: when a grabbed torrent's hash is not reported by the
// download client (removed out-of-band, or the client was restarted), the
// importer deliberately leaves the grab in "grabbed" and retries next tick
// rather than failing it — an absence can be transient.
//
// This is a characterization test of *current* behavior. The gap is that a
// torrent genuinely deleted in the client strands the grab in "grabbed" forever
// (the item never becomes grabbable again). When reconciliation lands, this
// expectation should flip — a persistently-absent hash should be reconciled, not
// left grabbed indefinitely.
func TestLeavesGrabWhenHashAbsentFromClient(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	// The client reports some other torrent but not "abc".
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "zzz", State: download.StateComplete, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	if len(target.Placed) != 0 {
		t.Error("nothing should be placed when the hash is absent from the client")
	}
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	if rows[0].Status != "grabbed" {
		t.Errorf("status = %q, want still grabbed (left for retry)", rows[0].Status)
	}
}

// TestLeavesGrabWhenSourceNotAccessible covers the other retry branch: the
// download completed but its ContentPath cannot be stat'd from here (commonly a
// path-mapping gap when the client runs on another host). The grab stays
// "grabbed" so a later scan — once the path resolves — can import it.
func TestLeavesGrabWhenSourceNotAccessible(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: filepath.Join(t.TempDir(), "does-not-exist.mkv")},
	}}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	if len(target.Placed) != 0 {
		t.Error("nothing should be placed when the source path is not accessible")
	}
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	if rows[0].Status != "grabbed" {
		t.Errorf("status = %q, want still grabbed (left for retry)", rows[0].Status)
	}
}

// rowsSeriesID fetches the series id for a wanted item (helper for assertions).
func rowsSeriesID(t *testing.T, st *store.Store, itemID int64) int64 {
	t.Helper()
	grabs, err := st.Q.ListGrabsByStatus(context.Background(), "imported")
	if err != nil || len(grabs) == 0 {
		// fall back: not imported yet
		grabs, _ = st.Q.ListGrabsByStatus(context.Background(), "grabbed")
	}
	for _, g := range grabs {
		if g.WantedItemID == itemID {
			return g.SeriesID
		}
	}
	t.Fatalf("series id not found for item %d", itemID)
	return 0
}
