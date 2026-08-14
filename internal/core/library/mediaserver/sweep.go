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

// SweepTemp removes staging files older than olderThan from the configured roots,
// reporting how many it removed (#132). Age is the whole safety margin: a copy in
// flight is still writing, so its mtime is current.
func (t *Target) SweepTemp(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().Add(-olderThan)
	removed := 0
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
			if err := os.Remove(path); err != nil {
				// Already gone: a nested root walked it first, or an import reclaimed it.
				if !errors.Is(err, fs.ErrNotExist) {
					t.log.Warn("mediaserver: stale temp file could not be removed", "path", path, "err", err)
				}
				return nil
			}
			removed++
			t.log.Info("mediaserver: removed a stale temp file",
				"path", path, "age", time.Since(info.ModTime()).Round(time.Minute))
			return nil
		}
		if err := filepath.WalkDir(root, walk); err != nil {
			return removed, err // only cancellation: every other error is skipped above
		}
	}
	return removed, nil
}

// sweepRoots de-duplicates the configured roots, since one directory set as both
// would otherwise be walked and counted twice.
func (t *Target) sweepRoots() []string {
	out := make([]string, 0, 2)
	seen := make(map[string]bool, 2)
	for _, root := range []string{t.roots.Series, t.roots.Movies} {
		if root == "" {
			continue
		}
		if clean := filepath.Clean(root); !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	return out
}

// isTempName reports whether name is one of our staging names over a video file.
// Requiring the video extension is what makes this the importer's own temp
// pattern rather than any ".partial": drift from importer's list costs a missed
// sweep, never a wrong delete.
func isTempName(name string) bool {
	for _, suffix := range tempSuffixes {
		if base, ok := strings.CutSuffix(name, suffix); ok {
			return videoExts[strings.ToLower(filepath.Ext(base))]
		}
	}
	return false
}
