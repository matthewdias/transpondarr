package importer

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/library/mediaserver"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seedMovieGrab creates a movie title with its single grabbed item.
func seedMovieGrab(t *testing.T, st *store.Store, title, hash string, year int64) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{
		Title: title, Format: string(domain.FormatMovie), Year: year, Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create title: %v", err)
	}
	item, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
		SeriesID: s.ID, Kind: string(domain.KindMovie),
		Number: sql.NullInt64{Int64: 1, Valid: true}, Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: item.ID, InfoHash: hash, ReleaseTitle: title + " release", Status: statusGrabbed,
	}); err != nil {
		t.Fatalf("upsert grab: %v", err)
	}
	return s.ID
}

// The target routes on format and names on year, so both have to reach Place —
// neither is derivable from the wanted item.
func TestImportPassesFormatAndYearToTheLibrary(t *testing.T) {
	st := coretest.NewStore(t)
	seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)

	dl := completedSource(t, "abc")
	target := &coretest.FakeLibrary{}
	im := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(target.Placed) != 1 {
		t.Fatalf("Place called %d times, want 1", len(target.Placed))
	}
	got := target.Placed[0].Title
	if got.Format != domain.FormatMovie || got.Year != 2019 {
		t.Errorf("placed title = %+v, want format MOVIE and year 2019", got)
	}
}

// The missing-root decision, end to end: a movie grabbed with no movies root
// configured holds with a legible error instead of landing in the series root,
// and imports on the next scan once the root is set.
func TestMovieWithoutAMoviesRootHoldsAndThenSelfHeals(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	ctx := context.Background()

	series, movies := t.TempDir(), t.TempDir()
	dl := completedSource(t, "abc")
	unconfigured := fakeSource{dl: dl, lib: mediaserver.New(mediaserver.Roots{Series: series}, "copy", nil)}

	if err := New(st, unconfigured, discardLogger(), noRecorder{}, nil).ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != statusGrabbed {
		t.Errorf("status = %q, want it still grabbed: a missing root is a config error, not a settled import", g.Status)
	}
	if !strings.Contains(g.LastError.String, "movies library directory") {
		t.Errorf("last_error = %q, want it to name the unconfigured movies directory", g.LastError.String)
	}
	if items, _ := st.Q.ListWantedItems(ctx, seriesID); items[0].InLibrary != 0 {
		t.Error("the item must not read as held when nothing was placed")
	}
	if entries, _ := os.ReadDir(series); len(entries) != 0 {
		t.Errorf("the series root holds %d entries; a movie must never fall back into it", len(entries))
	}

	configured := fakeSource{dl: dl, lib: mediaserver.New(mediaserver.Roots{Series: series, Movies: movies}, "copy", nil)}
	if err := New(st, configured, discardLogger(), noRecorder{}, nil).ScanOnce(ctx); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if g := grabByHash(t, st, "abc"); g.Status != statusImported {
		t.Errorf("status = %q, want imported once the root is configured", g.Status)
	}
	want := filepath.Join(movies, "Placeholder Film (2019)", "Placeholder Film (2019).mkv")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("stat %q: %v", want, err)
	}
}
