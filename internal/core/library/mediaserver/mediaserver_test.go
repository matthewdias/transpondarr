package mediaserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/library"
)

func writeSource(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("video-bytes"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return p
}

func req(src, title string, number int) library.ImportRequest {
	return library.ImportRequest{
		SourcePath: src,
		Title:      domain.Title{Name: title},
		Item:       domain.WantedItem{Number: number, Kind: domain.KindEpisode},
	}
}

func TestPlaceHardlinkLayout(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	target := New(root, "hardlink")

	dest, err := target.Place(context.Background(), req(src, "Placeholder Saga", 5))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	want := filepath.Join(root, "Placeholder Saga", "Season 01", "Placeholder Saga - S01E05.mkv")
	if dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}
	// Hardlink: destination is the same inode as the source.
	si, _ := os.Stat(src)
	di, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if !os.SameFile(si, di) {
		t.Error("expected the destination to be a hardlink of the source")
	}
}

func TestPlaceCopyMakesSeparateFile(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	target := New(root, "copy")

	dest, err := target.Place(context.Background(), req(src, "Placeholder Saga", 1))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	si, _ := os.Stat(src)
	di, _ := os.Stat(dest)
	if os.SameFile(si, di) {
		t.Error("copy mode should not produce a hardlink")
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "video-bytes" {
		t.Errorf("copied content = %q", b)
	}
	// No leftover .partial temp file.
	if _, err := os.Stat(dest + ".partial"); !os.IsNotExist(err) {
		t.Error("temp .partial file should not remain")
	}
}

// Each AniList entry is its own single-season show: a "2nd Season" entry keeps
// the season in its folder title but is filed under Season 01 (not Season 02, which
// would leave a phantom show with no Season 01 inside a "2nd Season" folder).
func TestPlaceSecondSeasonEntryFiledUnderSeason01(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	dest, err := New(root, "copy").Place(context.Background(), req(src, "Placeholder Saga 2nd Season", 7))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	want := filepath.Join(root, "Placeholder Saga 2nd Season", "Season 01", "Placeholder Saga 2nd Season - S01E07.mkv")
	if dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}
}

func TestPlaceSanitizesName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Foo: Bar/Baz?", "Foo - Bar Baz"},
		{"Con", "_Con"},           // reserved device name
		{"aux.txt", "_aux.txt"},   // reserved even with extension
		{"COM1", "_COM1"},         // reserved, case-insensitive
		{"Console", "Console"},    // only exact reserved names dodged
		{"Foo\x00\x1fBar", "FooBar"}, // control chars dropped
	}
	for _, c := range cases {
		if got := sanitize(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPlaceRejectsDirectory pins Place's file-only contract. Resolving a
// folder-wrapped payload down to its one episode file is the importer's job
// (importer.resolvePayloadFile), deliberately kept out of here: the target
// decides library *layout*, not which of a payload's files is the episode.
func TestPlaceRejectsDirectory(t *testing.T) {
	dir := t.TempDir() // a directory source: never reaches Place in the live pipeline
	_, err := New(t.TempDir(), "copy").Place(context.Background(), req(dir, "Placeholder Saga", 1))
	if err == nil {
		t.Fatal("expected an error for a directory source")
	}
}

func TestPlaceIdempotent(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	target := New(root, "copy")
	first, err := target.Place(context.Background(), req(src, "Placeholder Saga", 3))
	if err != nil {
		t.Fatalf("first Place: %v", err)
	}
	second, err := target.Place(context.Background(), req(src, "Placeholder Saga", 3))
	if err != nil {
		t.Fatalf("second Place: %v", err)
	}
	if first != second {
		t.Errorf("idempotent Place returned different paths: %q vs %q", first, second)
	}
}
