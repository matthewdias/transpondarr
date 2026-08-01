package acquire_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// fakeTitles stands in for catalog.Service: fixed title variants per provider id.
type fakeTitles struct {
	variants map[int64][]string
	err      error
}

func (f fakeTitles) TitleVariants(_ context.Context, id int64) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.variants[id], nil
}

// fakeConfig stands in for settings.Service. Automation is on unless a test
// turns it off, since the sweep is what most of these exercise.
type fakeConfig struct {
	category      string
	automationOff bool
	pinDelay      time.Duration
}

func (f fakeConfig) DownloadCategory() string {
	if f.category == "" {
		return "transpondarr"
	}
	return f.category
}

func (f fakeConfig) AutomationEnabled() bool { return !f.automationOff }

func (f fakeConfig) PinDelayDefault() time.Duration { return f.pinDelay }

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newService wires an acquire.Service over a temp store and the given fakes.
func newService(t *testing.T, st *store.Store, idx indexer.Indexer, titles fakeTitles) (*acquire.Service, *clients.Registry) {
	t.Helper()
	reg := newRegistry(idx, nil)
	return acquire.New(st, reg, titles, fakeConfig{}, discardLogger(), nil), reg
}

// newRegistry holds the fakes acquire reads its clients from (nil = unconfigured).
func newRegistry(idx indexer.Indexer, dl download.Client) *clients.Registry {
	reg := clients.New()
	if idx != nil {
		reg.SetIndexer(idx)
	}
	if dl != nil {
		reg.SetDownload(dl)
	}
	return reg
}

// seedSeries inserts a monitored series with episodes 1..count and no AniList id.
func seedSeries(t *testing.T, st *store.Store, title string, count int) int64 {
	t.Helper()
	return seedAniListSeries(t, st, title, 0, count)
}

// seedAniListSeries is seedSeries with an optional provider id (0 = none), so a
// test can exercise the title-variant lookup.
func seedAniListSeries(t *testing.T, st *store.Store, title string, anilistID int64, count int) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{
		Title:     title,
		Format:    "TV",
		Monitored: 1,
		AnilistID: sql.NullInt64{Int64: anilistID, Valid: anilistID != 0},
	})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	for n := 1; n <= count; n++ {
		if _, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID: s.ID, Kind: "episode", Number: sql.NullInt64{Int64: int64(n), Valid: true},
		}); err != nil {
			t.Fatalf("create item %d: %v", n, err)
		}
	}
	return s.ID
}

// The sanitized stored title is tried first; a zero-result answer falls back to
// the remaining title variants, and the term that produced results is reported.
func TestMatchSeriesFallsBackToVariantTerm(t *testing.T) {
	const english = "Fixture of the Sky Side Story"
	idx := &coretest.FakeIndexer{ByTerm: map[string][]indexer.Release{
		english: {{Title: "[ExampleSubs] Fixture of the Sky Side Story - 03 [1080p]",
			DownloadURL: "magnet:?xt=urn:btih:bb03", Seeders: 50}},
	}}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{variants: map[int64][]string{
		42: {"Sora・no・Fixture Gaiden", english},
	}})
	id := seedAniListSeries(t, st, "Sora・no・Fixture Gaiden", 42, 12)

	m, err := svc.MatchSeries(context.Background(), id)
	if err != nil {
		t.Fatalf("MatchSeries: %v", err)
	}
	if len(idx.Queries) != 2 ||
		idx.Queries[0].Term != "Sora no Fixture Gaiden" || idx.Queries[1].Term != english {
		t.Fatalf("indexer queried with %+v, want the sanitized romaji then the english variant", idx.Queries)
	}
	if m.Term != english {
		t.Errorf("term = %q, want the variant that produced results %q", m.Term, english)
	}
	if m.Series.ID != id {
		t.Errorf("series id = %d, want %d", m.Series.ID, id)
	}
	if len(m.Items) != 12 {
		t.Errorf("loaded %d wanted items, want 12", len(m.Items))
	}
	if len(m.Candidates) != 1 || !m.Candidates[0].Matched ||
		len(m.Candidates[0].Items) != 1 || m.Candidates[0].Items[0] != 3 {
		t.Errorf("candidates = %+v, want one release matched to item 3", m.Candidates)
	}
}

// An unconfigured indexer is reported as its own sentinel, so the HTTP layer can
// map it to a 503 without core knowing about status codes.
func TestMatchSeriesWithoutIndexer(t *testing.T) {
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, nil, fakeTitles{})
	id := seedSeries(t, st, "Placeholder Saga", 12)

	if _, err := svc.MatchSeries(context.Background(), id); !errors.Is(err, acquire.ErrNoIndexer) {
		t.Fatalf("MatchSeries error = %v, want ErrNoIndexer", err)
	}
}

func TestMatchSeriesUnknownSeries(t *testing.T) {
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, &coretest.FakeIndexer{}, fakeTitles{})

	if _, err := svc.MatchSeries(context.Background(), 404); !errors.Is(err, acquire.ErrSeriesNotFound) {
		t.Fatalf("MatchSeries error = %v, want ErrSeriesNotFound", err)
	}
}

// A failing indexer surfaces immediately rather than degrading to zero results.
func TestMatchSeriesIndexerError(t *testing.T) {
	idx := &coretest.FakeIndexer{Err: errors.New("torznab: upstream timeout")}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{})
	id := seedSeries(t, st, "Placeholder Saga", 12)

	_, err := svc.MatchSeries(context.Background(), id)
	if !errors.Is(err, acquire.ErrIndexerSearch) {
		t.Fatalf("MatchSeries error = %v, want ErrIndexerSearch", err)
	}
}

// A metadata lookup that fails must not fail the search: the stored title alone
// is a usable term.
func TestMatchSeriesTolerationOfVariantLookupFailure(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 03 [1080p]",
			DownloadURL: "magnet:?xt=urn:btih:cc03", Seeders: 10},
	}}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{err: errors.New("anilist: rate limited")})
	id := seedAniListSeries(t, st, "Placeholder Saga", 42, 12)

	m, err := svc.MatchSeries(context.Background(), id)
	if err != nil {
		t.Fatalf("MatchSeries: %v", err)
	}
	if m.Term != "Placeholder Saga" {
		t.Errorf("term = %q, want the stored title", m.Term)
	}
	if len(m.Candidates) != 1 || !m.Candidates[0].Matched {
		t.Errorf("candidates = %+v, want one matched release", m.Candidates)
	}
}
