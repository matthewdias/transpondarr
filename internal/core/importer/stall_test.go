package importer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/blocklist"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// stallAfter is a StallPolicy a test can change between scans, which is what
// makes the per-scan read observable.
type stallAfter struct{ d time.Duration }

func (s *stallAfter) StallTimeout() time.Duration { return s.d }

// backdateStalledSince sets a grab's stalled_since to ago in the past and
// returns it, so a test can assert the clock was not restarted.
func backdateStalledSince(t *testing.T, st *store.Store, hash string, ago time.Duration) string {
	t.Helper()
	value := store.FormatTimestamp(time.Now().Add(-ago))
	if err := st.Q.SetGrabStalledSince(context.Background(), db.SetGrabStalledSinceParams{
		StalledSince: sql.NullString{String: value, Valid: true},
		ID:           grabByHash(t, st, hash).ID,
	}); err != nil {
		t.Fatalf("set stalled_since: %v", err)
	}
	return value
}

// stalled is what the client reports for a torrent announcing fine with nothing
// coming in: present, so every other reconciliation path passes it over.
func stalled(hash string, progress float64) download.Status {
	return download.Status{Hash: hash, State: download.StateStalled, Progress: progress, ContentPath: "/whatever"}
}

// fetchingMetadata is what the client reports for a magnet whose swarm never
// answered: qBittorrent's metaDL and forcedMetaDL both map here (#246), so this
// is the "Downloading metadata" torrent the timeout could not previously reach.
func fetchingMetadata(hash string, progress float64) download.Status {
	return download.Status{Hash: hash, State: download.StateDownloading, Progress: progress, ContentPath: "/whatever"}
}

// A magnet parked at "Downloading metadata" is never reported stalled, so before
// #246 it sat open forever. The client says it is trying and nothing has arrived,
// which is the same fact the stall timeout acts on.
func TestFailsGrabFetchingMetadataAtZeroPastTimeout(t *testing.T) {
	st := coretest.NewStore(t)
	_, titleID := seedGrab(t, st, "abc")
	backdateStalledSince(t, st, "abc", 7*time.Hour)
	backdateSearchState(t, st, titleID)
	svc := blocklist.New(st, nil)
	dl := &coretest.FakeDownload{Statuses: []download.Status{fetchingMetadata("abc", 0)}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), svc, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if g := grabByHash(t, st, "abc"); g.Status != statusFailed {
		t.Errorf("status = %q, want failed: a download that never started is the stall the timeout is for", g.Status)
	}
	assertItemFreed(t, st, titleID, 5)
	// Blame is inherited from #242's arm rather than re-decided: the client holds
	// the torrent and reports no swarm, which is observed, not inferred (#241).
	entries, err := st.Q.ListBlocklistByTitle(context.Background(), titleID)
	if err != nil {
		t.Fatalf("list blocklist: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("recorded %d entries, want 1: nobody seeding a release we can see is the release's failure", len(entries))
	}
}

// A download that has moved is never abandoned, whichever state carries it: the
// widened predicate reads progress, not just the state name.
func TestLeavesAFetchingMetadataDownloadWithProgressAlone(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	backdateStalledSince(t, st, "abc", 7*time.Hour)
	dl := &coretest.FakeDownload{Statuses: []download.Status{fetchingMetadata("abc", 0.01)}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != statusGrabbed {
		t.Errorf("status = %q, want still grabbed: a peer had the data", g.Status)
	}
	if g.StalledSince.Valid {
		t.Errorf("stalled_since = %q, want cleared once progress moved", g.StalledSince.String)
	}
}

// The clearing loop and the switch arm must read the *same* predicate. If only
// the arm widened, a metadata stall would have its clock cleared and restarted
// every scan, so the timeout would never accumulate and the grab would sit open
// forever -- the bug being fixed, surviving the fix.
func TestKeepsMetadataStallClockAcrossScans(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	first := backdateStalledSince(t, st, "abc", time.Hour)
	dl := &coretest.FakeDownload{Statuses: []download.Status{fetchingMetadata("abc", 0)}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != statusGrabbed {
		t.Errorf("status = %q, want still grabbed one hour into a six-hour timeout", g.Status)
	}
	if g.StalledSince.String != first {
		t.Errorf("stalled_since = %q, want the original %q (the clock must not restart)", g.StalledSince.String, first)
	}
}

// A stall that outlives the timeout with nothing downloaded settles the grab, so
// the item is wanted again instead of sitting open forever (#242).
func TestFailsGrabStalledAtZeroPastTimeout(t *testing.T) {
	st := coretest.NewStore(t)
	_, titleID := seedGrab(t, st, "abc")
	backdateStalledSince(t, st, "abc", 7*time.Hour)
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0)}}
	target := &coretest.FakeLibrary{}

	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 0 {
		t.Error("nothing should be placed for a torrent that downloaded nothing")
	}
	if g := grabByHash(t, st, "abc"); g.Status != statusFailed {
		t.Errorf("status = %q, want failed once the stall outlived the timeout", g.Status)
	}
	assertItemFreed(t, st, titleID, 5)
}

// Nobody seeding this release is a fact about the release, so unlike an absence
// it is remembered and the title is re-fronted in the search queue (#241).
func TestStalledGrabRecordsBlocklistEntry(t *testing.T) {
	st := coretest.NewStore(t)
	_, titleID := seedGrab(t, st, "abc")
	backdateStalledSince(t, st, "abc", 7*time.Hour)
	backdateSearchState(t, st, titleID)
	svc := blocklist.New(st, nil)
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0)}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), svc, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if got := grabByHash(t, st, "abc").Status; got != statusFailed {
		t.Fatalf("status = %q, want failed past the timeout", got)
	}
	entries, err := st.Q.ListBlocklistByTitle(context.Background(), titleID)
	if err != nil {
		t.Fatalf("list blocklist: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("recorded %d entries, want 1: a release nobody seeds is the release's failure", len(entries))
	}
	// Not an existing reason: the client reported no error, so borrowing that
	// wording would misattribute it the way #241 and #244 both had to correct.
	if !strings.Contains(entries[0].Reason, "stalled at 0%") {
		t.Errorf("reason = %q, want it to name the stall", entries[0].Reason)
	}
	if backoff, _ := readSearchBackoff(t, st, titleID); backoff != 0 {
		t.Errorf("search backoff = %d, want 0: a blamed failure re-fronts the title", backoff)
	}
}

// The timeout runs from a stall that persisted, so the first sighting only
// starts the clock.
func TestWatchesStallOnFirstObservation(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0)}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != statusGrabbed {
		t.Errorf("status = %q, want still grabbed on the first stalled scan", g.Status)
	}
	if !g.StalledSince.Valid {
		t.Error("stalled_since not set on the first stalled observation")
	}
}

// A later scan must not restart the clock, or a 15s poll would reset it forever.
func TestKeepsStallClockAcrossScans(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	first := backdateStalledSince(t, st, "abc", time.Hour)
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0)}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != statusGrabbed {
		t.Errorf("status = %q, want still grabbed one hour into a six-hour timeout", g.Status)
	}
	if g.StalledSince.String != first {
		t.Errorf("stalled_since = %q, want the original %q (the clock must not restart)", g.StalledSince.String, first)
	}
}

// A torrent that moved at all proves a peer had the data, so the bytes on disk
// are the user's to discard however long it then sits.
func TestLeavesStallWithProgressAlone(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	backdateStalledSince(t, st, "abc", 30*24*time.Hour)
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0.01)}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != statusGrabbed {
		t.Errorf("status = %q, want still grabbed: a stall above 0%% is never given up on", g.Status)
	}
	if g.StalledSince.Valid {
		t.Errorf("stalled_since = %q, want cleared once any progress existed", g.StalledSince.String)
	}
}

// A transient stall is the common case, so progress arriving between two
// observations must put the clock back to nothing.
func TestStallThatResumesClearsTheClock(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0)}}
	im := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if !grabByHash(t, st, "abc").StalledSince.Valid {
		t.Fatal("stalled_since not set by the first scan, so the clear cannot be observed")
	}

	dl.Statuses = []download.Status{stalled("abc", 0.2)} // a peer turned up
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("second scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.StalledSince.Valid {
		t.Errorf("stalled_since = %q, want cleared the moment progress moved", g.StalledSince.String)
	}
	if g.Status != statusGrabbed {
		t.Errorf("status = %q, want still grabbed", g.Status)
	}
}

// Only a client that says it is trying qualifies. A pause is deliberate user
// intent, StateUnknown is a gap in our own mapping, and a queued torrent is one
// the client is holding back on purpose -- blaming a release for any of them is
// wrong, however long it sits (#246).
func TestStatesTheClientIsNotTryingAreNeverGivenUpOn(t *testing.T) {
	for _, state := range []download.State{
		download.StatePaused,
		download.StateUnknown,
		download.StateChecking,
		download.StateQueued,
	} {
		t.Run(string(state), func(t *testing.T) {
			st := coretest.NewStore(t)
			seedGrab(t, st, "abc")
			backdateStalledSince(t, st, "abc", 7*time.Hour)
			dl := &coretest.FakeDownload{Statuses: []download.Status{
				{Hash: "abc", State: state, Progress: 0, ContentPath: "/whatever"},
			}}

			if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).
				ScanOnce(context.Background()); err != nil {
				t.Fatalf("scan: %v", err)
			}

			g := grabByHash(t, st, "abc")
			if g.Status != statusGrabbed {
				t.Errorf("status = %q, want still grabbed: %s is not a stall", g.Status, state)
			}
			if g.StalledSince.Valid {
				t.Errorf("stalled_since = %q, want cleared once the client was not trying", g.StalledSince.String)
			}
		})
	}
}

// An upgrade holds an item the library already has (#97). Failing its grab must
// free the grab and nothing else -- the file we hold is untouched by a download
// that never started.
func TestStalledUpgradeLeavesTheHeldFileAlone(t *testing.T) {
	st := coretest.NewStore(t)
	itemID, titleID := seedGrab(t, st, "abc")
	if err := st.Q.SetWantedItemHeld(context.Background(), db.SetWantedItemHeldParams{
		InLibrary: 1, HeldReleaseTitle: "the release we hold", ID: itemID,
	}); err != nil {
		t.Fatalf("mark item held: %v", err)
	}
	backdateStalledSince(t, st, "abc", 7*time.Hour)
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0)}}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if g := grabByHash(t, st, "abc"); g.Status != statusFailed {
		t.Errorf("status = %q, want failed", g.Status)
	}
	item, err := st.Q.GetWantedItemByNumber(context.Background(), db.GetWantedItemByNumberParams{
		SeriesID: titleID, Kind: "episode", Number: sql.NullInt64{Int64: 5, Valid: true},
	})
	if err != nil {
		t.Fatalf("read item: %v", err)
	}
	if item.InLibrary != 1 {
		t.Error("in_library = 0; a failed upgrade must leave the file the library already holds")
	}
	if item.HeldReleaseTitle != "the release we hold" {
		t.Errorf("held release = %q, want the one still on disk", item.HeldReleaseTitle)
	}
}

// A VPN drop or a closed port stalls every torrent at 0% at once, and they all
// cross the timeout in one scan. #120's breaker is the containment, and it is
// only containment if this path feeds it: four distinct items are blamed and
// everything after is suppressed, however many dropped.
func TestStallFanOutIsContainedByTheBreaker(t *testing.T) {
	const titles = 8
	st := coretest.NewStore(t)
	svc := blocklist.New(st, nil)
	var titleIDs []int64
	var statuses []download.Status
	for i := range titles {
		hash := fmt.Sprintf("hash%d", i)
		titleIDs = append(titleIDs, seedTitleGrab(t, st, fmt.Sprintf("Placeholder Saga %d", i), hash, 1))
		backdateStalledSince(t, st, hash, 7*time.Hour)
		statuses = append(statuses, stalled(hash, 0))
	}
	dl := &coretest.FakeDownload{Statuses: statuses}

	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), svc, nil).
		ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	recorded := 0
	for _, id := range titleIDs {
		entries, err := st.Q.ListBlocklistByTitle(context.Background(), id)
		if err != nil {
			t.Fatalf("list blocklist: %v", err)
		}
		recorded += len(entries)
	}
	// Every item is freed either way: freeing is self-healing, blaming is the
	// judgement the breaker withholds.
	for _, id := range titleIDs {
		assertItemFreed(t, st, id, 1)
	}
	if recorded != 4 {
		t.Errorf("recorded %d entries across %d stalled titles, want 4: the breaker opens on the fifth item in the window",
			recorded, titles)
	}
}

// The threshold is read per scan, so an edit in the settings UI applies on the
// next tick -- the invariant a job closure capturing a snapshot would break.
func TestStallTimeoutIsReadEachScan(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	backdateStalledSince(t, st, "abc", 7*time.Hour)
	policy := &stallAfter{d: 24 * time.Hour}
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0)}}
	im := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil,
		WithStallPolicy(policy))

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if g := grabByHash(t, st, "abc"); g.Status != statusGrabbed {
		t.Fatalf("status = %q, want still grabbed seven hours into a 24h timeout", g.Status)
	}

	policy.d = time.Hour // the user shortened it in Settings
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if g := grabByHash(t, st, "abc"); g.Status != statusFailed {
		t.Errorf("status = %q, want failed: the same importer must honour the new threshold", g.Status)
	}
}

// Zero disables the timeout rather than making it instant, so an install that
// wants today's behaviour keeps it -- and it banks no time while off, or
// restoring a timeout would fail on the next scan what it was set to 0 to hold.
// Holding a download by pausing it and by turning the timeout off must agree.
func TestStallTimeoutZeroNeverGivesUpAndBanksNoTime(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	backdateStalledSince(t, st, "abc", 30*24*time.Hour)
	policy := &stallAfter{}
	dl := &coretest.FakeDownload{Statuses: []download.Status{stalled("abc", 0)}}
	im := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil,
		WithStallPolicy(policy))

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	g := grabByHash(t, st, "abc")
	if g.Status != statusGrabbed {
		t.Errorf("status = %q, want still grabbed: a zero timeout never gives up", g.Status)
	}
	if g.StalledSince.Valid {
		t.Errorf("stalled_since = %q, want cleared while the timeout is off", g.StalledSince.String)
	}

	policy.d = 6 * time.Hour // restored a month later
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if g := grabByHash(t, st, "abc"); g.Status != statusGrabbed {
		t.Errorf("status = %q, want still grabbed: restoring the timeout starts a fresh window", g.Status)
	}
}
