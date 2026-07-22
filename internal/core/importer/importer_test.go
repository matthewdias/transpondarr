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

// seedGrab creates a series + one wanted item + a grab.
func seedGrab(t *testing.T, st *store.Store, hash string) (itemID, seriesID int64) {
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
	return item.ID, s.ID
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// grabByHash returns the single grab row for an info hash.
func grabByHash(t *testing.T, st *store.Store, hash string) db.Grab {
	t.Helper()
	rows, err := st.Q.ListGrabsByInfoHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("list grabs by hash: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d grabs for %q, want 1", len(rows), hash)
	}
	return rows[0]
}

// setGrabStatus forces a grab's status, standing in for an earlier scan.
func setGrabStatus(t *testing.T, st *store.Store, hash, status string) {
	t.Helper()
	if err := st.Q.SetGrabStatus(context.Background(), db.SetGrabStatusParams{
		Status: status, ID: grabByHash(t, st, hash).ID,
	}); err != nil {
		t.Fatalf("set grab status: %v", err)
	}
}

// stampMissingSince backdates a grab's missing_since by ago and returns the
// stamp, so a test can assert the window was not restarted.
func stampMissingSince(t *testing.T, st *store.Store, hash string, ago time.Duration) string {
	t.Helper()
	stamp := time.Now().UTC().Add(-ago).Format("2006-01-02 15:04:05")
	if err := st.Q.SetGrabMissingSince(context.Background(), db.SetGrabMissingSinceParams{
		MissingSince: sql.NullString{String: stamp, Valid: true},
		ID:           grabByHash(t, st, hash).ID,
	}); err != nil {
		t.Fatalf("stamp missing_since: %v", err)
	}
	return stamp
}

// --- tests ------------------------------------------------------------------

func TestImportsCompletedGrab(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")

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
	items, _ := st.Q.ListWantedItems(context.Background(), seriesID)
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

// TestImportsFolderWrappedEpisode: a directory payload is not automatically a batch.
func TestImportsFolderWrappedEpisode(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	dir := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].nfo",
		"Subs/[ExampleSubs] Placeholder Saga - 05 [1080p].en.srt",
		"Sample/placeholder-saga-05-sample.mkv",
	)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	if len(target.Placed) != 1 {
		t.Fatalf("Place called %d times, want 1", len(target.Placed))
	}
	if got := filepath.Base(target.Placed[0].SourcePath); got != "[ExampleSubs] Placeholder Saga - 05 [1080p].mkv" {
		t.Errorf("placed %q, want the episode file inside the folder", got)
	}
	if grabByHash(t, st, "abc").Status != "imported" {
		t.Errorf("status = %q, want imported", grabByHash(t, st, "abc").Status)
	}
	items, _ := st.Q.ListWantedItems(context.Background(), seriesID)
	if items[0].Have != 1 {
		t.Errorf("have = %d, want 1", items[0].Have)
	}
}

// TestDefersMultiEpisodeDirectory: importing the one matching file out of a real
// batch would mark the grab imported and drop the rest of the pack.
func TestDefersMultiEpisodeDirectory(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dir := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 04 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 06 [1080p].mkv",
	)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	if len(target.Placed) != 0 {
		t.Error("a multi-episode payload should not be placed")
	}
	if g := grabByHash(t, st, "abc"); g.Status != "import_deferred" {
		t.Errorf("status = %q, want import_deferred", g.Status)
	}
}

// TestDoesNotReexamineDeferredGrab pins import_deferred as terminal for import.
func TestDoesNotReexamineDeferredGrab(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	setGrabStatus(t, st, "abc", "import_deferred")
	// A payload that would resolve, to prove the skip is about the status.
	dir := writeTree(t, "[ExampleSubs] Placeholder Saga - 05 [1080p].mkv")

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	if len(target.Placed) != 0 {
		t.Error("a deferred grab should not be re-imported")
	}
	if g := grabByHash(t, st, "abc"); g.Status != "import_deferred" {
		t.Errorf("status = %q, want still import_deferred", g.Status)
	}
}

// TestFailsDeferredGrabWhenAbsenceOutlivesGracePeriod: a deferred grab is still
// an outstanding torrent, so the same reconciliation must reach it.
func TestFailsDeferredGrabWhenAbsenceOutlivesGracePeriod(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	setGrabStatus(t, st, "abc", "import_deferred")
	stampMissingSince(t, st, "abc", time.Hour)

	dl := &coretest.FakeDownload{} // client reports nothing at all
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	if g := grabByHash(t, st, "abc"); g.Status != "failed" {
		t.Errorf("status = %q, want failed once the deferred grab's torrent was gone for the grace period", g.Status)
	}
}

// TestWatchesDeferredGrabOnFirstAbsence is the deferred counterpart of the watch path.
func TestWatchesDeferredGrabOnFirstAbsence(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	setGrabStatus(t, st, "abc", "import_deferred")

	dl := &coretest.FakeDownload{}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	g := grabByHash(t, st, "abc")
	if g.Status != "import_deferred" {
		t.Errorf("status = %q, want still import_deferred inside the grace period", g.Status)
	}
	if !g.MissingSince.Valid {
		t.Error("missing_since not stamped on a deferred grab's first absence")
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

// TestWatchesGrabOnFirstAbsenceFromClient: one absent scan only stamps
// missing_since, since the client may just be reloading its torrent list.
func TestWatchesGrabOnFirstAbsenceFromClient(t *testing.T) {
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
	g := grabByHash(t, st, "abc")
	if g.Status != "grabbed" {
		t.Errorf("status = %q, want still grabbed (inside the grace period)", g.Status)
	}
	if !g.MissingSince.Valid {
		t.Error("missing_since not stamped on the first absence")
	}
}

// TestKeepsGrabWhileAbsenceIsWithinGracePeriod: the window runs from the first
// absence, so a later scan must not restart it.
func TestKeepsGrabWhileAbsenceIsWithinGracePeriod(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	stamped := stampMissingSince(t, st, "abc", time.Minute)

	dl := &coretest.FakeDownload{} // client reports nothing at all
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	g := grabByHash(t, st, "abc")
	if g.Status != "grabbed" {
		t.Errorf("status = %q, want still grabbed one minute into the grace period", g.Status)
	}
	if g.MissingSince.String != stamped {
		t.Errorf("missing_since = %q, want the original stamp %q (window must not restart)", g.MissingSince.String, stamped)
	}
}

// TestFailsGrabWhenAbsenceOutlivesGracePeriod: a torrent removed out-of-band
// stops being reported, and "failed" is what reads as wanted again in the API.
func TestFailsGrabWhenAbsenceOutlivesGracePeriod(t *testing.T) {
	st := coretest.NewStore(t)
	itemID, seriesID := seedGrab(t, st, "abc")
	stampMissingSince(t, st, "abc", time.Hour)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "zzz", State: download.StateDownloading, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	if len(target.Placed) != 0 {
		t.Error("nothing should be placed for a torrent that vanished from the client")
	}
	g := grabByHash(t, st, "abc")
	if g.Status != "failed" {
		t.Errorf("status = %q, want failed once the absence outlived the grace period", g.Status)
	}
	items, _ := st.Q.ListWantedItems(context.Background(), seriesID)
	for _, it := range items {
		if it.ID == itemID && it.Have != 0 {
			t.Errorf("have = %d, want 0 for a failed grab", it.Have)
		}
	}
}

// TestReappearingHashClearsMissingSince: a client that comes back must recover
// fully, not limp toward a false failure on an already-expired stamp.
func TestReappearingHashClearsMissingSince(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	stampMissingSince(t, st, "abc", time.Hour)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateDownloading, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	New(st, fakeSource{dl: dl, lib: target}, discardLogger(), time.Second).ScanOnce(context.Background())

	g := grabByHash(t, st, "abc")
	if g.Status != "grabbed" {
		t.Errorf("status = %q, want grabbed: the client is reporting the torrent again", g.Status)
	}
	if g.MissingSince.Valid {
		t.Errorf("missing_since = %q, want cleared once the hash reappeared", g.MissingSince.String)
	}
}

// TestLeavesGrabWhenSourceNotAccessible: an unreachable ContentPath (a path-mapping
// gap when the client runs elsewhere) must stay grabbed for a later scan.
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
