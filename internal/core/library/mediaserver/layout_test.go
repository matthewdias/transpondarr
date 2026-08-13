package mediaserver

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/library"
)

func TestParseLayoutDefaultsToSeasonFolders(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Layout
	}{
		{"", LayoutSeasonFolders},
		{"   ", LayoutSeasonFolders},
		{"nonsense", LayoutSeasonFolders},
		{"season_folders", LayoutSeasonFolders},
		{"flat", LayoutFlat},
		{"  FLAT  ", LayoutFlat},
	} {
		if got := ParseLayout(tc.in); got != tc.want {
			t.Errorf("ParseLayout(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The upgrade guarantee, at this layer: an install that has never set a layout
// keeps the one its files are already in.
func TestUnsetLayoutKeepsTheSeasonFolderLayout(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()

	dest, err := New(Roots{Series: root}, ParseLayout(""), "copy", nil).
		Place(context.Background(), req(src, "Placeholder Saga", 5))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	want := filepath.Join(root, "Placeholder Saga", "Season 01", "Placeholder Saga - S01E05.mkv")
	if dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("nothing at the season-folder path: %v", err)
	}
}

func TestFlatLayoutDropsTheSeasonFolder(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()

	dest, err := New(Roots{Series: root}, LayoutFlat, "copy", nil).
		Place(context.Background(), req(src, "Placeholder Saga", 5))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	want := filepath.Join(root, "Placeholder Saga", "Placeholder Saga - S01E05.mkv")
	if dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("nothing at the flat path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "Placeholder Saga", "Season 01")); !os.IsNotExist(err) {
		t.Error("a season folder was created under the flat layout")
	}
}

// The layout is a shape within the series branch, never a second discriminator:
// a movie's path is the same under either.
func TestFlatLayoutLeavesTheMovieBranchAlone(t *testing.T) {
	series, movies := t.TempDir(), t.TempDir()

	dest, err := New(Roots{Series: series, Movies: movies}, LayoutFlat, "copy", nil).
		Place(context.Background(), movieReq(writeSource(t, "raw.mkv"), "Placeholder Film", 2019))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	want := filepath.Join(movies, "Placeholder Film (2019)", "Placeholder Film (2019).mkv")
	if dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}
}

// Format is still the discriminator under a flat layout: a one-item OVA takes
// the series shape, so it loses its season folder along with everything else.
func TestFlatLayoutKeepsASingleItemOVASeriesShaped(t *testing.T) {
	series, movies := t.TempDir(), t.TempDir()
	r := library.ImportRequest{
		SourcePath: writeSource(t, "raw.mkv"),
		Title:      domain.Title{Name: "Placeholder OVA", Format: domain.FormatOVA, Year: 2019},
		Item:       domain.WantedItem{Number: 1, Kind: domain.KindEpisode},
	}

	dest, err := New(Roots{Series: series, Movies: movies}, LayoutFlat, "copy", nil).
		Place(context.Background(), r)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	want := filepath.Join(series, "Placeholder OVA", "Placeholder OVA - S01E01.mkv")
	if dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}
}

// Flat puts every episode in one directory, so the stem-mate sweep has more
// neighbours to get wrong than it does under a season folder.
func TestFlatUpgradeClearsStemMatesAndSparesNeighbours(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Placeholder Saga")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create library dir: %v", err)
	}
	seed := func(name string, size int) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		return p
	}
	seed("Placeholder Saga - S01E03.mkv", 4096)
	seed("Placeholder Saga - S01E03.en.srt", 10)
	survivor := seed("Placeholder Saga - S01E30.mkv", 20)

	r := req(writeSized(t, "upgrade.mp4", 128), "Placeholder Saga", 3)
	r.Replace = true
	dest, err := New(Roots{Series: root}, LayoutFlat, "copy", nil).Place(context.Background(), r)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if dest != filepath.Join(dir, "Placeholder Saga - S01E03.mp4") {
		t.Fatalf("dest = %q, want the upgrade's own extension in the flat dir", dest)
	}
	for _, gone := range []string{"Placeholder Saga - S01E03.mkv", "Placeholder Saga - S01E03.en.srt"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived the upgrade", gone)
		}
	}
	if _, err := os.Stat(survivor); err != nil {
		t.Errorf("E30 was removed by an upgrade of E3: %v", err)
	}
}

// Switching the layout moves nothing already placed, so an upgrade writes the
// new shape and leaves the old file behind. The series folder still exists, so
// the missing-directory warning cannot fire — this is the only evidence there is.
func TestReplaceWarnsWhenTheOtherLayoutHoldsTheEpisode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layout Layout
		held   string
	}{
		{"switched to flat", LayoutFlat, filepath.Join("Season 01", "Placeholder Saga - S01E03.mkv")},
		{"switched to season folders", LayoutSeasonFolders, "Placeholder Saga - S01E03.mkv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			held := filepath.Join(root, "Placeholder Saga", tc.held)
			if err := os.MkdirAll(filepath.Dir(held), 0o755); err != nil {
				t.Fatalf("create library dir: %v", err)
			}
			if err := os.WriteFile(held, make([]byte, 4096), 0o644); err != nil {
				t.Fatalf("seed the other layout: %v", err)
			}

			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
			r := req(writeSized(t, "upgrade.mkv", 128), "Placeholder Saga", 3)
			r.Replace = true
			if _, err := New(Roots{Series: root}, tc.layout, "copy", log).Place(context.Background(), r); err != nil {
				t.Fatalf("Place: %v", err)
			}

			if !strings.Contains(buf.String(), "another layout") {
				t.Errorf("no warning that the other layout holds this episode; got %q", buf.String())
			}
			if _, err := os.Stat(held); err != nil {
				t.Errorf("the superseded file was removed rather than reported: %v", err)
			}
		})
	}
}

// A library that has never been switched must not warn, or the warning is noise
// on every ordinary upgrade.
func TestReplaceDoesNotWarnWhenOnlyTheCurrentLayoutHoldsTheEpisode(t *testing.T) {
	root := t.TempDir()
	seedLibraryFile(t, root, "Placeholder Saga - S01E03.mkv", 4096)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	r := req(writeSized(t, "upgrade.mkv", 128), "Placeholder Saga", 3)
	r.Replace = true
	if _, err := New(Roots{Series: root}, LayoutSeasonFolders, "copy", log).Place(context.Background(), r); err != nil {
		t.Fatalf("Place: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("an ordinary upgrade warned: %q", buf.String())
	}
}
