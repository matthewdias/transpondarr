package decide

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/matthewdias/transpondarr/internal/core/parser"
)

// anitogo's own bounds, so both readings of a four-digit token agree on what
// could be a year at all.
const (
	minReleaseYear = 1900
	maxReleaseYear = 2050
)

// movieCandidate is the movie path (#209): title, already gated by the caller,
// plus year. Episode numbers, season markers and batch tokens all appear on
// movie names and mean nothing there, so none of the episode-mapping apparatus
// is reached rather than being special-cased around.
func movieCandidate(c Candidate, variants []string, itemSet map[int]bool, held map[int]heldRelease, titleYear int) Candidate {
	// A title with no year on record still matches — the refusal it earns is an
	// ineligible reason, so a manual grab stays free (PR #57).
	if y := releaseYear(c.Parsed, variants); y != 0 && titleYear != 0 && y != titleYear {
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

// releaseYear is the year a release names, resolving the ambiguity the parser
// deliberately leaves alone: anitogo reports a year only when the name isolates
// one in brackets, so the scene form glues it onto the title instead. A
// four-digit tail token is therefore the release year unless an accepted variant
// carries it, in which case it belongs to the title. An incidental digit match
// reports no year and so passes the gate, which is the direction a manual grab
// must never be blocked in.
func releaseYear(p parser.Parsed, variants []string) int {
	if p.Year != 0 {
		return p.Year
	}
	fields := strings.Fields(p.Title)
	if len(fields) < 2 {
		return 0 // a title that is only a year names no film
	}
	tail := fields[len(fields)-1]
	n, err := strconv.Atoi(tail)
	if err != nil || len(tail) != 4 || n < minReleaseYear || n > maxReleaseYear {
		return 0
	}
	for _, v := range variants {
		if strings.Contains(v, tail) {
			return 0
		}
	}
	return n
}
