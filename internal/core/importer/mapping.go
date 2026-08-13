package importer

import (
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/parser"
)

// fileClaim is a payload file the mapping did not assign, with the episode
// number its name claims (0 when it names none).
type fileClaim struct {
	file   candidate
	number int
}

// mapResult is a payload's files mapped onto the items a release claimed: what
// to place, how many files nothing could tell apart, and what is left over.
type mapResult struct {
	assigned  map[int]candidate
	conflicts map[int]int
	leftovers []fileClaim
}

// mapFiles decides which payload file is which episode. It is pure so the rules
// driving an irreversible file move can be tested exhaustively. overrides are a
// human's explicit answer from the retry endpoint, keyed on the payload-relative
// path, and win over every rule below — being wrong about a filename is the
// whole reason the escape hatch exists. format is the discriminator: a movie is
// identified by size, never by a number in a filename.
func mapFiles(files []candidate, covers map[int]bool, overrides map[string]int, format domain.Format) mapResult {
	res := mapResult{assigned: make(map[int]candidate), conflicts: make(map[int]int)}

	rest := make([]candidate, 0, len(files))
	for _, f := range files {
		n, ok := overrides[f.rel]
		if !ok {
			rest = append(rest, f)
			continue
		}
		if covers[n] {
			res.assigned[n] = f
			continue
		}
		// An episode this release never claimed still goes through the
		// out-of-coverage guard, which only the caller can apply.
		res.leftovers = append(res.leftovers, fileClaim{file: f, number: n})
	}

	if format == domain.FormatMovie {
		return mapMovie(rest, covers, res)
	}

	// Identity by construction: we chose this release, so a lone file is the lone
	// item's episode however unhelpfully it is named.
	if len(overrides) == 0 && len(rest) == 1 && len(covers) == 1 {
		for n := range covers {
			res.assigned[n] = rest[0]
		}
		return res
	}

	claims := make(map[int][]candidate, len(rest))
	var order []int
	for _, f := range rest {
		n := claimNumber(f.parsed, covers)
		if n == 0 {
			res.leftovers = append(res.leftovers, fileClaim{file: f})
			continue
		}
		if _, seen := claims[n]; !seen {
			order = append(order, n)
		}
		claims[n] = append(claims[n], f)
	}

	for _, n := range order {
		_, taken := res.assigned[n]
		if !covers[n] || taken {
			for _, f := range claims[n] {
				res.leftovers = append(res.leftovers, fileClaim{file: f, number: n})
			}
			continue
		}
		best, tied := pickVersion(claims[n])
		if tied {
			// Guessing here silently drops the other file, so the row defers and a
			// human names the one they want. The caller words it: only it knows
			// whether the item is an episode or a movie.
			res.conflicts[n] = len(claims[n])
			continue
		}
		res.assigned[n] = best
	}
	return res
}

// mapMovie takes the payload's largest video as the film. A film is the biggest
// thing shipped with it, which is a property of what a movie payload *is* rather
// than of how a releaser named it — so a numbered extra ("Deleted Scene 1")
// cannot claim the one item the way a number-driven mapping let it. Samples are
// already gone before this sees anything, so size never re-admits one.
func mapMovie(rest []candidate, covers map[int]bool, res mapResult) mapResult {
	item, single := soleItem(covers)
	_, taken := res.assigned[item]
	if !single || taken || len(rest) == 0 {
		// Nothing left to identify: a human's override already answered, or there
		// is no film-shaped question to ask.
		return withLooseFiles(res, rest)
	}
	best, tied := largestVideo(rest)
	if tied > 1 {
		// Taking either silently drops the other, exactly as for same-number claims.
		res.conflicts[item] = tied
		return withLooseFiles(res, rest)
	}
	res.assigned[item] = best
	for _, f := range rest {
		if f.rel != best.rel {
			// A number on a movie's extra establishes nothing, so it names no item.
			res.leftovers = append(res.leftovers, fileClaim{file: f})
		}
	}
	return res
}

// largestVideo returns the biggest candidate and how many share its size.
func largestVideo(cands []candidate) (candidate, int) {
	best, tied := cands[0], 1
	for _, c := range cands[1:] {
		switch {
		case c.size > best.size:
			best, tied = c, 1
		case c.size == best.size:
			tied++
		}
	}
	return best, tied
}

// soleItem is the single item a release covers, reporting false when it covers
// any other number of them.
func soleItem(covers map[int]bool) (int, bool) {
	if len(covers) != 1 {
		return 0, false
	}
	for n := range covers {
		return n, true
	}
	return 0, false
}

// withLooseFiles leaves every remaining file unmatched and unnumbered.
func withLooseFiles(res mapResult, rest []candidate) mapResult {
	for _, f := range rest {
		res.leftovers = append(res.leftovers, fileClaim{file: f})
	}
	return res
}

// claimNumber is the episode a filename claims, whether or not the release
// covers it — an uncovered claim is what lets the importer place a file for an
// adjacent item. A range or a pack names no single episode, so it claims nothing.
func claimNumber(p parser.Parsed, covers map[int]bool) int {
	if p.Batch || p.EpisodeEnd != p.EpisodeStart {
		return 0
	}
	// Season-relative wins when both land inside the release, matching decide's stance.
	if p.EpisodeStart > 0 && covers[p.EpisodeStart] {
		return p.EpisodeStart
	}
	if p.AbsoluteEpisode > 0 && covers[p.AbsoluteEpisode] {
		return p.AbsoluteEpisode
	}
	if p.EpisodeStart > 0 {
		return p.EpisodeStart
	}
	return p.AbsoluteEpisode
}

// pickVersion resolves same-episode claimants, reporting a tie nothing separates.
func pickVersion(cands []candidate) (candidate, bool) {
	best, tied := cands[0], false
	for _, c := range cands[1:] {
		switch cmp := compareVersion(c.parsed, best.parsed); {
		case cmp > 0:
			best, tied = c, false
		case cmp == 0:
			tied = true
		}
	}
	return best, tied
}

// compareVersion orders two claims on one episode: the higher version, then a
// repack over the release it replaces.
func compareVersion(a, b parser.Parsed) int {
	switch va, vb := releaseVersion(a), releaseVersion(b); {
	case va > vb:
		return 1
	case va < vb:
		return -1
	case a.Repack && !b.Repack:
		return 1
	case !a.Repack && b.Repack:
		return -1
	}
	return 0
}

// releaseVersion reads an unmarked release as v1, so a v2 outranks it.
func releaseVersion(p parser.Parsed) int {
	if p.Version == 0 {
		return 1
	}
	return p.Version
}
