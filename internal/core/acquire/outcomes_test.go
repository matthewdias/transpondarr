package acquire

import (
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// The precedence the walk depends on: a candidate that settled an item is the
// last word, while a skip that left the item uncovered must not shout down the
// lower-ranked candidate that goes on to take it.
func TestOutcomeSetSettlingOverwritesTentative(t *testing.T) {
	set := outcomeSet{}
	set.tentative([]int{1}, outcome{kind: OutcomeContended, release: "contended release"})
	set.settle([]int{1}, outcome{kind: OutcomeGrabbed, release: "grabbed release"})
	if got := set[1]; got.kind != OutcomeGrabbed || got.release != "grabbed release" {
		t.Errorf("outcome = %+v, want the grab to overwrite the contention", got)
	}

	set = outcomeSet{}
	set.settle([]int{2}, outcome{kind: OutcomePinHeld, release: "held release"})
	set.tentative([]int{2}, outcome{kind: OutcomeDeclined, release: "declined release"})
	if got := set[2]; got.kind != OutcomePinHeld {
		t.Errorf("outcome = %+v, want the hold to stand: a tentative fills empty slots only", got)
	}

	// Two tentatives: the first one wins, because the walk is ranked.
	set = outcomeSet{}
	set.tentative([]int{3}, outcome{kind: OutcomeContended})
	set.tentative([]int{3}, outcome{kind: OutcomeDeclined})
	if got := set[3]; got.kind != OutcomeContended {
		t.Errorf("outcome = %+v, want the higher-ranked candidate's", got)
	}
}

// Every outcome is classified, and the two classes are what the precedence
// rests on: adding one without deciding which it is would make it silently
// tentative.
func TestEveryOutcomeIsClassified(t *testing.T) {
	cases := map[string]bool{
		OutcomeGrabbed:   true,
		OutcomeWouldGrab: true,
		OutcomePinHeld:   true,
		OutcomeContended: false,
		OutcomeAddFailed: false,
		OutcomeDeclined:  false,
		OutcomeNoMatch:   false,
	}
	if len(cases) != len(AllOutcomes) {
		t.Fatalf("classified %d outcomes, want all %d", len(cases), len(AllOutcomes))
	}
	for _, kind := range AllOutcomes {
		want, listed := cases[kind]
		if !listed {
			t.Errorf("outcome %q is not classified", kind)
			continue
		}
		if got := settling(kind); got != want {
			t.Errorf("settling(%q) = %t, want %t", kind, got, want)
		}
	}
}

func refused(title string, score, seeders int, pinned bool, items ...int) decide.Candidate {
	return decide.Candidate{
		Release:          indexer.Release{Title: title, Seeders: seeders},
		Matched:          true,
		Items:            items,
		Score:            score,
		Eligible:         false,
		IneligibleReason: "below the profile floor",
		Pinned:           pinned,
	}
}

// Blame drops decide's coverage tier deliberately. Coverage exists for grab
// efficiency -- one grab instead of N (#126) -- and says nothing about which
// release came closest for one episode, so inheriting it would let a wide
// low-scoring pack outrank a high-scoring single covering exactly the episode
// asked about.
func TestBestRefusalIgnoresTheCoverageTier(t *testing.T) {
	pack := refused("[Batchers] Sample Show 01-12", 100, 10, false, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12)
	single := refused("[SynthSubs] Sample Show - 07", 900, 5, false, 7)

	release, reason := bestRefusal([]decide.Candidate{pack, single}, map[int]bool{7: true})
	if release != single.Release.Title {
		t.Errorf("blamed %q, want the high-scoring single covering episode 7", release)
	}
	if reason != "below the profile floor" {
		t.Errorf("reason = %q, want the candidate's own", reason)
	}
}

// Pinned still outranks score: a pin is per-series knowledge about which group
// is definitive, so its near miss is the one worth reporting.
func TestBestRefusalPrefersThePinnedGroup(t *testing.T) {
	loud := refused("[LoudSubs] Sample Show - 07", 900, 50, false, 7)
	pinned := refused("[PinnedSubs] Sample Show - 07", 100, 5, true, 7)

	release, _ := bestRefusal([]decide.Candidate{loud, pinned}, map[int]bool{7: true})
	if release != pinned.Release.Title {
		t.Errorf("blamed %q, want the pinned group's release", release)
	}
}

// A refusal that covers none of the numbers asked about is not this item's
// story, and an eligible candidate was refused for something other than the
// profile -- neither may be blamed.
func TestBestRefusalIgnoresUncoveringAndEligibleCandidates(t *testing.T) {
	elsewhere := refused("[SynthSubs] Sample Show - 03", 900, 50, false, 3)
	eligible := decide.Candidate{
		Release: indexer.Release{Title: "[SynthSubs] Sample Show - 07", Seeders: 50},
		Matched: true, Items: []int{7}, Score: 900, Eligible: true,
	}

	release, reason := bestRefusal([]decide.Candidate{elsewhere, eligible}, map[int]bool{7: true})
	if release != "" || reason != "" {
		t.Errorf("blamed %q (%q), want nothing", release, reason)
	}
}
