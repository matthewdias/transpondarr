package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// seedHeldGrab is seedGrab for an item the library already holds: an upgrade in
// flight over the release named by heldTitle.
func seedHeldGrab(t *testing.T, st *store.Store, hash, heldTitle string) (itemID, titleID int64) {
	t.Helper()
	itemID, titleID = seedGrab(t, st, hash)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE wanted_items SET in_library = 1, held_release_title = ? WHERE id = ?`,
		heldTitle, itemID); err != nil {
		t.Fatalf("seed held item: %v", err)
	}
	return itemID, titleID
}

// heldTitleOf reads what the store says holds an item.
func heldTitleOf(t *testing.T, st *store.Store, itemID int64) (int64, string) {
	t.Helper()
	var inLibrary int64
	var title string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT in_library, held_release_title FROM wanted_items WHERE id = ?`, itemID).Scan(&inLibrary, &title); err != nil {
		t.Fatalf("read held state: %v", err)
	}
	return inLibrary, title
}

// completedSource is a finished download for the importer to place.
func completedSource(t *testing.T, hash string) *coretest.FakeDownload {
	t.Helper()
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("upgraded-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: hash, State: download.StateComplete, ContentPath: src},
	}}
}

// An import onto an item we already hold is a replacement, and it is the single
// place held identity is written: the library and the name of what is in it move
// together.
func TestImportOfAHeldItemReplacesAndRecordsTheNewRelease(t *testing.T) {
	st := coretest.NewStore(t)
	itemID, _ := seedHeldGrab(t, st, "abc", "[ExampleSubs] Placeholder Saga - 05 [480p]")
	target := &coretest.FakeLibrary{}

	im := New(st, fakeSource{dl: completedSource(t, "abc"), lib: target}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 1 || !target.Placed[0].Replace {
		t.Fatalf("placed = %+v, want one request replacing the held file", target.Placed)
	}
	inLibrary, held := heldTitleOf(t, st, itemID)
	if inLibrary != 1 || held != "rel" {
		t.Errorf("in_library = %d, held = %q, want the item still in the library and holding the imported release", inLibrary, held)
	}
	if g := grabByHash(t, st, "abc"); g.Status != statusImported {
		t.Errorf("grab status = %q, want imported", g.Status)
	}
}

// An ordinary first import is not a replacement, and records what landed.
func TestFirstImportIsNotAReplacement(t *testing.T) {
	st := coretest.NewStore(t)
	itemID, _ := seedGrab(t, st, "abc")
	target := &coretest.FakeLibrary{}

	im := New(st, fakeSource{dl: completedSource(t, "abc"), lib: target}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 1 || target.Placed[0].Replace {
		t.Fatalf("placed = %+v, want one request that replaces nothing", target.Placed)
	}
	if inLibrary, held := heldTitleOf(t, st, itemID); inLibrary != 1 || held != "rel" {
		t.Errorf("in_library = %d, held = %q, want the item in the library and holding the release that landed", inLibrary, held)
	}
}

// A failed upgrade must not cost us the episode: the item stays had, still
// naming the file on disk, which is what puts it back in the upgrade pool.
func TestFailedUpgradeLeavesTheHeldFileInPlace(t *testing.T) {
	const heldTitle = "[ExampleSubs] Placeholder Saga - 05 [480p]"
	st := coretest.NewStore(t)
	itemID, _ := seedHeldGrab(t, st, "abc", heldTitle)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError},
	}}

	im := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if g := grabByHash(t, st, "abc"); g.Status != statusFailed {
		t.Errorf("grab status = %q, want failed", g.Status)
	}
	inLibrary, held := heldTitleOf(t, st, itemID)
	if inLibrary != 1 || held != heldTitle {
		t.Errorf("in_library = %d, held = %q, want the held file untouched by a failed upgrade", inLibrary, held)
	}
}

// A payload with nothing to place for a held item defers rather than failing it,
// and the deferral leaves the library exactly as it was.
func TestDeferredUpgradeKeepsTheHeldFile(t *testing.T) {
	const heldTitle = "[ExampleSubs] Placeholder Saga - 05 [480p]"
	st := coretest.NewStore(t)
	itemID, titleID := seedHeldGrab(t, st, "abc", heldTitle)
	// A second item on the same release, so no lone-file rule can resolve either.
	other := addItem(t, st, titleID, 6)
	if _, err := st.DB.ExecContext(context.Background(),
		`INSERT INTO grabs (wanted_item_id, info_hash, release_title, status) VALUES (?, 'abc', 'rel', 'grabbed')`,
		other); err != nil {
		t.Fatalf("seed the second grab row: %v", err)
	}

	dir := t.TempDir()
	for _, name := range []string{"mystery-a.mkv", "mystery-b.mkv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}

	im := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	var status string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT status FROM grabs WHERE wanted_item_id = ?`, itemID).Scan(&status); err != nil {
		t.Fatalf("read grab status: %v", err)
	}
	if status != statusDeferred {
		t.Errorf("grab status = %q, want import_deferred", status)
	}
	if inLibrary, held := heldTitleOf(t, st, itemID); inLibrary != 1 || held != heldTitle {
		t.Errorf("in_library = %d, held = %q, want the held file untouched by a deferral", inLibrary, held)
	}
}
