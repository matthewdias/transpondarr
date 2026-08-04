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
		if hasNonEpisodeToken(name) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = name
		}
		cands = append(cands, candidate{path: path, rel: filepath.ToSlash(rel), parsed: parser.Parse(name)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cands, nil
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
