package importer

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/matthewdias/transpondarr/internal/core/parser"
)

var (
	errNoVideoFile      = errors.New("payload contains no video file")
	errAmbiguousPayload = errors.New("payload contains more than one episode file")
)

// videoExts are the extensions treated as episode content; everything else in a
// payload is a sidecar.
var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true, ".mov": true,
	".ts": true, ".m2ts": true, ".webm": true, ".ogm": true, ".wmv": true,
	".flv": true, ".mpg": true, ".mpeg": true, ".rmvb": true, ".divx": true,
}

// skipDirs never hold the episode itself; descending into them is how a sample
// becomes a second candidate and makes an obvious payload look ambiguous.
var skipDirs = map[string]bool{
	"sample": true, "samples": true, "extra": true, "extras": true,
	"featurette": true, "featurettes": true, "bonus": true, "menu": true,
	"screens": true, "screenshots": true, "proof": true,
	"subs": true, "subtitles": true, "trailers": true,
}

// nonEpisodeTokens mark a file as shipping *with* an episode. Matched as whole
// tokens, so "Extraordinary" is not an extra; bare op/ed are too collision-prone.
var nonEpisodeTokens = map[string]bool{
	"sample": true, "samples": true, "trailer": true, "preview": true,
	"promo": true, "teaser": true, "creditless": true, "nc": true,
	"ncop": true, "nced": true, "menu": true, "extra": true, "extras": true,
	"bonus": true,
}

// candidate is one payload file that could be the episode.
type candidate struct {
	path   string
	parsed parser.Parsed
}

// resolvePayloadFile returns the one file in a directory payload that is the
// episode for wantNumber, never a guess. No largest-file fallback: a season pack
// has a largest file too, and taking it would silently drop the rest of the pack.
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
		// Regular files only: a symlink may point outside the payload.
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
		// Identity by construction: we chose this release, so the sole video is the
		// episode however unhelpfully it is named.
		return cands[0].path, nil
	}
	return pickByNumber(cands, wantNumber)
}

// pickByNumber returns the sole candidate claiming wantNumber, refusing as soon
// as any other plausible episode is present.
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

// matchesNumber reports whether a filename claims exactly the wanted episode; a
// range or batch marker never does.
func matchesNumber(p parser.Parsed, want int) bool {
	if p.Batch || p.EpisodeEnd != p.EpisodeStart {
		return false
	}
	return p.EpisodeStart == want || p.AbsoluteEpisode == want
}

// hasNonEpisodeToken reports whether a filename is marked as an extra.
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
