package importer

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/library"
	"github.com/matthewdias/transpondarr/internal/core/notify"
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
	ntf *notify.Dispatcher
}

func (f fakeSource) Download() download.Client  { return f.dl }
func (f fakeSource) Library() library.Target    { return f.lib }
func (f fakeSource) Notify() *notify.Dispatcher { return f.ntf }

// cancelOnPlace cancels the run context the instant a file lands in the library,
// reproducing a SIGTERM landing between Place and the status writes.
type cancelOnPlace struct {
	coretest.FakeLibrary
	cancel context.CancelFunc
}

func (c *cancelOnPlace) Place(ctx context.Context, r library.ImportRequest) (string, error) {
	c.cancel()
	return c.FakeLibrary.Place(ctx, r)
}

// failOnItem refuses one item's Place, standing in for a per-file fault (a
// permission error on one destination) inside an otherwise healthy group.
type failOnItem struct {
	coretest.FakeLibrary
	fail int
}

func (f *failOnItem) Place(ctx context.Context, r library.ImportRequest) (string, error) {
	if r.Item.Number == f.fail {
		f.Placed = append(f.Placed, r)
		return "", errors.New("mkdir /library: permission denied")
	}
	return f.FakeLibrary.Place(ctx, r)
}

// refusingClaims models an item claimed by a grab already in flight.
type refusingClaims struct{}

func (refusingClaims) TryClaimItems([]int64) bool { return false }
func (refusingClaims) ReleaseClaims([]int64)      {}

// racingClaims grants the claim, but only after a grab has taken the item and
// released it again — the gap acquire's AutoGrab documents.
type racingClaims struct {
	st     *store.Store
	itemID int64
}

func (c racingClaims) TryClaimItems([]int64) bool {
	_, _ = c.st.Q.UpsertGrab(context.Background(), db.UpsertGrabParams{
		WantedItemID: c.itemID, InfoHash: "other", ReleaseTitle: "other release", Status: statusGrabbed,
	})
	return true
}
func (racingClaims) ReleaseClaims([]int64) {}

// --- helpers ----------------------------------------------------------------

// seedSeriesGrab creates a named series with one grabbed item on the given hash.
func seedSeriesGrab(t *testing.T, st *store.Store, title, hash string, number int) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: title, Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	item, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
		SeriesID: s.ID, Kind: "episode", Number: sql.NullInt64{Int64: int64(number), Valid: true},
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: item.ID, InfoHash: hash,
		ReleaseTitle: title + " - pack", Status: statusGrabbed,
	}); err != nil {
		t.Fatalf("upsert grab: %v", err)
	}
	return s.ID
}

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

// addItem adds one more wanted item to a series, with no grab of its own.
func addItem(t *testing.T, st *store.Store, seriesID int64, number int) int64 {
	t.Helper()
	item, err := st.Q.CreateWantedItem(context.Background(), db.CreateWantedItemParams{
		SeriesID: seriesID, Kind: "episode", Number: sql.NullInt64{Int64: int64(number), Valid: true},
	})
	if err != nil {
		t.Fatalf("create item %d: %v", number, err)
	}
	return item.ID
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// grabByHash returns the single grab row for an info hash.
func grabByHash(t *testing.T, st *store.Store, hash string) db.ListGrabsByInfoHashRow {
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

// setLastError forces a grab's last_error, standing in for an earlier failed attempt.
func setLastError(t *testing.T, st *store.Store, hash, msg string) {
	t.Helper()
	if err := st.Q.SetGrabLastError(context.Background(), db.SetGrabLastErrorParams{
		LastError: sql.NullString{String: msg, Valid: true}, ID: grabByHash(t, st, hash).ID,
	}); err != nil {
		t.Fatalf("set last_error: %v", err)
	}
}

// backdateMissingSince sets a grab's missing_since to ago in the past and
// returns it, so a test can assert the grace period was not restarted.
func backdateMissingSince(t *testing.T, st *store.Store, hash string, ago time.Duration) string {
	t.Helper()
	value := store.FormatTimestamp(time.Now().Add(-ago))
	if err := st.Q.SetGrabMissingSince(context.Background(), db.SetGrabMissingSinceParams{
		MissingSince: sql.NullString{String: value, Valid: true},
		ID:           grabByHash(t, st, hash).ID,
	}); err != nil {
		t.Fatalf("set missing_since: %v", err)
	}
	return value
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
	im := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

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

// TestFinishesInFlightImportAfterCancel: a signal arriving between Place and
// the status writes must not leave a placed file still marked grabbed.
func TestFinishesInFlightImportAfterCancel(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target := &cancelOnPlace{cancel: cancel}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: src},
	}}

	_ = New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(ctx)

	if g := grabByHash(t, st, "abc"); g.Status != "imported" {
		t.Errorf("status = %q, want imported: the file was already placed when the cancel arrived", g.Status)
	}
	items, _ := st.Q.ListWantedItems(context.Background(), seriesID)
	if items[0].Have != 1 {
		t.Errorf("have = %d, want 1", items[0].Have)
	}
	if n := len(target.Placed); n != 1 {
		t.Errorf("Place called %d times, want 1", n)
	}
}

// TestStopsScanMidwayOnCancel: a cancel mid-scan finishes the grab past its
// Place but must not start placing the next one.
func TestStopsScanMidwayOnCancel(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "aaa")
	seedGrab(t, st, "bbb")
	dir := t.TempDir()
	var statuses []download.Status
	for _, hash := range []string{"aaa", "bbb"} {
		src := filepath.Join(dir, hash+".mkv")
		if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, download.Status{Hash: hash, State: download.StateComplete, ContentPath: src})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target := &cancelOnPlace{cancel: cancel}
	dl := &coretest.FakeDownload{Statuses: statuses}

	err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("ScanOnce() = %v, want context.Canceled: a mid-scan cancel must surface, not read as a clean scan", err)
	}
	if n := len(target.Placed); n != 1 {
		t.Fatalf("Place called %d times, want 1: the second grab must wait for the next run", n)
	}
	byStatus := map[string]int{}
	for _, hash := range []string{"aaa", "bbb"} {
		byStatus[grabByHash(t, st, hash).Status]++
	}
	if byStatus["imported"] != 1 || byStatus["grabbed"] != 1 {
		t.Errorf("statuses = %v, want one imported (past Place) and one still grabbed", byStatus)
	}
}

// TestDoesNotScanAfterCancel: shutdown must not start work it cannot finish.
func TestDoesNotScanAfterCancel(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	_ = New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(ctx)

	if len(target.Placed) != 0 {
		t.Error("ScanOnce placed a file on an already-cancelled context")
	}
	if g := grabByHash(t, st, "abc"); g.Status != "grabbed" {
		t.Errorf("status = %q, want still grabbed (untouched)", g.Status)
	}
}

// TestScanOnceReturnsStatusError: a scan-level failure must surface in the
// return value, so the job runner's LastError can report it.
func TestScanOnceReturnsStatusError(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{StatusErr: errors.New("client unreachable")}
	im := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil)

	err := im.ScanOnce(context.Background())

	if err == nil || !strings.Contains(err.Error(), "client unreachable") {
		t.Errorf("ScanOnce() = %v, want the download client error", err)
	}
}

// TestScanOnceReturnsListGrabsError: the store-side failure path must surface
// in the return value too, not just the download-client one.
func TestScanOnceReturnsListGrabsError(t *testing.T) {
	st := coretest.NewStore(t)
	if err := st.DB.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	dl := &coretest.FakeDownload{}
	im := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil)

	err := im.ScanOnce(context.Background())

	if err == nil || !strings.Contains(err.Error(), "list grabs") {
		t.Errorf("ScanOnce() = %v, want the list-grabs error", err)
	}
}

func TestSkipsIncompleteGrab(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateDownloading, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

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
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

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

// The inverse of the old defer-the-whole-thing rule (#126): a multi-episode
// payload is walked file by file, so the grabbed item gets its own episode. The
// other files belong to items this series does not track, so they are left alone.
func TestImportsItsOwnEpisodeFromAMultiEpisodeDirectory(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	dir := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 04 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 06 [1080p].mkv",
	)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 1 {
		t.Fatalf("Place called %d times, want 1 — only episode 5 is tracked", len(target.Placed))
	}
	if got := filepath.Base(target.Placed[0].SourcePath); got != "[ExampleSubs] Placeholder Saga - 05 [1080p].mkv" {
		t.Errorf("placed %q, want the grabbed episode's own file", got)
	}
	if g := grabByHash(t, st, "abc"); g.Status != "imported" {
		t.Errorf("status = %q, want imported", g.Status)
	}
	items, _ := st.Q.ListWantedItems(context.Background(), seriesID)
	if items[0].Have != 1 {
		t.Errorf("have = %d, want 1", items[0].Have)
	}
}

// The whole point of #126: one grab per covered episode, one payload, N imports.
func TestImportsEveryEpisodeOfASeasonPack(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID, _ := seedBatchGrab(t, st, "abc", 3)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 02 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 03 [1080p].mkv",
	)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 3 {
		t.Fatalf("Place called %d times, want one per episode", len(target.Placed))
	}
	for i, p := range target.Placed {
		if p.Item.Number != i+1 {
			t.Errorf("Place %d took item %d, want %d (front to back)", i, p.Item.Number, i+1)
		}
	}
	rows, err := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	for _, g := range rows {
		if g.Status != "imported" {
			t.Errorf("grab for item %d = %q, want imported", g.ItemNumber.Int64, g.Status)
		}
	}
	items, _ := st.Q.ListWantedItems(context.Background(), seriesID)
	for _, it := range items {
		if it.Have != 1 {
			t.Errorf("item %d have = %d, want 1", it.Number.Int64, it.Have)
		}
	}
	// The pack keeps seeding: a fully-imported release is never removed.
	if len(dl.Removes) != 0 {
		t.Errorf("Remove called %+v, want the torrent left seeding", dl.Removes)
	}
}

// The hybrid split's fixable half: a covered item with no file of its own, but
// files still loose in the payload, defers with the detail a human acts on.
func TestDefersWhenAFileIsMissingButOthersAreLoose(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID, _ := seedBatchGrab(t, st, "abc", 3)
	// Episode 2 ships under a name nothing can read.
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"b1946ac92492d2347c6235b4d2611184.mkv",
		"[SynthSubs] Placeholder Saga - 03 [1080p].mkv",
	)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	rec := &fakeRecorder{}
	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	byStatus := map[string]string{}
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	for _, g := range rows {
		byStatus[g.Status] = g.LastError.String
		if g.ItemNumber.Int64 == 2 && g.Status != "import_deferred" {
			t.Errorf("item 2 = %q, want import_deferred with a file still loose", g.Status)
		}
		if g.ItemNumber.Int64 != 2 && g.Status != "imported" {
			t.Errorf("item %d = %q, want imported", g.ItemNumber.Int64, g.Status)
		}
	}
	// A deferral is not the release's fault, so nothing is blocklisted.
	if len(rec.calls) != 0 {
		t.Errorf("blocklist records = %+v, want none for a deferral", rec.calls)
	}
	items, _ := st.Q.ListWantedItems(context.Background(), seriesID)
	for _, it := range items {
		if want := int64(0); it.Number.Int64 == 2 && it.Have != want {
			t.Errorf("item 2 have = %d, want %d", it.Have, want)
		}
	}
}

// The hybrid split's unfixable half: nothing matched and nothing is left over,
// so the item goes back to wanted and the release is remembered — one incident,
// however many rows it covered.
func TestFailsAndBlocklistsWhenThePayloadHasNothingLeft(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID, _ := seedBatchGrab(t, st, "abc", 3)
	// Only episode 1 is in the payload; 2 and 3 have nothing to fall back on.
	dir := writeTree(t, "[SynthSubs] Placeholder Saga - 01 [1080p].mkv")

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	rec := &fakeRecorder{}
	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	for _, g := range rows {
		want := "failed"
		if g.ItemNumber.Int64 == 1 {
			want = "imported"
		}
		if g.Status != want {
			t.Errorf("item %d = %q, want %q", g.ItemNumber.Int64, g.Status, want)
		}
	}
	if len(rec.calls) != 1 {
		t.Fatalf("blocklist records = %d, want 1 for one release", len(rec.calls))
	}
	if got := len(rec.calls[0].itemIDs); got != 2 {
		t.Errorf("recorded %d items, want the 2 that failed", got)
	}
	if rec.calls[0].seriesID != seriesID {
		t.Errorf("recorded series %d, want %d", rec.calls[0].seriesID, seriesID)
	}
}

// The "titled 03, ships 03 and 04" case: the extra file lands on the item that
// exists and is still wanted, with a grab row written after the fact.
func TestImportsAFileForAnItemTheReleaseNeverClaimed(t *testing.T) {
	st := coretest.NewStore(t)
	ctx := context.Background()
	seriesID, _ := seedBatchGrab(t, st, "abc", 1)
	extra := addItem(t, st, seriesID, 2)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 02 [1080p].mkv",
	)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 2 {
		t.Fatalf("Place called %d times, want the claimed episode and the extra", len(target.Placed))
	}
	items, _ := st.Q.ListWantedItems(ctx, seriesID)
	for _, it := range items {
		if it.Have != 1 {
			t.Errorf("item %d have = %d, want 1", it.Number.Int64, it.Have)
		}
	}
	rows, _ := st.Q.ListGrabsByInfoHash(ctx, "abc")
	if len(rows) != 2 {
		t.Fatalf("grab rows = %d, want one written for the recovered item too", len(rows))
	}
	for _, g := range rows {
		if g.Status != "imported" {
			t.Errorf("item %d = %q, want imported", g.ItemNumber.Int64, g.Status)
		}
	}
	events := seriesEvents(t, st, seriesID)
	var recovered bool
	for _, e := range events {
		if e.WantedItemID == extra && e.Event == "grabbed" && e.Detail != "" {
			recovered = true
		}
	}
	if !recovered {
		t.Errorf("events = %+v, want a grabbed event explaining where the extra item came from", events)
	}
}

// Each guard on that placement, one payload at a time: a had item, an item with
// a grab of its own in flight, and an item this series does not track.
func TestLeavesAnUnclaimedFileAloneWhenTheGuardRefuses(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, st *store.Store, seriesID int64)
	}{
		{"item is already had", func(t *testing.T, st *store.Store, seriesID int64) {
			id := addItem(t, st, seriesID, 2)
			if err := st.Q.SetWantedItemHave(context.Background(), db.SetWantedItemHaveParams{Have: 1, ID: id}); err != nil {
				t.Fatal(err)
			}
		}},
		{"item has a grab of its own", func(t *testing.T, st *store.Store, seriesID int64) {
			id := addItem(t, st, seriesID, 2)
			if _, err := st.Q.UpsertGrab(context.Background(), db.UpsertGrabParams{
				WantedItemID: id, InfoHash: "other", ReleaseTitle: "other release", Status: "grabbed",
			}); err != nil {
				t.Fatal(err)
			}
		}},
		{"item does not exist", func(*testing.T, *store.Store, int64) {}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := coretest.NewStore(t)
			seriesID, _ := seedBatchGrab(t, st, "abc", 1)
			tc.setup(t, st, seriesID)
			dir := writeTree(t,
				"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
				"[SynthSubs] Placeholder Saga - 02 [1080p].mkv",
			)

			dl := &coretest.FakeDownload{Statuses: []download.Status{
				{Hash: "abc", State: download.StateComplete, ContentPath: dir},
			}}
			target := &coretest.FakeLibrary{}
			if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).
				ScanOnce(context.Background()); err != nil {
				t.Fatalf("scan: %v", err)
			}

			if len(target.Placed) != 1 {
				t.Errorf("Place called %d times, want only the claimed episode", len(target.Placed))
			}
		})
	}
}

// A claim held elsewhere means a grab for that item is in flight, and a
// copy-mode Place runs for minutes: yield rather than race it.
func TestLeavesAnUnclaimedFileAloneWhileItsItemIsClaimed(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID, _ := seedBatchGrab(t, st, "abc", 1)
	addItem(t, st, seriesID, 2)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 02 [1080p].mkv",
	)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, refusingClaims{}).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 1 {
		t.Errorf("Place called %d times, want only the claimed episode", len(target.Placed))
	}
}

// The guard reads grab state and then claims, so a grab that takes the item and
// releases inside that gap leaves a stale read the claim alone would pass — and
// the after-the-fact grab row would overwrite an in-flight one.
func TestLeavesAnUnclaimedFileAloneWhenItsItemIsGrabbedUnderTheClaim(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID, _ := seedBatchGrab(t, st, "abc", 1)
	extra := addItem(t, st, seriesID, 2)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 02 [1080p].mkv",
	)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, racingClaims{st: st, itemID: extra}).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 1 {
		t.Errorf("Place called %d times, want only the claimed episode", len(target.Placed))
	}
	grabs, err := st.Q.ListGrabsBySeries(context.Background(), seriesID)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	for _, g := range grabs {
		if g.WantedItemID != extra {
			continue
		}
		if g.Status != statusGrabbed || g.ReleaseTitle != "other release" {
			t.Errorf("the in-flight grab reads %q/%q, want it untouched", g.ReleaseTitle, g.Status)
		}
	}
}

// One torrent can back grabs for two series (a manual grab bypasses eligibility),
// and a group is the unit of both mapping and attribution — so it is per series,
// not per hash, or one series' rows borrow the other's numbering and title.
func TestKeepsTwoSeriesSharingAnInfoHashApart(t *testing.T) {
	st := coretest.NewStore(t)
	seedBatchGrab(t, st, "abc", 1)
	seedSeriesGrab(t, st, "Second Saga", "abc", 2)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[SynthSubs] Second Saga - 02 [1080p].mkv",
	)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	fn := coretest.NewFakeNotifier()
	target := &coretest.FakeLibrary{}
	if err := New(st, notifyingSource(dl, target, fn), discardLogger(), noRecorder{}, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	got := map[string]int{}
	for range 2 {
		ev := waitEvent(t, fn)
		got[ev.SeriesTitle] = ev.ItemNumber
	}
	if got["Placeholder Saga"] != 1 || got["Second Saga"] != 2 {
		t.Errorf("imports reported as %v, want each series its own episode", got)
	}
}

// A Place that fails mid-group leaves its row grabbed; the smaller group that
// re-forms next tick converges without re-importing what already landed.
func TestConvergesAfterAMidGroupPlaceFailure(t *testing.T) {
	st := coretest.NewStore(t)
	seedBatchGrab(t, st, "abc", 3)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 02 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 03 [1080p].mkv",
	)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &failOnItem{fail: 2}
	im := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	for _, g := range rows {
		want := "imported"
		if g.ItemNumber.Int64 == 2 {
			want = "grabbed"
		}
		if g.Status != want {
			t.Errorf("after scan 1, item %d = %q, want %q", g.ItemNumber.Int64, g.Status, want)
		}
	}

	target.fail = 0 // the condition clears
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	rows, _ = st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	for _, g := range rows {
		if g.Status != "imported" {
			t.Errorf("after scan 2, item %d = %q, want imported", g.ItemNumber.Int64, g.Status)
		}
	}
	if n := len(target.Placed); n != 4 {
		t.Errorf("Place called %d times, want 4 — the two that landed are not re-placed", n)
	}
}

// A group whose rows are all settled is skipped entirely: deferred bytes never
// re-resolve on their own.
func TestSkipsAGroupWithNothingStillGrabbed(t *testing.T) {
	st := coretest.NewStore(t)
	seedBatchGrab(t, st, "abc", 2)
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	for _, g := range rows {
		if err := st.Q.SetGrabStatus(context.Background(), db.SetGrabStatusParams{
			Status: "import_deferred", ID: g.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 02 [1080p].mkv",
	)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 0 {
		t.Errorf("Place called %d times, want none for an all-deferred group", len(target.Placed))
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
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

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
	backdateMissingSince(t, st, "abc", time.Hour)

	dl := &coretest.FakeDownload{} // client reports nothing at all
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

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
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != "import_deferred" {
		t.Errorf("status = %q, want still import_deferred inside the grace period", g.Status)
	}
	if !g.MissingSince.Valid {
		t.Error("missing_since not set on a deferred grab's first absence")
	}
}

func TestFailsErroredGrab(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 0 {
		t.Error("an errored download should not be placed")
	}
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	if rows[0].Status != "failed" {
		t.Errorf("status = %q, want failed", rows[0].Status)
	}
}

// TestWatchesGrabOnFirstAbsenceFromClient: one absent scan only records the
// absence, since the client may just be reloading its torrent list.
func TestWatchesGrabOnFirstAbsenceFromClient(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	// The client reports some other torrent but not "abc".
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "zzz", State: download.StateComplete, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 0 {
		t.Error("nothing should be placed when the hash is absent from the client")
	}
	g := grabByHash(t, st, "abc")
	if g.Status != "grabbed" {
		t.Errorf("status = %q, want still grabbed (inside the grace period)", g.Status)
	}
	if !g.MissingSince.Valid {
		t.Error("missing_since not set on the first absence")
	}
}

// TestKeepsGrabWhileAbsenceIsWithinGracePeriod: the grace period runs from the
// first absence, so a later scan must not restart it.
func TestKeepsGrabWhileAbsenceIsWithinGracePeriod(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	firstAbsence := backdateMissingSince(t, st, "abc", time.Minute)

	dl := &coretest.FakeDownload{} // client reports nothing at all
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != "grabbed" {
		t.Errorf("status = %q, want still grabbed one minute into the grace period", g.Status)
	}
	if g.MissingSince.String != firstAbsence {
		t.Errorf("missing_since = %q, want the original %q (grace period must not restart)", g.MissingSince.String, firstAbsence)
	}
}

// TestFailsGrabWhenAbsenceOutlivesGracePeriod: a torrent removed out-of-band
// stops being reported, and "failed" is what reads as wanted again in the API.
func TestFailsGrabWhenAbsenceOutlivesGracePeriod(t *testing.T) {
	st := coretest.NewStore(t)
	itemID, seriesID := seedGrab(t, st, "abc")
	backdateMissingSince(t, st, "abc", time.Hour)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "zzz", State: download.StateDownloading, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

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
// fully, not fail the grab on an absence that has already ended.
func TestReappearingHashClearsMissingSince(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	backdateMissingSince(t, st, "abc", time.Hour)

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateDownloading, ContentPath: "/whatever"},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != "grabbed" {
		t.Errorf("status = %q, want grabbed: the client is reporting the torrent again", g.Status)
	}
	if g.MissingSince.Valid {
		t.Errorf("missing_since = %q, want cleared once the hash reappeared", g.MissingSince.String)
	}
}

// TestLeavesGrabWhenSourceNotAccessible: an unreachable ContentPath (a path-mapping
// gap when the client runs elsewhere) must stay grabbed for a later scan — and
// the reason must be recorded on the grab, not just logged (#37).
func TestLeavesGrabWhenSourceNotAccessible(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: filepath.Join(t.TempDir(), "does-not-exist.mkv")},
	}}
	target := &coretest.FakeLibrary{}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 0 {
		t.Error("nothing should be placed when the source path is not accessible")
	}
	g := grabByHash(t, st, "abc")
	if g.Status != "grabbed" {
		t.Errorf("status = %q, want still grabbed (left for retry)", g.Status)
	}
	if !g.LastError.Valid || !strings.Contains(g.LastError.String, "source not accessible") {
		t.Errorf("last_error = %+v, want a recorded source-not-accessible reason", g.LastError)
	}
}

// TestRecordsLastErrorWhenPlaceFails: a failing library target (permissions,
// disk full) retries forever; the reason must be visible on the grab (#37).
func TestRecordsLastErrorWhenPlaceFails(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: src},
	}}
	target := &coretest.FakeLibrary{DestErr: errors.New("mkdir /library: permission denied")}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != "grabbed" {
		t.Errorf("status = %q, want still grabbed (left for retry)", g.Status)
	}
	if !g.LastError.Valid || !strings.Contains(g.LastError.String, "permission denied") {
		t.Errorf("last_error = %+v, want the Place failure recorded", g.LastError)
	}
}

// TestClearsLastErrorOnSuccessfulImport: once the condition is fixed and the
// import lands, the stale reason must not survive.
func TestClearsLastErrorOnSuccessfulImport(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	setLastError(t, st, "abc", "source not accessible: stat /gone: no such file or directory")
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: src},
	}}
	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != "imported" {
		t.Fatalf("status = %q, want imported", g.Status)
	}
	if g.LastError.Valid {
		t.Errorf("last_error = %q, want cleared on a successful import", g.LastError.String)
	}
}

// TestClearsLastErrorOnRegrab: a fresh download starts with a clean slate, like
// missing_since.
func TestClearsLastErrorOnRegrab(t *testing.T) {
	st := coretest.NewStore(t)
	itemID, _ := seedGrab(t, st, "abc")
	setLastError(t, st, "abc", "import failed: disk full")

	if _, err := st.Q.UpsertGrab(context.Background(), db.UpsertGrabParams{
		WantedItemID: itemID, InfoHash: "def", ReleaseTitle: "rel2", Status: "grabbed",
	}); err != nil {
		t.Fatalf("re-grab: %v", err)
	}
	if g := grabByHash(t, st, "def"); g.LastError.Valid {
		t.Errorf("last_error = %q, want cleared by the re-grab", g.LastError.String)
	}
}
