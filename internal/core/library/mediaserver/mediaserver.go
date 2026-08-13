// Package mediaserver implements the library.Target interface by placing files
// into a Jellyfin/Plex-friendly layout, one root per format:
//
//	<series root>/<Series Name>/Season 01/<Series Name> - S01EMM<ext>
//	<movies root>/<Movie Name> (<Year>)/<Movie Name> (<Year>)<ext>
//
// The format is the discriminator and the item count never is, so a
// single-episode OVA takes the series layout; both media servers expect one
// under Shows. A movie with no year on record drops the suffix from both
// components rather than filing under a year the provider has not published.
//
// Anime providers model each season as a SEPARATE entry with its own title (e.g. "...
// 2nd Season") and its own 1..N numbering, so each entry maps to its own
// single-season media-server show: the season is carried by the folder title,
// and inside it everything is Season 01. (Merging entries into one multi-season
// show is a TVDB/relationship-mapping feature deliberately out of v1.) Episode
// numbering is the wanted item's number.
package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/library"
)

// seasonNumber is the season every entry is filed under. Each AniList entry is
// its own single-season show; a "2nd Season" is a distinct entry/title, not
// Season 02 inside the first entry's folder.
const seasonNumber = 1

// ErrNoMoviesRoot and ErrNoSeriesRoot are why a file cannot be placed when the
// root its format calls for is unset. Deliberately an error rather than a
// fallback into the other root: the grab stays open and the next scan imports
// it once the root is set, where a file already hardlinked into the wrong
// library would not. Either root alone is a supported library, so each format
// answers for its own.
var (
	ErrNoMoviesRoot = errors.New("no movies library directory is configured; set one under Settings > Library")
	ErrNoSeriesRoot = errors.New("no series library directory is configured; set one under Settings > Library")
)

// Mode selects how a file is transferred into the library.
type Mode string

const (
	ModeAuto     Mode = "auto"     // hardlink, falling back to copy across filesystems
	ModeHardlink Mode = "hardlink" // hardlink only (fail if cross-filesystem)
	ModeCopy     Mode = "copy"     // always copy
)

// ParseMode maps a config string to a Mode, defaulting to auto.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hardlink":
		return ModeHardlink
	case "copy":
		return ModeCopy
	default:
		return ModeAuto
	}
}

// Roots are the per-format library destinations. Movies get their own because
// Plex and Jellyfin want a Movies library separate from Shows; Series takes
// every other format, single-episode OVAs included.
type Roots struct {
	Series string
	Movies string
}

// Target places imported files into a media-server library layout.
type Target struct {
	roots Roots
	mode  Mode
	log   *slog.Logger
}

// New constructs a media-server layout target over roots. mode is
// auto|hardlink|copy (see ParseMode). A nil log discards. Roots are trimmed
// here so the path joined is the path configured: a pasted " /media/films" is
// otherwise a directory named " " away, or relative to the process cwd.
func New(roots Roots, mode string, log *slog.Logger) *Target {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	roots.Series = strings.TrimSpace(roots.Series)
	roots.Movies = strings.TrimSpace(roots.Movies)
	return &Target{roots: roots, mode: ParseMode(mode), log: log}
}

func (t *Target) Name() string { return "mediaserver" }

var _ library.Target = (*Target)(nil)

// Place transfers a single downloaded file into the library and returns its final
// path. A directory source (a batch/season pack) is rejected — per-file batch
// import is a later phase. A destination at least the source's size is already
// imported; a smaller one is a truncated past import and is re-copied. A Replace
// request overwrites whatever size the destination is, and clears its stem-mates.
func (t *Target) Place(ctx context.Context, req library.ImportRequest) (string, error) {
	info, err := os.Stat(req.SourcePath)
	if err != nil {
		return "", fmt.Errorf("mediaserver: stat source: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("mediaserver: source is a directory (batch import not yet supported): %s", req.SourcePath)
	}

	name := sanitize(req.Title.Name)
	if name == "" {
		return "", errors.New("mediaserver: empty title name")
	}
	ext := filepath.Ext(req.SourcePath)

	destDir, stem, err := t.destination(req, name)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(destDir, stem+ext)

	occupied := false
	if destInfo, err := os.Stat(dest); err == nil {
		switch {
		case req.Replace:
			// A better release can be a smaller file, so an upgrade never asks the
			// size check whether it is already done.
			occupied = true
		case destInfo.Size() >= info.Size():
			// Size-checked idempotency only covers open grabs: a settled grab's source is
			// gone, and nothing calls Place again — that recovery is deliberately out of scope.
			return dest, nil
		default:
			if err := os.Remove(dest); err != nil { // free the name — link mode can't replace it
				return "", fmt.Errorf("mediaserver: remove truncated dest: %w", err)
			}
		}
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mediaserver: create dir: %w", err)
	}
	if occupied {
		if err := t.replace(ctx, req.SourcePath, dest); err != nil {
			return "", err
		}
	} else if err := t.transfer(ctx, req.SourcePath, dest); err != nil {
		return "", err
	}
	if req.Replace {
		t.removeStemMates(destDir, stem, dest)
	}
	return dest, nil
}

// destination picks the root and the extension-less path shape for a request.
// Format is the sole discriminator, so a one-item OVA is still series-shaped;
// #129 will parameterize the shape within each branch, not the branch itself.
func (t *Target) destination(req library.ImportRequest, name string) (dir, stem string, err error) {
	if req.Title.Format == domain.FormatMovie {
		if t.roots.Movies == "" {
			return "", "", fmt.Errorf("mediaserver: %w", ErrNoMoviesRoot)
		}
		folder := movieName(name, req.Title.Year)
		return filepath.Join(t.roots.Movies, folder), folder, nil
	}
	if t.roots.Series == "" {
		return "", "", fmt.Errorf("mediaserver: %w", ErrNoSeriesRoot)
	}
	return filepath.Join(t.roots.Series, name, fmt.Sprintf("Season %02d", seasonNumber)),
		fmt.Sprintf("%s - S%02dE%02d", name, seasonNumber, req.Item.Number), nil
}

// movieName is the folder and file stem a movie is filed under. A year of 0
// means none is on record, and drops the suffix rather than inventing one.
func movieName(name string, year int) string {
	if year <= 0 {
		return name
	}
	return fmt.Sprintf("%s (%d)", name, year)
}

// replace transfers over a destination the library already holds. Link mode
// cannot link onto an occupied name, so it links beside it and renames; copy
// mode's temp-and-rename already is that. Transferring before removing anything
// is the crash-safe order: the worst case is two files, never none.
func (t *Target) replace(ctx context.Context, src, dest string) error {
	if t.mode == ModeCopy {
		return copyFile(ctx, src, dest)
	}
	tmp := dest + ".upgrade"
	_ = os.Remove(tmp) // a previous attempt's staging link
	if err := os.Link(src, tmp); err != nil {
		if t.mode == ModeAuto && isUnsupportedLink(err) {
			return copyFile(ctx, src, dest)
		}
		return fmt.Errorf("mediaserver: hardlink: %w", err)
	}
	if err := syncLinked(tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mediaserver: rename upgrade over dest: %w", err)
	}
	syncDir(filepath.Dir(dest))
	return nil
}

// removeStemMates drops what the superseded release left under this episode's
// stem — another container, a sidecar — so a media server does not scan two
// copies of it. Best-effort: the upgrade is already in place, and a stray file
// is not worth failing an import that otherwise succeeded. The trailing dot is
// what keeps an upgrade of E10 from removing E100.
func (t *Target) removeStemMates(dir, stem, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.log.Debug("mediaserver: stem-mate sweep skipped", "dir", dir, "err", err)
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == filepath.Base(keep) || !strings.HasPrefix(name, stem+".") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.log.Debug("mediaserver: superseded stem-mate left behind", "path", filepath.Join(dir, name), "err", err)
		}
	}
}

// transfer moves bytes from src to dest according to the configured mode. In auto
// mode a hardlink is attempted first and falls back to a copy when the filesystem
// can't hardlink here (a different device, or a mount that doesn't support/permit
// hardlinks at all).
func (t *Target) transfer(ctx context.Context, src, dest string) error {
	switch t.mode {
	case ModeCopy:
		return copyFile(ctx, src, dest)
	case ModeHardlink:
		if err := os.Link(src, dest); err != nil {
			return fmt.Errorf("mediaserver: hardlink: %w", err)
		}
		return syncLinked(dest)
	default: // ModeAuto
		if err := os.Link(src, dest); err != nil {
			if isUnsupportedLink(err) {
				return copyFile(ctx, src, dest)
			}
			return fmt.Errorf("mediaserver: hardlink: %w", err)
		}
		return syncLinked(dest)
	}
}

// syncLinked flushes a fresh link's data — the download client's writes, possibly
// still unflushed — so a crash can't truncate the file behind a settled import.
// On failure the link is removed so a retry re-links instead of passing the size check.
func syncLinked(dest string) error {
	err := func() error {
		f, err := os.Open(dest)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		return f.Sync()
	}()
	if err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("mediaserver: sync hardlink: %w", err)
	}
	syncDir(filepath.Dir(dest))
	return nil
}

// isUnsupportedLink reports whether a hardlink failure means the filesystem simply
// can't hardlink src to dest, so auto mode should fall back to a copy: a different
// device (EXDEV), or a mount that doesn't permit/support hardlinks (EPERM/ENOTSUP/
// EOPNOTSUPP — common on SMB/CIFS, FUSE, mergerfs/rclone, and some Docker volumes).
// Other errors (a missing source, a full disk) are real and must surface.
func isUnsupportedLink(err error) bool {
	return errors.Is(err, syscall.EXDEV) ||
		errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP)
}

// copyFile copies src to dest via a deterministic temp file + rename: a failed copy
// never leaves a partial at the destination, and the next attempt reclaims the temp.
func copyFile(ctx context.Context, src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("mediaserver: open source: %w", err)
	}
	defer func() { _ = in.Close() }()

	tmp := dest + ".partial"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("mediaserver: create temp: %w", err)
	}
	if _, err := io.Copy(out, ctxReader{ctx, in}); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("mediaserver: copy: %w", err)
	}
	// Durability ordering: flush the bytes before the rename, the directory after —
	// otherwise a crash can leave the final name pointing at unwritten blocks.
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("mediaserver: sync temp: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mediaserver: close temp: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mediaserver: rename: %w", err)
	}
	syncDir(filepath.Dir(dest))
	return nil
}

// ctxReader checks ctx between chunks so shutdown can abort a multi-GB copy —
// a deliberate trade of io.Copy's zero-copy fast path for cancellability.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// syncDir flushes a directory so a completed rename or new hardlink entry
// survives a crash. It is best-effort: Windows can't sync a directory and some
// filesystems reject it, and the file is already correctly in place by this
// point, so a failure never fails an import that otherwise succeeded.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// reservedNames are Windows device names that can't be a path component, even
// with an extension ("CON.txt" is still reserved). A series literally named one
// of these would break on a Windows/SMB share, so sanitize prefixes an underscore.
var reservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// sanitize strips characters that are illegal or awkward in file paths so the
// series name is usable as a directory/file component. It also drops control
// characters and dodges Windows reserved device names, so the layout survives an
// SMB/CIFS share mounted from Windows.
func sanitize(name string) string {
	// Drop control characters (0x00–0x1F and DEL): illegal on most filesystems
	// and invisible/dangerous in a path.
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)

	replacer := strings.NewReplacer(
		"/", " ", "\\", " ",
		":", " -",
		"*", "", "?", "", `"`, "",
		"<", "", ">", "", "|", "",
	)
	out := strings.Join(strings.Fields(replacer.Replace(name)), " ")
	out = strings.TrimRight(out, " .")

	// Dodge Windows reserved device names. The check is on the base (before any
	// extension, since "CON.txt" is reserved too) and case-insensitive.
	if base, _, _ := strings.Cut(out, "."); reservedNames[strings.ToLower(base)] {
		out = "_" + out
	}
	return out
}
