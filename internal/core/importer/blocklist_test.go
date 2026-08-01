package importer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// recorded is one call the importer made to the blocklist.
type recorded struct {
	seriesID     int64
	infoHash     string
	releaseTitle string
	reason       string
}

// noRecorder is the recorder for tests about anything other than blocklisting.
type noRecorder struct{}

func (noRecorder) Record(context.Context, int64, string, string, string) error { return nil }

type fakeRecorder struct {
	calls []recorded
	err   error
}

func (f *fakeRecorder) Record(_ context.Context, seriesID int64, infoHash, releaseTitle, reason string) error {
	f.calls = append(f.calls, recorded{seriesID, infoHash, releaseTitle, reason})
	return f.err
}

// backdateSearchState puts a series behind an accumulated backoff, so a test can
// see the reset a failure is supposed to trigger.
func backdateSearchState(t *testing.T, st *store.Store, seriesID int64) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 4, next_search_at = ? WHERE id = ?`,
		store.FormatTimestamp(time.Now().Add(24*time.Hour)), seriesID,
	); err != nil {
		t.Fatalf("backdate search state: %v", err)
	}
}

func readSearchBackoff(t *testing.T, st *store.Store, seriesID int64) (int64, bool) {
	t.Helper()
	var backoff int64
	var next *string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_backoff, next_search_at FROM series WHERE id = ?`, seriesID).Scan(&backoff, &next); err != nil {
		t.Fatalf("read search state: %v", err)
	}
	return backoff, next != nil
}

// A download the client reports as errored is the release's failure, so it is
// remembered and the series is put back at the front of the search queue.
func TestFailedDownloadRecordsBlocklistEntry(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	backdateSearchState(t, st, seriesID)
	rec := &fakeRecorder{}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("blocklist records = %d, want 1", len(rec.calls))
	}
	got := rec.calls[0]
	if got.seriesID != seriesID || got.infoHash != "abc" || got.releaseTitle != "rel" {
		t.Errorf("recorded %+v, want the failed grab's series, hash and release title", got)
	}
	if got.reason == "" {
		t.Error("recorded an empty reason")
	}
	if grabByHash(t, st, "abc").Status != "failed" {
		t.Error("grab not failed")
	}
	// A failure is new information: retry promptly with the next-best release.
	if backoff, hasNext := readSearchBackoff(t, st, seriesID); backoff != 0 || hasNext {
		t.Errorf("search state = backoff %d, next set %v; want the series reset", backoff, hasNext)
	}
}

// The grace-period path fails a grab for the same reason and must remember it too.
func TestGrabGoneFromClientRecordsBlocklistEntry(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	backdateMissingSince(t, st, "abc", time.Hour)
	rec := &fakeRecorder{}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "zzz", State: download.StateDownloading, ContentPath: "/whatever"},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(rec.calls) != 1 || rec.calls[0].seriesID != seriesID {
		t.Fatalf("blocklist records = %+v, want one for series %d", rec.calls, seriesID)
	}
	if grabByHash(t, st, "abc").Status != "failed" {
		t.Error("grab not failed")
	}
}

// A blocklist write that fails must never wedge the grab in "grabbed", or the
// item is never freed back to wanted.
func TestBlocklistWriteFailureStillFailsTheGrab(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	rec := &fakeRecorder{err: errors.New("store is on fire")}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := grabByHash(t, st, "abc").Status; got != "failed" {
		t.Errorf("status = %q, want failed despite the blocklist write failing", got)
	}
}

// A deferred batch is a settled status that is not a release failure: the bytes
// arrived fine and nothing could pick one file from them. Only failGrab records,
// so this holds by construction — pinned here because a later refactor that
// routed deferral through failGrab would blocklist every batch.
func TestDeferredBatchDoesNotRecordBlocklistEntry(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	rec := &fakeRecorder{}
	dir := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 04 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
	)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if got := grabByHash(t, st, "abc").Status; got != "import_deferred" {
		t.Fatalf("status = %q, want import_deferred", got)
	}
	if len(rec.calls) != 0 {
		t.Errorf("recorded %+v, want nothing for a deferred batch", rec.calls)
	}
}

// An import that merely could not be placed stays grabbed and retries, so it
// must not blocklist a release for what is usually a path-mapping gap.
func TestUnplaceableImportDoesNotRecordBlocklistEntry(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	rec := &fakeRecorder{}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: "/nonexistent/path.mkv"},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("recorded %+v, want nothing for an unreachable source", rec.calls)
	}
	if got := grabByHash(t, st, "abc").Status; got != "grabbed" {
		t.Errorf("status = %q, want grabbed (the attempt retries)", got)
	}
}
