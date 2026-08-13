package settings

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/library"
	"github.com/matthewdias/transpondarr/internal/store"
)

// A saved movies root reaches the rebuilt target, not just the settings table:
// the whole point of the setting is that a movie placed after the save lands
// there without a restart.
func TestUpdateLibraryWiresTheMoviesRootIntoTheLiveTarget(t *testing.T) {
	svc, reg, st := newTestService(t)
	ctx := context.Background()
	series, movies := t.TempDir(), t.TempDir()

	if err := svc.UpdateLibrary(ctx, LibraryConfig{Dir: series, MoviesDir: movies, Mode: "copy"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got, _ := st.Q.GetSetting(ctx, keyLibraryMoviesDir); got != movies {
		t.Fatalf("movies dir persisted as %q, want %q", got, movies)
	}
	if got := svc.Snapshot().Library.MoviesDir; got != movies {
		t.Fatalf("snapshot movies dir = %q, want %q", got, movies)
	}

	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("video-bytes"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dest, err := reg.Library().Place(ctx, library.ImportRequest{
		SourcePath: src,
		Title:      domain.Title{Name: "Placeholder Film", Format: domain.FormatMovie, Year: 2019},
		Item:       domain.WantedItem{Number: 1, Kind: domain.KindMovie},
	})
	if err != nil {
		t.Fatalf("place a movie through the rebuilt target: %v", err)
	}
	if !strings.HasPrefix(dest, movies+string(os.PathSeparator)) {
		t.Errorf("movie placed at %q, want it under the movies root %q", dest, movies)
	}
}

func TestLibraryMoviesRootFromEnvBaseline(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })
	base := &config.Config{LibraryDir: "/media/Anime", LibraryMoviesDir: "/media/Anime Films"}
	svc, err := New(context.Background(), st, base, clients.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if got := svc.Snapshot().Library.MoviesDir; got != "/media/Anime Films" {
		t.Fatalf("env movies dir = %q, want /media/Anime Films", got)
	}
}

// The Test button covers both roots, so a movies directory that does not exist
// is caught in Settings rather than by the first film that imports.
func TestTestLibraryChecksTheMoviesRoot(t *testing.T) {
	svc, _, _ := newTestService(t)
	series := t.TempDir()

	if err := svc.TestLibrary(context.Background(), LibraryConfig{Dir: series}); err != nil {
		t.Fatalf("an unset movies root is not an error: %v", err)
	}
	err := svc.TestLibrary(context.Background(), LibraryConfig{
		Dir: series, MoviesDir: filepath.Join(series, "nope"),
	})
	if err == nil {
		t.Fatal("expected an error for a movies root that does not exist")
	}
	if !strings.Contains(err.Error(), "movies") {
		t.Errorf("error %q should name the movies directory as the failing one", err)
	}
}
