package mediaserver

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sweepAge is the threshold every sweep test passes. Stale files are backdated
// well past it and fresh ones keep their real mtime, so nothing here sleeps.
const sweepAge = time.Hour

// alwaysStale makes the age clause inert, so a test can isolate a clause age
// would otherwise mask — and a hardlink's inherited mtime makes that the real
// case, not a contrived one.
const alwaysStale = -time.Second

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
			t.Errorf("removed = %d, want 1", removed)
		}
		assertGone(t, stale, "a stale temp under a root that is both")
	})

	// A nested root is enumerated by both walks and de-duping the roots cannot
	// catch it, so the second sighting reaches the removal already gone. Counting
	// it or reporting it would both be wrong, and the quiet log is the only
	// evidence of the second: the count alone cannot tell the two apart.
	t.Run("movies root nested in the series root", func(t *testing.T) {
		series := t.TempDir()
		movies := filepath.Join(series, "Movies")
		stale := seedAged(t, filepath.Join(movies, "Placeholder Film (2019)"),
			"Placeholder Film (2019).mkv.partial", 48*time.Hour)

		var buf bytes.Buffer
		log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		if removed := sweep(t, New(Roots{Series: series, Movies: movies}, LayoutSeasonFolders, "copy", log)); removed != 1 {
			t.Errorf("removed = %d, want 1 (the same file counted twice?)", removed)
		}
		assertGone(t, stale, "a stale temp under a nested movies root")
		if buf.Len() > 0 {
			t.Errorf("a second sighting of an already-removed path warned: %q", buf.String())
		}
	})
}

// A symlinked root is an ordinary NAS shape, and WalkDir will not descend one:
// unresolved, the sweep reports a healthy (0, nil) forever.
func TestSweepTempResolvesASymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	stale := seedAged(t, seasonDir(real), "Placeholder Saga - S01E01.mkv.partial", 48*time.Hour)
	link := filepath.Join(t.TempDir(), "media")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if removed := sweep(t, New(Roots{Series: link}, LayoutSeasonFolders, "copy", nil)); removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	assertGone(t, stale, "a stale temp under a symlinked root")
}

// The other side of resolving the root: WalkDir does not follow links inside the
// tree, so a link planted in the library cannot aim the sweep outside it.
func TestSweepTempDoesNotEscapeARootThroughASymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	stale := seedAged(t, outside, "Placeholder Saga - S01E01.mkv.partial", 48*time.Hour)
	if err := os.Symlink(outside, filepath.Join(root, "elsewhere")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if removed := sweep(t, New(Roots{Series: root}, LayoutSeasonFolders, "copy", nil)); removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	assertKept(t, stale, "outside every configured root")
}

// os.Remove on a symlink drops the link, so a link named like a temp file is a
// file of someone else's the sweep must decline. Age cannot make this call — a
// fresh link would pass a real threshold too — so the threshold is made inert.
func TestSweepTempDeclinesASymlink(t *testing.T) {
	root := t.TempDir()
	dir := seasonDir(root)
	target := seedAged(t, dir, "elsewhere.mkv", 0)
	link := filepath.Join(dir, "Placeholder Saga - S01E01.mkv.partial")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	removed, err := New(Roots{Series: root}, LayoutSeasonFolders, "copy", nil).
		SweepTemp(context.Background(), alwaysStale)
	if err != nil {
		t.Fatalf("SweepTemp: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	assertKept(t, link, "a symlink is not a staging file this package wrote")
}

// The registry is what protects a live transfer, because a staging hardlink
// inherits the payload's mtime and can read as days old the instant it exists.
func TestSweepTempSkipsAStagingFileInFlight(t *testing.T) {
	root := t.TempDir()
	dir := seasonDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dest := filepath.Join(dir, "Placeholder Saga - S01E01.mkv")
	target := New(Roots{Series: root}, LayoutSeasonFolders, "hardlink", nil)

	var tmp string
	err := target.staged(dest, upgradeSuffix, func(staging string) error {
		tmp = staging
		if err := os.WriteFile(tmp, []byte("x"), 0o644); err != nil {
			return err
		}
		backdate(t, tmp, 48*time.Hour)
		if removed := sweep(t, target); removed != 0 {
			t.Errorf("removed = %d while the transfer was in flight, want 0", removed)
		}
		assertKept(t, tmp, "a staging file this process is writing")
		return nil
	})
	if err != nil {
		t.Fatalf("staged: %v", err)
	}

	// Released: the same file is now an orphan and nothing protects it.
	if removed := sweep(t, target); removed != 1 {
		t.Errorf("removed = %d after the transfer finished, want 1", removed)
	}
	assertGone(t, tmp, "an unregistered staging file past the threshold")
}

// End to end over the real copy path: a sweep running throughout must not break
// the import, with the threshold inert so only the registry can save it.
func TestSweepTempSparesACopyInFlight(t *testing.T) {
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, make([]byte, 8<<20), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	root := t.TempDir()
	target := New(Roots{Series: root}, LayoutSeasonFolders, "copy", nil)

	done := make(chan error, 1)
	go func() {
		_, err := target.Place(context.Background(), req(src, "Placeholder Saga", 1))
		done <- err
	}()
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Place during a sweep: %v", err)
			}
			dest := filepath.Join(seasonDir(root), "Placeholder Saga - S01E01.mkv")
			if info, err := os.Stat(dest); err != nil || info.Size() != 8<<20 {
				t.Fatalf("dest = %v (size %v), want the whole source", err, info)
			}
			return
		default:
			if removed, err := target.SweepTemp(context.Background(), alwaysStale); err != nil || removed != 0 {
				t.Fatalf("sweep during a copy removed %d (err %v), want 0", removed, err)
			}
		}
	}
}

// A registration outliving its transfer would make the file permanently
// unsweepable, so both the settled and the aborted path must let go.
func TestPlaceLeavesNoStagingRegistration(t *testing.T) {
	staged := func(t *testing.T, target *Target) int {
		t.Helper()
		target.stagingMu.Lock()
		defer target.stagingMu.Unlock()
		return len(target.staging)
	}

	t.Run("a completed copy", func(t *testing.T) {
		target := New(Roots{Series: t.TempDir()}, LayoutSeasonFolders, "copy", nil)
		if _, err := target.Place(context.Background(), req(writeSource(t, "raw.mkv"), "Placeholder Saga", 1)); err != nil {
			t.Fatalf("Place: %v", err)
		}
		if n := staged(t, target); n != 0 {
			t.Errorf("%d staging paths still registered, want 0", n)
		}
	})

	t.Run("an aborted copy", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		target := New(Roots{Series: t.TempDir()}, LayoutSeasonFolders, "copy", nil)
		if _, err := target.Place(ctx, req(writeSource(t, "raw.mkv"), "Placeholder Saga", 1)); err == nil {
			t.Fatal("Place on a cancelled context should fail")
		}
		if n := staged(t, target); n != 0 {
			t.Errorf("%d staging paths still registered, want 0", n)
		}
	})

	t.Run("a completed hardlink upgrade", func(t *testing.T) {
		root := t.TempDir()
		seedLibraryFile(t, root, "Placeholder Saga - S01E03.mkv", 4096)
		target := New(Roots{Series: root}, LayoutSeasonFolders, "hardlink", nil)
		r := req(writeSized(t, "upgrade.mkv", 128), "Placeholder Saga", 3)
		r.Replace = true
		if _, err := target.Place(context.Background(), r); err != nil {
			t.Fatalf("Place: %v", err)
		}
		if n := staged(t, target); n != 0 {
			t.Errorf("%d staging paths still registered, want 0", n)
		}
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
