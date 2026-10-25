package acquire_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// The shape of #345: the provider names a title one way and every release names
// it another, so only an alias the user entered connects the two.
const (
	providerName = "Starlight Knight (2026)"
	providerTerm = "Starlight Knight 2026"
	aliasName    = "Hoshi no Kishi 2026"
)

// aliasRelease is shaped like the SubsPlease file names in #345.
func aliasRelease(name string, number int, url string) indexer.Release {
	return indexer.Release{
		Title:       fmt.Sprintf("[Grp] %s - %02d (1080p) [ABCD1234].mkv", name, number),
		DownloadURL: url,
		Seeders:     100,
	}
}

func setAliases(t *testing.T, st *store.Store, titleID int64, aliases ...string) {
	t.Helper()
	if _, err := st.Q.SetTitleAliases(t.Context(), db.SetTitleAliasesParams{
		Aliases: store.EncodeAliases(aliases), ID: titleID,
	}); err != nil {
		t.Fatalf("set aliases: %v", err)
	}
}

func queryTerms(idx *coretest.FakeIndexer) []string {
	out := make([]string, 0, len(idx.Queries))
	for _, q := range idx.Queries {
		out = append(out, q.Term)
	}
	return out
}

// The feed poll reads aliases from the title row, so a metadata cache miss (and
// no provider request) still matches through one.
func TestFeedPoll_AliasMatchesWithEmptyMetadataCache(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	titles := &fakeCachedTitles{}
	rel := aliasRelease(aliasName, 3, "magnet:?xt=urn:btih:alias03")
	h := newFeedPollWithTitles(t, []indexer.FeedEntry{
		{Release: rel, GUID: "guid-alias-03", Published: time.Now().Add(-10 * time.Minute)},
	}, fakeConfig{}, titles)
	id := seedSweep(t, h.st, providerName, true, sweepItem{number: 3, airsAt: &past})
	setTitleProviderID(t, h.st, id, 42)
	setAliases(t, h.st, id, aliasName)

	if err := h.svc.PollFeedOnce(t.Context()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3] through the alias", got)
	}
	if titles.fetchCalls != 0 {
		t.Errorf("poll made %d provider requests, want 0", titles.fetchCalls)
	}
}

// The second title would match if it were handed the first title's alias, so
// this fails if one title's aliases are ever read for another.
func TestFeedPoll_AliasStaysWithItsTitle(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	rel := aliasRelease(aliasName, 3, "magnet:?xt=urn:btih:alias03")
	h := newFeedPollWithTitles(t, []indexer.FeedEntry{
		{Release: rel, GUID: "guid-alias-03", Published: time.Now().Add(-10 * time.Minute)},
	}, fakeConfig{}, fakeTitles{})
	aliased := seedSweep(t, h.st, providerName, true, sweepItem{number: 3, airsAt: &past})
	setAliases(t, h.st, aliased, aliasName)
	other := seedSweep(t, h.st, "Moonlight Knight", true, sweepItem{number: 3, airsAt: &past})

	if err := h.svc.PollFeedOnce(t.Context()); err != nil {
		t.Fatalf("PollFeedOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, aliased); len(got) != 1 {
		t.Fatalf("aliased title grabbed %v, want item 3", got)
	}
	if got := grabbedItemNumbers(t, h.st, other); len(got) != 0 {
		t.Errorf("the other title grabbed %v through an alias it doesn't have", got)
	}
}

// The search sweep asks for the alias and grabs what it finds.
func TestSweep_GrabsAReleaseNamedByAnAlias(t *testing.T) {
	past := time.Now().Add(-2 * time.Hour)
	h := newSweep(t, nil, fakeConfig{})
	h.idx.ByTerm = map[string][]indexer.Release{
		aliasName: {aliasRelease(aliasName, 3, "magnet:?xt=urn:btih:alias03")},
	}
	id := seedSweep(t, h.st, providerName, true, sweepItem{number: 3, airsAt: &past})
	setAliases(t, h.st, id, aliasName)

	if err := h.svc.SweepOnce(t.Context()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if got := grabbedItemNumbers(t, h.st, id); len(got) != 1 || got[0] != 3 {
		t.Fatalf("grabbed items = %v, want [3] through the alias", got)
	}
}

// Decision 1 of #345: the first term and every alias are each searched, and the
// results merged, so adding an alias never loses the provider-named releases.
func TestMatchTitle_SearchesAliasAndFirstTermAndMerges(t *testing.T) {
	idx := &coretest.FakeIndexer{ByTerm: map[string][]indexer.Release{
		providerTerm: {aliasRelease("Starlight Knight 2026", 3, "magnet:?xt=urn:btih:prov03")},
		aliasName:    {aliasRelease(aliasName, 4, "magnet:?xt=urn:btih:alias04")},
	}}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{variants: map[int64][]string{42: {providerName}}})
	id := seedAniListTitle(t, st, providerName, 42, 12)
	setAliases(t, st, id, aliasName)

	m, err := svc.MatchTitle(t.Context(), id)
	if err != nil {
		t.Fatalf("MatchTitle: %v", err)
	}
	if got := queryTerms(idx); !slices.Equal(got, []string{providerTerm, aliasName}) {
		t.Fatalf("queried %q, want the first term then the alias", got)
	}
	if m.Term != providerTerm {
		t.Errorf("term = %q, want the first term that returned a release", m.Term)
	}
	matched := map[int]bool{}
	for _, c := range m.Candidates {
		if c.Matched {
			matched[c.Items[0]] = true
		}
	}
	if !matched[3] || !matched[4] || len(m.Candidates) != 2 {
		t.Errorf("candidates = %+v, want both releases matched (items 3 and 4)", m.Candidates)
	}
}

// Jackett and Prowlarr list one AnimeBytes torrent once per name with one URL,
// so the merge drops only exact duplicates and keeps the copy whose name matches.
func TestMatchTitle_KeepsOneURLUnderTwoNames(t *testing.T) {
	const url = "magnet:?xt=urn:btih:same03"
	provCopy := aliasRelease("Starlight Knight 2026", 3, url)
	aliasCopy := aliasRelease(aliasName, 3, url)
	idx := &coretest.FakeIndexer{ByTerm: map[string][]indexer.Release{
		providerTerm: {provCopy},
		aliasName:    {aliasCopy, provCopy},
	}}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{})
	id := seedTitle(t, st, providerName, 12)
	setAliases(t, st, id, aliasName)

	m, err := svc.MatchTitle(t.Context(), id)
	if err != nil {
		t.Fatalf("MatchTitle: %v", err)
	}
	var names []string
	for _, c := range m.Candidates {
		names = append(names, c.Release.Title)
	}
	want := []string{provCopy.Title, aliasCopy.Title}
	slices.Sort(want)
	slices.Sort(names)
	if got := names; !slices.Equal(got, want) {
		t.Errorf("candidates = %q, want %q: one per name, the exact duplicate dropped", got, want)
	}
}

// The provider names keep their zero-result fallback among themselves; the alias
// is asked for after them either way.
func TestMatchTitle_FallbackAmongProviderNamesUnchanged(t *testing.T) {
	const english = "Starlight Knight Saga"
	idx := &coretest.FakeIndexer{ByTerm: map[string][]indexer.Release{
		english: {aliasRelease(english, 3, "magnet:?xt=urn:btih:eng03")},
	}}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{variants: map[int64][]string{42: {providerName, english}}})
	id := seedAniListTitle(t, st, providerName, 42, 12)
	setAliases(t, st, id, aliasName)

	m, err := svc.MatchTitle(t.Context(), id)
	if err != nil {
		t.Fatalf("MatchTitle: %v", err)
	}
	if got := queryTerms(idx); !slices.Equal(got, []string{providerTerm, english, aliasName}) {
		t.Fatalf("queried %q, want the first term, its fallback, then the alias", got)
	}
	if m.Term != english {
		t.Errorf("term = %q, want the fallback that returned a release", m.Term)
	}
}

// Without an alias a title costs one search, as it did before aliases existed: a
// second provider name stays a zero-result fallback, never a merged search.
func TestMatchTitle_NoAliasIssuesOneQuery(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		aliasRelease("Starlight Knight 2026", 3, "magnet:?xt=urn:btih:prov03"),
	}}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{variants: map[int64][]string{42: {providerName, "Starlight Knight Saga"}}})
	id := seedAniListTitle(t, st, providerName, 42, 12)

	if _, err := svc.MatchTitle(t.Context(), id); err != nil {
		t.Fatalf("MatchTitle: %v", err)
	}
	if got := queryTerms(idx); !slices.Equal(got, []string{providerTerm}) {
		t.Errorf("queried %q, want one search for the first term", got)
	}
}

// An alias that sanitizes to a term already asked for costs no second search.
func TestMatchTitle_AliasEqualToFirstTermIsNotSearchedTwice(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		aliasRelease("Starlight Knight 2026", 3, "magnet:?xt=urn:btih:prov03"),
	}}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{})
	id := seedTitle(t, st, providerName, 12)
	setAliases(t, st, id, "starlight knight 2026")

	if _, err := svc.MatchTitle(t.Context(), id); err != nil {
		t.Fatalf("MatchTitle: %v", err)
	}
	if got := queryTerms(idx); len(got) != 1 {
		t.Errorf("queried %q, want one search", got)
	}
}

// Decision 2 of #345: an alias is a full accepted name, so its season marker sets
// the season the title matches, in both directions.
func TestMatchTitle_AliasSeasonMarkerMatchesThatSeason(t *testing.T) {
	s2 := indexer.Release{Title: "[Grp] Starlight Knight S2 - 03 [1080p]", DownloadURL: "magnet:?xt=urn:btih:s203"}
	s1 := indexer.Release{Title: "[Grp] Starlight Knight S1 - 04 [1080p]", DownloadURL: "magnet:?xt=urn:btih:s104"}
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{s2, s1}}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{})
	id := seedTitle(t, st, "Starlight Knight: Second Arc", 12)
	setAliases(t, st, id, "Starlight Knight 2nd Season")

	m, err := svc.MatchTitle(t.Context(), id)
	if err != nil {
		t.Fatalf("MatchTitle: %v", err)
	}
	matched := map[string]bool{}
	for _, c := range m.Candidates {
		matched[c.Release.Title] = c.Matched
	}
	if !matched[s2.Title] {
		t.Errorf("season 2 release unmatched, want the alias's marker to accept it: %+v", m.Candidates)
	}
	if matched[s1.Title] {
		t.Errorf("season 1 release matched, want the alias's marker to refuse it: %+v", m.Candidates)
	}
}
