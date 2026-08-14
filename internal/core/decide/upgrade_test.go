package decide

import (
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// All fixtures use invented title/group names; only the naming structure under
// test is real.

// upgradeProfile ranks TopSubs above MidSubs, so a held release and a candidate
// score on the same axes: group 2000/1900, resolution 400/300/200.
func upgradeProfile(cutoff int) domain.QualityProfile {
	return domain.QualityProfile{
		Groups:               []string{"TopSubs", "MidSubs"},
		ResolutionOrder:      []string{"1080p", "720p", "480p"},
		UpgradesEnabled:      true,
		CutoffScore:          cutoff,
		UpgradeV2AboveCutoff: true,
	}
}

// heldItems is a 12-item entry holding item 3, the only shape these cases need.
func heldItems(heldTitle string) []Item {
	its := items(12)
	its[2].HeldTitle = heldTitle
	return its
}

// candidateFor returns the candidate for a release title, which is what the
// upgrade policy annotates.
func candidateFor(t *testing.T, got []Candidate, title string) Candidate {
	t.Helper()
	for _, c := range got {
		if c.Release.Title == title {
			return c
		}
	}
	t.Fatalf("no candidate for %q", title)
	return Candidate{}
}

const held480 = "[TopSubs] Placeholder Saga - 03 [480p]"   // 2200
const held1080 = "[TopSubs] Placeholder Saga - 03 [1080p]" // 2400

// The upgrade policy is cutoff, not chase: below the cutoff any strictly better
// release is taken, at or above it only the v2/repack carve-out gets through.
func TestUpgradePolicy(t *testing.T) {
	cases := []struct {
		name    string
		profile domain.QualityProfile
		held    string
		release string
		want    bool   // the candidate may upgrade the held item
		reason  string // substring of the refusal, when blocked
	}{
		{
			name:    "a strictly better release below cutoff is taken",
			profile: upgradeProfile(2400),
			held:    held480,
			release: "[TopSubs] Placeholder Saga - 03 [1080p]",
			want:    true,
		},
		{
			name:    "an equal release is not an upgrade",
			profile: upgradeProfile(2400),
			held:    held480,
			release: "[TopSubs] Placeholder Saga - 03 [480p]",
			reason:  "does not beat the held release",
		},
		{
			name:    "a worse release is not an upgrade",
			profile: upgradeProfile(2400),
			held:    held480,
			release: "[MidSubs] Placeholder Saga - 03 [480p]",
			reason:  "does not beat the held release",
		},
		{
			name:    "a better release above the cutoff is left alone",
			profile: upgradeProfile(2000),
			held:    held480,
			release: "[TopSubs] Placeholder Saga - 03 [1080p]",
			reason:  "already meets the profile cutoff",
		},
		{
			name:    "a v2 of what we hold passes the cutoff",
			profile: upgradeProfile(2400),
			held:    held1080,
			release: "[TopSubs] Placeholder Saga - 03v2 [1080p]",
			want:    true,
		},
		{
			name:    "a repack of what we hold passes the cutoff",
			profile: upgradeProfile(2400),
			held:    held1080,
			release: "[TopSubs] Placeholder Saga - 03 [1080p] [REPACK]",
			want:    true,
		},
		{
			name:    "another group's v2 is not a fix for ours",
			profile: upgradeProfile(2400),
			held:    held1080,
			release: "[MidSubs] Placeholder Saga - 03v2 [1080p]",
			reason:  "already meets the profile cutoff",
		},
		{
			name:    "a v2 at another resolution is a different release",
			profile: upgradeProfile(2400),
			held:    held1080,
			release: "[TopSubs] Placeholder Saga - 03v2 [720p]",
			reason:  "already meets the profile cutoff",
		},
		{
			name: "the carve-out is a per-profile toggle",
			profile: func() domain.QualityProfile {
				p := upgradeProfile(2400)
				p.UpgradeV2AboveCutoff = false
				return p
			}(),
			held:    held1080,
			release: "[TopSubs] Placeholder Saga - 03v2 [1080p]",
			reason:  "already meets the profile cutoff",
		},
		{
			name: "a profile that never opted in upgrades nothing",
			profile: func() domain.QualityProfile {
				p := upgradeProfile(2400)
				p.UpgradesEnabled = false
				return p
			}(),
			held:    held480,
			release: "[TopSubs] Placeholder Saga - 03 [1080p]",
			reason:  "upgrades are not enabled",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Match(heldItems(tc.held), []string{"Placeholder Saga"},
				[]indexer.Release{{Title: tc.release, Seeders: 10}}, tc.profile)
			c := candidateFor(t, got, tc.release)
			if !c.Matched {
				t.Fatalf("release did not match a held item: %s", c.Reason)
			}
			taken := contains(c.UpgradeItems, 3)
			blocked, wasBlocked := c.UpgradeBlocked[3]
			if taken == wasBlocked {
				t.Fatalf("item 3 must be either upgradable or blocked: upgrade=%v blocked=%q",
					c.UpgradeItems, blocked)
			}
			if taken != tc.want {
				t.Fatalf("upgrade taken = %v, want %v (refusal %q)", taken, tc.want, blocked)
			}
			if tc.reason != "" && !strings.Contains(blocked, tc.reason) {
				t.Errorf("refusal = %q, want it to mention %q", blocked, tc.reason)
			}
			if take := c.TakeItems(); tc.want != contains(take, 3) {
				t.Errorf("TakeItems() = %v, want item 3 present = %v", take, tc.want)
			}
		})
	}
}

// A matched held item reads as an upgrade rather than as an ordinary wanted
// episode, so the Releases tab does not claim we are missing it — on the batch
// path as much as the single-episode one.
func TestUpgradeMatchReason(t *testing.T) {
	cases := []struct {
		name    string
		items   func() []Item
		release string
		want    string
	}{
		{"single", func() []Item { return heldItems(held480) },
			"[TopSubs] Placeholder Saga - 03 [1080p]", "upgrades a held item"},
		{"batch all held", func() []Item {
			its := items(2)
			its[0].HeldTitle = "[TopSubs] Placeholder Saga - 01 [480p]"
			its[1].HeldTitle = "[TopSubs] Placeholder Saga - 02 [480p]"
			return its
		}, "[TopSubs] Placeholder Saga - 01-02 [1080p]", "upgrades held items"},
		{"batch mixed", func() []Item {
			its := items(2)
			its[0].HeldTitle = "[TopSubs] Placeholder Saga - 01 [480p]"
			return its
		}, "[TopSubs] Placeholder Saga - 01-02 [1080p]", "upgrades held"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Match(tc.items(), []string{"Placeholder Saga"},
				[]indexer.Release{{Title: tc.release}}, upgradeProfile(2400))
			if r := got[0].Reason; !strings.Contains(r, tc.want) {
				t.Errorf("reason = %q, want it to mention %q", r, tc.want)
			}
		})
	}
}

// Held is only a candidate when the pass says so: an entry point that withholds
// the item (every one but the upgrade pool) matches exactly as it did before.
func TestHeldItemOutsideThePassIsNotAnUpgrade(t *testing.T) {
	its := heldItems(held480)
	its[2].Grabbable = false
	got := Match(its, []string{"Placeholder Saga"},
		[]indexer.Release{{Title: "[TopSubs] Placeholder Saga - 03 [1080p]"}}, upgradeProfile(2400))
	c := got[0]
	if c.Matched || len(c.UpgradeItems) > 0 || len(c.UpgradeBlocked) > 0 {
		t.Errorf("a withheld held item must not match: matched=%v upgrade=%v blocked=%v",
			c.Matched, c.UpgradeItems, c.UpgradeBlocked)
	}
}

// Coverage ranks on what automation may actually take, so a pack whose entire
// coverage is cutoff-blocked cannot outrank a single covering a wanted item.
func TestCoverageRanksOnTheTakeSet(t *testing.T) {
	its := items(3)
	its[0].HeldTitle = "[TopSubs] Placeholder Saga - 01 [1080p]"
	its[1].HeldTitle = "[TopSubs] Placeholder Saga - 02 [1080p]"

	pack := "[TopSubs] Placeholder Saga - 01-02 [1080p]"
	single := "[MidSubs] Placeholder Saga - 03 [720p]"
	got := Match(its, []string{"Placeholder Saga"}, []indexer.Release{
		{Title: pack, Seeders: 100},
		{Title: single, Seeders: 1},
	}, upgradeProfile(2400))

	if got[0].Release.Title != single {
		t.Errorf("ranked %q first; a pack covering only blocked held items has nothing to take",
			got[0].Release.Title)
	}
	if take := candidateFor(t, got, pack).TakeItems(); len(take) != 0 {
		t.Errorf("pack TakeItems() = %v, want nothing takeable", take)
	}
	if take := candidateFor(t, got, single).TakeItems(); len(take) != 1 || take[0] != 3 {
		t.Errorf("single TakeItems() = %v, want [3]", take)
	}
}

// The cutoff is chosen from landmarks in the profile editor, which mirrors these
// constants (frontend/src/lib/score-landmarks.ts). Pin them so a reweighting has
// to move the mirror too.
func TestScoreLandmarksArePinned(t *testing.T) {
	if scoreGroupBase != 2000 {
		t.Errorf("scoreGroupBase = %d, want 2000 (any ranked group)", scoreGroupBase)
	}
	if scoreGroupBase+scoreResBase != 2400 {
		t.Errorf("top group at best resolution = %d, want 2400", scoreGroupBase+scoreResBase)
	}
	if scoreGroupMin != 1000 {
		t.Errorf("scoreGroupMin = %d, want 1000 (the any-ranked-group landmark)", scoreGroupMin)
	}
	if scoreResStep != 100 {
		t.Errorf("scoreResStep = %d, want 100 (anchors the second-best-resolution landmark)", scoreResStep)
	}
}

func contains(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}
