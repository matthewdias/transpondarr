package importer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/blocklist"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// recorded is one call the importer made to the blocklist.
type recorded struct {
	titleID      int64
	itemIDs      []int64
	infoHash     string
	releaseTitle string
	reason       string
}

// noRecorder is the recorder for tests about anything other than blocklisting.
type noRecorder struct{}

func (noRecorder) Record(context.Context, int64, []int64, string, string, string) (bool, error) {
	return true, nil
}

type fakeRecorder struct {
	calls []recorded
	err   error
	// suppress models the breaker: the record is refused, not failed.
	suppress bool
}

func (f *fakeRecorder) Record(_ context.Context, titleID int64, itemIDs []int64, infoHash, releaseTitle, reason string) (bool, error) {
	f.calls = append(f.calls, recorded{titleID, itemIDs, infoHash, releaseTitle, reason})
	return !f.suppress, f.err
}

// backdateSearchState puts a title behind an accumulated backoff, so a test can
// see the reset a failure is supposed to trigger.
func backdateSearchState(t *testing.T, st *store.Store, titleID int64) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 4, next_search_at = ? WHERE id = ?`,
		store.FormatTimestamp(time.Now().Add(24*time.Hour)), titleID,
	); err != nil {
		t.Fatalf("backdate search state: %v", err)
	}
}

func readSearchBackoff(t *testing.T, st *store.Store, titleID int64) (int64, bool) {
	t.Helper()
	var backoff int64
	var next *string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_backoff, next_search_at FROM series WHERE id = ?`, titleID).Scan(&backoff, &next); err != nil {
		t.Fatalf("read search state: %v", err)
	}
	return backoff, next != nil
}

// A download the client reports as errored is the release's failure, so it is
// remembered and the title is put back at the front of the search queue.
func TestFailedDownloadRecordsBlocklistEntry(t *testing.T) {
	st := coretest.NewStore(t)
	_, titleID := seedGrab(t, st, "abc")
	backdateSearchState(t, st, titleID)
	rec := &fakeRecorder{}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(rec.calls) != 1 {
		t.Fatalf("blocklist records = %d, want 1", len(rec.calls))
	}
	got := rec.calls[0]
	if got.titleID != titleID || got.infoHash != "abc" || got.releaseTitle != "rel" {
		t.Errorf("recorded %+v, want the failed grab's series, hash and release title", got)
	}
	if got.reason == "" {
		t.Error("recorded an empty reason")
	}
	if grabByHash(t, st, "abc").Status != "failed" {
		t.Error("grab not failed")
	}
	// A failure is new information: retry promptly with the next-best release.
	if backoff, hasNext := readSearchBackoff(t, st, titleID); backoff != 0 || hasNext {
		t.Errorf("search state = backoff %d, next set %v; want the series reset", backoff, hasNext)
	}
}

// Re-fronting the search queue is justified by the failure being news about
// this release. Once the breaker judges it news about the environment instead,
// the reset would only tighten a retry loop around the same fault (#120) -- but
// the item must still be freed, or a fault would strand every grab it touched.
func TestSuppressedRecordLeavesTheSearchQueueAlone(t *testing.T) {
	st := coretest.NewStore(t)
	_, titleID := seedGrab(t, st, "abc")
	backdateSearchState(t, st, titleID)
	rec := &fakeRecorder{suppress: true}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if grabByHash(t, st, "abc").Status != "failed" {
		t.Error("grab not failed: the breaker suppresses the memory, not the lifecycle")
	}
	backoff, hasNext := readSearchBackoff(t, st, titleID)
	if backoff == 0 || !hasNext {
		t.Errorf("search state = backoff %d, next set %v; want the backdated cadence left as it was",
			backoff, hasNext)
	}
}

// seedBatchGrab grabs one release across items, the shape a season batch takes:
// one grab row per covered episode, all sharing an info hash and a title.
func seedBatchGrab(t *testing.T, st *store.Store, hash string, items int) (titleID int64, itemIDs []int64) {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateTitle(ctx, db.CreateTitleParams{
		Title: "Placeholder Saga", Format: "TV", Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	for n := 1; n <= items; n++ {
		item, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID: s.ID, Kind: "episode", Number: sql.NullInt64{Int64: int64(n), Valid: true},
			Monitored: 1,
		})
		if err != nil {
			t.Fatalf("create item %d: %v", n, err)
		}
		if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
			WantedItemID: item.ID, InfoHash: hash,
			ReleaseTitle: "[SynthSubs] Placeholder Saga - 01-03 [Batch]", Status: "grabbed",
		}); err != nil {
			t.Fatalf("upsert grab for item %d: %v", n, err)
		}
		itemIDs = append(itemIDs, item.ID)
	}
	return s.ID, itemIDs
}

// One failure is one step on the ladder, however many episodes the release
// covered. A batch is N grab rows, and recording each separately walked
// 24h -> 7d -> permanent in a single incident, so one transient client hiccup
// blocklisted a healthy 3-episode release forever (#124).
func TestBatchFailingOnceEscalatesOneStep(t *testing.T) {
	st := coretest.NewStore(t)
	titleID, _ := seedBatchGrab(t, st, "abc", 3)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(),
		blocklist.New(st, nil), nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	entries, err := st.Q.ListBlocklistByTitle(context.Background(), titleID)
	if err != nil {
		t.Fatalf("list blocklist: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("blocklist entries = %d, want 1 for one release", len(entries))
	}
	if entries[0].Failures != 1 {
		t.Errorf("failures = %d after one incident, want 1", entries[0].Failures)
	}
	if !entries[0].BlockedUntil.Valid {
		t.Error("blocked_until is NULL: one incident blocked a batch permanently")
	}
}

// regrabBatch puts a failed batch's rows back in flight, so a test can drive a
// second incident against the same release.
func regrabBatch(t *testing.T, st *store.Store, itemIDs []int64, hash string) {
	t.Helper()
	for _, id := range itemIDs {
		if _, err := st.Q.UpsertGrab(context.Background(), db.UpsertGrabParams{
			WantedItemID: id, InfoHash: hash,
			ReleaseTitle: "[SynthSubs] Placeholder Saga - 01-03 [Batch]", Status: "grabbed",
		}); err != nil {
			t.Fatalf("re-grab item %d: %v", id, err)
		}
	}
}

// Aggregating must not cost the ladder its reach: separate incidents still
// escalate, and the third still blocks permanently.
func TestBatchReachesPermanentOverSeparateIncidents(t *testing.T) {
	st := coretest.NewStore(t)
	ctx := context.Background()
	titleID, itemIDs := seedBatchGrab(t, st, "abc", 3)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}
	im := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), blocklist.New(st, nil), nil)

	var last db.ReleaseBlocklist
	for incident := int64(1); incident <= 3; incident++ {
		if incident > 1 {
			regrabBatch(t, st, itemIDs, "abc")
		}
		if err := im.ScanOnce(ctx); err != nil {
			t.Fatalf("scan %d: %v", incident, err)
		}
		entries, err := st.Q.ListBlocklistByTitle(ctx, titleID)
		if err != nil {
			t.Fatalf("list blocklist: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("blocklist entries = %d after incident %d, want 1", len(entries), incident)
		}
		if entries[0].Failures != incident {
			t.Fatalf("failures = %d after incident %d, want %d", entries[0].Failures, incident, incident)
		}
		last = entries[0]
	}
	if last.BlockedUntil.Valid {
		t.Errorf("blocked_until = %q after a third incident, want NULL (permanent)", last.BlockedUntil.String)
	}
}

// The fan-out the breaker exists for, driven through the importer: distinct
// releases failing across enough items still trips it, so aggregating per
// release did not aggregate the evidence away.
func TestDistinctReleasesFailingAcrossItemsStillTripTheBreaker(t *testing.T) {
	st := coretest.NewStore(t)
	ctx := context.Background()
	s, err := st.Q.CreateTitle(ctx, db.CreateTitleParams{
		Title: "Placeholder Saga", Format: "TV", Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	var statuses []download.Status
	for n := 1; n <= 6; n++ {
		item, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID: s.ID, Kind: "episode", Number: sql.NullInt64{Int64: int64(n), Valid: true},
			Monitored: 1,
		})
		if err != nil {
			t.Fatalf("create item %d: %v", n, err)
		}
		hash := fmt.Sprintf("hash%02d", n)
		if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
			WantedItemID: item.ID, InfoHash: hash,
			ReleaseTitle: fmt.Sprintf("[SynthSubs] Placeholder Saga - %02d [1080p]", n),
			Status:       "grabbed",
		}); err != nil {
			t.Fatalf("upsert grab %d: %v", n, err)
		}
		statuses = append(statuses, download.Status{Hash: hash, State: download.StateError, ContentPath: "/whatever"})
	}
	svc := blocklist.New(st, nil)
	dl := &coretest.FakeDownload{Statuses: statuses}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), svc, nil).
		ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if !svc.BreakerState().Open {
		t.Fatal("six unrelated releases failing at once left the breaker closed")
	}
	entries, err := st.Q.ListBlocklistByTitle(ctx, s.ID)
	if err != nil {
		t.Fatalf("list blocklist: %v", err)
	}
	if len(entries) != 4 {
		t.Errorf("recorded %d entries, want the 4 written before the breaker opened", len(entries))
	}
}

// The breaker half of #124: a batch wide enough to reach the threshold on its
// own must still be remembered. It holds because the breaker credits a release
// once, however many calls carry it -- so it does not depend on one Record call
// meaning one release.
func TestWideBatchFailingIsStillRemembered(t *testing.T) {
	st := coretest.NewStore(t)
	titleID, _ := seedBatchGrab(t, st, "abc", 8)
	svc := blocklist.New(st, nil)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), svc, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	entries, err := st.Q.ListBlocklistByTitle(context.Background(), titleID)
	if err != nil {
		t.Fatalf("list blocklist: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("blocklist entries = %d, want the batch remembered once", len(entries))
	}
	if svc.BreakerState().Open {
		t.Error("one release's own breadth opened the breaker")
	}
}

// The grace-period path fails a grab for the same reason and must remember it too.
func TestGrabGoneFromClientRecordsBlocklistEntry(t *testing.T) {
	st := coretest.NewStore(t)
	_, titleID := seedGrab(t, st, "abc")
	backdateMissingSince(t, st, "abc", time.Hour)
	rec := &fakeRecorder{}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "zzz", State: download.StateDownloading, ContentPath: "/whatever"},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(rec.calls) != 1 || rec.calls[0].titleID != titleID {
		t.Fatalf("blocklist records = %+v, want one for series %d", rec.calls, titleID)
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

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got := grabByHash(t, st, "abc").Status; got != "failed" {
		t.Errorf("status = %q, want failed despite the blocklist write failing", got)
	}
}

// A deferral is a settled status that is not a release failure: the bytes
// arrived fine and one file could not be told apart from the rest. Only failGrab
// records, so this holds by construction — pinned here because a later refactor
// routing deferral through failGrab would blocklist every fixable payload.
func TestDeferredBatchDoesNotRecordBlocklistEntry(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	rec := &fakeRecorder{}
	// Two files claim episode 5 and nothing separates them.
	dir := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[OtherGroup] Placeholder Saga - 05 [720p].mkv",
	)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec, nil).
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

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), rec, nil).
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
