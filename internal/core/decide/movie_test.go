package decide

import (
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// All fixtures use invented series/group names; only the naming structure under
// test is real.

// A numberless "complete" release fills 1..maxItem through batchItems, so before
// #208 it matched a movie's single item, was eligible, and would be grabbed --
// with no year check and no movie naming. #209 lifts the refusal.
func TestMovieRefusesANumberlessPack(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (Complete) [1080p][HEVC]", Seeders: 40},
	}
	got := Match([]Item{{Number: 1, Grabbable: true}}, []string{"Sample Film"}, releases,
		domain.QualityProfile{}, MatchOpts{Format: domain.FormatMovie})

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].Matched {
		t.Errorf("a movie release matched (items %v, reason %q); nothing may grab a movie yet",
			got[0].Items, got[0].Reason)
	}
	if got[0].Reason == "" {
		t.Error("the refusal carries no reason")
	}
}

// The refusal keys on the Format alone, so the same release still covers a
// series. Guards against over-firing.
func TestNumberlessPackStillCoversASeries(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Show (Complete) [1080p][HEVC]", Seeders: 40},
	}
	got := Match(items(12), []string{"Sample Show"}, releases,
		domain.QualityProfile{}, MatchOpts{Format: domain.FormatTV})

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if !got[0].Matched || len(got[0].Items) != 12 {
		t.Errorf("candidate = matched %v over %v, want matched over 1-12",
			got[0].Matched, got[0].Items)
	}
}

// The zero-value Format must read as non-movie, so a caller that passes no
// MatchOpts at all is unaffected.
func TestZeroFormatDoesNotRefuse(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Show - 01 [1080p]", Seeders: 40},
	}
	got := Match(items(12), []string{"Sample Show"}, releases, domain.QualityProfile{})

	if len(got) != 1 || !got[0].Matched {
		t.Fatalf("candidate = %+v, want matched", got)
	}
}

// A release for a different title keeps the honest reason: the movie refusal
// sits after the title gate, not before it.
func TestMovieKeepsTheTitleMismatchReason(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Unrelated Work - 01 [1080p]", Seeders: 40},
	}
	got := Match([]Item{{Number: 1, Grabbable: true}}, []string{"Sample Film"}, releases,
		domain.QualityProfile{}, MatchOpts{Format: domain.FormatMovie})

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].Reason != "title does not match this series" {
		t.Errorf("reason = %q, want the title mismatch", got[0].Reason)
	}
}
