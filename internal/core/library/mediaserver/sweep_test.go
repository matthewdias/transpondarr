package mediaserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sweepAge is the threshold every sweep test passes. Stale files are backdated
// well past it and fresh ones keep their real mtime, so nothing here sleeps.
const sweepAge = time.Hour

// seedAged writes a file of a chosen age under dir, creating dir if needed.
func seedAged(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed %s: %v", name, err)
	}
	backdate(t, p, age)
	return p
}

// backdate sets a path's mtime to age ago.
func backdate(t *testing.T, path string, age time.Duration) {
	t.Helper()
	at := time.Now().Add(-age)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("backdate %s: %v", path, err)
	}
}

func sweep(t *testing.T, target *Target) int {
	t.Helper()
	removed, err := target.SweepTemp(context.Background(), sweepAge)
	if err != nil {
		t.Fatalf("SweepTemp: %v", err)
	}
	return removed
}

func assertGone(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s: %s still exists", why, filepath.Base(path))
	}
}

func assertKept(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s: %s was removed (%v)", why, filepath.Base(path), err)
	}
}

// seasonDir is where an episode and its debris live under a series root.
func seasonDir(root string) string {
	return filepath.Join(root, "Placeholder Saga", "Season 01")
}

// A copy killed mid-transfer leaves a .partial whose destination may never be
// written again; age is the only thing that separates it from a live copy.
func TestSweepTempRemovesAStalePartial(t *testing.T) {
	root := t.TempDir()
	dir := seasonDir(root)
	stale := seedAged(t, dir, "Placeholder Saga - S01E01.mkv.partial", 48*time.Hour)
	fresh := seedAged(t, dir, "Placeholder Saga - S01E02.mkv.partial", 0)

	if removed := sweep(t, New(Roots{Series: root}, LayoutSeasonFolders, "copy", nil)); removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertGone(t, stale, "a stale .partial is an orphan no import will reclaim")
	assertKept(t, fresh, "a fresh .partial may be a copy still running")
}

// The upgrade staging link is the same shape and the worse leak: it holds a
// hardlink to the payload, so it keeps those bytes alive after the torrent goes.
func TestSweepTempRemovesAStaleUpgradeStaging(t *testing.T) {
	root := t.TempDir()
	dir := seasonDir(root)
	stale := seedAged(t, dir, "Placeholder Saga - S01E01.mkv.upgrade", 48*time.Hour)
	fresh := seedAged(t, dir, "Placeholder Saga - S01E02.mkv.upgrade", 0)

	if removed := sweep(t, New(Roots{Series: root}, LayoutSeasonFolders, "hardlink", nil)); removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertGone(t, stale, "a stale .upgrade is an orphaned link nothing will reclaim")
	assertKept(t, fresh, "a fresh .upgrade may be an upgrade mid-rename")
}

// The guarantee the whole feature rests on: the sweep considers only the temp
// names this package writes, and deletes nothing else, ever.
func TestSweepTempLeavesEverythingElseAlone(t *testing.T) {
	root := t.TempDir()
	dir := seasonDir(root)
	kept := []string{
		seedAged(t, dir, "Placeholder Saga - S01E03.mkv", 48*time.Hour),      // the episode itself
		seedAged(t, dir, "Placeholder Saga - S01E03.en.srt", 48*time.Hour),   // a sidecar
		seedAged(t, dir, "notes.txt", 48*time.Hour),                          // someone else's file
		seedAged(t, dir, "report.txt.partial", 48*time.Hour),                 // our suffix, not our shape
		seedAged(t, dir, "Placeholder Saga - S01E04.mkv.part", 48*time.Hour), // another tool's temp
	}

	if removed := sweep(t, New(Roots{Series: root}, LayoutSeasonFolders, "copy", nil)); removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	for _, p := range kept {
		assertKept(t, p, "not a temp this package writes")
	}
}

// Sweeping the upgrade path's staging link must not disturb the import path that
// writes it: the placed file is still there, and still the source's inode.
func TestSweepTempSparesAHardlinkedEpisode(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	target := New(Roots{Series: root}, LayoutSeasonFolders, "hardlink", nil)

	dest, err := target.Place(context.Background(), req(src, "Placeholder Saga", 5))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	backdate(t, dest, 48*time.Hour)
	stale := seedAged(t, seasonDir(root), "Placeholder Saga - S01E06.mkv.upgrade", 48*time.Hour)

	if removed := sweep(t, target); removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertGone(t, stale, "the staging orphan is ours to clean")
	assertKept(t, dest, "a placed episode is the library, not a temp")
	si, _ := os.Stat(src)
	di, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if !os.SameFile(si, di) {
		t.Error("the placed file is no longer the source's inode")
	}
}

// Either root can hold a temp, and a single-directory config must not walk twice
// and count the same file twice.
func TestSweepTempCoversBothRoots(t *testing.T) {
	t.Run("distinct roots", func(t *testing.T) {
		series, movies := t.TempDir(), t.TempDir()
		ep := seedAged(t, seasonDir(series), "Placeholder Saga - S01E01.mkv.partial", 48*time.Hour)
		film := seedAged(t, filepath.Join(movies, "Placeholder Film (2019)"),
			"Placeholder Film (2019).mkv.partial", 48*time.Hour)

		if removed := sweep(t, New(Roots{Series: series, Movies: movies}, LayoutSeasonFolders, "copy", nil)); removed != 2 {
			t.Errorf("removed = %d, want 2", removed)
		}
		assertGone(t, ep, "a stale temp under the series root")
		assertGone(t, film, "a stale temp under the movies root")
	})

	t.Run("one directory configured as both", func(t *testing.T) {
		root := t.TempDir()
		stale := seedAged(t, seasonDir(root), "Placeholder Saga - S01E01.mkv.partial", 48*time.Hour)

		if removed := sweep(t, New(Roots{Series: root, Movies: root}, LayoutSeasonFolders, "copy", nil)); removed != 1 {
			t.Errorf("removed = %d, want 1 (the same file counted twice?)", removed)
		}
		assertGone(t, stale, "a stale temp under a root that is both")
	})

	// The series walk already covers a movies root nested inside it, so the file is
	// found twice; the second removal must not be reported as a failure.
	t.Run("movies root nested in the series root", func(t *testing.T) {
		series := t.TempDir()
		movies := filepath.Join(series, "Movies")
		stale := seedAged(t, filepath.Join(movies, "Placeholder Film (2019)"),
			"Placeholder Film (2019).mkv.partial", 48*time.Hour)

		if removed := sweep(t, New(Roots{Series: series, Movies: movies}, LayoutSeasonFolders, "copy", nil)); removed != 1 {
			t.Errorf("removed = %d, want 1", removed)
		}
		assertGone(t, stale, "a stale temp under a nested movies root")
	})
}

// An unset root is a supported library and an absent one is an unmounted or
// not-yet-created share: neither is an error, and neither has anything to sweep.
func TestSweepTempSkipsUnconfiguredAndMissingRoots(t *testing.T) {
	for _, tc := range []struct {
		name  string
		roots Roots
	}{
		{"neither configured", Roots{}},
		{"series only, and it does not exist", Roots{Series: filepath.Join(t.TempDir(), "absent")}},
		{"movies only, and it does not exist", Roots{Movies: filepath.Join(t.TempDir(), "absent")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			removed, err := New(tc.roots, LayoutSeasonFolders, "copy", nil).SweepTemp(context.Background(), sweepAge)
			if err != nil {
				t.Errorf("SweepTemp: %v", err)
			}
			if removed != 0 {
				t.Errorf("removed = %d, want 0", removed)
			}
		})
	}
}
