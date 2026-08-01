package decide

import (
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

func TestNormalizeReleaseTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"[ExampleSubs] Placeholder Saga - 03 [1080p].mkv", "[examplesubs] placeholder saga - 03 [1080p].mkv"},
		{"  [ExampleSubs]   Placeholder Saga - 03  ", "[examplesubs] placeholder saga - 03"},
		{"[ExampleSubs]\tPlaceholder Saga\n- 03", "[examplesubs] placeholder saga - 03"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeReleaseTitle(c.in); got != c.want {
			t.Errorf("NormalizeReleaseTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The blocklist is the actionable reason, so it is reported ahead of a profile
// one when a release trips both.
func TestBlocklistedTitleIsIneligible(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 03 [1080p]", Seeders: 500},
		{Title: "[OtherSubs] Placeholder Saga - 03 [1080p]", Seeders: 10},
	}
	blocked := BlockedSet{Titles: map[string]string{
		NormalizeReleaseTitle("[ExampleSubs] Placeholder Saga - 03 [1080p]"): "download failed in the client",
	}}
	got := Match(items(12), []string{"Placeholder Saga"}, rels, domain.QualityProfile{}, MatchOpts{Blocked: blocked})

	// Ineligible sorts below eligible, so the un-blocklisted release leads.
	if got[0].Release.Title != "[OtherSubs] Placeholder Saga - 03 [1080p]" {
		t.Errorf("top candidate = %q, want the un-blocklisted release", got[0].Release.Title)
	}
	var blockedCand Candidate
	for _, c := range got {
		if strings.Contains(c.Release.Title, "ExampleSubs") {
			blockedCand = c
		}
	}
	if blockedCand.Eligible {
		t.Fatal("blocklisted release is still eligible")
	}
	if !strings.Contains(blockedCand.IneligibleReason, "download failed in the client") {
		t.Errorf("ineligible reason = %q, want the blocklist reason", blockedCand.IneligibleReason)
	}
	// Blocking is eligibility, not matching: the release still maps to its item.
	if !blockedCand.Matched {
		t.Error("blocklisted release should still be matched, only ineligible")
	}
}

// Torznab often omits the infohash, so a hash match must work on its own for the
// feeds that do publish one.
func TestBlocklistedHashIsIneligible(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 03 [1080p]", InfoHash: "ABCD1234", Seeders: 500},
	}
	blocked := BlockedSet{Hashes: map[string]string{"abcd1234": "download gone from the client"}}
	got := Match(items(12), []string{"Placeholder Saga"}, rels, domain.QualityProfile{}, MatchOpts{Blocked: blocked})
	if got[0].Eligible {
		t.Fatal("release with a blocklisted hash is still eligible")
	}
	if !strings.Contains(got[0].IneligibleReason, "download gone from the client") {
		t.Errorf("ineligible reason = %q, want the blocklist reason", got[0].IneligibleReason)
	}
}

// An empty hash must never match the blocklist, or every hashless release from a
// feed would be blocked by one hashless entry.
func TestEmptyHashDoesNotMatchBlocklist(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 03 [1080p]", Seeders: 500},
	}
	blocked := BlockedSet{Hashes: map[string]string{"": "should never apply"}}
	got := Match(items(12), []string{"Placeholder Saga"}, rels, domain.QualityProfile{}, MatchOpts{Blocked: blocked})
	if !got[0].Eligible {
		t.Errorf("hashless release blocked by an empty-hash entry: %q", got[0].IneligibleReason)
	}
}

// The blocklist reason wins over a profile one: it is the actionable difference.
func TestBlocklistReasonPreferredOverProfileReason(t *testing.T) {
	prof := domain.QualityProfile{BlockedGroups: []string{"ExampleSubs"}}
	rels := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 03 [1080p]", Seeders: 500},
	}
	blocked := BlockedSet{Titles: map[string]string{
		NormalizeReleaseTitle("[ExampleSubs] Placeholder Saga - 03 [1080p]"): "download failed in the client",
	}}
	got := Match(items(12), []string{"Placeholder Saga"}, rels, prof, MatchOpts{Blocked: blocked})
	if !strings.Contains(got[0].IneligibleReason, "download failed in the client") {
		t.Errorf("ineligible reason = %q, want the blocklist reason ahead of the profile's", got[0].IneligibleReason)
	}
}

func TestNoBlocklistLeavesEligibilityAlone(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 03 [1080p]", InfoHash: "abcd1234", Seeders: 500},
	}
	got := Match(items(12), []string{"Placeholder Saga"}, rels, domain.QualityProfile{}, MatchOpts{})
	if !got[0].Eligible {
		t.Errorf("release ineligible with no blocklist: %q", got[0].IneligibleReason)
	}
}
