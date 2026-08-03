package airing_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/airing"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// fakeProvider is a metadata.Provider that publishes a schedule, recording what
// each series was asked for.
type fakeProvider struct {
	schedules map[int64][]metadata.Airing
	errs      map[int64]error

	calls         []int64 // provider ids, in call order
	notYetAired   map[int64]bool
	onGetSchedule func() // runs mid-fetch, for interleaving concurrent writers
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{
		schedules:   map[int64][]metadata.Airing{},
		errs:        map[int64]error{},
		notYetAired: map[int64]bool{},
	}
}

func (f *fakeProvider) Name() string { return "anilist" }

func (f *fakeProvider) Search(context.Context, string) ([]metadata.Candidate, error) {
	return nil, nil
}

func (f *fakeProvider) GetTitle(context.Context, int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	return metadata.TitleMeta{}, nil, nil
}

func (f *fakeProvider) GetSchedule(_ context.Context, id int64, notYetAired bool) ([]metadata.Airing, error) {
	f.calls = append(f.calls, id)
	f.notYetAired[id] = notYetAired
	if f.onGetSchedule != nil {
		f.onGetSchedule()
	}
	if err := f.errs[id]; err != nil {
		return nil, err
	}
	return f.schedules[id], nil
}

// plainProvider has no schedule to publish.
type plainProvider struct{}

func (*plainProvider) Name() string { return "plain" }

func (*plainProvider) Search(context.Context, string) ([]metadata.Candidate, error) {
	return nil, nil
}

func (*plainProvider) GetTitle(context.Context, int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	return metadata.TitleMeta{}, nil, nil
}

func newService(t *testing.T, st *store.Store, prov metadata.Provider) *airing.Service {
	t.Helper()
	return airing.New(st, prov, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// seedSeries inserts a monitored series with items 1..episodes and returns its id.
func seedSeries(t *testing.T, st *store.Store, anilistID int64, episodes int) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO series (anilist_id, title, monitored) VALUES (?, 'Placeholder', 1) RETURNING id`,
		anilistID).Scan(&id); err != nil {
		t.Fatalf("insert series: %v", err)
	}
	for n := 1; n <= episodes; n++ {
		if _, err := st.DB.ExecContext(ctx,
			`INSERT INTO wanted_items (series_id, kind, number) VALUES (?, 'episode', ?)`, id, n); err != nil {
			t.Fatalf("insert item %d: %v", n, err)
		}
	}
	return id
}

func setSyncedAt(t *testing.T, st *store.Store, seriesID int64, at time.Time) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET airing_synced_at = ? WHERE id = ?`, store.FormatTimestamp(at), seriesID); err != nil {
		t.Fatalf("set airing_synced_at: %v", err)
	}
}

func setCachedStatus(t *testing.T, st *store.Store, anilistID int64, status string) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`INSERT INTO metadata_cache (provider, provider_id, status, raw) VALUES ('anilist', ?, ?, '{}')`,
		anilistID, status); err != nil {
		t.Fatalf("seed metadata cache: %v", err)
	}
}

// airsAt reads one item's stored air date; ok is false when it is still null.
func airsAt(t *testing.T, st *store.Store, seriesID int64, number int) (value string, ok bool) {
	t.Helper()
	var stored *string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT airs_at FROM wanted_items WHERE series_id = ? AND number = ?`, seriesID, number).Scan(&stored); err != nil {
		t.Fatalf("read airs_at: %v", err)
	}
	if stored == nil {
		return "", false
	}
	return *stored, true
}

func syncedAt(t *testing.T, st *store.Store, seriesID int64) (string, bool) {
	t.Helper()
	var stored *string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT airing_synced_at FROM series WHERE id = ?`, seriesID).Scan(&stored); err != nil {
		t.Fatalf("read airing_synced_at: %v", err)
	}
	if stored == nil {
		return "", false
	}
	return *stored, true
}

func TestSyncWritesAirDatesForANeverSyncedSeries(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 100, 3)

	prov := newFakeProvider()
	prov.schedules[100] = []metadata.Airing{
		{Number: 1, AirsAt: time.Date(2026, 1, 4, 15, 30, 0, 0, time.UTC)},
		{Number: 2, AirsAt: time.Date(2026, 1, 11, 15, 30, 0, 0, time.UTC)},
	}

	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if got, ok := airsAt(t, st, seriesID, 1); !ok || got != "2026-01-04 15:30:00" {
		t.Errorf("item 1 airs_at = %q (set=%t), want 2026-01-04 15:30:00", got, ok)
	}
	if got, ok := airsAt(t, st, seriesID, 2); !ok || got != "2026-01-11 15:30:00" {
		t.Errorf("item 2 airs_at = %q (set=%t), want 2026-01-11 15:30:00", got, ok)
	}
	// Item 3 is outside the schedule AniList published; it must stay null rather
	// than pick up a neighbour's date.
	if got, ok := airsAt(t, st, seriesID, 3); ok {
		t.Errorf("item 3 airs_at = %q, want null", got)
	}
	if _, ok := syncedAt(t, st, seriesID); !ok {
		t.Error("airing_synced_at was not stamped")
	}
	// Never synced before, so history is fetched in full exactly once.
	if prov.notYetAired[100] {
		t.Error("a never-synced series fetched only the tail, so its aired history is lost")
	}
}

// itemState reads one item's have and airs_at; found is false when no row exists.
func itemState(t *testing.T, st *store.Store, seriesID int64, number int) (have int, airsAt *string, found bool) {
	t.Helper()
	err := st.DB.QueryRowContext(context.Background(),
		`SELECT have, airs_at FROM wanted_items WHERE series_id = ? AND number = ?`,
		seriesID, number).Scan(&have, &airsAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, false
	}
	if err != nil {
		t.Fatalf("read item %d: %v", number, err)
	}
	return have, airsAt, true
}

// A null-count long-runner (AniList never publishes an episode total mid-run)
// has no items for the count-driven refresh to create; the schedule the sync
// already fetched is the only source that knows those episodes exist.
func TestSyncCreatesItemsTheScheduleKnowsAbout(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 100, 0)

	prov := newFakeProvider()
	prov.schedules[100] = []metadata.Airing{
		{Number: 1, AirsAt: time.Date(2026, 1, 4, 15, 30, 0, 0, time.UTC)},
		{Number: 2, AirsAt: time.Date(2026, 1, 11, 15, 30, 0, 0, time.UTC)},
	}

	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	have, airs, found := itemState(t, st, seriesID, 1)
	if !found || airs == nil || *airs != "2026-01-04 15:30:00" {
		t.Errorf("item 1: found=%t airs_at=%v, want a created item dated 2026-01-04 15:30:00", found, airs)
	}
	if have != 0 {
		t.Errorf("item 1 have = %d, want a fresh item at 0", have)
	}
	if _, _, found := itemState(t, st, seriesID, 2); !found {
		t.Error("item 2 was not created from the schedule")
	}
}

// AniList lists no entry when two episodes share a broadcast slot, so the
// schedule reads 1, 3, 4. With a null count nothing else would ever create
// episode 2 — the gap is invisible because nothing claims the item should exist.
func TestSyncFillsTheGapsAScheduleSkips(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 100, 0)

	prov := newFakeProvider()
	prov.schedules[100] = []metadata.Airing{
		{Number: 1, AirsAt: time.Date(2026, 1, 4, 15, 30, 0, 0, time.UTC)},
		{Number: 3, AirsAt: time.Date(2026, 1, 18, 15, 30, 0, 0, time.UTC)},
	}

	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	have, airs, found := itemState(t, st, seriesID, 2)
	if !found {
		t.Fatal("item 2 was not created, so the episode that shared a slot is silently missing")
	}
	if airs != nil {
		t.Errorf("item 2 airs_at = %v, want null — the schedule gave it no date", *airs)
	}
	if have != 0 {
		t.Errorf("item 2 have = %d, want a fresh item at 0", have)
	}
	if _, _, found := itemState(t, st, seriesID, 4); found {
		t.Error("item 4 was created past the highest number the schedule knows")
	}
}

// searchCadence reads a series' accumulated backoff and its next due time.
func searchCadence(t *testing.T, st *store.Store, seriesID int64) (backoff int, next *string) {
	t.Helper()
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_backoff, next_search_at FROM series WHERE id = ?`, seriesID).Scan(&backoff, &next); err != nil {
		t.Fatalf("read search cadence: %v", err)
	}
	return backoff, next
}

// A gap-filled item carries no air date, so airedSince cannot see it and the
// series that skipped an episode — likely the one that climbed the ladder
// finding nothing — would wait out its backoff before looking.
func TestGapFillResetsSearchCadence(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 100, 0)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 8, next_search_at = ? WHERE id = ?`,
		store.FormatTimestamp(time.Now().Add(24*time.Hour)), seriesID); err != nil {
		t.Fatalf("seed a long backoff: %v", err)
	}

	prov := newFakeProvider()
	prov.schedules[100] = []metadata.Airing{
		{Number: 1, AirsAt: time.Date(2026, 1, 4, 15, 30, 0, 0, time.UTC)},
		{Number: 3, AirsAt: time.Date(2026, 1, 18, 15, 30, 0, 0, time.UTC)},
	}

	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if backoff, next := searchCadence(t, st, seriesID); backoff != 0 || next != nil {
		t.Errorf("cadence = backoff %d, next %v; want it reset so the sweep looks for the filled item", backoff, next)
	}
}

// A sync that fills nothing must not hand a backed-off series a free retry.
func TestSyncKeepsSearchCadenceWhenNothingIsFilled(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 100, 2)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 8 WHERE id = ?`, seriesID); err != nil {
		t.Fatalf("seed a long backoff: %v", err)
	}

	prov := newFakeProvider()
	prov.schedules[100] = []metadata.Airing{
		{Number: 1, AirsAt: time.Date(2026, 1, 4, 15, 30, 0, 0, time.UTC)},
		{Number: 2, AirsAt: time.Date(2026, 1, 11, 15, 30, 0, 0, time.UTC)},
	}

	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if backoff, _ := searchCadence(t, st, seriesID); backoff != 8 {
		t.Errorf("search_backoff = %d, want the accumulated 8", backoff)
	}
}

// A schedule whose lowest number is not 1 means AniList lost the early records,
// not an offset season, so a full fetch fills from 1 rather than from that low.
func TestFullFetchFillsFromOneBelowTheScheduleWindow(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 100, 0)

	prov := newFakeProvider()
	prov.schedules[100] = []metadata.Airing{
		{Number: 13, AirsAt: time.Date(2026, 4, 5, 15, 30, 0, 0, time.UTC)},
		{Number: 14, AirsAt: time.Date(2026, 4, 12, 15, 30, 0, 0, time.UTC)},
	}

	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	for _, n := range []int{1, 12} {
		_, airs, found := itemState(t, st, seriesID, n)
		if !found {
			t.Errorf("item %d was not created, so the run below the schedule window is lost", n)
		} else if airs != nil {
			t.Errorf("item %d airs_at = %v, want null", n, *airs)
		}
	}
	if _, _, found := itemState(t, st, seriesID, 15); found {
		t.Error("item 15 was created past the highest number the schedule knows")
	}
}

// A tail fetch is a partial view of the numbering, so it fills gaps only inside
// its own span rather than re-deriving a long-runner's whole back catalogue.
func TestSyncTailFillsOnlyInsideItsOwnSpan(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 101, 0)
	setCachedStatus(t, st, 101, "RELEASING")
	setSyncedAt(t, st, seriesID, time.Now().Add(-24*time.Hour))

	prov := newFakeProvider()
	prov.schedules[101] = []metadata.Airing{
		{Number: 13, AirsAt: time.Date(2026, 4, 5, 15, 30, 0, 0, time.UTC)},
		{Number: 15, AirsAt: time.Date(2026, 4, 19, 15, 30, 0, 0, time.UTC)},
	}

	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if _, _, found := itemState(t, st, seriesID, 14); !found {
		t.Error("item 14 was not created, so the gap inside the tail stays missing")
	}
	if _, _, found := itemState(t, st, seriesID, 1); found {
		t.Error("the tail fetch created items below its own span")
	}
}

func TestSyncUpsertDoesNotClobberHave(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 100, 1)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE wanted_items SET have = 1 WHERE series_id = ? AND number = 1`, seriesID); err != nil {
		t.Fatalf("mark item had: %v", err)
	}

	prov := newFakeProvider()
	prov.schedules[100] = []metadata.Airing{
		{Number: 1, AirsAt: time.Date(2026, 1, 4, 15, 30, 0, 0, time.UTC)},
	}

	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	have, airs, _ := itemState(t, st, seriesID, 1)
	if have != 1 {
		t.Errorf("item 1 have = %d, want 1 (sync must not clobber have)", have)
	}
	if airs == nil || *airs != "2026-01-04 15:30:00" {
		t.Errorf("item 1 airs_at = %v, want the date still applied", airs)
	}
}

// The metadata refresh clears airing_synced_at when a series grows; a sync
// already in flight must not stamp over that clear, or the grown item could
// wait out the long TTL (or forever, for aired history) for its air date.
func TestSyncDoesNotRestampASeriesClearedMidSync(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 100, 2)
	setCachedStatus(t, st, 100, "RELEASING")
	setSyncedAt(t, st, seriesID, time.Now().Add(-24*time.Hour))

	prov := newFakeProvider()
	prov.onGetSchedule = func() {
		if _, err := st.DB.ExecContext(context.Background(),
			`UPDATE series SET airing_synced_at = NULL WHERE id = ?`, seriesID); err != nil {
			t.Errorf("clear stamp mid-sync: %v", err)
		}
	}

	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if stamp, ok := syncedAt(t, st, seriesID); ok {
		t.Errorf("airing_synced_at = %q, want the mid-sync clear preserved", stamp)
	}
}

// The asymmetry that makes full history affordable: a resync pages only the tail.
func TestSyncRefetchesOnlyTheTailOnResync(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 101, 2)
	setCachedStatus(t, st, 101, "RELEASING")
	setSyncedAt(t, st, seriesID, time.Now().Add(-24*time.Hour))

	prov := newFakeProvider()
	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if len(prov.calls) != 1 {
		t.Fatalf("provider called %d times, want 1", len(prov.calls))
	}
	if !prov.notYetAired[101] {
		t.Error("a resync re-paged aired history instead of just the tail")
	}
}

func TestSyncSkipsFreshlySyncedSeries(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 102, 2)
	setCachedStatus(t, st, 102, "RELEASING")
	setSyncedAt(t, st, seriesID, time.Now().Add(-time.Minute))

	prov := newFakeProvider()
	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(prov.calls) != 0 {
		t.Errorf("provider called %d times for a freshly synced series, want 0", len(prov.calls))
	}
}

// A finished title's aired times never change, so it must not resync on the
// releasing cadence.
func TestSyncHoldsFinishedSeriesForTheLongCutoff(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 103, 2)
	setCachedStatus(t, st, 103, "FINISHED")
	setSyncedAt(t, st, seriesID, time.Now().Add(-48*time.Hour))

	prov := newFakeProvider()
	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(prov.calls) != 0 {
		t.Errorf("a finished series resynced after 48h; want it held to the long cutoff")
	}
}

// AniList's coverage thins out badly before ~2015. An empty schedule is a normal
// answer, and re-asking every tick would burn the request budget for nothing.
func TestSyncStampsSeriesWithNoScheduleData(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 104, 2)

	prov := newFakeProvider()
	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if _, ok := syncedAt(t, st, seriesID); !ok {
		t.Fatal("a series with no schedule data was left unsynced, so it retries forever")
	}
	if got, ok := airsAt(t, st, seriesID, 1); ok {
		t.Errorf("item 1 airs_at = %q, want null", got)
	}
}

func TestSyncSkipsUnmonitoredSeries(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, 105, 1)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET monitored = 0 WHERE id = ?`, seriesID); err != nil {
		t.Fatalf("unmonitor: %v", err)
	}

	prov := newFakeProvider()
	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(prov.calls) != 0 {
		t.Errorf("provider called %d times for an unmonitored series, want 0", len(prov.calls))
	}
}

// One pass fetches at most seriesPerPass series, and never-synced series go
// ahead of stale ones, so a newly added title is never queued behind refreshes.
func TestSyncBoundsEachPassAndPrioritizesNeverSynced(t *testing.T) {
	st := coretest.NewStore(t)
	for id := int64(200); id < 206; id++ {
		seedSeries(t, st, id, 1)
	}
	stale := seedSeries(t, st, 210, 1)
	setCachedStatus(t, st, 210, "RELEASING")
	setSyncedAt(t, st, stale, time.Now().Add(-24*time.Hour))

	prov := newFakeProvider()
	svc := newService(t, st, prov)
	if err := svc.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if len(prov.calls) != 5 {
		t.Fatalf("first pass fetched %d series, want the 5-series bound", len(prov.calls))
	}
	for _, id := range prov.calls {
		if id == 210 {
			t.Fatal("the stale series was fetched ahead of never-synced ones")
		}
	}

	if err := svc.SyncOnce(context.Background()); err != nil {
		t.Fatalf("second SyncOnce: %v", err)
	}
	rest := prov.calls[5:]
	if len(rest) != 2 || rest[0] == 210 || rest[1] != 210 {
		t.Fatalf("second pass fetched %v, want the last never-synced series then the stale one", rest)
	}
}

// One unreachable title must not cost every other series its sync.
func TestSyncContinuesPastAFailingSeries(t *testing.T) {
	st := coretest.NewStore(t)
	failing := seedSeries(t, st, 106, 1)
	healthy := seedSeries(t, st, 107, 1)

	prov := newFakeProvider()
	prov.errs[106] = errors.New("boom")
	prov.schedules[107] = []metadata.Airing{{Number: 1, AirsAt: time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)}}

	err := newService(t, st, prov).SyncOnce(context.Background())
	if err == nil {
		t.Fatal("SyncOnce reported success despite a failing series")
	}

	if _, ok := airsAt(t, st, healthy, 1); !ok {
		t.Error("the healthy series was skipped because another one failed")
	}
	// A failed fetch must not be recorded as a successful sync.
	if _, ok := syncedAt(t, st, failing); ok {
		t.Error("the failing series was stamped as synced, so its schedule is never retried")
	}
}

// A provider with no schedule to publish is a supported configuration, not a
// failure: the job no-ops instead of erroring every tick.
func TestSyncNoOpsWithoutTheAiringCapability(t *testing.T) {
	st := coretest.NewStore(t)
	seedSeries(t, st, 108, 1)

	if err := newService(t, st, &plainProvider{}).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce on a provider without schedules: %v", err)
	}
}
