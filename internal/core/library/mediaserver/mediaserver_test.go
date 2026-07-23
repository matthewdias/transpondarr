package mediaserver

import (
	"context"
	"errors"
	"fmt"
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

// A copy interrupted by a crash leaves a stray .partial; the temp name is
// deterministic and os.Create truncates, so the retry reclaims it — no reaper needed.
func TestPlaceCopyReclaimsStrayPartial(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	destDir := filepath.Join(root, "Placeholder Saga", "Season 01")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stray := filepath.Join(destDir, "Placeholder Saga - S01E02.mkv.partial")
	if err := os.WriteFile(stray, []byte("stale-longer-than-the-source-bytes"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	dest, err := New(root, "copy").Place(context.Background(), req(src, "Placeholder Saga", 2))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "video-bytes" {
		t.Errorf("copied content = %q, want the source bytes (stale temp not truncated?)", b)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("stray .partial should have been reclaimed by the retry")
	}
}

// An aborted copy must surface the cancellation and leave neither a destination
// nor a .partial, so the retried grab starts fresh.
func TestPlaceCopyAbortsOnCancel(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New(root, "copy").Place(ctx, req(src, "Placeholder Saga", 4))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Place error = %v, want context.Canceled", err)
	}
	dest := filepath.Join(root, "Placeholder Saga", "Season 01", "Placeholder Saga - S01E04.mkv")
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("an aborted copy must not produce the destination")
	}
	if _, err := os.Stat(dest + ".partial"); !os.IsNotExist(err) {
		t.Error("an aborted copy must remove its .partial")
	}
}

// plantDest writes a pre-existing destination file for episode number and returns its path.
func plantDest(t *testing.T, root string, number int, content string) string {
	t.Helper()
	destDir := filepath.Join(root, "Placeholder Saga", "Season 01")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dest := filepath.Join(destDir, fmt.Sprintf("Placeholder Saga - S01E%02d.mkv", number))
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		t.Fatalf("plant dest: %v", err)
	}
	return dest
}

// A dest smaller than its still-available source is a truncated past import,
// not an idempotent hit — it must be re-copied.
func TestPlaceReclaimsTruncatedDest(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	dest := plantDest(t, root, 6, "vid")

	got, err := New(root, "copy").Place(context.Background(), req(src, "Placeholder Saga", 6))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if got != dest {
		t.Errorf("dest = %q, want %q", got, dest)
	}
	if b, _ := os.ReadFile(dest); string(b) != "video-bytes" {
		t.Errorf("content = %q, want the source re-copied over the truncated file", b)
	}
}

// Link mode reclaims too: the truncated name must be removed first, since
// os.Link cannot replace an existing file.
func TestPlaceHardlinkReclaimsTruncatedDest(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	dest := plantDest(t, root, 8, "vid")

	if _, err := New(root, "hardlink").Place(context.Background(), req(src, "Placeholder Saga", 8)); err != nil {
		t.Fatalf("Place: %v", err)
	}
	si, _ := os.Stat(src)
	di, _ := os.Stat(dest)
	if !os.SameFile(si, di) {
		t.Error("truncated dest should have been replaced by a hardlink of the source")
	}
}

// An equal-sized dest is a completed past import and must stay untouched.
func TestPlaceKeepsEqualSizedDest(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	dest := plantDest(t, root, 9, "other-bytes") // same length as "video-bytes"

	if _, err := New(root, "copy").Place(context.Background(), req(src, "Placeholder Saga", 9)); err != nil {
		t.Fatalf("Place: %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "other-bytes" {
		t.Errorf("content = %q, want the existing equal-sized file left alone", b)
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

// TestPlaceRejectsDirectory pins Place's file-only contract: the target decides
// layout, not which of a payload's files is the episode.
func TestPlaceRejectsDirectory(t *testing.T) {
	dir := t.TempDir() // resolved by the importer before Place in the live pipeline
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
