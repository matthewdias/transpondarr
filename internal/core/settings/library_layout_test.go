package settings

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/library"
	"github.com/matthewdias/transpondarr/internal/store"
)

func placeEpisode(t *testing.T, target library.Target, name string, number int) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("video-bytes"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dest, err := target.Place(context.Background(), library.ImportRequest{
		SourcePath: src,
		Title:      domain.Title{Name: name},
		Item:       domain.WantedItem{Number: number, Kind: domain.KindEpisode},
	})
	if err != nil {
		t.Fatalf("place an episode: %v", err)
	}
	return dest
}

// The upgrade guarantee: an install that predates the setting has no row for it
// and no env var, and must keep placing exactly where its files already are.
func TestExistingInstallKeepsSeasonFoldersWithNoLayoutStored(t *testing.T) {
	svc, reg, st := newTestService(t)
	ctx := context.Background()
	series := t.TempDir()

	if _, err := st.Q.GetSetting(ctx, keyLibraryLayout); err == nil {
		t.Fatal("a fresh install should have no stored series layout")
	}
	if err := svc.UpdateLibrary(ctx, LibraryConfig{Dir: series, Mode: "copy"}); err != nil {
		t.Fatalf("update: %v", err)
	}

	dest := placeEpisode(t, reg.Library(), "Placeholder Saga", 5)
	want := filepath.Join(series, "Placeholder Saga", "Season 01", "Placeholder Saga - S01E05.mkv")
	if dest != want {
		t.Errorf("dest = %q, want the season-folder layout %q", dest, want)
	}
	// The select must show the effective layout rather than an empty option.
	if got := svc.Snapshot().Library.SeriesLayout; got != "season_folders" {
		t.Errorf("snapshot layout = %q, want season_folders", got)
	}
}

// The setting reaching the live target without a restart is the whole point.
func TestUpdateLibraryWiresTheFlatLayoutIntoTheLiveTarget(t *testing.T) {
	svc, reg, st := newTestService(t)
	ctx := context.Background()
	series := t.TempDir()

	if err := svc.UpdateLibrary(ctx, LibraryConfig{Dir: series, SeriesLayout: "flat", Mode: "copy"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got, _ := st.Q.GetSetting(ctx, keyLibraryLayout); got != "flat" {
		t.Fatalf("layout persisted as %q, want flat", got)
	}
	if got := svc.Snapshot().Library.SeriesLayout; got != "flat" {
		t.Fatalf("snapshot layout = %q, want flat", got)
	}

	dest := placeEpisode(t, reg.Library(), "Placeholder Saga", 5)
	want := filepath.Join(series, "Placeholder Saga", "Placeholder Saga - S01E05.mkv")
	if dest != want {
		t.Errorf("dest = %q, want the flat layout %q", dest, want)
	}
}

func TestLibrarySeriesLayoutFromEnvBaseline(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })

	series := t.TempDir()
	base := &config.Config{LibraryDir: series, LibrarySeriesLayout: "flat", ImportMode: "copy"}
	reg := clients.New()
	svc, err := New(context.Background(), st, base, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if got := svc.Snapshot().Library.SeriesLayout; got != "flat" {
		t.Fatalf("env layout = %q, want flat", got)
	}
	dest := placeEpisode(t, reg.Library(), "Placeholder Saga", 5)
	if want := filepath.Join(series, "Placeholder Saga", "Placeholder Saga - S01E05.mkv"); dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}
}

// An unrecognised layout is refused rather than silently defaulted: the stored
// value is what the next start reads, so accepting it would hide the typo.
func TestUpdateLibraryRejectsAnUnknownSeriesLayout(t *testing.T) {
	svc, _, _ := newTestService(t)
	err := svc.UpdateLibrary(context.Background(), LibraryConfig{Dir: t.TempDir(), SeriesLayout: "seasons"})
	if err == nil {
		t.Fatal("expected an error for an unknown series layout")
	}
	if !ValidSeriesLayout("") {
		t.Error("an omitted layout must stay valid and mean the default")
	}
}
