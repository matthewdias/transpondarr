package importer

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/blocklist"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// TestKeepsStallClockAcrossScans and TestKeepsGrabWhileAbsenceIsWithinGracePeriod
// are not subsumed here: they pin a stamp surviving a scan, not just uniformity.

// seedPack creates one title covered by a pack: an item per number, each with a
// grab row on the same hash, which is the shape groupByHash buckets.
func seedPack(t *testing.T, st *store.Store, hash string, numbers ...int) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateTitle(ctx, db.CreateTitleParams{Title: "Placeholder Saga", Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	for _, n := range numbers {
		item, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID: s.ID, Kind: "episode", Number: sql.NullInt64{Int64: int64(n), Valid: true},
			Monitored: 1,
		})
		if err != nil {
			t.Fatalf("create item %d: %v", n, err)
		}
		if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
			WantedItemID: item.ID, InfoHash: hash, ReleaseTitle: "rel", Status: statusGrabbed,
		}); err != nil {
			t.Fatalf("upsert grab for item %d: %v", n, err)
		}
	}
	return s.ID
}

// packGrabs returns every row on a hash; grabByHash cannot, since it asserts one.
func packGrabs(t *testing.T, st *store.Store, hash string) []db.ListGrabsByInfoHashRow {
	t.Helper()
	rows, err := st.Q.ListGrabsByInfoHash(context.Background(), hash)
	if err != nil {
		t.Fatalf("list grabs by hash: %v", err)
	}
	return rows
}

// grabIDForItem addresses one row of a pack, which is what backdating a single
// row's clock needs in order to model the row a later add wrote.
func grabIDForItem(t *testing.T, st *store.Store, hash string, number int64) int64 {
	t.Helper()
	for _, g := range packGrabs(t, st, hash) {
		if g.ItemNumber.Int64 == number {
			return g.ID
		}
	}
	t.Fatalf("no grab on %q for item %d", hash, number)
	return 0
}

// backdateStalledSinceForItem puts one row of a pack ago in the past.
func backdateStalledSinceForItem(t *testing.T, st *store.Store, hash string, number int64, ago time.Duration) {
	t.Helper()
	if err := st.Q.SetGrabStalledSince(context.Background(), db.SetGrabStalledSinceParams{
		StalledSince: sql.NullString{String: store.FormatTimestamp(time.Now().Add(-ago)), Valid: true},
		ID:           grabIDForItem(t, st, hash, number),
	}); err != nil {
		t.Fatalf("set stalled_since: %v", err)
	}
}

// backdateMissingSinceForItem is backdateStalledSinceForItem for the other clock.
func backdateMissingSinceForItem(t *testing.T, st *store.Store, hash string, number int64, ago time.Duration) {
	t.Helper()
	if err := st.Q.SetGrabMissingSince(context.Background(), db.SetGrabMissingSinceParams{
		MissingSince: sql.NullString{String: store.FormatTimestamp(time.Now().Add(-ago)), Valid: true},
		ID:           grabIDForItem(t, st, hash, number),
	}); err != nil {
		t.Fatalf("set missing_since: %v", err)
	}
}

// assertPackSettled reports any row of a pack the scan left open, which is what
// a per-row clock does to the row written after the others.
func assertPackSettled(t *testing.T, st *store.Store, hash string, want int) {
	t.Helper()
	rows := packGrabs(t, st, hash)
	if len(rows) != want {
		t.Fatalf("got %d grabs on %q, want %d", len(rows), hash, want)
	}
	for _, g := range rows {
		if g.Status != statusFailed {
			t.Errorf("item %d grab status = %q, want failed: every row of one torrent settles in one scan (#247)",
				g.ItemNumber.Int64, g.Status)
		}
	}
}

// backdateOpenRows pushes whatever the scan left open past the stall timeout.
// Under a per-group clock nothing is open and this does nothing; under a per-row
// one it is what carries the late row to the second rung.
func backdateOpenRows(t *testing.T, st *store.Store, hash string, ago time.Duration) {
	t.Helper()
	for _, g := range packGrabs(t, st, hash) {
		if g.Status == statusGrabbed {
			backdateStalledSinceForItem(t, st, hash, g.ItemNumber.Int64, ago)
		}
	}
}

// assertOneRung is the acceptance criterion: one incident, one entry, first rung.
// The upsert is keyed on (title, normalized title), so a split incident shows up
// as failures = 2 rather than as a second row -- and blockDuration reads that as
// a repeat and blocks for 7d (#118).
func assertOneRung(t *testing.T, st *store.Store, titleID int64) {
	t.Helper()
	entries, err := st.Q.ListBlocklistByTitle(context.Background(), titleID)
	if err != nil {
		t.Fatalf("list blocklist: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("recorded %d blocklist entries, want 1", len(entries))
	}
	if entries[0].Failures != 1 {
		t.Errorf("blocklist failures = %d, want 1: one stalled torrent is one incident, not two (#247)",
			entries[0].Failures)
	}
}

// The issue's scenario: a pack is grabbed while only item 1 exists, the torrent
// stalls at 0%, and item 2 airs and converges onto the same release hours later
// (#241), writing a row whose stalled_since is NULL. A pack is one torrent, so
// the whole group fails in one scan and takes one rung; per-row clocks split it
// across two scans, which the ladder reads as a repeat and escalates to 7d.
func TestStalledPackWithALateRowTakesOneRung(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedPack(t, st, "abc", 1, 2)
	backdateStalledSinceForItem(t, st, "abc", 1, 7*time.Hour)
	svc := blocklist.New(st, nil)
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0)}}
	im := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), svc, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	assertPackSettled(t, st, "abc", 2)
	assertItemFreed(t, st, titleID, 1)
	assertItemFreed(t, st, titleID, 2)
	assertOneRung(t, st, titleID)

	// The incident is over, so no later scan may add to it however long the
	// client keeps reporting the torrent.
	backdateOpenRows(t, st, "abc", 7*time.Hour)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	assertOneRung(t, st, titleID)
}

// The late row takes the group's earliest clock, not now: taking now would leave
// it on a clock of its own and reproduce the split exactly.
func TestLateRowInheritsTheGroupsEarliestStallClock(t *testing.T) {
	st := coretest.NewStore(t)
	seedPack(t, st, "abc", 1, 2)
	// Inside the timeout, so the scan only stamps and nothing is settled yet.
	backdateStalledSinceForItem(t, st, "abc", 1, 2*time.Hour)
	want := grabIDForItem(t, st, "abc", 1)
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0)}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	var first string
	for _, g := range packGrabs(t, st, "abc") {
		if g.ID == want {
			first = g.StalledSince.String
		}
	}
	for _, g := range packGrabs(t, st, "abc") {
		if g.Status != statusGrabbed {
			t.Errorf("item %d settled early; 2h is inside the 6h timeout", g.ItemNumber.Int64)
		}
		// Stored, not merely computed: the Activity queue renders AbandonAt from
		// this row's own column, so a divergent value shows the late episode a
		// countdown it will not be settled on.
		if g.StalledSince.String != first {
			t.Errorf("item %d stalled_since = %q, want the group's earliest %q",
				g.ItemNumber.Int64, g.StalledSince.String, first)
		}
	}
}

// missing_since has the identical shape. An absence is never blamed (#241), so
// no rung is at stake -- but remember() states that "the grab_failed
// notification groups the same way: one per incident", and a split group makes
// that claim false.
func TestVanishedPackWithALateRowIsOneIncident(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedPack(t, st, "abc", 1, 2)
	backdateMissingSinceForItem(t, st, "abc", 1, 10*time.Minute)
	rec := &fakeRecorder{}
	fn := coretest.NewFakeNotifier()
	// No status for the hash at all: the client has stopped reporting the torrent.
	dl := &coretest.FakeDownload{}
	im := New(st, notifyingSource(dl, &coretest.FakeLibrary{}, fn), discardLogger(), rec, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	assertPackSettled(t, st, "abc", 2)
	assertItemFreed(t, st, titleID, 1)
	assertItemFreed(t, st, titleID, 2)
	if len(rec.calls) != 0 {
		t.Errorf("recorded %d blocklist entries, want 0: absence is not a verdict (#241)", len(rec.calls))
	}

	ev := waitEvent(t, fn)
	if ev.Kind != notify.KindGrabFailed {
		t.Fatalf("kind = %s, want grab_failed", ev.Kind)
	}
	// remember() reports an item number only for a lone row, so a pack naming one
	// episode is the tell that the group was split.
	if ev.ItemNumber != 0 {
		t.Errorf("item number = %d, want 0: one incident covering a pack names the release, not an episode",
			ev.ItemNumber)
	}
	expectNoEvent(t, fn)

	// Nothing is left open to report a second time.
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	expectNoEvent(t, fn)
}
