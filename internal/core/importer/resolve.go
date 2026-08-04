package importer

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/matthewdias/transpondarr/internal/core/parser"
)

// videoExts are the extensions treated as episode content; every other file in a
// payload is ignored.
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

// sampleTokens are the subset never plausible as a word in a title, so they hold
// their file out of the sole-video relaxation below rather than merely losing to
// a better candidate.
var sampleTokens = map[string]bool{"sample": true, "samples": true}

// candidate is one payload file that could be an episode.
type candidate struct {
	path   string // absolute, as handed to the library target
	rel    string // payload-relative, the identity a retry assignment names
	parsed parser.Parsed
}

// collectPayloadFiles lists every plausible episode file in a payload. A plain
// file is taken as-is — identity by construction, we chose this release — while
// a directory is walked past samples, extras and sidecars.
func collectPayloadFiles(root string) ([]candidate, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		name := filepath.Base(root)
		return []candidate{{path: root, rel: name, parsed: parser.Parse(name)}}, nil
	}

	var cands []candidate
	var sole candidate
	var videos int
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		if hasToken(name, sampleTokens) {
			return nil // a truncated copy of the episode, never the episode
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = name
		}
		c := candidate{path: path, rel: filepath.ToSlash(rel), parsed: parser.Parse(name)}
		videos++
		if videos == 1 {
			sole = c
		}
		if hasNonEpisodeToken(name) {
			return nil
		}
		cands = append(cands, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Identity by construction, as for a plain-file payload: one video and nothing
	// to confuse it with, so an extras token in its name is a word in the title.
	if len(cands) == 0 && videos == 1 {
		return []candidate{sole}, nil
	}
	return cands, nil
}

// hasNonEpisodeToken reports whether a filename is marked as an extra.
func hasNonEpisodeToken(name string) bool { return hasToken(name, nonEpisodeTokens) }

// hasToken reports whether a filename carries any of tokens as a whole word.
func hasToken(name string, tokens map[string]bool) bool {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	for _, tok := range strings.FieldsFunc(base, func(r rune) bool {
		return !isAlphanumeric(r)
	}) {
		if tokens[strings.ToLower(tok)] {
			return true
		}
	}
	return false
}

func isAlphanumeric(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}
