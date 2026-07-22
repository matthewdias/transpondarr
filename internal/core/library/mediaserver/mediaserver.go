// Package mediaserver implements the library.Target interface by placing files
// into a Jellyfin/Plex-friendly layout:
//
//	<root>/<Series Name>/Season 01/<Series Name> - S01EMM<ext>
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
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/matthewdias/transpondarr/internal/core/library"
)

// seasonNumber is the season every entry is filed under. Each AniList entry is
// its own single-season show; a "2nd Season" is a distinct entry/title, not
// Season 02 inside the first entry's folder.
const seasonNumber = 1

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

// Target places imported files into a media-server library layout.
type Target struct {
	root string
	mode Mode
}

// New constructs a media-server layout target rooted at root. mode is
// auto|hardlink|copy (see ParseMode).
func New(root, mode string) *Target {
	return &Target{root: root, mode: ParseMode(mode)}
}

func (t *Target) Name() string { return "mediaserver" }

var _ library.Target = (*Target)(nil)

// Place transfers a single downloaded file into the library and returns its final
// path. A directory source (a batch/season pack) is rejected — per-file batch
// import is a later phase. Existing destinations are treated as already imported.
func (t *Target) Place(_ context.Context, req library.ImportRequest) (string, error) {
	info, err := os.Stat(req.SourcePath)
	if err != nil {
		return "", fmt.Errorf("mediaserver: stat source: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("mediaserver: source is a directory (batch import not yet supported): %s", req.SourcePath)
	}

	name := sanitize(req.Title.Name)
	if name == "" {
		return "", errors.New("mediaserver: empty series name")
	}
	ext := filepath.Ext(req.SourcePath)

	destDir := filepath.Join(t.root, name, fmt.Sprintf("Season %02d", seasonNumber))
	filename := fmt.Sprintf("%s - S%02dE%02d%s", name, seasonNumber, req.Item.Number, ext)
	dest := filepath.Join(destDir, filename)

	if _, err := os.Stat(dest); err == nil {
		return dest, nil // already present — idempotent
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mediaserver: create dir: %w", err)
	}
	if err := t.transfer(req.SourcePath, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// transfer moves bytes from src to dest according to the configured mode. In auto
// mode a hardlink is attempted first and falls back to a copy when the filesystem
// can't hardlink here (a different device, or a mount that doesn't support/permit
// hardlinks at all).
func (t *Target) transfer(src, dest string) error {
	switch t.mode {
	case ModeCopy:
		return copyFile(src, dest)
	case ModeHardlink:
		if err := os.Link(src, dest); err != nil {
			return fmt.Errorf("mediaserver: hardlink: %w", err)
		}
		return nil
	default: // ModeAuto
		if err := os.Link(src, dest); err != nil {
			if isUnsupportedLink(err) {
				return copyFile(src, dest)
			}
			return fmt.Errorf("mediaserver: hardlink: %w", err)
		}
		return nil
	}
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

// copyFile copies src to dest via a temp file + rename, so a failed copy never
// leaves a partial file at the destination path.
func copyFile(src, dest string) error {
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
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("mediaserver: copy: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mediaserver: close temp: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mediaserver: rename: %w", err)
	}
	return nil
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
