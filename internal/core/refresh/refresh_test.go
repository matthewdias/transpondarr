package refresh_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/core/metadata/dbcache"
	"github.com/matthewdias/transpondarr/internal/core/refresh"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// fakeProvider answers GetTitle from a fixture map, recording who was asked.
type fakeProvider struct {
	episodes map[int64]int
	errs     map[int64]error

	calls []int64 // provider ids, in call order
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{episodes: map[int64]int{}, errs: map[int64]error{}}
}

func (f *fakeProvider) Name() string { return "anilist" }

func (f *fakeProvider) Search(context.Context, string) ([]metadata.Candidate, error) {
	return nil, nil
}

func (f *fakeProvider) GetTitle(_ context.Context, id int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	f.calls = append(f.calls, id)
	if err := f.errs[id]; err != nil {
		return metadata.TitleMeta{}, nil, err
	}
	n := f.episodes[id]
	items := make([]metadata.ItemMeta, 0, n)
	for i := 1; i <= n; i++ {
		items = append(items, metadata.ItemMeta{Number: i})
	}
	return metadata.TitleMeta{ProviderID: id, Episodes: n, Status: "RELEASING"}, items, nil
}

func newService(t *testing.T, st *store.Store, prov metadata.Provider) *refresh.Service {
	t.Helper()
	return refresh.New(st, prov, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// seedTitle inserts a monitored title with items 1..episodes and returns its id.
func seedTitle(t *testing.T, st *store.Store, anilistID int64, episodes int) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO series (provider, provider_id, title, monitored) VALUES ('anilist', ?, 'Placeholder', 1) RETURNING id`,
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

// seedUnmonitoredTitle is the seedTitle a title-level unmonitor produces.
func seedUnmonitoredTitle(t *testing.T, st *store.Store, anilistID int64, episodes int) int64 {
	t.Helper()
	id := seedTitle(t, st, anilistID, episodes)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET monitored = 0 WHERE id = ?`, id); err != nil {
		t.Fatalf("unmonitor series: %v", err)
	}
	return id
}

// seedCache inserts a metadata_cache row; a zero episodes stores NULL, matching
// how dbcache mirrors an unknown count.
func seedCache(t *testing.T, st *store.Store, anilistID int64, status string, episodes int, fetchedAt time.Time) {
	t.Helper()
	var count any
	if episodes > 0 {
		count = episodes
	}
	if _, err := st.DB.ExecContext(context.Background(),
		`INSERT INTO metadata_cache (provider, provider_id, status, episode_count, raw, fetched_at)
		 VALUES ('anilist', ?, ?, ?, '{}', ?)`,
		anilistID, status, count, store.FormatTimestamp(fetchedAt)); err != nil {
		t.Fatalf("seed metadata cache: %v", err)
	}
}

func setSyncedAt(t *testing.T, st *store.Store, titleID int64, at time.Time) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET airing_synced_at = ? WHERE id = ?`, store.FormatTimestamp(at), titleID); err != nil {
		t.Fatalf("set airing_synced_at: %v", err)
	}
}

func syncedAt(t *testing.T, st *store.Store, titleID int64) (string, bool) {
	t.Helper()
	var stored *string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT airing_synced_at FROM series WHERE id = ?`, titleID).Scan(&stored); err != nil {
		t.Fatalf("read airing_synced_at: %v", err)
	}
	if stored == nil {
		return "", false
	}
	return *stored, true
}

// items returns number -> in_library for a title's wanted items.
func items(t *testing.T, st *store.Store, titleID int64) map[int]int {
	t.Helper()
	rows, err := st.DB.QueryContext(context.Background(),
		`SELECT number, in_library FROM wanted_items WHERE series_id = ?`, titleID)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]int{}
	for rows.Next() {
		var number, inLibrary int
		if err := rows.Scan(&number, &inLibrary); err != nil {
			t.Fatalf("scan item: %v", err)
		}
		out[number] = inLibrary
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate items: %v", err)
	}
	return out
}

func TestRefreshGrowsTitleWhenEpisodeCountRises(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 13
	id := seedTitle(t, st, 100, 12)

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	got := items(t, st, id)
	if len(got) != 13 {
		t.Fatalf("got %d items, want 13: %v", len(got), got)
	}
	if inLibrary, ok := got[13]; !ok || inLibrary != 0 {
		t.Errorf("item 13: in_library=%d ok=%v, want a fresh in_library=0 item", inLibrary, ok)
	}
}

func TestRefreshLeavesExistingItemsUntouched(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	id := seedTitle(t, st, 100, 12)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE wanted_items SET in_library = 1 WHERE series_id = ? AND number = 5`, id); err != nil {
		t.Fatalf("mark item had: %v", err)
	}

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	got := items(t, st, id)
	if len(got) != 12 {
		t.Fatalf("got %d items, want 12 (no duplicates): %v", len(got), got)
	}
	if got[5] != 1 {
		t.Errorf("item 5 in_library = %d, want 1 (refresh must not clobber it)", got[5])
	}
}

func TestRefreshFillsItemsForATitleAddedWithNullCount(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	id := seedTitle(t, st, 100, 0)

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if got := items(t, st, id); len(got) != 12 {
		t.Fatalf("got %d items, want 12 once the provider publishes a count: %v", len(got), got)
	}
}

func TestRefreshClearsAiringStampWhenTheTitleGrows(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 13
	id := seedTitle(t, st, 100, 12)
	setSyncedAt(t, st, id, time.Now())

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if stamp, ok := syncedAt(t, st, id); ok {
		t.Errorf("airing_synced_at = %q, want cleared so the airing sync re-pages the new item", stamp)
	}
}

// A new episode is worth looking for now, whatever backoff the sweep had
// accumulated while the title had nothing left to find.
func TestGrowthResetsSearchCadence(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 13
	id := seedTitle(t, st, 100, 12)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 8, next_search_at = ? WHERE id = ?`,
		store.FormatTimestamp(time.Now().Add(24*time.Hour)), id); err != nil {
		t.Fatalf("seed a long backoff: %v", err)
	}

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	var backoff int
	var next *string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_backoff, next_search_at FROM series WHERE id = ?`, id).Scan(&backoff, &next); err != nil {
		t.Fatalf("read search cadence: %v", err)
	}
	if backoff != 0 || next != nil {
		t.Errorf("cadence = backoff %d, next %v; want it reset so the sweep looks for the new episode", backoff, next)
	}
}

// A refresh that inserts nothing must not hand a backed-off title a free retry.
func TestRefreshKeepsSearchCadenceWhenNothingNew(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	id := seedTitle(t, st, 100, 12)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET search_backoff = 8 WHERE id = ?`, id); err != nil {
		t.Fatalf("seed a long backoff: %v", err)
	}

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	var backoff int
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_backoff FROM series WHERE id = ?`, id).Scan(&backoff); err != nil {
		t.Fatalf("read search cadence: %v", err)
	}
	if backoff != 8 {
		t.Errorf("search_backoff = %d, want the accumulated 8", backoff)
	}
}

func TestRefreshKeepsAiringStampWhenNothingNew(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	id := seedTitle(t, st, 100, 12)
	setSyncedAt(t, st, id, time.Now())

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if _, ok := syncedAt(t, st, id); !ok {
		t.Error("airing_synced_at cleared by a refresh that inserted nothing")
	}
}

func TestRefreshSkipsFreshlyFetchedTitles(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	seedTitle(t, st, 100, 12)
	seedCache(t, st, 100, "RELEASING", 12, time.Now())

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if len(prov.calls) != 0 {
		t.Errorf("provider called %v, want no calls for a fresh snapshot", prov.calls)
	}
}

func TestRefreshHoldsFinishedTitlesForTheLongCutoff(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	seedTitle(t, st, 100, 12)
	seedCache(t, st, 100, "FINISHED", 12, time.Now().Add(-7*24*time.Hour))

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if len(prov.calls) != 0 {
		t.Errorf("provider called %v, want a finished series held for the long cutoff", prov.calls)
	}
}

func TestRefreshRefetchesFinishedTitlesPastTheLongCutoff(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	seedTitle(t, st, 100, 12)
	seedCache(t, st, 100, "FINISHED", 12, time.Now().Add(-31*24*time.Hour))

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if len(prov.calls) != 1 {
		t.Errorf("provider called %v, want one call past the long cutoff", prov.calls)
	}
}

// Replaces TestRefreshUsesShortCutoffWhenTheCountIsUnknown, whose assertion this
// change deliberately inverts: a count AniList will never publish is not worth
// re-asking every 6 hours (#151).
func TestRefreshHoldsAnUnknownCountTitleForTheMiddleCutoff(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	seedTitle(t, st, 100, 0)
	seedCache(t, st, 100, "FINISHED", 0, time.Now().Add(-7*time.Hour))

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if len(prov.calls) != 0 {
		t.Errorf("provider called %v, want an unknown count held for the middle cutoff", prov.calls)
	}
}

func TestRefreshRefetchesAnUnknownCountTitlePastTheMiddleCutoff(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	seedTitle(t, st, 100, 0)
	seedCache(t, st, 100, "FINISHED", 0, time.Now().Add(-8*24*time.Hour))

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if len(prov.calls) != 1 {
		t.Errorf("provider called %v, want one call past the middle cutoff", prov.calls)
	}
}

// A count the provider does publish keeps the long cutoff, which is what makes
// the middle tier a third arm rather than a demotion of every finished title.
func TestRefreshHoldsAKnownCountTitleForTheLongCutoff(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	seedTitle(t, st, 100, 12)
	seedCache(t, st, 100, "FINISHED", 12, time.Now().Add(-8*24*time.Hour))

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if len(prov.calls) != 0 {
		t.Errorf("provider called %v, want a known count held for the long cutoff", prov.calls)
	}
}

// An unmonitored title's cached status would otherwise freeze at its add-time
// value, and the airing sync reads that status to pick its TTL tier -- so one
// added while RELEASING would ride the 6h tier forever, never reaching 30d (#183).
func TestRefreshIncludesUnmonitoredTitles(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 12
	id := seedUnmonitoredTitle(t, st, 100, 0)

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if len(prov.calls) != 1 {
		t.Fatalf("provider called %v, want the unmonitored series refreshed once", prov.calls)
	}
	var items int
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM wanted_items WHERE series_id = ?`, id).Scan(&items); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if items != 12 {
		t.Errorf("items = %d, want 12 -- an unmonitored title still grows", items)
	}
}

// The budget's priority is unchanged: an unmonitored title takes a slot only
// once no monitored one wants it, however long it has gone unfetched.
func TestRefreshGivesMonitoredTitlesEverySlot(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	// Five stale monitored title fill the pass; the unmonitored one is
	// never-fetched, so it outranks them on every key but monitoring.
	for id := int64(1); id <= 5; id++ {
		prov.episodes[id] = 12
		seedTitle(t, st, id, 12)
		seedCache(t, st, id, "RELEASING", 12, time.Now().Add(-24*time.Hour))
	}
	prov.episodes[10] = 12
	seedUnmonitoredTitle(t, st, 10, 0)

	svc := newService(t, st, prov)
	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if len(prov.calls) != 5 {
		t.Fatalf("first pass fetched %v, want the 5-series bound", prov.calls)
	}
	for _, id := range prov.calls {
		if id == 10 {
			t.Fatal("the unmonitored series took a slot a monitored one wanted")
		}
	}

	// The raw fake never stamps a cache row, so settle the five by hand the way
	// the cached provider would -- without it no pass ever runs out of monitored
	// work and "ordered last" would be indistinguishable from "starved forever".
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE metadata_cache SET fetched_at = ? WHERE provider_id BETWEEN 1 AND 5`,
		store.FormatTimestamp(time.Now())); err != nil {
		t.Fatalf("settle the monitored cache rows: %v", err)
	}

	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("second RefreshOnce: %v", err)
	}
	if rest := prov.calls[5:]; len(rest) != 1 || rest[0] != 10 {
		t.Fatalf("second pass fetched %v, want only the unmonitored series", rest)
	}
}

func TestRefreshBoundsEachPassAndPrioritizesNeverFetched(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	// Six stale title and one never fetched: a pass is capped at five, and the
	// never-fetched one must make the cut.
	for i := int64(1); i <= 6; i++ {
		prov.episodes[i] = 12
		seedTitle(t, st, i, 12)
		seedCache(t, st, i, "RELEASING", 12, time.Now().Add(-24*time.Hour))
	}
	prov.episodes[7] = 12
	seedTitle(t, st, 7, 12)

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	if len(prov.calls) != 5 {
		t.Fatalf("provider called %d times, want a pass bounded at 5: %v", len(prov.calls), prov.calls)
	}
	if prov.calls[0] != 7 {
		t.Errorf("first call = %d, want the never-fetched series (7) first", prov.calls[0])
	}
}

// Through the real cached provider, a refresh both grows the title and stamps
// the cache row, so the next pass finds nothing due and costs no request.
func TestRefreshThroughCachedProviderSettlesUntilStale(t *testing.T) {
	st := coretest.NewStore(t)
	inner := newFakeProvider()
	inner.episodes[100] = 13
	id := seedTitle(t, st, 100, 12)
	seedCache(t, st, 100, "RELEASING", 12, time.Now().Add(-7*time.Hour))

	svc := newService(t, st, metadata.Cached(inner, dbcache.New(st.Q)))
	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if got := items(t, st, id); len(got) != 13 {
		t.Fatalf("got %d items, want 13: %v", len(got), got)
	}

	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("second RefreshOnce: %v", err)
	}
	if len(inner.calls) != 1 {
		t.Errorf("provider called %d times, want 1 (second pass should find nothing due)", len(inner.calls))
	}
}

func TestRefreshContinuesPastAFailingTitle(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.errs[1] = errors.New("boom")
	prov.episodes[2] = 12
	seedTitle(t, st, 1, 0)
	idB := seedTitle(t, st, 2, 0)

	err := newService(t, st, prov).RefreshOnce(context.Background())
	if err == nil {
		t.Fatal("RefreshOnce: want the failing series' error surfaced, got nil")
	}

	if got := items(t, st, idB); len(got) != 12 {
		t.Errorf("series 2 got %d items, want 12 despite series 1 failing", len(got))
	}
}

// Refresh growth reads the same cut, with the two consequences of an insert
// disentangled: the air-date sync ignores monitoring entirely, so the stamp
// still clears, while the search cadence is only worth resetting for something
// the sweep will actually look for.
func TestRefreshHonoursTheTitleMonitorCut(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 13
	id := seedTitle(t, st, 100, 12)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET monitor_new_from = NULL, search_backoff = 8, next_search_at = ? WHERE id = ?`,
		store.FormatTimestamp(time.Now().Add(24*time.Hour)), id); err != nil {
		t.Fatalf("narrow the series and seed a long backoff: %v", err)
	}
	setSyncedAt(t, st, id, time.Now())

	if err := newService(t, st, prov).RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	var monitored int64
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT monitored FROM wanted_items WHERE series_id = ? AND number = 13`, id).Scan(&monitored); err != nil {
		t.Fatalf("read the new item: %v", err)
	}
	if monitored != 0 {
		t.Errorf("new item monitored = %d, want 0 under a null cut", monitored)
	}
	if stamp, ok := syncedAt(t, st, id); ok {
		t.Errorf("airing_synced_at = %q, want cleared -- the air-date sync ignores monitoring", stamp)
	}
	var backoff int
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_backoff FROM series WHERE id = ?`, id).Scan(&backoff); err != nil {
		t.Fatalf("read search cadence: %v", err)
	}
	if backoff != 8 {
		t.Errorf("search_backoff = %d, want the accumulated 8 -- an unmonitored insert is not news", backoff)
	}
}
