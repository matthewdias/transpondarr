package decide

import (
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// All fixtures use invented series/group names; only the naming structure under
// test is real.

func items(n int) []domain.WantedItem {
	out := make([]domain.WantedItem, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, domain.WantedItem{Number: i, Kind: domain.KindEpisode})
	}
	return out
}

// An already-had episode must not be re-matched (and thus not re-grabbed).
func TestAlreadyHadEpisodeNotMatched(t *testing.T) {
	its := items(12)
	its[2].Have = true // episode 3 already imported
	rels := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga S1E03 [1080p]", Seeders: 100},
	}
	got := Match(its, []string{"Placeholder Saga"}, rels)
	if got[0].Matched {
		t.Errorf("already-had episode 3 should not match: items %v", got[0].Items)
	}
}

func TestMatchSingleEpisode(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga S1E03 [1080p]", Seeders: 100},
	}
	got := Match(items(12), []string{"Placeholder Saga"}, rels)
	c := got[0]
	if !c.Matched {
		t.Fatalf("expected match, reason: %s", c.Reason)
	}
	if len(c.Items) != 1 || c.Items[0] != 3 {
		t.Errorf("items = %v, want [3]", c.Items)
	}
	// Parsed attributes should be reflected back onto the release.
	if c.Release.Resolution != "1080p" {
		t.Errorf("resolution not enriched: %q", c.Release.Resolution)
	}
}

// v0.1.0 recognises batch ranges but refuses them (no per-file batch import yet),
// so they are surfaced unmatched with an explanatory reason rather than recorded
// as a grab that would silently never import.
func TestBatchRangeRefused(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[Batchers] Placeholder Saga S1 (01-06) [1080p][Batch]", Seeders: 50},
	}
	got := Match(items(12), []string{"Placeholder Saga"}, rels)
	c := got[0]
	if c.Matched {
		t.Fatalf("batch should not match in v0.1.0, items %v", c.Items)
	}
	if c.Items != nil {
		t.Errorf("refused batch should carry no items, got %v", c.Items)
	}
	if !strings.Contains(c.Reason, "batch") {
		t.Errorf("expected a batch reason, got %q", c.Reason)
	}
}

// A season/complete pack is likewise refused (but still title-parsed/enriched).
func TestSeasonPackRefused(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[DualCorp] Placeholder Saga (S01) [BD 1080p][Dual-Audio] (Batch)", Seeders: 10},
	}
	got := Match(items(3), []string{"Placeholder Saga"}, rels)
	c := got[0]
	if c.Matched {
		t.Fatalf("season pack should not match in v0.1.0, items %v", c.Items)
	}
	if !strings.Contains(c.Reason, "batch") && !strings.Contains(c.Reason, "pack") {
		t.Errorf("expected a batch/pack reason, got %q", c.Reason)
	}
	// Enrichment still happens before the refusal.
	if !c.Release.DualAudio {
		t.Error("dual-audio not enriched onto release")
	}
}

// A short title variant must not match an unrelated show whose normalized title
// merely contains it as a substring ("Air" inside "Fairy Tail").
func TestShortTitleDoesNotOverMatch(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[Group] Fairy Tail S1E12 [1080p]", Seeders: 100},
	}
	got := Match(items(12), []string{"Air"}, rels)
	if got[0].Matched {
		t.Errorf("short title 'Air' should not match 'Fairy Tail': items %v", got[0].Items)
	}
}

// An episode number beyond this entry's range (absolute numbering from a
// multi-season run) must be surfaced, not silently mismatched.
func TestAbsoluteNumberBeyondRangeIsFlagged(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[FakeGroup] Placeholder Saga - 40 [1080p]", Seeders: 5},
	}
	got := Match(items(12), []string{"Placeholder Saga"}, rels)
	c := got[0]
	if c.Matched {
		t.Error("episode 40 of a 12-item entry should not match")
	}
	if c.Reason == "" || c.Items != nil {
		t.Errorf("expected an explanatory reason and no items, got reason=%q items=%v", c.Reason, c.Items)
	}
}

// A release for a different series must be filtered out by title.
func TestForeignTitleRejected(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[Group] Completely Different Show S1E01 [1080p]", Seeders: 999},
	}
	got := Match(items(12), []string{"Placeholder Saga"}, rels)
	if got[0].Matched {
		t.Error("a different series should not match")
	}
}

// An English-title variant must match a release that uses the English name even
// when the primary series name is the romaji form.
func TestMatchesAlternateTitleVariant(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[Group] Placeholder Legend S1E02 [1080p]", Seeders: 20},
	}
	got := Match(items(12), []string{"Puraseihoruda Densetsu", "Placeholder Legend"}, rels)
	if !got[0].Matched {
		t.Fatalf("expected the English variant to match, reason: %s", got[0].Reason)
	}
}

// The season-collision bug: a season-2 release must NOT match a season-1 entry
// just because the episode numbers line up.
func TestSeasonTwoReleaseRejectedForSeasonOneEntry(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga 2nd Season S2E05 [1080p]", Seeders: 100},
		{Title: "[Group] Placeholder Saga S02E05 [1080p]", Seeders: 90},
	}
	got := Match(items(28), []string{"Placeholder Saga"}, rels) // entry has no season marker -> S1
	for _, c := range got {
		if c.Matched {
			t.Errorf("season-2 release matched an S1 entry: %q -> items %v", c.Release.Title, c.Items)
		}
		if !strings.Contains(c.Reason, "season") {
			t.Errorf("expected a season reason, got %q", c.Reason)
		}
	}
}

// A season-2 entry (its title carries the marker) should match season-2 releases.
func TestSeasonTwoEntryMatchesSeasonTwoRelease(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga 2nd Season S2E05 [1080p]", Seeders: 100},
	}
	got := Match(items(10), []string{"Placeholder Saga 2nd Season", "Placeholder Saga Second Season"}, rels)
	if !got[0].Matched || len(got[0].Items) != 1 || got[0].Items[0] != 5 {
		t.Fatalf("expected S2E05 to match item 5, got matched=%v items=%v reason=%q",
			got[0].Matched, got[0].Items, got[0].Reason)
	}
}

// A release with no explicit season (absolute/S1 style) still matches an S1 entry.
func TestNoSeasonReleaseMatchesSeasonOneEntry(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[FakeGroup] Placeholder Saga - 24 (1080p) [ABCD1234].mkv", Seeders: 40},
	}
	got := Match(items(28), []string{"Placeholder Saga"}, rels)
	if !got[0].Matched || got[0].Items[0] != 24 {
		t.Fatalf("expected absolute ep 24 to match item 24, got matched=%v items=%v reason=%q",
			got[0].Matched, got[0].Items, got[0].Reason)
	}
}

// A season-2 pack must not fill a season-1 entry's items.
func TestSeasonTwoPackRejectedForSeasonOneEntry(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[Batchers] Placeholder Saga S2 (01-10) [1080p][Batch]", Seeders: 30},
	}
	got := Match(items(28), []string{"Placeholder Saga"}, rels)
	if got[0].Matched {
		t.Errorf("season-2 pack matched an S1 entry: items %v", got[0].Items)
	}
}

// Matched releases rank ahead of unmatched, and by seeders within matched.
func TestRankingMatchedThenSeeders(t *testing.T) {
	rels := []indexer.Release{
		{Title: "[Group] Unrelated Thing S1E01", Seeders: 1000},
		{Title: "[Group] Placeholder Saga S1E01 [720p]", Seeders: 10},
		{Title: "[Group] Placeholder Saga S1E02 [1080p]", Seeders: 80},
	}
	got := Match(items(12), []string{"Placeholder Saga"}, rels)
	if !got[0].Matched || !got[1].Matched {
		t.Fatal("matched releases should sort first")
	}
	if got[0].Release.Seeders < got[1].Release.Seeders {
		t.Errorf("matched releases should be seeders-descending: %d then %d",
			got[0].Release.Seeders, got[1].Release.Seeders)
	}
	if got[2].Matched {
		t.Error("the unrelated release should sort last as unmatched")
	}
}
