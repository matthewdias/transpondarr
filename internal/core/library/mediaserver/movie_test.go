package mediaserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/library"
)

func movieReq(src, title string, year int) library.ImportRequest {
	return library.ImportRequest{
		SourcePath: src,
		Title:      domain.Title{Name: title, Format: domain.FormatMovie, Year: year},
		Item:       domain.WantedItem{Number: 1, Kind: domain.KindMovie},
	}
}

func TestPlaceMovieUsesTheMoviesRoot(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	series, movies := t.TempDir(), t.TempDir()

	dest, err := New(Roots{Series: series, Movies: movies}, "copy", nil).
		Place(context.Background(), movieReq(src, "Placeholder Film", 2019))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	want := filepath.Join(movies, "Placeholder Film (2019)", "Placeholder Film (2019).mkv")
	if dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}
	if entries, _ := os.ReadDir(series); len(entries) != 0 {
		t.Errorf("the series root should be untouched, holds %d entries", len(entries))
	}
}

// The null-year rule's naming half (#208): no year on record drops the suffix
// rather than filing the movie under a year it does not have.
func TestPlaceMovieWithoutAYearDropsTheSuffix(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	movies := t.TempDir()

	dest, err := New(Roots{Series: t.TempDir(), Movies: movies}, "copy", nil).
		Place(context.Background(), movieReq(src, "Placeholder Film", 0))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	want := filepath.Join(movies, "Placeholder Film", "Placeholder Film.mkv")
	if dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}
}

// A missing movies root is a configuration error, never a fallback into the
// series root: the grab stays open and self-heals once the root is set, where a
// file hardlinked into the wrong library would not.
func TestPlaceMovieWithoutAMoviesRootIsAnError(t *testing.T) {
	t.Chdir(t.TempDir()) // an empty root must not resolve to a relative path
	src := writeSource(t, "raw.mkv")
	series := t.TempDir()

	_, err := New(Roots{Series: series}, "copy", nil).
		Place(context.Background(), movieReq(src, "Placeholder Film", 2019))
	if !errors.Is(err, ErrNoMoviesRoot) {
		t.Fatalf("Place error = %v, want ErrNoMoviesRoot", err)
	}
	if entries, _ := os.ReadDir(series); len(entries) != 0 {
		t.Errorf("nothing may land in the series root, holds %d entries", len(entries))
	}
}

// Format is the discriminator, item count never is: a one-item OVA is still
// series-shaped, which is also where Plex and Jellyfin expect to find it.
func TestPlaceSingleItemOVAStaysInTheSeriesRoot(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	series, movies := t.TempDir(), t.TempDir()

	r := library.ImportRequest{
		SourcePath: src,
		Title:      domain.Title{Name: "Placeholder OVA", Format: domain.FormatOVA, Year: 2019},
		Item:       domain.WantedItem{Number: 1, Kind: domain.KindEpisode},
	}
	dest, err := New(Roots{Series: series, Movies: movies}, "copy", nil).Place(context.Background(), r)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	want := filepath.Join(series, "Placeholder OVA", "Season 01", "Placeholder OVA - S01E01.mkv")
	if dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}
}

// The movie stem is deterministic, so an upgrade overwrites in place and clears
// what the superseded release left beside it.
func TestPlaceMovieUpgradeReplacesAndClearsStemMates(t *testing.T) {
	movies := t.TempDir()
	target := New(Roots{Series: t.TempDir(), Movies: movies}, "copy", nil)

	old := writeSource(t, "old.mkv")
	if _, err := target.Place(context.Background(), movieReq(old, "Placeholder Film", 2019)); err != nil {
		t.Fatalf("first Place: %v", err)
	}
	dir := filepath.Join(movies, "Placeholder Film (2019)")
	mate := filepath.Join(dir, "Placeholder Film (2019).en.srt")
	if err := os.WriteFile(mate, []byte("subs"), 0o644); err != nil {
		t.Fatalf("write stem-mate: %v", err)
	}

	better := filepath.Join(t.TempDir(), "better.mkv")
	if err := os.WriteFile(better, []byte("better-bytes"), 0o644); err != nil {
		t.Fatalf("write better source: %v", err)
	}
	r := movieReq(better, "Placeholder Film", 2019)
	r.Replace = true
	dest, err := target.Place(context.Background(), r)
	if err != nil {
		t.Fatalf("upgrade Place: %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "better-bytes" {
		t.Errorf("destination content = %q, want the upgrade's bytes", b)
	}
	if _, err := os.Stat(mate); !os.IsNotExist(err) {
		t.Error("the superseded release's stem-mate should have been cleared")
	}
}
