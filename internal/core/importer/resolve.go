package importer

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// headExts open an archive set. Disc images, .par2 and .sfv are deliberately
// absent: none of them is something a human extracts an episode out of.
var headExts = map[string]bool{".rar": true, ".zip": true, ".7z": true}

// continuationExt matches the volumes that follow a head: .r00-.r99 after a
// .rar, .z01-.z99 after a .zip, and .001-.999 after a .7z.
var continuationExt = regexp.MustCompile(`^\.(?:[rz][0-9]{2}|[0-9]{3})$`)

// partVolume matches the other multipart rar scheme, where every volume is a
// .rar and the head is the one numbered 1.
var partVolume = regexp.MustCompile(`(?i)\.part([0-9]+)$`)

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
	size   int64  // what identifies a movie, where numbering says nothing
	parsed parser.Parsed
}

// archive is one archive set: what a human extracts, and how many volumes it spans.
type archive struct {
	rel   string // payload-relative path of the volume to open
	parts int
}

// payload is what one walk found. Archives ride beside the candidates rather than
// among them, so nothing downstream can place one in the library.
type payload struct {
	files    []candidate
	archives []archive
}

// collectPayloadFiles lists every plausible episode file in a payload. A plain
// file is taken as-is — identity by construction, we chose this release — while
// a directory is walked past samples, extras and sidecars.
func collectPayloadFiles(root string) (payload, error) {
	info, err := os.Stat(root)
	if err != nil {
		return payload{}, err
	}
	if !info.IsDir() {
		name := filepath.Base(root)
		if _, _, ok := archivePart(name); ok {
			return payload{archives: []archive{{rel: name, parts: 1}}}, nil
		}
		return payload{files: []candidate{{path: root, rel: name, size: info.Size(), parsed: parser.Parse(name)}}}, nil
	}

	var cands []candidate
	var sole candidate
	var videos int
	sets := newArchiveSets()
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
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = name
		}
		rel = filepath.ToSlash(rel)
		if set, first, ok := archivePart(name); ok {
			sets.add(filepath.ToSlash(filepath.Dir(rel))+"/"+set, rel, first)
			return nil
		}
		if !videoExts[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		if hasToken(name, sampleTokens) {
			return nil // a truncated copy of the episode, never the episode
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		c := candidate{path: path, rel: rel, size: fi.Size(), parsed: parser.Parse(name)}
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
		return payload{}, err
	}
	// Identity by construction, as for a plain-file payload: one video and nothing
	// to confuse it with, so an extras token in its name is a word in the title.
	if len(cands) == 0 && videos == 1 {
		cands = []candidate{sole}
	}
	return payload{files: cands, archives: sets.list()}, nil
}

// noVideoReason names why a payload yielded nothing, so a settled deferral says
// what to do rather than only what failed.
func noVideoReason(archives []archive) string {
	const prefix = "the payload holds no video file"
	if len(archives) == 0 {
		return prefix
	}
	return fmt.Sprintf("%s, only %s; %s", prefix, archiveSummary(archives), extractAdvice(archives))
}

// archiveSummary names what a human would extract. Callers guard against an
// empty set, since "no archive" is never something to describe.
func archiveSummary(archives []archive) string {
	switch {
	case len(archives) > 1:
		return fmt.Sprintf("%d archive sets (%s)", len(archives), archiveNames(archives))
	case archives[0].parts > 1:
		return fmt.Sprintf("a %d-part archive set (%s)", archives[0].parts, filepath.Base(archives[0].rel))
	default:
		return fmt.Sprintf("the archive %q", filepath.Base(archives[0].rel))
	}
}

// archiveNames lists what to go and find, capped so a season of rars does not
// bury the instruction that follows it.
func archiveNames(archives []archive) string {
	const cap = 3
	shown := archives
	if len(shown) > cap {
		shown = shown[:cap]
	}
	names := make([]string, 0, len(shown))
	for _, a := range shown {
		names = append(names, filepath.Base(a.rel))
	}
	out := strings.Join(names, ", ")
	if rest := len(archives) - len(shown); rest > 0 {
		out += fmt.Sprintf(", and %d more", rest)
	}
	return out
}

func extractAdvice(archives []archive) string {
	what := "it"
	if len(archives) > 1 {
		what = "them"
	}
	return "Transpondarr does not unpack archives, so extract " + what +
		" into the download folder and use Fix import"
}

// archivePart reports the set a filename belongs to and whether it is the volume
// a human opens, or ok=false when the name is not an archive at all.
func archivePart(name string) (set string, first, ok bool) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	switch lower := strings.ToLower(ext); {
	case headExts[lower]:
		if m := partVolume.FindStringSubmatch(stem); m != nil {
			n, _ := strconv.Atoi(m[1])
			return stem[:len(stem)-len(m[0])], n == 1, true
		}
		return stem, true, true
	case continuationExt.MatchString(lower):
		// A bare .NNN is a volume only behind a .7z; on its own it is anyone's file.
		if lower[1] >= '0' && lower[1] <= '9' {
			if !strings.HasSuffix(strings.ToLower(stem), ".7z") {
				return "", false, false
			}
			return stem[:len(stem)-len(".7z")], false, true
		}
		return stem, false, true
	}
	return "", false, false
}

// archiveSets groups volumes in walk order, because map order is random and both
// the deferral reason and the retry dialog need a stable answer.
type archiveSets struct {
	order []string
	sets  map[string]*archive
	heads map[string]bool
}

func newArchiveSets() *archiveSets {
	return &archiveSets{sets: map[string]*archive{}, heads: map[string]bool{}}
}

func (s *archiveSets) add(key, rel string, first bool) {
	set, ok := s.sets[key]
	if !ok {
		set = &archive{rel: rel}
		s.sets[key], s.order = set, append(s.order, key)
	}
	set.parts++
	// Without a head volume — a folder holding only .r00, .r01 — the smallest
	// name stands in, so the set is always named by something that exists.
	switch {
	case first:
		set.rel, s.heads[key] = rel, true
	case !s.heads[key] && rel < set.rel:
		set.rel = rel
	}
}

func (s *archiveSets) list() []archive {
	out := make([]archive, 0, len(s.order))
	for _, key := range s.order {
		out = append(out, *s.sets[key])
	}
	return out
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
