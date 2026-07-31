package acquire_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// sweepItem describes one wanted item to seed: its number, whether it is had,
// its air time (nil = the provider published none), and any grab against it.
type sweepItem struct {
	number int
	have   bool
	airsAt *time.Time
	grab   string
}

// seedSweep inserts a series with the given items and returns its id.
func seedSweep(t *testing.T, st *store.Store, title string, monitored bool, items ...sweepItem) int64 {
	t.Helper()
	ctx := context.Background()
	var mon int64
	if monitored {
		mon = 1
	}
	s, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: title, Format: "TV", Monitored: mon})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	for _, it := range items {
		var have int64
		if it.have {
			have = 1
		}
		row, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID: s.ID, Kind: "episode",
			Number: sql.NullInt64{Int64: int64(it.number), Valid: true},
			Have:   have,
		})
		if err != nil {
			t.Fatalf("create item %d: %v", it.number, err)
		}
		if it.airsAt != nil {
			if _, err := st.DB.ExecContext(ctx, `UPDATE wanted_items SET airs_at = ? WHERE id = ?`,
				store.FormatTimestamp(*it.airsAt), row.ID); err != nil {
				t.Fatalf("set airs_at on item %d: %v", it.number, err)
			}
		}
		if it.grab != "" {
			if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
				WantedItemID: row.ID, InfoHash: fmt.Sprintf("existing%d", it.number),
				ReleaseTitle: "existing release", Status: it.grab,
			}); err != nil {
				t.Fatalf("seed grab on item %d: %v", it.number, err)
			}
		}
	}
	return s.ID
}

// episodeRelease builds a synthetic single-episode release for a series title.
func episodeRelease(title string, number int) indexer.Release {
	return indexer.Release{
		Title:       fmt.Sprintf("[ExampleSubs] %s - %02d [1080p]", title, number),
		DownloadURL: fmt.Sprintf("magnet:?xt=urn:btih:%s%02d", "aa", number),
		Seeders:     100,
	}
}

// searchState is the cadence a sweep wrote for one series.
type searchState struct {
	backoff      int64
	nextSearchAt sql.NullString
	lastSearched sql.NullString
}

func readSearchState(t *testing.T, st *store.Store, id int64) searchState {
	t.Helper()
	var s searchState
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_backoff, next_search_at, last_searched_at FROM series WHERE id = ?`, id).
		Scan(&s.backoff, &s.nextSearchAt, &s.lastSearched); err != nil {
		t.Fatalf("read search state: %v", err)
	}
	return s
}

// wantNextSearchNear asserts next_search_at lands within a minute of want, which
// is as precise as a real-clock, store-backed test can be.
func wantNextSearchNear(t *testing.T, got sql.NullString, want time.Time) {
	t.Helper()
	if !got.Valid {
		t.Fatalf("next_search_at is NULL, want ~%s", store.FormatTimestamp(want))
	}
	at, err := store.ParseTimestamp(got.String)
	if err != nil {
		t.Fatalf("parse next_search_at %q: %v", got.String, err)
	}
	if d := at.Sub(want.UTC()); d > time.Minute || d < -time.Minute {
		t.Errorf("next_search_at = %s, want ~%s (off by %s)", at, want.UTC(), d)
	}
}

func grabbedItemNumbers(t *testing.T, st *store.Store, seriesID int64) []int {
	t.Helper()
	ctx := context.Background()
	grabs, err := st.Q.ListGrabsBySeries(ctx, seriesID)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	items, err := st.Q.ListWantedItems(ctx, seriesID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	number := make(map[int64]int, len(items))
	for _, it := range items {
		number[it.ID] = int(it.Number.Int64)
	}
	out := make([]int, 0, len(grabs))
	for _, g := range grabs {
		out = append(out, number[g.WantedItemID])
	}
	return out
}

// sweepHarness bundles what a sweep test asserts against.
type sweepHarness struct {
	svc *acquire.Service
	st  *store.Store
	idx *coretest.FakeIndexer
	dl  *coretest.FakeDownload
}

func newSweep(t *testing.T, releases []indexer.Release, cfg fakeConfig) *sweepHarness {
	t.Helper()
	st := coretest.NewStore(t)
	idx := &coretest.FakeIndexer{Releases: releases}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "swept", Outcome: download.AddSuccess}}
	reg := newRegistry(idx, dl)
	return &sweepHarness{
		svc: acquire.New(st, reg, fakeTitles{}, cfg, discardLogger()),
		st:  st, idx: idx, dl: dl,
	}
}

// The headline behaviour of #100: an aired, ungrabbed item is found and grabbed
// with no user action.
func TestSweepGrabsEligibleAiredItem(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 1, have: true}, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3]", got)
	}
	// A successful grab means more may be waiting: due again next tick.
	state := readSearchState(t, h.st, id)
	if state.backoff != 0 || state.nextSearchAt.Valid {
		t.Errorf("state = %+v, want backoff 0 and next_search_at NULL after a grab", state)
	}
	if !state.lastSearched.Valid {
		t.Error("last_searched_at not stamped")
	}
}

// The kill switch is checked per run, so the job stays registered and inert.
func TestSweepNoOpsWhenAutomationDisabled(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{automationOff: true})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(h.idx.Queries) != 0 || len(h.dl.Adds) != 0 {
		t.Errorf("disabled sweep searched %d times and added %d torrents, want none",
			len(h.idx.Queries), len(h.dl.Adds))
	}
	if state := readSearchState(t, h.st, id); state.lastSearched.Valid {
		t.Error("disabled sweep wrote search state")
	}
}

// An unconfigured integration is a supported state, not an error: the sweep
// no-ops until Settings supplies both clients.
func TestSweepNoOpsWithoutIndexerOrDownloadClient(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	for _, tc := range []struct {
		name    string
		withIdx bool
		withDL  bool
	}{
		{"no indexer", false, true},
		{"no download client", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := coretest.NewStore(t)
			idx := &coretest.FakeIndexer{Releases: []indexer.Release{episodeRelease("Placeholder Saga", 3)}}
			dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "x", Outcome: download.AddSuccess}}
			var reg = newRegistry(nil, nil)
			if tc.withIdx {
				reg = newRegistry(idx, nil)
			}
			if tc.withDL {
				reg.SetDownload(dl)
			}
			svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{}, discardLogger())
			id := seedSweep(t, st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

			if err := svc.SweepOnce(context.Background()); err != nil {
				t.Fatalf("SweepOnce: %v", err)
			}
			if len(idx.Queries) != 0 || len(dl.Adds) != 0 {
				t.Errorf("sweep acted with a missing client: %d searches, %d adds",
					len(idx.Queries), len(dl.Adds))
			}
			if state := readSearchState(t, st, id); state.lastSearched.Valid {
				t.Error("sweep wrote search state with a missing client")
			}
		})
	}
}

// Monitoring is what gates automation (half of #102): an unmonitored series is
// never swept, however wanted its items are.
func TestSweepSkipsUnmonitoredSeries(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", false, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Errorf("grabbed %v for an unmonitored series, want nothing", got)
	}
}

// An episode that has not aired is not grabbed; one whose air date the provider
// never published is, because absent is normal, not "not yet".
func TestSweepSkipsFutureItemsButTreatsNullAirsAtAsEligible(t *testing.T) {
	future := time.Now().Add(48 * time.Hour)
	h := newSweep(t, []indexer.Release{
		episodeRelease("Placeholder Saga", 3),
		episodeRelease("Placeholder Saga", 9),
	}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3},
		sweepItem{number: 9, airsAt: &future})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Errorf("grabbed items = %v, want only the unscheduled item 3", got)
	}
}

// The profile floor is enforcement, not advice, on an automatic grab (PR #57
// exempts manual grabs only).
func TestSweepNeverGrabsIneligibleOnlyCandidate(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{episodeRelease("Placeholder Saga", 3)}, fakeConfig{})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE quality_profiles SET min_score = 9000 WHERE id = 1`); err != nil {
		t.Fatalf("raise the profile floor: %v", err)
	}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 0 {
		t.Errorf("grabbed %v below the profile floor, want nothing", got)
	}
	// Nothing taken means back off, not retry every tick.
	if state := readSearchState(t, h.st, id); state.backoff != 1 {
		t.Errorf("backoff = %d, want 1 after an empty pass", state.backoff)
	}
}

// An item already downloading must not be grabbed a second time; its siblings
// still can be.
func TestSweepDoesNotRegrabInFlightItems(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{
		episodeRelease("Placeholder Saga", 3),
		episodeRelease("Placeholder Saga", 4),
	}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past, grab: "grabbed"},
		sweepItem{number: 4, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(h.dl.Adds) != 1 {
		t.Fatalf("download Add called %d times, want 1 (item 4 only)", len(h.dl.Adds))
	}
	grabs, _ := h.st.Q.ListGrabsBySeries(context.Background(), id)
	for _, g := range grabs {
		if g.InfoHash == "existing3" && g.Status != "grabbed" {
			t.Errorf("in-flight grab was disturbed: %+v", g)
		}
	}
}

// One pass takes everything it can: the budget is searches, not grabs.
func TestSweepGrabsMultipleReleasesInOnePass(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, []indexer.Release{
		episodeRelease("Placeholder Saga", 3),
		episodeRelease("Placeholder Saga", 4),
		episodeRelease("Placeholder Saga", 5),
	}, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past},
		sweepItem{number: 4, airsAt: &past},
		sweepItem{number: 5, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 3 {
		t.Errorf("grabbed items = %v, want all three", got)
	}
	if len(h.idx.Queries) != 1 {
		t.Errorf("indexer queried %d times for one series, want 1", len(h.idx.Queries))
	}
}

// A dead download URL is one release's problem. It must not cost the rest of the
// series its pass, and it must not skip the cadence write — that would re-search
// the same series every tick forever.
func TestSweepContinuesPastAFailedAdd(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	dead := episodeRelease("Placeholder Saga", 3)
	h := newSweep(t, []indexer.Release{dead, episodeRelease("Placeholder Saga", 4)}, fakeConfig{})
	h.dl.FailURLs = map[string]error{dead.DownloadURL: errors.New("404 fetching .torrent")}
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past},
		sweepItem{number: 4, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v — one bad release must not fail the pass", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 4 {
		t.Errorf("grabbed items = %v, want [4] — the healthy release still lands", got)
	}
	if !readSearchState(t, h.st, id).lastSearched.Valid {
		t.Error("last_searched_at is NULL: the pass did not record that it ran")
	}
}

// Items under a failed add stay unclaimed, so the next-ranked release covering
// the same episode is still tried in the same pass.
func TestSweepFallsBackToTheNextCandidateForTheSameItem(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	best := episodeRelease("Placeholder Saga", 3)
	best.Seeders = 500 // ranks first, and its URL is dead
	alt := episodeRelease("Placeholder Saga", 3)
	alt.Title = "[OtherSubs] Placeholder Saga - 03 [1080p]"
	alt.DownloadURL = "magnet:?xt=urn:btih:alt03"

	h := newSweep(t, []indexer.Release{best, alt}, fakeConfig{})
	h.dl.FailURLs = map[string]error{best.DownloadURL: errors.New("404 fetching .torrent")}
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3] from the fallback release", got)
	}
	if got := h.dl.Adds[len(h.dl.Adds)-1].URL; got != alt.DownloadURL {
		t.Errorf("last add URL = %q, want the runner-up %q", got, alt.DownloadURL)
	}
}

// Repeated add failures mean the client is unwell, not that every release is
// dead: the series gives up rather than walking the whole candidate list.
func TestSweepStopsAfterRepeatedAddFailures(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	var releases []indexer.Release
	items := []sweepItem{}
	for n := 3; n <= 10; n++ {
		releases = append(releases, episodeRelease("Placeholder Saga", n))
		items = append(items, sweepItem{number: n, airsAt: &past})
	}
	h := newSweep(t, releases, fakeConfig{})
	h.dl.Err = errors.New("connection refused")
	id := seedSweep(t, h.st, "Placeholder Saga", true, items...)

	if err := h.svc.SweepOnce(context.Background()); err == nil {
		t.Fatal("SweepOnce returned nil, want the client failure surfaced")
	}
	if len(h.dl.Adds) > 3 {
		t.Errorf("attempted %d adds against a failing client, want at most 3", len(h.dl.Adds))
	}
	// No cadence write: the series is retried next tick, not backed off for an
	// outage that was never its own.
	if readSearchState(t, h.st, id).lastSearched.Valid {
		t.Error("last_searched_at was written despite the pass failing")
	}
}

// A series nobody is seeding for backs off exponentially rather than asking the
// indexer every tick forever, and the backoff stops growing at the cap.
func TestSweepEmptySearchBackoffGrowsAndCaps(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &past})

	before := time.Now()
	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	state := readSearchState(t, h.st, id)
	if state.backoff != 1 {
		t.Fatalf("backoff = %d, want 1 after the first empty pass", state.backoff)
	}
	wantNextSearchNear(t, state.nextSearchAt, before.Add(time.Hour))

	// Far enough along that the doubling would overshoot a day.
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 10, next_search_at = NULL WHERE id = ?`, id); err != nil {
		t.Fatalf("advance the backoff: %v", err)
	}
	before = time.Now()
	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	state = readSearchState(t, h.st, id)
	if state.backoff != 11 {
		t.Errorf("backoff = %d, want 11", state.backoff)
	}
	wantNextSearchNear(t, state.nextSearchAt, before.Add(24*time.Hour))
}

// A new episode restarts the clock: whatever the accumulated backoff, an episode
// that aired since the last search is worth looking for now (#100).
func TestSweepNewlyAiredItemResetsBackoff(t *testing.T) {
	now := time.Now()
	justAired := now.Add(-30 * time.Minute)
	h := newSweep(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true, sweepItem{number: 3, airsAt: &justAired})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 6, last_searched_at = ?, next_search_at = NULL WHERE id = ?`,
		store.FormatTimestamp(now.Add(-3*time.Hour)), id); err != nil {
		t.Fatalf("seed a long backoff: %v", err)
	}

	before := time.Now()
	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	state := readSearchState(t, h.st, id)
	if state.backoff != 1 {
		t.Errorf("backoff = %d, want 1 — the new episode should have reset the clock", state.backoff)
	}
	wantNextSearchNear(t, state.nextSearchAt, before.Add(time.Hour))
}

// The next broadcast clamps the backoff, so a weekly show is always searched at
// air time no matter how many empty passes preceded it.
func TestSweepClampsNextSearchToUpcomingAirDate(t *testing.T) {
	now := time.Now()
	past := now.Add(-3 * time.Hour)
	soon := now.Add(20 * time.Minute)
	h := newSweep(t, nil, fakeConfig{})
	id := seedSweep(t, h.st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past},
		sweepItem{number: 4, airsAt: &soon})
	if _, err := h.st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 10, last_searched_at = ? WHERE id = ?`,
		store.FormatTimestamp(now.Add(-1*time.Hour)), id); err != nil {
		t.Fatalf("seed a long backoff: %v", err)
	}

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	wantNextSearchNear(t, readSearchState(t, h.st, id).nextSearchAt, soon)
}

// The per-pass cap is what keeps the indexer budget bounded.
func TestSweepStopsAtTheSeriesPerPassCap(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, nil, fakeConfig{})
	for i := range 8 {
		seedSweep(t, h.st, fmt.Sprintf("Placeholder Saga %d", i), true, sweepItem{number: 1, airsAt: &past})
	}

	if err := h.svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(h.idx.Queries) != 5 {
		t.Errorf("searched %d series in one pass, want the 5-series cap", len(h.idx.Queries))
	}
}
