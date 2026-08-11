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

	UpgradeItems   []int          // covered items we hold that this release may replace
	UpgradeBlocked map[int]string // covered held items the upgrade policy refused, by reason
}

// TakeItems is what automation may act on: Items minus the held items the
// upgrade policy refused. Manual paths keep reading Items, mirroring how they
// read a candidate that is matched but ineligible (PR #57).
func (c Candidate) TakeItems() []int {
	if len(c.UpgradeBlocked) == 0 {
		return c.Items
	}
	out := make([]int, 0, len(c.Items))
	for _, n := range c.Items {
		if _, blocked := c.UpgradeBlocked[n]; !blocked {
			out = append(out, n)
		}
	}
	return out
}

// takeCount is TakeItems' length without its allocation, for the comparator.
func (c Candidate) takeCount() int {
	if len(c.UpgradeBlocked) == 0 {
		return len(c.Items)
	}
	n := 0
	for _, it := range c.Items {
		if _, blocked := c.UpgradeBlocked[it]; !blocked {
			n++
		}
	}
	return n
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

// Item is a wanted item as the matcher sees it. Grabbable is candidacy, not
// library state — the sweep also withholds in-flight and unaired items — so a
// caller derives it per pass, and #97 can set it for an item we already hold.
// HeldTitle names what the library holds, making a grabbable item an upgrade.
type Item struct {
	Number    int
	Grabbable bool
	HeldTitle string
}

// heldRelease is what a held item holds, parsed and scored once per pass so the
// incumbent and every candidate are rated under one profile snapshot.
type heldRelease struct {
	parsed parser.Parsed
	score  int
}

// Match evaluates releases against a title. titleVariants are the accepted names
// for the title (e.g. romaji/english/native) used to filter out releases for
// other series. Results are ranked matched-first, then eligible-first, then
// pinned-first, then by how many still-wanted items they cover, then by profile
// score; seeders are only the tie-break between equal scores. A pin is an
// absolute tier, never a score: it wins only among eligible releases, so it can
// never bypass a block, exclude, or the floor.
func Match(items []Item, titleVariants []string, releases []indexer.Release, profile domain.QualityProfile, opts ...MatchOpts) []Candidate {
	// maxItem spans every item, grabbable or not, so however a caller scoped the
	// pass, absolute-numbering detection below is unaffected.
	itemSet := make(map[int]bool, len(items))
	held := make(map[int]heldRelease)
	maxItem := 0
	for _, it := range items {
		if it.Number > maxItem {
			maxItem = it.Number
		}
		if !it.Grabbable {
			continue
		}
		itemSet[it.Number] = true
		if it.HeldTitle == "" {
			continue
		}
		p := parser.Parse(it.HeldTitle)
		score, _ := Score(p, indexer.Release{}, profile)
		held[it.Number] = heldRelease{parsed: p, score: score}
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
		c := evaluate(rel, variants, expectedSeason, itemSet, maxItem, held)
		c.Score, c.ScoreParts = Score(c.Parsed, c.Release, profile)
		c.IneligibleReason = ineligibleReason(c.Release, c.Parsed, profile, blocked, c.Score)
		c.Eligible = c.IneligibleReason == ""
		c.Pinned = pin != "" && strings.EqualFold(c.Parsed.Group, pin)
		applyUpgradePolicy(&c, held, profile)
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
		// Wider coverage first is "one grab instead of N" (#126), counted on what
		// automation may take so a pack whose held coverage is all cutoff-blocked
		// cannot outrank a single covering a genuinely wanted item. Below the pin
		// deliberately: a pin is per-series knowledge, so coverage only decides
		// among equally pinned candidates. Weekly singles tie at 1 and fall
		// through to score.
		if out[a].takeCount() != out[b].takeCount() {
			return out[a].takeCount() > out[b].takeCount()
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

// UnmetGoals is Score's complement: the axes a release scores below the
// profile's best on, each carrying the points still available. It lives here so
// the gap and the earnings can never disagree about what an axis is worth
// (#150's Cutoff Unmet reads it; the fix bonus is a repair, not a goal).
func UnmetGoals(p parser.Parsed, profile domain.QualityProfile) []ScorePart {
	var goals []ScorePart
	gap := func(label string, best, earned int) {
		if best > earned {
			goals = append(goals, ScorePart{Label: label, Points: best - earned})
		}
	}
	if len(profile.Groups) > 0 {
		earned := 0
		if i := indexFold(profile.Groups, p.Group); i >= 0 {
			earned = stepped(scoreGroupBase, scoreGroupStep, scoreGroupMin, i)
		}
		gap("group "+profile.Groups[0], scoreGroupBase, earned)
	}
	if len(profile.ResolutionOrder) > 0 {
		earned := 0
		if i := indexFold(profile.ResolutionOrder, p.Resolution); i >= 0 {
			earned = stepped(scoreResBase, scoreResStep, scoreResMin, i)
		}
		gap("resolution "+profile.ResolutionOrder[0], scoreResBase, earned)
	}
	if profile.PreferredSource != "" && !strings.EqualFold(p.Source, profile.PreferredSource) {
		gap("source "+profile.PreferredSource, scoreSource, 0)
	}
	if profile.PreferDualAudio && !p.DualAudio {
		gap("dual audio", scoreDualAudio, 0)
	}
	if profile.SubPref != "" && !strings.EqualFold(p.Subs, profile.SubPref) {
		gap("subs "+profile.SubPref, scoreSubs, 0)
	}
	if profile.CodecPref != "" && !strings.EqualFold(p.Codec, profile.CodecPref) {
		gap("codec "+profile.CodecPref, scoreCodec, 0)
	}
	return goals
}

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
	if indexFold(profile.BlockedGroups, p.Group) >= 0 {
		return fmt.Sprintf("group %s is blocked by the profile", p.Group)
	}
	for _, tok := range profile.HardExcludes {
		// Every axis Score rewards except group, which BlockedGroups owns.
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

// applyUpgradePolicy splits a candidate's held coverage into what automation may
// replace and what it must leave alone, first refusal winning. It is cutoff, not
// chase: below the cutoff any strictly higher score is taken, and at or above it
// only a v2/repack of the very release we hold gets through, that being a fix
// for a broken file rather than a better one.
func applyUpgradePolicy(c *Candidate, held map[int]heldRelease, profile domain.QualityProfile) {
	if len(held) == 0 || !c.Matched {
		return
	}
	for _, n := range c.Items {
		h, ok := held[n]
		if !ok {
			continue
		}
		if reason := upgradeRefusal(*c, h, profile); reason != "" {
			if c.UpgradeBlocked == nil {
				c.UpgradeBlocked = make(map[int]string, 1)
			}
			c.UpgradeBlocked[n] = reason
			continue
		}
		c.UpgradeItems = append(c.UpgradeItems, n)
	}
}

// upgradeRefusal reports why this candidate may not replace a held release, or
// "" when it may.
func upgradeRefusal(c Candidate, h heldRelease, profile domain.QualityProfile) string {
	if !profile.UpgradesEnabled {
		return "upgrades are not enabled for this profile"
	}
	if isFixOf(c.Parsed, h.parsed, profile) {
		return ""
	}
	if h.score >= profile.CutoffScore {
		return fmt.Sprintf("the held release already meets the profile cutoff (score %d >= %d)",
			h.score, profile.CutoffScore)
	}
	if c.Score <= h.score {
		return fmt.Sprintf("score %d does not beat the held release (score %d)", c.Score, h.score)
	}
	return ""
}

// isFixOf reports whether a release is the same group's re-release of the very
// file we hold: a v2 or a repack, at the same resolution. Anything else is a
// different release, and above the cutoff we are not chasing those.
func isFixOf(c, h parser.Parsed, profile domain.QualityProfile) bool {
	if !profile.UpgradeV2AboveCutoff {
		return false
	}
	if c.Group == "" || !strings.EqualFold(c.Group, h.Group) || !strings.EqualFold(c.Resolution, h.Resolution) {
		return false
	}
	return c.Version > h.Version || (c.Repack && !h.Repack)
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

func evaluate(rel indexer.Release, variants []string, expectedSeason int, itemSet map[int]bool, maxItem int, held map[int]heldRelease) Candidate {
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

	// A pack matches what it covers, and since #126 the importer places it file
	// by file, so it is a candidate like any other.
	if p.Batch {
		if p.EpisodeEnd > maxItem {
			// Same guess as the single-episode case below: a 01-48 pack against a
			// 12-item entry is absolute numbering, or another season entirely.
			c.Reason = "episode range exceeds this entry's range (possible absolute/season mismatch)"
			return c
		}
		if covered := batchItems(p, itemSet, maxItem); len(covered) > 0 {
			c.Matched, c.Items = true, covered
			heldCount := 0
			for _, n := range covered {
				if _, ok := held[n]; ok {
					heldCount++
				}
			}
			switch heldCount {
			case 0:
				c.Reason = "batch / season pack covers wanted items"
			case len(covered):
				c.Reason = "batch / season pack upgrades held items"
			default:
				c.Reason = "batch / season pack covers wanted and upgrades held items"
			}
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
			if _, ok := held[p.EpisodeStart]; ok {
				c.Reason = "episode upgrades a held item"
			}
			return c
		}
		if p.EpisodeStart > maxItem {
			// Very likely absolute numbering from a multi-season run, or a
			// different season entirely — flag rather than guess.
			c.Reason = "episode number exceeds this entry's range (possible absolute/season mismatch)"
			return c
		}
		c.Reason = "episode already in the library / not wanted"
		return c
	}

	c.Reason = "no episode number found"
	return c
}

// batchItems is what a pack covers: its explicit range, or every item still
// wanted when it names no numbers at all, which is what a season pack holds. A
// range past this entry is rejected by the caller, so what reaches here is
// either bounded by maxItem or numberless.
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
