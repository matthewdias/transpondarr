package decide

import (
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// All fixtures use invented film/series/group names; only the naming structure
// under test is real.

// movieItem is a movie's whole item list: one item, per #208's add.
func movieItem() []Item { return []Item{{Number: 1, Grabbable: true}} }

// movieOpts is the per-title matcher input a movie carries.
func movieOpts(year int) MatchOpts {
	return MatchOpts{Format: domain.FormatMovie, Year: year}
}

func TestMovieMatchesOnTitleAndYear(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (2019) [1080p][HEVC]", Seeders: 40},
	}
	got := Match(movieItem(), []string{"Sample Film"}, releases,
		domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if !got[0].Matched {
		t.Fatalf("candidate = unmatched (%q), want matched", got[0].Reason)
	}
	if len(got[0].Items) != 1 || got[0].Items[0] != 1 {
		t.Errorf("items = %v, want the film's single item", got[0].Items)
	}
	if got[0].Reason != "movie matches a wanted item" {
		t.Errorf("reason = %q, want the movie match reason", got[0].Reason)
	}
	if !got[0].Eligible {
		t.Errorf("eligible = false (%q), want eligible", got[0].IneligibleReason)
	}
}

// A wrong year is a matching refusal with a human reason, the same honesty rule
// the season mismatch follows.
func TestMovieRejectsAWrongYear(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (2021) [1080p][HEVC]", Seeders: 40},
	}
	got := Match(movieItem(), []string{"Sample Film"}, releases,
		domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].Matched {
		t.Errorf("candidate matched over %v, want the wrong year refused", got[0].Items)
	}
	if got[0].Reason != "year 2021 does not match this entry (year 2019)" {
		t.Errorf("reason = %q, want the year mismatch", got[0].Reason)
	}
}

// A release naming no year is not refused: the gate fires only on disagreement,
// exactly as the season gate lets a season-less release through.
func TestMovieWithoutAReleaseYearStillMatches(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film [BD 1080p][Dual Audio]", Seeders: 40},
	}
	got := Match(movieItem(), []string{"Sample Film"}, releases,
		domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 1 || !got[0].Matched {
		t.Fatalf("candidate = %+v, want matched", got)
	}
}

// The scene form glues the year into the parsed title, so the parser reports
// none; decide recovers it from the title's trailing token and still refuses a
// wrong year. Without this the gate would be inert on the form films ship in.
func TestMovieRecoversASceneFormYear(t *testing.T) {
	releases := []indexer.Release{
		{Title: "Sample.Film.2019.2160p.WEB-DL.H.264-EXGRP", Seeders: 40},
		{Title: "Sample.Film.2021.2160p.WEB-DL.H.264-EXGRP", Seeders: 40},
	}
	got := Match(movieItem(), []string{"Sample Film"}, releases,
		domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2", len(got))
	}
	if !got[0].Matched || got[0].Release.Title != "Sample.Film.2019.2160p.WEB-DL.H.264-EXGRP" {
		t.Errorf("first = %q matched %v, want the 2019 release matched",
			got[0].Release.Title, got[0].Matched)
	}
	if got[1].Matched {
		t.Errorf("second matched, want the 2021 release refused")
	}
	if got[1].Reason != "year 2021 does not match this entry (year 2019)" {
		t.Errorf("reason = %q, want the year mismatch", got[1].Reason)
	}
}

// A title that carries its own trailing year keeps it: the number is in an
// accepted variant, so it names the film rather than the release. Reading it as
// a release year would refuse every release the film has.
func TestMovieDoesNotReadATitlesOwnYearAsAReleaseYear(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Legend 1979 [BD 1080p]", Seeders: 40},
	}
	got := Match(movieItem(), []string{"Placeholder Legend 1979"}, releases,
		domain.QualityProfile{}, movieOpts(2014))

	if len(got) != 1 || !got[0].Matched {
		t.Fatalf("candidate = matched %v reason %q, want matched", got[0].Matched, got[0].Reason)
	}
}

// #208 parked movie matching behind a hard stop; #209 lifts it. A numberless
// pack now matches the film -- and with no year on record the match is
// ineligible, so automation cannot take it while a manual grab stays free.
func TestMovieWithNoYearOnRecordMatchesButIsIneligible(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (Complete) [1080p][HEVC]", Seeders: 40},
	}
	got := Match(movieItem(), []string{"Sample Film"}, releases,
		domain.QualityProfile{}, movieOpts(0))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if !got[0].Matched || len(got[0].Items) != 1 {
		t.Fatalf("candidate = matched %v over %v, want matched over the single item",
			got[0].Matched, got[0].Items)
	}
	if got[0].Eligible {
		t.Error("eligible = true, want a null-year movie withheld from automation")
	}
	if got[0].IneligibleReason != "the movie has no year on record" {
		t.Errorf("ineligibleReason = %q, want the null-year reason", got[0].IneligibleReason)
	}
}

// The null-year reason is a title-level fact, so a release-specific refusal --
// more actionable, and different on every row -- is reported ahead of it.
func TestNullYearReasonYieldsToAReleaseSpecificOne(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[BadSubs] Sample Film (Complete) [1080p]", Seeders: 40},
	}
	profile := domain.QualityProfile{BlockedGroups: []string{"BadSubs"}}
	got := Match(movieItem(), []string{"Sample Film"}, releases, profile, movieOpts(0))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].IneligibleReason != "group BadSubs is blocked by the profile" {
		t.Errorf("ineligibleReason = %q, want the blocked-group reason first", got[0].IneligibleReason)
	}
}

// A held film is an upgrade candidate, exactly as a held episode is.
func TestMovieUpgradesAHeldFile(t *testing.T) {
	its := []Item{{Number: 1, Grabbable: true, HeldTitle: "[OldSubs] Sample Film (2019) [720p]"}}
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (2019) [1080p]", Seeders: 40},
	}
	got := Match(its, []string{"Sample Film"}, releases, domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 1 || !got[0].Matched {
		t.Fatalf("candidate = %+v, want matched", got)
	}
	if got[0].Reason != "movie upgrades a held item" {
		t.Errorf("reason = %q, want the upgrade reason", got[0].Reason)
	}
}

// A film already had and not offered as an upgrade is not re-matched, so the
// sweep cannot re-grab it.
func TestMovieAlreadyHadIsNotMatched(t *testing.T) {
	its := []Item{{Number: 1, Grabbable: false}}
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (2019) [1080p]", Seeders: 40},
	}
	got := Match(its, []string{"Sample Film"}, releases, domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].Matched {
		t.Errorf("candidate matched over %v, want a had film left alone", got[0].Items)
	}
	if got[0].Reason != "movie already in the library / not wanted" {
		t.Errorf("reason = %q, want the not-wanted reason", got[0].Reason)
	}
}

// The mode keys on the Format, never on item count: a one-item OVA is
// series-shaped, so its releases are matched by number and never year-gated.
func TestSingleItemOVAStillMatchesEpisodically(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Work - 01 [1080p]", Seeders: 40},
	}
	got := Match([]Item{{Number: 1, Grabbable: true}}, []string{"Sample Work"}, releases,
		domain.QualityProfile{}, MatchOpts{Format: domain.FormatOVA, Year: 2019})

	if len(got) != 1 || !got[0].Matched {
		t.Fatalf("candidate = %+v, want matched", got)
	}
	if got[0].Reason != "episode matches a wanted item" {
		t.Errorf("reason = %q, want the episode reason", got[0].Reason)
	}
}

// A null-year OVA is eligible: the null-year gate is movie-only, so it cannot
// leak onto the formats that never had a year to match on.
func TestSingleItemOVAWithNoYearIsEligible(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Work - 01 [1080p]", Seeders: 40},
	}
	got := Match([]Item{{Number: 1, Grabbable: true}}, []string{"Sample Work"}, releases,
		domain.QualityProfile{}, MatchOpts{Format: domain.FormatOVA})

	if len(got) != 1 || !got[0].Eligible {
		t.Fatalf("candidate = eligible %v (%q), want eligible", got[0].Eligible, got[0].IneligibleReason)
	}
}

// The movie path keys on the Format alone, so the same release still covers a
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

// A release for a different title keeps the honest reason: the movie path sits
// after the title gate, not before it.
func TestMovieKeepsTheTitleMismatchReason(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Unrelated Work - 01 [1080p]", Seeders: 40},
	}
	got := Match(movieItem(), []string{"Sample Film"}, releases,
		domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].Reason != "title does not match this series" {
		t.Errorf("reason = %q, want the title mismatch", got[0].Reason)
	}
}
