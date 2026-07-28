package browse_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/browse"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

type seasonKey struct {
	season metadata.Season
	year   int
}

// fakeProvider is a metadata.Provider that can chart seasons, recording what it
// was asked for.
type fakeProvider struct {
	entries map[seasonKey][]metadata.SeasonEntry
	errs    map[seasonKey]error
	calls   []seasonKey
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{
		entries: map[seasonKey][]metadata.SeasonEntry{},
		errs:    map[seasonKey]error{},
	}
}

func (f *fakeProvider) Name() string { return "anilist" }

func (f *fakeProvider) Search(context.Context, string) ([]metadata.Candidate, error) {
	return nil, nil
}

func (f *fakeProvider) GetTitle(context.Context, int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	return metadata.TitleMeta{}, nil, nil
}

func (f *fakeProvider) BrowseSeason(_ context.Context, season metadata.Season, year int) ([]metadata.SeasonEntry, error) {
	k := seasonKey{season, year}
	f.calls = append(f.calls, k)
	if err := f.errs[k]; err != nil {
		return nil, err
	}
	return f.entries[k], nil
}

// plainProvider cannot chart a season.
type plainProvider struct{}

func (*plainProvider) Name() string { return "plain" }

func (*plainProvider) Search(context.Context, string) ([]metadata.Candidate, error) {
	return nil, nil
}

func (*plainProvider) GetTitle(context.Context, int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	return metadata.TitleMeta{}, nil, nil
}

func newService(t *testing.T, st *store.Store, prov metadata.Provider) *browse.Service {
	t.Helper()
	return browse.New(st, prov, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func entries(ids ...int64) []metadata.SeasonEntry {
	out := make([]metadata.SeasonEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, metadata.SeasonEntry{ProviderID: id, Titles: metadata.Titles{Romaji: fmt.Sprintf("Show %d", id)}})
	}
	return out
}

// backdate rewrites a cached season's fetched_at, standing in for the passage
// of time.
func backdate(t *testing.T, st *store.Store, season metadata.Season, year int, age time.Duration) {
	t.Helper()
	res, err := st.DB.Exec(`UPDATE season_cache SET fetched_at = ? WHERE provider = 'anilist' AND season = ? AND year = ?`,
		store.FormatTimestamp(time.Now().Add(-age)), string(season), year)
	if err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("backdate matched %d rows, want 1", n)
	}
}

func TestSeasonMissFetchesAndCaches(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.entries[seasonKey{metadata.SeasonSpring, 2026}] = entries(1, 2)
	svc := newService(t, st, prov)

	got, err := svc.Season(context.Background(), metadata.SeasonSpring, 2026)
	if err != nil {
		t.Fatalf("Season: %v", err)
	}
	if len(got) != 2 || got[0].ProviderID != 1 {
		t.Fatalf("got %+v, want the provider's 2 entries", got)
	}

	// Second view is served from cache: no further provider spend.
	if _, err := svc.Season(context.Background(), metadata.SeasonSpring, 2026); err != nil {
		t.Fatalf("Season (cached): %v", err)
	}
	if len(prov.calls) != 1 {
		t.Errorf("provider called %d times across two views, want 1", len(prov.calls))
	}
}

// A stale cached season is still served without provider spend: refresh belongs
// to the job, never the page view.
func TestSeasonStaleStillServedFromCache(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.entries[seasonKey{metadata.SeasonWinter, 2020}] = entries(4)
	svc := newService(t, st, prov)

	if _, err := svc.Season(context.Background(), metadata.SeasonWinter, 2020); err != nil {
		t.Fatalf("Season: %v", err)
	}
	backdate(t, st, metadata.SeasonWinter, 2020, 90*24*time.Hour)

	got, err := svc.Season(context.Background(), metadata.SeasonWinter, 2020)
	if err != nil {
		t.Fatalf("Season (stale): %v", err)
	}
	if len(got) != 1 || got[0].ProviderID != 4 {
		t.Errorf("got %+v, want the stale cached chart", got)
	}
	if len(prov.calls) != 1 {
		t.Errorf("provider called %d times, want 1 (stale view must not refetch)", len(prov.calls))
	}
}

// A provider that cannot browse is a supported configuration: an empty chart,
// not an error.
func TestSeasonWithoutCapabilityIsEmpty(t *testing.T) {
	st := coretest.NewStore(t)
	svc := newService(t, st, &plainProvider{})

	got, err := svc.Season(context.Background(), metadata.SeasonSpring, 2026)
	if err != nil {
		t.Fatalf("Season: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want no entries", got)
	}
}

func TestSeasonProviderErrorPropagates(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.errs[seasonKey{metadata.SeasonSpring, 2026}] = errors.New("rate limited")
	svc := newService(t, st, prov)

	if _, err := svc.Season(context.Background(), metadata.SeasonSpring, 2026); err == nil {
		t.Fatal("expected the provider error to propagate on a cache miss")
	}
}

// A poisoned cache row degrades to a re-fetch, not a failure.
func TestSeasonMalformedRowRefetches(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.entries[seasonKey{metadata.SeasonSummer, 2026}] = entries(8)
	svc := newService(t, st, prov)

	if _, err := st.DB.Exec(`INSERT INTO season_cache (provider, season, year, raw) VALUES ('anilist', 'SUMMER', 2026, 'not json')`); err != nil {
		t.Fatalf("seed poisoned row: %v", err)
	}

	got, err := svc.Season(context.Background(), metadata.SeasonSummer, 2026)
	if err != nil {
		t.Fatalf("Season: %v", err)
	}
	if len(got) != 1 || got[0].ProviderID != 8 {
		t.Errorf("got %+v, want the re-fetched chart", got)
	}
	if len(prov.calls) != 1 {
		t.Errorf("provider called %d times, want 1", len(prov.calls))
	}
}

// The first refresh pass populates the current season with no rows to go on.
func TestRefreshOncePopulatesCurrentSeason(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	season, year := browse.CurrentSeason(time.Now())
	prov.entries[seasonKey{season, year}] = entries(1)
	svc := newService(t, st, prov)

	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if len(prov.calls) != 1 || prov.calls[0] != (seasonKey{season, year}) {
		t.Fatalf("provider calls = %v, want exactly the current season %s %d", prov.calls, season, year)
	}

	// The chart is now served without a provider round trip.
	got, err := svc.Season(context.Background(), season, year)
	if err != nil {
		t.Fatalf("Season: %v", err)
	}
	if len(got) != 1 || len(prov.calls) != 1 {
		t.Errorf("view after refresh: %d entries, %d provider calls, want 1 and 1", len(got), len(prov.calls))
	}
}

// A fresh current season and a young past season leave nothing to do.
func TestRefreshOnceFreshIsIdle(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	season, year := browse.CurrentSeason(time.Now())
	svc := newService(t, st, prov)

	if _, err := svc.Season(context.Background(), season, year); err != nil {
		t.Fatalf("seed current: %v", err)
	}
	if _, err := svc.Season(context.Background(), metadata.SeasonWinter, 2019); err != nil {
		t.Fatalf("seed past: %v", err)
	}
	backdate(t, st, metadata.SeasonWinter, 2019, 7*24*time.Hour) // young for a past season
	prov.calls = nil

	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if len(prov.calls) != 0 {
		t.Errorf("provider calls = %v, want none on a fresh cache", prov.calls)
	}
}

// A stale current season and a stale past season are both re-fetched.
func TestRefreshOnceRefetchesStaleSeasons(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	season, year := browse.CurrentSeason(time.Now())
	svc := newService(t, st, prov)

	if _, err := svc.Season(context.Background(), season, year); err != nil {
		t.Fatalf("seed current: %v", err)
	}
	if _, err := svc.Season(context.Background(), metadata.SeasonWinter, 2019); err != nil {
		t.Fatalf("seed past: %v", err)
	}
	backdate(t, st, season, year, 7*time.Hour)                    // past the 6h current-season TTL
	backdate(t, st, metadata.SeasonWinter, 2019, 31*24*time.Hour) // past the 30d past-season TTL
	prov.calls = nil

	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	want := map[seasonKey]bool{
		{season, year}:                true,
		{metadata.SeasonWinter, 2019}: true,
	}
	if len(prov.calls) != 2 || !want[prov.calls[0]] || !want[prov.calls[1]] {
		t.Errorf("provider calls = %v, want both stale seasons", prov.calls)
	}
}

// One pass is bounded, and the current season goes first.
func TestRefreshOnceBoundsWorkPerPass(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	season, year := browse.CurrentSeason(time.Now())
	svc := newService(t, st, prov)

	for _, k := range []seasonKey{{season, year}, {metadata.SeasonWinter, 2018}, {metadata.SeasonSummer, 2019}} {
		if _, err := svc.Season(context.Background(), k.season, k.year); err != nil {
			t.Fatalf("seed %v: %v", k, err)
		}
		backdate(t, st, k.season, k.year, 400*24*time.Hour)
	}
	prov.calls = nil

	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if len(prov.calls) != 2 {
		t.Fatalf("provider calls = %v, want the 2-season per-pass bound", prov.calls)
	}
	if prov.calls[0] != (seasonKey{season, year}) {
		t.Errorf("first refresh = %v, want the current season first", prov.calls[0])
	}
}

// A provider that cannot browse makes the job a no-op.
func TestRefreshOnceWithoutCapabilityIsNoOp(t *testing.T) {
	st := coretest.NewStore(t)
	svc := newService(t, st, &plainProvider{})
	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
}

// One season's error never costs the rest their refresh.
func TestRefreshOnceContinuesPastOneError(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	season, year := browse.CurrentSeason(time.Now())
	prov.errs[seasonKey{season, year}] = errors.New("rate limited")
	svc := newService(t, st, prov)

	if _, err := svc.Season(context.Background(), metadata.SeasonWinter, 2019); err != nil {
		t.Fatalf("seed past: %v", err)
	}
	backdate(t, st, metadata.SeasonWinter, 2019, 31*24*time.Hour)
	prov.calls = nil

	err := svc.RefreshOnce(context.Background())
	if err == nil {
		t.Fatal("expected the failed season's error to surface")
	}
	if len(prov.calls) != 2 {
		t.Errorf("provider calls = %v, want the stale past season still refreshed", prov.calls)
	}
}

func TestCurrentSeason(t *testing.T) {
	cases := []struct {
		month  time.Month
		season metadata.Season
	}{
		{time.January, metadata.SeasonWinter},
		{time.March, metadata.SeasonWinter},
		{time.April, metadata.SeasonSpring},
		{time.June, metadata.SeasonSpring},
		{time.July, metadata.SeasonSummer},
		{time.September, metadata.SeasonSummer},
		{time.October, metadata.SeasonFall},
		{time.December, metadata.SeasonFall},
	}
	for _, tc := range cases {
		now := time.Date(2026, tc.month, 15, 0, 0, 0, 0, time.UTC)
		season, year := browse.CurrentSeason(now)
		if season != tc.season || year != 2026 {
			t.Errorf("CurrentSeason(%s) = %s %d, want %s 2026", tc.month, season, year, tc.season)
		}
	}
}
