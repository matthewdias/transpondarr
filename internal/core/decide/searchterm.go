package decide

import "strings"

// AniList typography that never appears in release names: mapped to a space so
// the surrounding words still hit a Torznab full-text search.
const separatorTypography = "・☆♪★～〜½()[]"

// SearchTerm sanitizes a stored title into an indexer query term: releases
// write "×" as "x" and never carry the rest of AniList's typography.
func SearchTerm(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r == '×':
			b.WriteString(" x ")
		case strings.ContainsRune(separatorTypography, r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// SearchTerms returns the sanitized query terms to try in order — the stored
// title first, then each variant as a zero-result fallback — deduped
// case-insensitively so a variant differing only in typography costs no query.
func SearchTerms(primary string, variants []string) []string {
	seen := make(map[string]bool, len(variants)+1)
	out := make([]string, 0, len(variants)+1)
	for _, t := range append([]string{primary}, variants...) {
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
