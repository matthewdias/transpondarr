package acquire

import (
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
)

// What a pass decided about one wanted item (#181). Exported because
// internal/server reads these rows back: one vocabulary means the stored text
// and the API's enum cannot drift apart.
const (
	OutcomeGrabbed   = "grabbed"
	OutcomeWouldGrab = "would_grab"
	OutcomePinHeld   = "pin_held"
	OutcomeContended = "contended"
	OutcomeAddFailed = "add_failed"
	OutcomeDeclined  = "declined"
	OutcomeNoMatch   = "no_match"
)

// AllOutcomes is the closed set, so a reader can assert it has handled it whole.
var AllOutcomes = []string{
	OutcomeGrabbed, OutcomeWouldGrab, OutcomePinHeld,
	OutcomeContended, OutcomeAddFailed, OutcomeDeclined, OutcomeNoMatch,
}

// outcome is one item's row before it has an id: what was decided, the release
// it was decided about, and the window a hold runs to.
type outcome struct {
	kind      string
	release   string
	detail    string
	heldUntil time.Time
}

// settling reports whether an outcome closed the item for this pass. The three
// that do are exactly the ones that mark their items covered.
func settling(kind string) bool {
	switch kind {
	case OutcomeGrabbed, OutcomeWouldGrab, OutcomePinHeld:
		return true
	}
	return false
}

// outcomeSet is what one pass decided, by item number.
type outcomeSet map[int]outcome

// settle records a decision that closed its items, overwriting whatever an
// earlier candidate left there.
func (s outcomeSet) settle(numbers []int, o outcome) {
	for _, n := range numbers {
		s[n] = o
	}
}

// tentative records a decision that left its items uncovered, so it only fills
// an empty slot: a lower-ranked candidate may still settle them.
func (s outcomeSet) tentative(numbers []int, o outcome) {
	for _, n := range numbers {
		if _, taken := s[n]; !taken {
			s[n] = o
		}
	}
}

// bestRefusal names the matched-but-refused candidate that came closest for the
// given item numbers, or nothing when none covers them.
//
// It re-ranks rather than trusting the incoming order, because decide's
// comparator puts coverage above score: coverage buys grab efficiency (#126)
// and says nothing about which release came closest for one episode, so
// inheriting it would blame a wide low-scoring pack over a high-scoring single
// covering exactly the episode asked about. Pinned stays on top -- a pin is
// per-series knowledge about which group is definitive.
func bestRefusal(cands []decide.Candidate, numbers map[int]bool) (release, reason string) {
	var best *decide.Candidate
	for i, c := range cands {
		if !c.Matched || c.Eligible || !coversAny(c.Items, numbers) {
			continue
		}
		if best == nil || closerMiss(c, *best) {
			best = &cands[i]
		}
	}
	if best == nil {
		return "", ""
	}
	return best.Release.Title, best.IneligibleReason
}

func coversAny(items []int, numbers map[int]bool) bool {
	for _, n := range items {
		if numbers[n] {
			return true
		}
	}
	return false
}

// closerMiss is bestRefusal's ordering: pinned, then score, then seeders. Ties
// keep the incumbent, so decide's ranking still breaks what this does not.
func closerMiss(c, best decide.Candidate) bool {
	if c.Pinned != best.Pinned {
		return c.Pinned
	}
	if c.Score != best.Score {
		return c.Score > best.Score
	}
	return c.Release.Seeders > best.Release.Seeders
}
