package mediaserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeSized writes a source file of a chosen size, so a test can make an
// upgrade smaller than what it replaces.
func writeSized(t *testing.T, name string, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return p
}

// seedLibraryFile puts a file where a past import left it.
func seedLibraryFile(t *testing.T, root, name string, size int) string {
	t.Helper()
	dir := filepath.Join(root, "Placeholder Saga", "Season 01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create library dir: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		t.Fatalf("seed library file: %v", err)
	}
	return p
}

// The size check is what makes an ordinary import idempotent, and exactly what
// an upgrade must not obey: a better release can be a smaller file.
func TestReplaceOverwritesALargerDestination(t *testing.T) {
	root := t.TempDir()
	old := seedLibraryFile(t, root, "Placeholder Saga - S01E03.mkv", 4096)
	src := writeSized(t, "upgrade.mkv", 128)

	r := req(src, "Placeholder Saga", 3)
	r.Replace = true
	dest, err := New(Roots{Series: root}, "copy", nil).Place(context.Background(), r)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if dest != old {
		t.Fatalf("dest = %q, want the held file's own path %q", dest, old)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if info.Size() != 128 {
		t.Errorf("dest size = %d, want the upgrade's 128 bytes", info.Size())
	}
}

// Without Replace the short-circuit stands: nothing re-copies a file already in
// the library.
func TestPlaceWithoutReplaceKeepsTheLargerDestination(t *testing.T) {
	root := t.TempDir()
	seedLibraryFile(t, root, "Placeholder Saga - S01E03.mkv", 4096)
	src := writeSized(t, "raw.mkv", 128)

	dest, err := New(Roots{Series: root}, "copy", nil).Place(context.Background(), req(src, "Placeholder Saga", 3))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if info, _ := os.Stat(dest); info.Size() != 4096 {
		t.Errorf("dest size = %d, want the existing file untouched", info.Size())
	}
}

// An upgrade in a different container would otherwise leave the media server
// scanning two copies of the episode.
func TestReplaceRemovesTheStemMatesItSupersedes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Placeholder Saga", "Season 01")
	seedLibraryFile(t, root, "Placeholder Saga - S01E03.mkv", 4096)
	seedLibraryFile(t, root, "Placeholder Saga - S01E03.en.srt", 10)
	// A different episode whose number merely starts the same: E30 must survive
	// an upgrade of E3.
	survivor := seedLibraryFile(t, root, "Placeholder Saga - S01E30.mkv", 20)

	src := writeSized(t, "upgrade.mp4", 128)
	r := req(src, "Placeholder Saga", 3)
	r.Replace = true
	dest, err := New(Roots{Series: root}, "copy", nil).Place(context.Background(), r)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if dest != filepath.Join(dir, "Placeholder Saga - S01E03.mp4") {
		t.Fatalf("dest = %q, want the upgrade's own extension", dest)
	}
	for _, gone := range []string{"Placeholder Saga - S01E03.mkv", "Placeholder Saga - S01E03.en.srt"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s survived the upgrade", gone)
		}
	}
	if _, err := os.Stat(survivor); err != nil {
		t.Errorf("E30 was removed by an upgrade of E3: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("the upgrade itself is missing: %v", err)
	}
}

// Link mode cannot link onto an occupied name, so a replacement links beside the
// destination and renames over it: the library never loses the episode.
func TestReplaceInLinkModeSwapsAtomically(t *testing.T) {
	root := t.TempDir()
	dest := seedLibraryFile(t, root, "Placeholder Saga - S01E03.mkv", 4096)
	src := writeSized(t, "upgrade.mkv", 128)

	r := req(src, "Placeholder Saga", 3)
	r.Replace = true
	got, err := New(Roots{Series: root}, "hardlink", nil).Place(context.Background(), r)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	if got != dest {
		t.Fatalf("dest = %q, want %q", got, dest)
	}
	si, _ := os.Stat(src)
	di, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if !os.SameFile(si, di) {
		t.Error("the replacement is not a hardlink of the upgrade source")
	}
	if _, err := os.Stat(dest + ".upgrade"); !os.IsNotExist(err) {
		t.Error("the staging link was left behind")
	}
}
