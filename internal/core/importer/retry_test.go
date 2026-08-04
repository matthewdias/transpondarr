package importer

import (
	"context"
	"errors"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// deferOne runs a scan over a payload that leaves one item deferred, and returns
// the importer plus that row's grab id — the state every retry test starts from.
func deferOne(t *testing.T, st *store.Store, dir string, items int) (*Importer, *coretest.FakeLibrary, int64) {
	t.Helper()
	seedBatchGrab(t, st, "abc", items)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	target := &coretest.FakeLibrary{}
	im := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows, err := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	for _, g := range rows {
		if g.Status == "import_deferred" {
			return im, target, g.ID
		}
	}
	t.Fatal("no row was deferred by the scan")
	return nil, nil, 0
}

// The read behind the dialog: every payload file with what its name parsed to,
// so a human can see why nothing mapped it.
func TestListPayloadReportsFilesAndItems(t *testing.T) {
	st := coretest.NewStore(t)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"b1946ac92492d2347c6235b4d2611184.mkv",
	)
	im, _, grabID := deferOne(t, st, dir, 2)

	info, err := im.ListPayload(context.Background(), grabID)
	if err != nil {
		t.Fatalf("ListPayload: %v", err)
	}
	if info.InfoHash != "abc" || info.ReleaseTitle == "" {
		t.Errorf("info = %+v, want the release identified", info)
	}
	if len(info.Items) != 2 {
		t.Errorf("items = %+v, want every row of the release", info.Items)
	}
	if len(info.Files) != 2 {
		t.Fatalf("files = %+v, want both payload files", info.Files)
	}
	for _, f := range info.Files {
		if f.Path == "b1946ac92492d2347c6235b4d2611184.mkv" && f.EpisodeStart != 0 {
			t.Errorf("file %+v, want no episode read from an unreadable name", f)
		}
	}
}

// A row that is not awaiting a fix has no payload question to answer.
func TestListPayloadRefusesANonDeferredGrab(t *testing.T) {
	st := coretest.NewStore(t)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"b1946ac92492d2347c6235b4d2611184.mkv",
	)
	im, _, _ := deferOne(t, st, dir, 2)
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	var imported int64
	for _, g := range rows {
		if g.Status == "imported" {
			imported = g.ID
		}
	}

	if _, err := im.ListPayload(context.Background(), imported); !errors.Is(err, ErrNotDeferred) {
		t.Errorf("err = %v, want ErrNotDeferred", err)
	}
	if _, err := im.ListPayload(context.Background(), 9999); !errors.Is(err, ErrGrabNotFound) {
		t.Errorf("err = %v, want ErrGrabNotFound for an unknown id", err)
	}
}

// A torrent the client no longer reports has no payload to walk, and the retry
// must say so rather than reporting an empty file list.
func TestListPayloadReportsAVanishedPayload(t *testing.T) {
	st := coretest.NewStore(t)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"b1946ac92492d2347c6235b4d2611184.mkv",
	)
	seedBatchGrab(t, st, "abc", 2)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	im := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows, _ := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	var deferred int64
	for _, g := range rows {
		if g.Status == "import_deferred" {
			deferred = g.ID
		}
	}

	dl.Statuses = nil // the client forgot the torrent
	if _, err := im.ListPayload(context.Background(), deferred); !errors.Is(err, ErrPayloadGone) {
		t.Errorf("err = %v, want ErrPayloadGone", err)
	}
}

// The escape hatch itself: naming the file settles the deferred row imported,
// with the item marked had and the history event appended.
func TestRetryImportWithAnAssignmentImports(t *testing.T) {
	st := coretest.NewStore(t)
	ctx := context.Background()
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"b1946ac92492d2347c6235b4d2611184.mkv",
	)
	im, target, grabID := deferOne(t, st, dir, 2)

	results, err := im.RetryImport(ctx, grabID, map[string]int{
		"b1946ac92492d2347c6235b4d2611184.mkv": 2,
	})
	if err != nil {
		t.Fatalf("RetryImport: %v", err)
	}
	if len(results) != 1 || results[0].ItemNumber != 2 || results[0].Outcome != "imported" {
		t.Fatalf("results = %+v, want episode 2 imported", results)
	}
	if len(target.Placed) != 2 {
		t.Errorf("Place called %d times, want the retry to add one", len(target.Placed))
	}
	rows, _ := st.Q.ListGrabsByInfoHash(ctx, "abc")
	for _, g := range rows {
		if g.Status != "imported" {
			t.Errorf("item %d = %q, want imported", g.ItemNumber.Int64, g.Status)
		}
	}
	var haveAll = true
	items, _ := st.Q.ListWantedItems(ctx, rows[0].SeriesID)
	for _, it := range items {
		if it.Have != 1 {
			haveAll = false
		}
	}
	if !haveAll {
		t.Errorf("items = %+v, want every one marked had", items)
	}
}

// A retry with no assignments re-runs the automatic mapping — the way out when
// the payload itself changed (a rename, an unpack) rather than the parse.
func TestRetryImportWithoutAssignmentsRemaps(t *testing.T) {
	st := coretest.NewStore(t)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"b1946ac92492d2347c6235b4d2611184.mkv",
	)
	im, _, grabID := deferOne(t, st, dir, 2)

	results, err := im.RetryImport(context.Background(), grabID, nil)
	if err != nil {
		t.Fatalf("RetryImport: %v", err)
	}
	// Nothing changed about the payload, so the same answer comes back — and the
	// row stays settled rather than being left open.
	if len(results) != 1 || results[0].Outcome != "import_deferred" {
		t.Errorf("results = %+v, want the row still deferred", results)
	}
}

// Assignment validation refuses the whole retry rather than half-applying it.
func TestRetryImportRejectsInvalidAssignments(t *testing.T) {
	st := coretest.NewStore(t)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"b1946ac92492d2347c6235b4d2611184.mkv",
	)
	im, target, grabID := deferOne(t, st, dir, 2)
	placedBefore := len(target.Placed)

	tests := map[string]map[string]int{
		"a file not in the payload":      {"somewhere-else.mkv": 2},
		"an episode already had":         {"b1946ac92492d2347c6235b4d2611184.mkv": 1},
		"an episode this series has not": {"b1946ac92492d2347c6235b4d2611184.mkv": 99},
	}
	for name, assignments := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := im.RetryImport(context.Background(), grabID, assignments); !errors.Is(err, ErrBadAssignment) {
				t.Errorf("err = %v, want ErrBadAssignment", err)
			}
		})
	}
	if len(target.Placed) != placedBefore {
		t.Errorf("Place called during a refused retry: %d -> %d", placedBefore, len(target.Placed))
	}
}

// Two files assigned to one episode would silently drop one of them.
func TestRetryImportRejectsDuplicateAssignments(t *testing.T) {
	st := coretest.NewStore(t)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"b1946ac92492d2347c6235b4d2611184.mkv",
		"c1946ac92492d2347c6235b4d2611184.mkv",
	)
	im, _, grabID := deferOne(t, st, dir, 2)

	_, err := im.RetryImport(context.Background(), grabID, map[string]int{
		"b1946ac92492d2347c6235b4d2611184.mkv": 2,
		"c1946ac92492d2347c6235b4d2611184.mkv": 2,
	})
	if !errors.Is(err, ErrBadAssignment) {
		t.Errorf("err = %v, want ErrBadAssignment", err)
	}
}

// Only a settled row reopens: a row still downloading under the same release
// stays the scan's business, so a retry never disturbs it.
func TestRetryImportLeavesGrabbedRowsAlone(t *testing.T) {
	st := coretest.NewStore(t)
	ctx := context.Background()
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"b1946ac92492d2347c6235b4d2611184.mkv",
	)
	im, _, grabID := deferOne(t, st, dir, 2)

	// A third item joins the release after the fact, still downloading.
	rows, _ := st.Q.ListGrabsByInfoHash(ctx, "abc")
	third := addItem(t, st, rows[0].SeriesID, 3)
	if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: third, InfoHash: "abc", ReleaseTitle: rows[0].ReleaseTitle, Status: "grabbed",
	}); err != nil {
		t.Fatal(err)
	}

	results, err := im.RetryImport(ctx, grabID, map[string]int{
		"b1946ac92492d2347c6235b4d2611184.mkv": 2,
	})
	if err != nil {
		t.Fatalf("RetryImport: %v", err)
	}
	for _, r := range results {
		if r.ItemNumber == 3 {
			t.Errorf("results = %+v, want the still-downloading row untouched", results)
		}
	}
	rows, _ = st.Q.ListGrabsByInfoHash(ctx, "abc")
	for _, g := range rows {
		if g.ItemNumber.Int64 == 3 && g.Status != "grabbed" {
			t.Errorf("item 3 = %q, want still grabbed", g.Status)
		}
	}
}
