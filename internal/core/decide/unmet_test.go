package decide

import (
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/parser"
)

// All fixtures use invented title/group names; only the naming structure under
// test is real.

func unmetProfile() domain.QualityProfile {
	return domain.QualityProfile{
		Groups:          []string{"TopSubs", "MidSubs"},
		ResolutionOrder: []string{"1080p", "720p"},
	}
}

func goalSet(t *testing.T, goals []ScorePart) map[string]int {
	t.Helper()
	out := make(map[string]int, len(goals))
	for _, g := range goals {
		out[g.Label] = g.Points
	}
	return out
}

// A goal is an axis the profile prefers that the release scores below its best
// on, carrying the points still available -- the gap, not the earnings.
func TestUnmetGoalsNamesTheGap(t *testing.T) {
	got := goalSet(t, UnmetGoals(
		parser.Parse("[MidSubs] Placeholder Saga - 03 [720p]"), unmetProfile()))
	if len(got) != 2 {
		t.Fatalf("goals = %v, want the group and resolution gaps only", got)
	}
	if got["group TopSubs"] != 100 {
		t.Errorf("group gap = %d, want the 100 between rank 2 and rank 1", got["group TopSubs"])
	}
	if got["resolution 1080p"] != 100 {
		t.Errorf("resolution gap = %d, want the 100 between rank 2 and rank 1", got["resolution 1080p"])
	}
}

// The profile's best release has no goals left, so the list is empty rather
// than a zero-point entry per axis.
func TestUnmetGoalsEmptyAtTheTop(t *testing.T) {
	goals := UnmetGoals(
		parser.Parse("[TopSubs] Placeholder Saga - 03 [1080p]"), unmetProfile())
	if len(goals) != 0 {
		t.Fatalf("goals = %v, want none for the profile's best", goals)
	}
}

// An axis the release misses entirely owes the whole axis, and a preference
// axis the profile never stated is not a goal at all.
func TestUnmetGoalsUnrankedAndPreferenceAxes(t *testing.T) {
	profile := unmetProfile()
	profile.PreferredSource = "bd"
	profile.PreferDualAudio = true
	got := goalSet(t, UnmetGoals(
		parser.Parse("[StrangerSubs] Placeholder Saga - 03 (web)"), profile))
	if got["group TopSubs"] != 2000 {
		t.Errorf("unranked group owes %d, want the whole 2000", got["group TopSubs"])
	}
	if got["resolution 1080p"] != 400 {
		t.Errorf("unlabelled resolution owes %d, want the whole 400", got["resolution 1080p"])
	}
	if got["source bd"] != 150 || got["dual audio"] != 100 {
		t.Errorf("goals = %v, want the stated source and dual-audio preferences", got)
	}
	if _, ok := got["subs softsub"]; ok {
		t.Error("subs was never a stated preference and must not be a goal")
	}
}
