package decide

import (
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
)

// All fixtures use invented film/title/group names; only the naming structure
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

// The title gate is fuzzy containment, so a long-runner sharing a name prefix
// with a film reaches the movie path. A film has no episode 250, and skipping
// the episode-mapping apparatus must not mean skipping that: unrefused, this is
// a release the feed poll grabs into the film's single item.
func TestMovieRefusesAnEpisodeItCannotHave(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 250 [1080p]", Seeders: 900},
	}
	got := Match(movieItem(), []string{"Placeholder Saga: The Final"}, releases,
		domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].Matched {
		t.Errorf("candidate matched over %v, want a long-runner's episode refused", got[0].Items)
	}
	if got[0].Reason != "release names episode 250, which this film does not have" {
		t.Errorf("reason = %q, want the episode refusal", got[0].Reason)
	}
}

// A film cannot span episodes, so a pack naming a range is refused whatever the
// range is.
func TestMovieRefusesAnEpisodeRange(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[Batchers] Placeholder Saga (01-24) [1080p][Batch]", Seeders: 900},
	}
	got := Match(movieItem(), []string{"Placeholder Saga: The Final"}, releases,
		domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 1 || got[0].Matched {
		t.Fatalf("candidate = matched %v over %v, want the range refused", got[0].Matched, got[0].Items)
	}
	if got[0].Reason != "release spans episodes 1-24, which this film does not have" {
		t.Errorf("reason = %q, want the range refusal", got[0].Reason)
	}
}

// A numbered sequel film reads as an episode, so the refusal above must not eat
// it: the number is the film's own name, which the variants are what can say.
func TestMovieKeepsASequelNumberInItsName(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film 2 (2021) [1080p]", Seeders: 900},
	}
	got := Match(movieItem(), []string{"Sample Film 2"}, releases,
		domain.QualityProfile{}, movieOpts(2021))

	if len(got) != 1 || !got[0].Matched {
		t.Fatalf("candidate = matched %v reason %q, want the sequel matched",
			got[0].Matched, got[0].Reason)
	}
}

// The same, zero-padded: a film named with a padded number must not be refused
// over a formatting difference in the number anitogo handed back.
func TestMovieKeepsAZeroPaddedNumberInItsName(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Legend 0080 [BD 1080p]", Seeders: 900},
	}
	got := Match(movieItem(), []string{"Placeholder Legend 0080"}, releases,
		domain.QualityProfile{}, movieOpts(1989))

	if len(got) != 1 || !got[0].Matched {
		t.Fatalf("candidate = matched %v reason %q, want the padded name matched",
			got[0].Matched, got[0].Reason)
	}
}

// anitogo leaves unrecognized scene tags on the title, so the year is not always
// the last token. Reading only the tail let a wrong year through on exactly the
// form the recovery exists for.
func TestMovieReadsAYearBehindSceneTags(t *testing.T) {
	for _, tag := range []string{"REPACK", "LIMITED", "JAPANESE"} {
		t.Run(tag, func(t *testing.T) {
			releases := []indexer.Release{
				{Title: "Sample.Film.2021." + tag + ".1080p.BluRay.x264-GRP", Seeders: 900},
			}
			got := Match(movieItem(), []string{"Sample Film"}, releases,
				domain.QualityProfile{}, movieOpts(2019))

			if len(got) != 1 {
				t.Fatalf("candidates = %d, want 1", len(got))
			}
			if got[0].Matched {
				t.Errorf("candidate matched, want the wrong year refused behind %s", tag)
			}
			if got[0].Reason != "year 2021 does not match this entry (year 2019)" {
				t.Errorf("reason = %q, want the year mismatch", got[0].Reason)
			}
		})
	}
}

// Bracket style is a naming convention, not a fact about the film, so the two
// forms of one release must reach the same verdict. They disagree the moment the
// variant check is applied to only one of the two sources a year can come from —
// and the bracketed form is the one that then refuses, blocking a manual grab.
func TestMovieYearReadingAgreesAcrossReleaseForms(t *testing.T) {
	variants := []string{"Placeholder Legend 1979"}
	forms := map[string]string{
		"scene":     "Placeholder.Legend.1979.1080p.WEB-DL-EXGRP",
		"bracketed": "[ExampleSubs] Placeholder Legend (1979) [1080p]",
	}
	for name, title := range forms {
		t.Run(name, func(t *testing.T) {
			got := Match(movieItem(), variants, []indexer.Release{{Title: title, Seeders: 900}},
				domain.QualityProfile{}, movieOpts(2024))

			if len(got) != 1 {
				t.Fatalf("candidates = %d, want 1", len(got))
			}
			// 1979 is the film's own name, not a release year, in both forms.
			if !got[0].Matched {
				t.Errorf("candidate refused (%q), want matched — the title's own number is not a release year",
					got[0].Reason)
			}
		})
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

// #208 parked movie matching behind a hard stop; #209 lifts it. A film release
// matches -- and with no year on record the match is ineligible, so automation
// cannot take it while a manual grab stays free. The fixture deliberately
// carries no batch token, which earns an ineligible reason of its own.
func TestMovieWithNoYearOnRecordMatchesButIsIneligible(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film [1080p][HEVC]", Seeders: 40},
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
		{Title: "[BadSubs] Sample Film [1080p]", Seeders: 40},
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
// title-shaped, so its releases are matched by number and never year-gated.
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
// title. Guards against over-firing.
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

// The wrong grab both numeric gates are blind to: a numberless season pack of
// the film's parent title names no episode and carries no year, so nothing
// above refuses it, and the importer would then place the title' episode 1 as
// the film. It is an eligibility rule rather than a matching one -- the pack may
// genuinely be a multi-part film, so only automation is held back (PR #57).
func TestMovieWithholdsASeasonPackFromAutomation(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga (Complete Series) [1080p]", Seeders: 900},
	}
	got := Match(movieItem(), []string{"Placeholder Saga: The Final"}, releases,
		domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if !got[0].Matched {
		t.Fatalf("matched = false (%q), want matched so a manual grab stays free",
			got[0].Reason)
	}
	if got[0].Eligible {
		t.Fatal("eligible = true, want a season pack withheld from automation")
	}
	if got[0].IneligibleReason !=
		"the release is a batch or season pack, which may be the series rather than the film" {
		t.Errorf("ineligible reason = %q, want the pack reason", got[0].IneligibleReason)
	}
}

// The rule is per release, not per title: an ordinary film release beside a pack
// is still taken, so one suspicious candidate never mutes the whole search.
func TestMovieStillTakesAPlainReleaseBesideAPack(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (Complete Series) [1080p]", Seeders: 900},
		{Title: "[ExampleSubs] Sample Film (2019) [1080p]", Seeders: 10},
	}
	got := Match(movieItem(), []string{"Sample Film"}, releases,
		domain.QualityProfile{}, movieOpts(2019))

	if len(got) != 2 {
		t.Fatalf("candidates = %d, want 2", len(got))
	}
	// Eligible-first ranking puts the plain release on top despite fewer seeders.
	if !got[0].Eligible || got[0].Release.Title != "[ExampleSubs] Sample Film (2019) [1080p]" {
		t.Errorf("first = %q eligible=%v, want the plain film release ranked first",
			got[0].Release.Title, got[0].Eligible)
	}
}

// The pack reason is per release and the null-year one is per title, so a pack
// against a year-less film reports the pack: it is the half a human can act on.
func TestPackReasonIsReportedAheadOfTheNullYearOne(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film (Complete Series) [1080p]", Seeders: 40},
	}
	got := Match(movieItem(), []string{"Sample Film"}, releases,
		domain.QualityProfile{}, movieOpts(0))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].IneligibleReason !=
		"the release is a batch or season pack, which may be the series rather than the film" {
		t.Errorf("ineligibleReason = %q, want the pack reason first", got[0].IneligibleReason)
	}
}

// A profile rule the user set outranks the pack suspicion, keeping the existing
// chain intact: the pack rule slotted in above the null-year one, not above all.
func TestPackReasonYieldsToAProfileRule(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[BadSubs] Sample Film (Complete Series) [1080p]", Seeders: 40},
	}
	profile := domain.QualityProfile{BlockedGroups: []string{"BadSubs"}}
	got := Match(movieItem(), []string{"Sample Film"}, releases, profile, movieOpts(2019))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].IneligibleReason != "group BadSubs is blocked by the profile" {
		t.Errorf("ineligibleReason = %q, want the blocked-group reason first", got[0].IneligibleReason)
	}
}

// A batch token on a title release keeps meaning exactly what it always has:
// the pack matches the items it covers and is eligible. The new rule is gated on
// Format, and this is what proves it.
func TestSeriesSeasonPackIsUnaffectedByTheMoviePackRule(t *testing.T) {
	items := []Item{{Number: 1, Grabbable: true}, {Number: 2, Grabbable: true}}
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga (Complete Series) [1080p]", Seeders: 40},
	}
	got := Match(items, []string{"Placeholder Saga"}, releases, domain.QualityProfile{})

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if !got[0].Matched || len(got[0].Items) != 2 {
		t.Fatalf("matched = %v over %v, want the pack covering both items",
			got[0].Matched, got[0].Items)
	}
	if !got[0].Eligible {
		t.Errorf("eligible = false (%q), want a series pack unaffected", got[0].IneligibleReason)
	}
	if got[0].Reason != "batch / season pack covers wanted items" {
		t.Errorf("reason = %q, want the unchanged series pack reason", got[0].Reason)
	}
}

// The guard above only bit when the film's title was longer than the release's
// parsed title. Reverse the lengths and fuzzy containment made it inert: the
// rebuilt name contains the variant by construction, so a long-runner's episode
// was matched and grabbed as the film. Both directions must refuse.
func TestMovieRefusesAnEpisodeWhateverTheNameLengths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		variant string
		release string
	}{
		{"film title equals the release's", "Placeholder Saga",
			"[ExampleSubs] Placeholder Saga - 250 [1080p]"},
		{"film title is contained in the release's", "Sample Film",
			"[ExampleSubs] Sample Film Chronicles - 250 [1080p]"},
		{"film title is longer than the release's", "Placeholder Saga: The Final",
			"[ExampleSubs] Placeholder Saga - 250 [1080p]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Match(movieItem(), []string{tc.variant},
				[]indexer.Release{{Title: tc.release, Seeders: 900}},
				domain.QualityProfile{}, movieOpts(2019))

			if len(got) != 1 {
				t.Fatalf("candidates = %d, want 1", len(got))
			}
			if got[0].Matched {
				t.Fatalf("candidate matched over %v, want the episode refused", got[0].Items)
			}
			if got[0].Reason != "release names episode 250, which this film does not have" {
				t.Errorf("reason = %q, want the episode refusal", got[0].Reason)
			}
		})
	}
}

// The accepted cost of comparing exactly: a film whose variant renders its
// number differently from the release goes unmatched -- and being a matching
// refusal rather than an eligibility one, that 422s the manual grab too. Named
// deliberately, so the strictness is not later read as an oversight and loosened
// back into the false positives it exists to prevent.
func TestMovieRefusesANumberedFilmWhoseVariantRendersDifferently(t *testing.T) {
	releases := []indexer.Release{
		{Title: "[ExampleSubs] Sample Film 2 (2021) [1080p]", Seeders: 900},
	}
	got := Match(movieItem(), []string{"Sample Film 2nd Movie"}, releases,
		domain.QualityProfile{}, movieOpts(2021))

	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].Matched {
		t.Fatalf("candidate matched over %v, want the differing rendering refused", got[0].Items)
	}
	if got[0].Reason != "release names episode 2, which this film does not have" {
		t.Errorf("reason = %q, want the episode refusal", got[0].Reason)
	}
}
