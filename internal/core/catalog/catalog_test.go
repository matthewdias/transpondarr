package catalog

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// fakeProvider is a metadata.Provider whose responses are set per-test. It
// records how often GetTitle is called so tests can assert the store, not the
// network, is what AddSeries reads on a second add.
type fakeProvider struct {
	meta     metadata.TitleMeta
	items    []metadata.ItemMeta
	getErr   error
	getCalls int
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Search(context.Context, string) ([]metadata.Candidate, error) {
	return nil, nil
}

func (f *fakeProvider) GetTitle(context.Context, int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	f.getCalls++
	if f.getErr != nil {
		return metadata.TitleMeta{}, nil, f.getErr
	}
	return f.meta, f.items, nil
}

// fakeCachedProvider is a provider carrying metadata's cache-read capability.
type fakeCachedProvider struct {
	fakeProvider
	cachedMeta metadata.TitleMeta
	cachedOK   bool
	cacheCalls int
}

func (f *fakeCachedProvider) TitleFromCache(context.Context, int64) (metadata.TitleMeta, []metadata.ItemMeta, bool, error) {
	f.cacheCalls++
	if !f.cachedOK {
		return metadata.TitleMeta{}, nil, false, nil
	}
	return f.cachedMeta, nil, true, nil
}

func TestCachedTitleVariantsHitCostsNoProviderRequest(t *testing.T) {
	prov := &fakeCachedProvider{
		cachedMeta: metadata.TitleMeta{Titles: metadata.Titles{
			Romaji: "Cowboy Bebop", English: "Cowboy Bebop", Native: "カウボーイビバップ",
		}},
		cachedOK: true,
	}
	svc := NewService(coretest.NewStore(t), prov)

	got, ok, err := svc.CachedTitleVariants(context.Background(), 42)
	if err != nil {
		t.Fatalf("CachedTitleVariants: %v", err)
	}
	if !ok {
		t.Fatal("a cached snapshot reported as a miss")
	}
	if len(got) != 2 || got[0] != "Cowboy Bebop" || got[1] != "カウボーイビバップ" {
		t.Errorf("variants = %v, want the deduped romaji and native", got)
	}
	if prov.getCalls != 0 {
		t.Errorf("provider GetTitle called %d times, want 0", prov.getCalls)
	}
	if prov.cacheCalls != 1 {
		t.Errorf("cache read called %d times, want 1", prov.cacheCalls)
	}
}

func TestCachedTitleVariantsMissDoesNotFallThroughToProvider(t *testing.T) {
	prov := &fakeCachedProvider{cachedOK: false}
	svc := NewService(coretest.NewStore(t), prov)

	got, ok, err := svc.CachedTitleVariants(context.Background(), 42)
	if err != nil || ok || got != nil {
		t.Fatalf("got %v / %v / %v, want nil / false / nil on a miss", got, ok, err)
	}
	if prov.getCalls != 0 {
		t.Errorf("provider GetTitle called %d times on a miss, want 0", prov.getCalls)
	}
}

// A provider without the capability is a supported configuration, not an error.
func TestCachedTitleVariantsWithoutCacheCapability(t *testing.T) {
	prov := &fakeProvider{}
	svc := NewService(coretest.NewStore(t), prov)

	got, ok, err := svc.CachedTitleVariants(context.Background(), 42)
	if err != nil || ok || got != nil {
		t.Fatalf("got %v / %v / %v, want nil / false / nil without the capability", got, ok, err)
	}
	if prov.getCalls != 0 {
		t.Errorf("provider GetTitle called %d times, want 0", prov.getCalls)
	}
}

func TestAddSeriesPersistsTitleAndItems(t *testing.T) {
	st := coretest.NewStore(t)
	prov := &fakeProvider{
		meta: metadata.TitleMeta{
			ProviderID: 42,
			Titles:     metadata.Titles{Romaji: "Cowboy Bebop", English: "Cowboy Bebop"},
			Format:     "TV",
			Episodes:   3,
			Status:     "FINISHED",
		},
		items: []metadata.ItemMeta{{Number: 1}, {Number: 2}, {Number: 3}},
	}
	svc := NewService(st, prov)

	title, err := svc.AddSeries(context.Background(), prov.Name(), 42, true, MonitorAll, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	if title.ID == 0 {
		t.Error("expected a persisted series id, got 0")
	}
	if title.Name != "Cowboy Bebop" {
		t.Errorf("name = %q, want Cowboy Bebop", title.Name)
	}
	if title.Format != domain.FormatTV {
		t.Errorf("format = %q, want TV", title.Format)
	}
	if !title.Monitored {
		t.Error("expected monitored = true")
	}
	if len(title.Items) != 3 {
		t.Fatalf("returned %d items, want 3", len(title.Items))
	}
	for i, it := range title.Items {
		if it.Number != i+1 {
			t.Errorf("item %d has number %d, want %d", i, it.Number, i+1)
		}
		if it.Kind != domain.KindEpisode {
			t.Errorf("item %d kind = %q, want episode", i, it.Kind)
		}
	}

	if title.Provider != prov.Name() || title.ProviderID != 42 {
		t.Errorf("identity = (%q, %d), want (%q, 42)", title.Provider, title.ProviderID, prov.Name())
	}

	// Verify the rows actually landed in the DB, not just the returned struct.
	srow, err := st.Q.GetSeriesByProviderID(context.Background(), db.GetSeriesByProviderIDParams{
		Provider:   sql.NullString{String: prov.Name(), Valid: true},
		ProviderID: sql.NullInt64{Int64: 42, Valid: true},
	})
	if err != nil {
		t.Fatalf("GetSeriesByProviderID: %v", err)
	}
	if srow.Title != "Cowboy Bebop" || srow.Monitored != 1 {
		t.Errorf("stored series = %+v, want title Cowboy Bebop / monitored 1", srow)
	}
	if srow.Provider.String != prov.Name() {
		t.Errorf("stored provider = %q, want %q", srow.Provider.String, prov.Name())
	}
	rows, err := st.Q.ListWantedItems(context.Background(), srow.ID)
	if err != nil {
		t.Fatalf("ListWantedItems: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("stored %d wanted items, want 3", len(rows))
	}
}

func TestAddSeriesIsIdempotentByProviderID(t *testing.T) {
	st := coretest.NewStore(t)
	prov := &fakeProvider{
		meta:  metadata.TitleMeta{ProviderID: 7, Titles: metadata.Titles{Romaji: "Trigun"}, Format: "TV", Episodes: 1},
		items: []metadata.ItemMeta{{Number: 1}},
	}
	svc := NewService(st, prov)

	if _, err := svc.AddSeries(context.Background(), prov.Name(), 7, true, MonitorAll, 0); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}

	_, err := svc.AddSeries(context.Background(), prov.Name(), 7, true, MonitorAll, 0)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second AddSeries error = %v, want ErrAlreadyExists", err)
	}

	// The duplicate must be rejected before the provider is consulted again, and
	// must not create a second series row.
	if prov.getCalls != 1 {
		t.Errorf("provider GetTitle called %d times, want 1 (duplicate short-circuits)", prov.getCalls)
	}
	all, err := st.Q.ListSeries(context.Background())
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("stored %d series, want 1", len(all))
	}
}

// Idempotency is keyed on the pair, so an id that collides across id spaces is
// not mistaken for a title we already track. Deduping the two as one title needs
// the cross-reference layer (#189) and is deliberately not attempted here.
func TestAddSeriesIdempotencyIsScopedToTheProvider(t *testing.T) {
	st := coretest.NewStore(t)
	prov := &fakeProvider{
		meta:  metadata.TitleMeta{ProviderID: 7, Titles: metadata.Titles{Romaji: "Trigun"}, Format: "TV", Episodes: 1},
		items: []metadata.ItemMeta{{Number: 1}},
	}
	svc := NewService(st, prov)

	if _, err := svc.AddSeries(context.Background(), prov.Name(), 7, true, MonitorAll, 0); err != nil {
		t.Fatalf("first AddSeries: %v", err)
	}
	// The same number in another id space is another title, and this provider
	// cannot read it -- so it is refused for naming an unreachable id space, never
	// for colliding with the row above.
	_, err := svc.AddSeries(context.Background(), "mal", 7, true, MonitorAll, 0)
	if !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("cross-provider AddSeries error = %v, want ErrUnknownProvider", err)
	}
}

// A releasing title with an unknown episode count yields zero items; the series
// is still created so a later refresh can fill items in.
func TestAddSeriesWithUnknownEpisodeCount(t *testing.T) {
	st := coretest.NewStore(t)
	prov := &fakeProvider{
		meta:  metadata.TitleMeta{ProviderID: 9, Titles: metadata.Titles{Romaji: "Ongoing Show"}, Format: "TV", Episodes: 0, Status: "RELEASING"},
		items: nil,
	}
	svc := NewService(st, prov)

	title, err := svc.AddSeries(context.Background(), prov.Name(), 9, true, MonitorAll, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if len(title.Items) != 0 {
		t.Errorf("returned %d items, want 0", len(title.Items))
	}
	rows, err := st.Q.ListWantedItems(context.Background(), title.ID)
	if err != nil {
		t.Fatalf("ListWantedItems: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("stored %d wanted items, want 0", len(rows))
	}
}

// A provider failure must surface as an error and leave nothing behind (the
// existence check happens before the fetch, so no partial series row).
func TestAddSeriesProviderErrorPersistsNothing(t *testing.T) {
	st := coretest.NewStore(t)
	prov := &fakeProvider{getErr: errors.New("boom")}
	svc := NewService(st, prov)

	if _, err := svc.AddSeries(context.Background(), prov.Name(), 1, true, MonitorAll, 0); err == nil {
		t.Fatal("expected an error from a failing provider")
	}
	all, err := st.Q.ListSeries(context.Background())
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("stored %d series after a failed add, want 0", len(all))
	}
}
