package decide

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// asciiPunct folds typography with a direct ASCII counterpart that NFKD leaves
// alone; "×" gets spaces because releases write it as a standalone "x".
var asciiPunct = map[rune]string{
	'×': " x ",
	'’': "'", '‘': "'", '“': `"`, '”': `"`,
	'‐': "-", '–': "-", '—': "-", '―': "-",
}

// foldTitle transliterates a title toward release-name ASCII — NFKD folds
// compatibility forms (fullwidth, ½ → 1⁄2, Ⅲ → III), Latin accents drop, and
// asciiPunct maps the rest. SearchTerm and normalize share it so the search
// finds exactly the releases the matcher can accept (#107).
func foldTitle(s string) string {
	if isASCII(s) {
		return s
	}
	var b strings.Builder
	latinBase := false
	for _, r := range s {
		if repl, ok := asciiPunct[r]; ok {
			b.WriteString(repl)
			latinBase = false
			continue
		}
		d := norm.NFKD.String(string(r))
		// ½ → "1⁄2" gets spaces so the digits don't glue to adjacent words;
		// letter expansions (ﬁ → "fi") must stay glued.
		pad := unicode.IsNumber(r) && utf8.RuneCountInString(d) > 1
		if pad {
			b.WriteRune(' ')
		}
		for _, e := range d {
			// Combining marks: an accent on a Latin base drops, but kana voicing
			// marks (U+3099/309A) must survive for NFC to recompose ズ from ス.
			if unicode.Is(unicode.Mn, e) {
				if !latinBase {
					b.WriteRune(e)
				}
				continue
			}
			b.WriteRune(e)
			latinBase = e < utf8.RuneSelf && unicode.IsLetter(e)
		}
		if pad {
			b.WriteRune(' ')
		}
	}
	return norm.NFC.String(b.String())
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// searchWrappers is ASCII wrapper typography a release search never needs.
const searchWrappers = "()[]{}~"

// SearchTerm sanitizes a stored title into an indexer query term: fold to
// release-name ASCII, then any leftover non-letter symbol becomes a space.
func SearchTerm(title string) string {
	var b strings.Builder
	for _, r := range foldTitle(title) {
		switch {
		case strings.ContainsRune(searchWrappers, r):
			b.WriteRune(' ')
		case r < utf8.RuneSelf, unicode.IsLetter(r), unicode.IsNumber(r), unicode.IsMark(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// SearchTerms returns the sanitized query terms to try in order — first title
// first, later ones as zero-result fallbacks — deduped case-insensitively so a
// variant differing only in typography costs no query.
func SearchTerms(titles []string) []string {
	seen := make(map[string]bool, len(titles))
	out := make([]string, 0, len(titles))
	for _, t := range titles {
		s := SearchTerm(t)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}
