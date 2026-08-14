package mediaserver

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tempSuffixes are the staging names a transfer writes beside its destination.
var tempSuffixes = []string{partialSuffix, upgradeSuffix}

// staged runs fn against a staging path beside dest, registered as in flight for
// the duration. It owns the name so a temp cannot exist unregistered.
func (t *Target) staged(dest, suffix string, fn func(tmp string) error) error {
	tmp := dest + suffix
	key := canonical(tmp)

	t.stagingMu.Lock()
	t.staging[key] = true
	t.stagingMu.Unlock()
	defer func() {
		t.stagingMu.Lock()
		delete(t.staging, key)
		t.stagingMu.Unlock()
	}()

	return fn(tmp)
}

// removeUnstaged unlinks path unless a transfer is staging it. The check and the
// unlink share the lock, or a transfer could register between them.
func (t *Target) removeUnstaged(path string) (bool, error) {
	t.stagingMu.Lock()
	defer t.stagingMu.Unlock()
	if t.staging[path] {
		return false, nil
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}

// canonical is a staging path as the sweep will see it: the sweep walks resolved
// roots and WalkDir never descends a link, so every path it yields is resolved.
func canonical(path string) string {
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Join(dir, filepath.Base(path))
}

// staleTemp is one staging file the walk condemned, carrying its mtime so the
// removal can report how long it sat there.
type staleTemp struct {
	path    string
	modTime time.Time
}

// SweepTemp removes staging files no transfer is writing and older than
// olderThan, reporting how many it removed (#132).
func (t *Target) SweepTemp(ctx context.Context, olderThan time.Duration) (int, error) {
	stale, err := t.collectStale(ctx, time.Now().Add(-olderThan))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, s := range stale {
		gone, err := t.removeUnstaged(s.path)
		if err != nil {
			// Already unlinked is the ordinary case, not a fault: this path was
			// enumerated twice, or an import reclaimed its own temp since the walk.
			if !errors.Is(err, fs.ErrNotExist) {
				t.log.Warn("mediaserver: stale temp file could not be removed", "path", s.path, "err", err)
			}
			continue
		}
		if !gone {
			continue
		}
		removed++
		t.log.Info("mediaserver: removed a stale temp file",
			"path", s.path, "age", time.Since(s.modTime).Round(time.Minute))
	}
	return removed, nil
}

// collectStale walks the roots for staging files old enough to be abandoned.
func (t *Target) collectStale(ctx context.Context, cutoff time.Time) ([]staleTemp, error) {
	var out []staleTemp
	for _, root := range t.sweepRoots() {
		walk := func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// An unreadable path — an unmounted root included — is one dark corner
			// rather than a failed sweep, so it is skipped instead of returned.
			if err != nil {
				t.log.Debug("mediaserver: temp sweep skipped a path", "path", path, "err", err)
				return nil
			}
			if d.IsDir() || !isTempName(d.Name()) {
				return nil
			}
			info, err := d.Info()
			if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
				return nil
			}
			out = append(out, staleTemp{path: path, modTime: info.ModTime()})
			return nil
		}
		if err := filepath.WalkDir(root, walk); err != nil {
			return nil, err // only cancellation: every other error is skipped above
		}
	}
	return out, nil
}

// sweepRoots resolves the configured roots, since WalkDir will not descend a
// symlinked root and `/media -> /mnt/user/media` would sweep nothing. Dropping
// duplicates only spares a second walk; a repeated path is already harmless.
func (t *Target) sweepRoots() []string {
	out := make([]string, 0, 2)
	seen := make(map[string]bool, 2)
	for _, root := range []string{t.roots.Series, t.roots.Movies} {
		if root == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			resolved = filepath.Clean(root)
		}
		if !seen[resolved] {
			seen[resolved] = true
			out = append(out, resolved)
		}
	}
	return out
}

// isTempName reports whether name is one of our staging names over a video file —
// the video check is what makes it ours rather than any ".partial".
func isTempName(name string) bool {
	for _, suffix := range tempSuffixes {
		if base, ok := strings.CutSuffix(name, suffix); ok {
			return videoExts[strings.ToLower(filepath.Ext(base))]
		}
	}
	return false
}
