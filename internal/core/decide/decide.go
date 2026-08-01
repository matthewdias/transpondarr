// Package decide is the "decide" stage of the pipeline: given a tracked Title's
// wanted items and a set of raw indexer releases, it works out which release
// satisfies which item. It parses each release (via the parser package), filters
// to the ones that plausibly belong to this title, and maps episode numbers to
// wanted-item numbers — surfacing a human-readable reason for every decision so
// the matching can be eyeballed before it drives an automatic grab. Matched
// candidates are then ranked by a pure, profile-driven score (group first — see
// the weights), below an absolute tier for a series' pinned group; an explicit
// floor lets the answer be "nothing yet".
//
// v1 is deliberately transparent rather than clever: reconciling absolute vs
// season-relative numbering is genuinely ambiguous without per-episode metadata,
// so releases it cannot confidently place are returned unmatched with the reason,
// not silently mis-mapped.
package decide

import (
	"fmt"
	"sort"
	"strings"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/parser"
)

// Candidate is one release evaluated against a title's wanted items.
type Candidate struct {
	Release indexer.Release // ReleaseGroup/Resolution/DualAudio filled in from the parse
	Parsed  parser.Parsed
	Matched bool  // belongs to this title AND maps to at least one wanted item
	Items   []int // wanted-item numbers this release satisfies
	Reason  string

	Score            int
	ScoreParts       []ScorePart
	Eligible         bool
	IneligibleReason string // non-empty exactly when !Eligible
	Pinned           bool   // release group equals the series' pinned group
}

// MatchOpts carries per-series knobs that are neither profile nor title data.
type MatchOpts struct {
	PinnedGroup string
	Blocked     BlockedSet
}

// BlockedSet is the series' active release blocklist as plain data, so decide
// stays pure. A release matches on either axis: Torznab often omits the infohash.
type BlockedSet struct {
	Hashes map[string]string // lowercased info hash -> reason
	Titles map[string]string // normalized release title -> reason
}

// reason reports why this release is blocked, if it is.
func (b BlockedSet) reason(rel indexer.Release) string {
	if len(b.Hashes) == 0 && len(b.Titles) == 0 {
		return "" // the common path: no per-release normalizing for an unblocked series
	}
	if h := strings.ToLower(strings.TrimSpace(rel.InfoHash)); h != "" {
		if r, ok := b.Hashes[h]; ok {
			return r
		}
	}
	if t := NormalizeReleaseTitle(rel.Title); t != "" {
		if r, ok := b.Titles[t]; ok {
			return r
		}
	}
	return ""
}

// NormalizeReleaseTitle is a blocklist entry's identity. Gentler than normalize,
// which would fold two different releases of one episode into one entry.
func NormalizeReleaseTitle(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// ScorePart is one axis' contribution to a candidate's score, for display.
type ScorePart struct {
	Label  string // e.g. "group ExampleSubs (rank 1)"
	Points int
}

// Match evaluates releases against a title. titleVariants are the accepted names
// for the title (e.g. romaji/english/native) used to filter out releases for
// other series. Results are ranked matched-first, then eligible-first, then
// pinned-first, then by profile score; seeders are only the tie-break between
// equal scores. A pin is an absolute tier, never a score: it wins only among
// eligible releases, so it can never bypass a block, exclude, or the floor.
//
// An item's Have means only "not a candidate" here, not library state: the sweep
// also sets it for in-flight and unaired items (#97 must not read it as "held").
func Match(items []domain.WantedItem, titleVariants []string, releases []indexer.Release, profile domain.QualityProfile, opts ...MatchOpts) []Candidate {
	// itemSet holds the numbers still worth grabbing. Already-had items are excluded
	// so a fully-downloaded episode is not re-matched and re-grabbed; maxItem still
	// spans every item (had or not) so absolute-numbering detection below is unaffected.
	itemSet := make(map[int]bool, len(items))
	maxItem := 0
	for _, it := range items {
		if it.Number > maxItem {
			maxItem = it.Number
		}
		if it.Have {
			continue
		}
		itemSet[it.Number] = true
	}
	pin := ""
	var blocked BlockedSet
	if len(opts) > 0 {
		pin = opts[0].PinnedGroup
		blocked = opts[0].Blocked
	}
	variants := normalizeVariants(titleVariants)
	// Which season this entry represents, derived from its own title (AniList
	// models each season as a separate entry, e.g. "... 2nd Season"). Defaults to
	// 1 when the title carries no marker. Releases that name a *different* season
	// are rejected — this is what stops S2E05 from matching item 5 of an S1 entry.
	expectedSeason := expectedSeasonFrom(titleVariants)

	out := make([]Candidate, 0, len(releases))
	for _, rel := range releases {
		c := evaluate(rel, variants, expectedSeason, itemSet, maxItem)
		c.Score, c.ScoreParts = Score(c.Parsed, c.Release, profile)
		c.IneligibleReason = ineligibleReason(c.Release, c.Parsed, profile, blocked, c.Score)
		c.Eligible = c.IneligibleReason == ""
		c.Pinned = pin != "" && strings.EqualFold(c.Parsed.Group, pin)
		out = append(out, c)
	}

	// Scoring ranks within the matched set; it never relaxes matching itself.
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Matched != out[b].Matched {
			return out[a].Matched // matched first
		}
		if out[a].Eligible != out[b].Eligible {
			return out[a].Eligible
		}
		if out[a].Pinned != out[b].Pinned {
			return out[a].Pinned
		}
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		return out[a].Release.Seeders > out[b].Release.Seeders
	})
	return out
}

// Fixed axis weights: group dominates by construction — its floor exceeds the
// 850 max every other axis can sum to, so any listed group beats every unlisted
// one. Within listed groups, bonuses may flip adjacent ranks by design.
const (
	scoreGroupBase = 2000
	scoreGroupStep = 100
	scoreGroupMin  = 1000
	scoreResBase   = 400
	scoreResStep   = 100
	scoreResMin    = 50
	scoreSource    = 150
	scoreDualAudio = 100
	scoreSubs      = 100
	scoreCodec     = 75
	scoreFix       = 25
)

// Score rates a release against a profile. It is deliberately pure — no store
// or network — so the ranking that decides what lands in a library can be
// tested exhaustively.
func Score(p parser.Parsed, rel indexer.Release, profile domain.QualityProfile) (int, []ScorePart) {
	var parts []ScorePart
	add := func(label string, pts int) { parts = append(parts, ScorePart{Label: label, Points: pts}) }

	if i := indexFold(profile.Groups, p.Group); i >= 0 {
		add(fmt.Sprintf("group %s (rank %d)", p.Group, i+1), stepped(scoreGroupBase, scoreGroupStep, scoreGroupMin, i))
	}
	if i := indexFold(profile.ResolutionOrder, p.Resolution); i >= 0 {
		// A folded resolution names the dimensions it was read from, so the tier
		// never reads as something the release itself claimed.
		if p.ResolutionRaw != "" {
			add(fmt.Sprintf("resolution %s (from %s, rank %d)", p.Resolution, p.ResolutionRaw, i+1), stepped(scoreResBase, scoreResStep, scoreResMin, i))
		} else {
			add(fmt.Sprintf("resolution %s (rank %d)", p.Resolution, i+1), stepped(scoreResBase, scoreResStep, scoreResMin, i))
		}
	}
	if profile.PreferredSource != "" && strings.EqualFold(p.Source, profile.PreferredSource) {
		add("source "+p.Source, scoreSource)
	}
	if profile.PreferDualAudio && p.DualAudio {
		add("dual audio", scoreDualAudio)
	}
	// Preferences only reward — a mismatch is unrewarded, never penalised, so
	// blocking stays HardExcludes' job and scores stay non-negative.
	if profile.SubPref != "" && strings.EqualFold(p.Subs, profile.SubPref) {
		add("subs "+p.Subs, scoreSubs)
	}
	if profile.CodecPref != "" && strings.EqualFold(p.Codec, profile.CodecPref) {
		add("codec "+p.Codec, scoreCodec)
	}
	if p.Repack || p.Version > 1 {
		add("repack/v2", scoreFix)
	}

	total := 0
	for _, pt := range parts {
		total += pt.Points
	}
	return total, parts
}

// ineligibleReason is the floor from #16: the way the answer can be "nothing
// yet" instead of the least-bad release available. "" means eligible. Scores
// are never negative, so the zero-value MinScore expresses no floor.
func ineligibleReason(rel indexer.Release, p parser.Parsed, profile domain.QualityProfile, blocked BlockedSet, score int) string {
	// The blocklist first: when a release trips both, "this one already failed"
	// is the more actionable answer than a profile rule.
	if r := blocked.reason(rel); r != "" {
		return r
	}
	// Automation must not volunteer for a state it cannot finish (#125): the
	// importer can only defer a true multi-episode payload, and deferred is
	// settled, so a grabbed pack parks its items instead of failing them back to
	// wanted. Lifting this is what #126's per-file batch import unlocks.
	if p.Batch {
		return "batch / season pack — automatic import cannot split it yet"
	}
	if indexFold(profile.BlockedGroups, p.Group) >= 0 {
		return fmt.Sprintf("group %s is blocked by the profile", p.Group)
	}
	for _, tok := range profile.HardExcludes {
		for _, v := range []string{p.Subs, p.Codec, p.Source, p.Resolution} {
			if v != "" && strings.EqualFold(v, strings.TrimSpace(tok)) {
				what := strings.ToLower(v)
				if v == p.Resolution && p.ResolutionRaw != "" {
					what += ", from " + p.ResolutionRaw
				}
				return fmt.Sprintf("release is %s (excluded by the profile)", what)
			}
		}
	}
	if score < profile.MinScore {
		return fmt.Sprintf("score %d is below the profile minimum %d", score, profile.MinScore)
	}
	return ""
}

func stepped(base, step, min, idx int) int {
	if v := base - idx*step; v > min {
		return v
	}
	return min
}

func indexFold(list []string, v string) int {
	if v == "" {
		return -1
	}
	for i, s := range list {
		if strings.EqualFold(s, v) {
			return i
		}
	}
	return -1
}

func evaluate(rel indexer.Release, variants []string, expectedSeason int, itemSet map[int]bool, maxItem int) Candidate {
	p := parser.Parse(rel.Title)
	// Enrich the release with parsed attributes (the fields the indexer left blank).
	rel.ReleaseGroup = p.Group
	rel.Resolution = p.Resolution
	rel.DualAudio = p.DualAudio

	c := Candidate{Release: rel, Parsed: p}

	if !titleBelongs(p.Title, variants) {
		c.Reason = "title does not match this series"
		return c
	}

	// Season gate: a release that explicitly names a different season is not this
	// entry (releases with no season token pass — that's the common S1/absolute case).
	if p.Season != 0 && p.Season != expectedSeason {
		c.Reason = fmt.Sprintf("season %d does not match this entry (season %d)", p.Season, expectedSeason)
		return c
	}

	// A pack matches what it covers; refusing it is eligibility's job (#125), so a
	// human can see which episodes it holds and grab it deliberately.
	if p.Batch {
		if covered := batchItems(p, itemSet, maxItem); len(covered) > 0 {
			c.Matched, c.Items = true, covered
			c.Reason = "batch / season pack covers wanted items"
			return c
		}
		c.Reason = "batch / season pack covers nothing still wanted"
		return c
	}

	// A single episode.
	if p.EpisodeStart > 0 {
		if itemSet[p.EpisodeStart] {
			c.Matched, c.Items = true, []int{p.EpisodeStart}
			c.Reason = "episode matches a wanted item"
			return c
		}
		if p.EpisodeStart > maxItem {
			// Very likely absolute numbering from a multi-season run, or a
			// different season entirely — flag rather than guess.
			c.Reason = "episode number exceeds this entry's range (possible absolute/season mismatch)"
			return c
		}
		c.Reason = "episode already have / not wanted"
		return c
	}

	c.Reason = "no episode number found"
	return c
}

// batchItems is what a pack covers: its explicit range, or every item still
// wanted when it names no numbers at all, which is what a season pack holds.
// Unlike the single-episode path it has no "exceeds this entry's range" guard, so
// an absolute-numbered 01-48 pack claims a 12-item entry's items 1-12 — right for
// a first season, a guess otherwise. Only a manual grab can reach it while packs
// are ineligible; #126 must add the guard before it lifts that.
func batchItems(p parser.Parsed, itemSet map[int]bool, maxItem int) []int {
	start, end := p.EpisodeStart, p.EpisodeEnd
	if start == 0 {
		start, end = 1, maxItem
	}
	var out []int
	for n := start; n <= end; n++ {
		if itemSet[n] {
			out = append(out, n)
		}
	}
	return out
}

// titleBelongs reports whether a parsed release title matches one of the series'
// accepted names, comparing on a punctuation- and space-stripped form so
// "Frieren: Beyond Journey's End" and "Frieren Beyond Journeys End" compare equal.
func titleBelongs(parsedTitle string, variants []string) bool {
	got := normalize(parsedTitle)
	if got == "" {
		return false
	}
	for _, v := range variants {
		if v == "" {
			continue
		}
		if got == v {
			return true
		}
		// Substring (fuzzy) match only when the shorter, contained title is long
		// enough to be meaningful. Without a floor, short or prefix names — "K",
		// "Air", "Fate" — match unrelated shows ("Fairy Tail", "Fate/Zero") whose
		// normalized title happens to contain them. Exact matches above still cover
		// legitimately short-titled series against their own releases.
		shorter := got
		if len(v) < len(shorter) {
			shorter = v
		}
		if len(shorter) >= minFuzzyTitleLen && (strings.Contains(v, got) || strings.Contains(got, v)) {
			return true
		}
	}
	return false
}

// minFuzzyTitleLen is the shortest normalized title allowed to match by substring
// containment rather than exact equality (see titleBelongs).
const minFuzzyTitleLen = 5

// expectedSeasonFrom derives the season an entry represents from its own title
// variants (e.g. "Show 2nd Season" -> 2). Defaults to 1 when no variant carries
// a season marker — the ordinary case for a first season or a single-cour show.
func expectedSeasonFrom(variants []string) int {
	season := 0
	for _, v := range variants {
		if s := parser.Parse(v).Season; s > season {
			season = s
		}
	}
	if season == 0 {
		return 1
	}
	return season
}

func normalizeVariants(variants []string) []string {
	out := make([]string, 0, len(variants))
	for _, v := range variants {
		if n := normalize(v); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// normalize lowercases and strips everything but ASCII letters and digits, so
// titles that differ only in punctuation/spacing/romanization glyphs compare
// equal. foldTitle first, so what SearchTerm finds this can match (#107).
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(foldTitle(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
