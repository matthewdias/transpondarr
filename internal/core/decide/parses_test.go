package decide

import (
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/parser"
)

// All fixtures use invented title/group names; only the naming structure under
// test is real.

// A supplied parse is used instead of parsing the release name again. The
// supplied one deliberately disagrees with what Parse would return, because
// agreement could not tell the two apart.
func TestMatchUsesSuppliedReleaseParses(t *testing.T) {
	const title = "[ExampleSubs] Placeholder Saga - 03 [1080p]"
	supplied := parser.Parse(title)
	supplied.Group = "SuppliedSubs"

	got := Match(items(12), []string{"Placeholder Saga"},
		[]indexer.Release{{Title: title, Seeders: 10}}, domain.QualityProfile{},
		MatchOpts{Parses: ReleaseParses{title: supplied}})

	c := candidateFor(t, got, title)
	if c.Parsed.Group != "SuppliedSubs" {
		t.Errorf("parsed group = %q, want the supplied parse's %q", c.Parsed.Group, "SuppliedSubs")
	}
	if c.Release.ReleaseGroup != "SuppliedSubs" {
		t.Errorf("release group = %q, want the supplied parse to enrich the release",
			c.Release.ReleaseGroup)
	}
	if !c.Matched || !contains(c.Items, 3) {
		t.Errorf("supplied parse must still map normally: matched=%v items=%v", c.Matched, c.Items)
	}
}

// A release the lookup does not carry is parsed as before, so a partial or nil
// map is a cache and never a filter.
func TestMatchParsesAReleaseTheLookupMisses(t *testing.T) {
	const known = "[ExampleSubs] Placeholder Saga - 03 [1080p]"
	const missing = "[OtherSubs] Placeholder Saga - 04 [1080p]"
	supplied := parser.Parse(known)
	supplied.Group = "SuppliedSubs"

	got := Match(items(12), []string{"Placeholder Saga"},
		[]indexer.Release{{Title: known, Seeders: 10}, {Title: missing, Seeders: 10}},
		domain.QualityProfile{}, MatchOpts{Parses: ReleaseParses{known: supplied}})

	c := candidateFor(t, got, missing)
	if c.Parsed.Group != "OtherSubs" {
		t.Errorf("parsed group = %q, want the freshly parsed %q", c.Parsed.Group, "OtherSubs")
	}
	if !c.Matched || !contains(c.Items, 4) {
		t.Errorf("a missed release must still match: matched=%v items=%v", c.Matched, c.Items)
	}
}
