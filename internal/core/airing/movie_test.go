package airing_test

import (
	"context"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// seedMovie inserts a monitored movie with its single kind='movie' item.
func seedMovie(t *testing.T, st *store.Store, providerID int64) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO series (provider, provider_id, title, format, monitored)
		 VALUES ('anilist', ?, 'Sample Film', 'MOVIE', 1) RETURNING id`, providerID).Scan(&id); err != nil {
		t.Fatalf("insert movie series: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO wanted_items (series_id, kind, number) VALUES (?, 'movie', 1)`, id); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	return id
}

func itemCount(t *testing.T, st *store.Store, seriesID int64) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM wanted_items WHERE series_id = ?`, seriesID).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	return n
}

// AniList does publish schedule nodes for films with a TV/streaming premiere.
// One node at episode 1 is desirable: it dates the item we already have.
func TestMovieScheduleUpsertsKindMovie(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedMovie(t, st, 300)

	prov := newFakeProvider()
	prov.schedules[300] = []metadata.Airing{
		{Number: 1, AirsAt: time.Date(2026, 3, 6, 15, 30, 0, 0, time.UTC)},
	}
	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if n := itemCount(t, st, seriesID); n != 1 {
		t.Errorf("items after sync = %d, want 1", n)
	}
	if _, ok := airsAt(t, st, seriesID, 1); !ok {
		t.Error("the movie's item was left undated; the node at episode 1 should date it")
	}
	var kind string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT kind FROM wanted_items WHERE series_id = ?`, seriesID).Scan(&kind); err != nil {
		t.Fatalf("read kind: %v", err)
	}
	if kind != string(domain.KindMovie) {
		t.Errorf("kind = %q, want movie", kind)
	}
}

// A node numbered past 1 would create items 2..N and break the one-item
// invariant the whole movie Format rests on.
func TestMovieScheduleNeverCreatesASecondItem(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedMovie(t, st, 301)

	prov := newFakeProvider()
	prov.schedules[301] = []metadata.Airing{
		{Number: 1, AirsAt: time.Date(2026, 3, 6, 15, 30, 0, 0, time.UTC)},
		{Number: 3, AirsAt: time.Date(2026, 3, 20, 15, 30, 0, 0, time.UTC)},
	}
	if err := newService(t, st, prov).SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	if n := itemCount(t, st, seriesID); n != 1 {
		t.Errorf("items after sync = %d, want 1 (a movie has exactly one)", n)
	}
}

// The stamp-even-when-empty behaviour is what stops an unschedulable title being
// re-asked every tick; a movie is the common case of one.
func TestMovieWithNoScheduleIsStampedAndNotReasked(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedMovie(t, st, 302)

	prov := newFakeProvider()
	svc := newService(t, st, prov)
	if err := svc.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if _, ok := syncedAt(t, st, seriesID); !ok {
		t.Fatal("a movie with no schedule was left unsynced, so it retries forever")
	}

	calls := len(prov.calls)
	if err := svc.SyncOnce(context.Background()); err != nil {
		t.Fatalf("second SyncOnce: %v", err)
	}
	if len(prov.calls) != calls {
		t.Errorf("provider asked again immediately (%d -> %d calls)", calls, len(prov.calls))
	}
	if n := itemCount(t, st, seriesID); n != 1 {
		t.Errorf("items after sync = %d, want 1", n)
	}
}
