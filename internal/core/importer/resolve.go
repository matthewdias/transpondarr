package importer

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/matthewdias/transpondarr/internal/core/parser"
)

// A completed download's payload is often a directory even when it holds a
// single episode: the release ships the video alongside subtitles, an nfo, a
// sample, or a screenshots folder. resolvePayloadFile picks that one episode
// file so the rest of the pipeline keeps dealing in files — library.Target's
// Place contract stays file-only, and directory-shaped payloads never reach it.
//
// It is deliberately unwilling to guess. It never falls back to "largest file
// wins": a genuine season pack has a largest file too, and force-importing one
// episode out of it would mark the whole grab imported while the rest of the
// pack is silently dropped. When the payload holds more than one plausible
// episode, resolution fails and the grab is deferred, exactly as before.
var (
	// errNoVideoFile means the payload held nothing importable (an archive, a
	// disc structure, an aborted download).
	errNoVideoFile = errors.New("payload contains no video file")
	// errAmbiguousPayload means the payload holds more than one plausible
	// episode — a real batch, which per-file batch import will handle later.
	errAmbiguousPayload = errors.New("payload contains more than one episode file")
)

// videoExts are the container extensions treated as episode content. Everything
// else in the payload (subtitles, nfo, images, checksums) is a sidecar.
var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true, ".mov": true,
	".ts": true, ".m2ts": true, ".webm": true, ".ogm": true, ".wmv": true,
	".flv": true, ".mpg": true, ".mpeg": true, ".rmvb": true, ".divx": true,
}

// skipDirs are payload subdirectories that by convention never hold the episode
// itself. Descending into them is how a sample or a creditless opening sneaks in
// as a second "episode" and makes an otherwise-obvious payload ambiguous.
var skipDirs = map[string]bool{
	"sample": true, "samples": true, "extra": true, "extras": true,
	"featurette": true, "featurettes": true, "bonus": true, "menu": true,
	"screens": true, "screenshots": true, "proof": true,
	"subs": true, "subtitles": true, "trailers": true,
}

// nonEpisodeTokens mark a file as something that ships *with* an episode rather
// than being one. Matching is on whole tokens, not substrings, so a series
// whose title contains "Extraordinary" or "Preview" is not mistaken for an
// extra. Bare "op"/"ed" are deliberately absent: too collision-prone for the
// benefit, and the numbering check below already separates extras from episodes.
var nonEpisodeTokens = map[string]bool{
	"sample": true, "samples": true, "trailer": true, "preview": true,
	"promo": true, "teaser": true, "creditless": true, "nc": true,
	"ncop": true, "nced": true, "menu": true, "extra": true, "extras": true,
	"bonus": true,
}

// candidate is one payload file that could be the episode, with its filename
// parsed so competing candidates can be told apart by episode number.
type candidate struct {
	path   string
	parsed parser.Parsed
}

// resolvePayloadFile walks a directory payload and returns the single file that
// is the episode for wantNumber (the grab's wanted item number; 0 when the item
// carries no number). It returns errNoVideoFile or errAmbiguousPayload when no
// unambiguous choice exists — never a guess.
func resolvePayloadFile(root string, wantNumber int) (string, error) {
	var cands []candidate

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (skipDirs[strings.ToLower(name)] || strings.HasPrefix(name, ".")) {
				return fs.SkipDir
			}
			return nil
		}
		// Only regular files: a symlink's target may live outside the payload, and
		// WalkDir does not follow them anyway.
		if !d.Type().IsRegular() || strings.HasPrefix(name, ".") {
			return nil
		}
		if !videoExts[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		if hasNonEpisodeToken(name) {
			return nil
		}
		cands = append(cands, candidate{path: path, parsed: parser.Parse(name)})
		return nil
	})
	if err != nil {
		return "", err
	}

	switch len(cands) {
	case 0:
		return "", errNoVideoFile
	case 1:
		// Identity by construction: we chose this release for this item, and the
		// payload holds exactly one video file, so that file is the episode. Its
		// filename need not agree — plenty of releases name the file by hash or by
		// absolute number.
		return cands[0].path, nil
	}
	return pickByNumber(cands, wantNumber)
}

// pickByNumber disambiguates several video files by what their names claim.
// Files carrying no episode number at all are extras that escaped the token and
// directory filters, and are ignored. Exactly one file claiming the wanted
// number wins; a file claiming a *different* number means the payload really is
// a batch, and two files claiming the same number is a choice we refuse to make.
func pickByNumber(cands []candidate, wantNumber int) (string, error) {
	if wantNumber <= 0 {
		return "", errAmbiguousPayload // nothing to match against
	}
	match := -1
	for i, c := range cands {
		if !numbered(c.parsed) {
			continue // an extra, not a competing episode
		}
		if !matchesNumber(c.parsed, wantNumber) {
			return "", errAmbiguousPayload // another episode is present: a batch
		}
		if match >= 0 {
			return "", errAmbiguousPayload // two files claim the same episode
		}
		match = i
	}
	if match < 0 {
		return "", errAmbiguousPayload
	}
	return cands[match].path, nil
}

// numbered reports whether a filename claims an episode number at all.
func numbered(p parser.Parsed) bool { return p.EpisodeStart > 0 || p.AbsoluteEpisode > 0 }

// matchesNumber reports whether a filename claims exactly the wanted episode.
// A range or batch marker never matches: one file covering several episodes is
// not this item's file.
func matchesNumber(p parser.Parsed, want int) bool {
	if p.Batch || p.EpisodeEnd != p.EpisodeStart {
		return false
	}
	return p.EpisodeStart == want || p.AbsoluteEpisode == want
}

// hasNonEpisodeToken reports whether a filename is marked as an extra. The name
// is split on non-alphanumeric runs so matching is on whole tokens.
func hasNonEpisodeToken(name string) bool {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	for _, tok := range strings.FieldsFunc(base, func(r rune) bool {
		return !isAlphanumeric(r)
	}) {
		if nonEpisodeTokens[strings.ToLower(tok)] {
			return true
		}
	}
	return false
}

func isAlphanumeric(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}
