package decide

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/matthewdias/transpondarr/internal/core/parser"
)

// anitogo's own bounds, so both readings of a four-digit token use the same
// range of years.
const (
	minReleaseYear = 1900
	maxReleaseYear = 2050
)

// movieCandidate is the movie path (#209): title, already checked by the caller,
// plus year. Episode numbers, season markers and batch tokens all appear on movie
// names and cannot map onto a film, so none of the episode-mapping apparatus
// runs. Skipping the mapping is not skipping the numeric guards, though — the
// title check is fuzzy containment, so a long-runner sharing a name prefix with a
// film is evaluated here with an episode number the film plainly does not have.
func movieCandidate(c Candidate, variants []string, itemSet map[int]bool, held map[int]heldRelease, titleYear int) Candidate {
	p := c.Parsed
	if p.EpisodeStart > 0 && !numberNamesTheFilm(p, variants) {
		if p.EpisodeEnd > p.EpisodeStart {
			c.Reason = fmt.Sprintf("release spans episodes %d-%d, which this film does not have",
				p.EpisodeStart, p.EpisodeEnd)
		} else {
			c.Reason = fmt.Sprintf("release names episode %d, which this film does not have", p.EpisodeStart)
		}
		return c
	}

	// A title with no year on record still matches — its refusal is an ineligible
	// reason, so a manual grab stays free (PR #57).
	if y := releaseYear(p, variants); y != 0 && titleYear != 0 && y != titleYear {
		c.Reason = fmt.Sprintf("year %d does not match this entry (year %d)", y, titleYear)
		return c
	}

	covered := slices.Sorted(maps.Keys(itemSet))
	if len(covered) == 0 {
		c.Reason = "movie already in the library / not wanted"
		return c
	}
	c.Matched, c.Items = true, covered
	c.Reason = "movie matches a wanted item"
	for _, n := range covered {
		if _, ok := held[n]; ok {
			c.Reason = "movie upgrades a held item"
			break
		}
	}
	return c
}

// numberNamesTheFilm reports whether the number anitogo read as an episode is
// part of the film's name — a sequel number, "Sample Film 2" — by
// reattaching it to the parsed title and comparing against the variants, the
// only thing that can distinguish the two. Padded widths are tried because a
// release writes "0080" where anitogo returns 80.
//
// Compared exactly, not by titleBelongs' containment: on a name we assembled,
// any variant prefixing the parsed title is contained by
// construction, so a fuzzy answer here can only ever be a false positive.
func numberNamesTheFilm(p parser.Parsed, variants []string) bool {
	if p.EpisodeEnd > p.EpisodeStart {
		return false // a range spans episodes; a film is one thing
	}
	base := normalize(p.Title)
	if base == "" {
		return false
	}
	for width := 1; width <= 4; width++ {
		if slices.Contains(variants, fmt.Sprintf("%s%0*d", base, width, p.EpisodeStart)) {
			return true
		}
	}
	return false
}

// releaseYear is the year a release names, resolving the ambiguity the parser
// deliberately does not settle: anitogo reports a year only when the name isolates
// one in brackets, so the scene form glues it onto the parsed title instead. Whichever
// source it came from, a year an accepted variant contains names the film rather
// than the release ("Placeholder Legend 1979") — so the variant check is applied
// once, after the derivation, and bracket style cannot change the verdict. A
// collision reports no year and so passes the year check, which is the direction a
// manual grab must never be blocked in.
func releaseYear(p parser.Parsed, variants []string) int {
	y := p.Year
	if y == 0 {
		y = yearInTitle(p.Title)
	}
	if y == 0 || carriedByVariant(y, variants) {
		return 0
	}
	return y
}

// yearInTitle is the year a scene-form name glued onto the parsed title: the rightmost
// four-digit token in range, since unrecognized scene tags come after it — but never
// the first token, which is the film naming itself.
func yearInTitle(title string) int {
	fields := strings.Fields(title)
	for i := len(fields) - 1; i >= 1; i-- {
		n, err := strconv.Atoi(fields[i])
		if err != nil || len(fields[i]) != 4 || n < minReleaseYear || n > maxReleaseYear {
			continue
		}
		return n
	}
	return 0
}

func carriedByVariant(year int, variants []string) bool {
	s := strconv.Itoa(year)
	for _, v := range variants {
		if strings.Contains(v, s) {
			return true
		}
	}
	return false
}
