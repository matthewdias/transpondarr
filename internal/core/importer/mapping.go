package importer

import (
	"fmt"

	"github.com/matthewdias/transpondarr/internal/core/parser"
)

// fileClaim is a payload file the mapping did not assign, with the episode
// number its name claims (0 when it names none).
type fileClaim struct {
	file   candidate
	number int
}

// mapResult is a payload's files mapped onto the items a release claimed: what
// to place, what nothing could tell apart, and what is left over.
type mapResult struct {
	assigned  map[int]candidate
	conflicts map[int]string
	leftovers []fileClaim
}

// mapFiles decides which payload file is which episode. It is pure so the rules
// driving an irreversible file move can be tested exhaustively. overrides are a
// human's explicit answer from the retry endpoint, keyed on the payload-relative
// path, and win over every rule below — being wrong about a filename is the
// whole reason the escape hatch exists.
func mapFiles(files []candidate, covers map[int]bool, overrides map[string]int) mapResult {
	res := mapResult{assigned: make(map[int]candidate), conflicts: make(map[int]string)}

	rest := make([]candidate, 0, len(files))
	for _, f := range files {
		if n, ok := overrides[f.rel]; ok {
			res.assigned[n] = f
			continue
		}
		rest = append(rest, f)
	}

	// Identity by construction: we chose this release, so a lone file is the lone
	// item's episode however unhelpfully it is named.
	if len(res.assigned) == 0 && len(rest) == 1 && len(covers) == 1 {
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
			// human names the one they want.
			res.conflicts[n] = fmt.Sprintf("%d files claim episode %d and nothing tells them apart", len(claims[n]), n)
			continue
		}
		res.assigned[n] = best
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
