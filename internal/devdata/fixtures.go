// Package devdata builds a development database dense enough to walk every
// screen offline, plus the local stubs standing in for AniList and a Torznab
// indexer. It is imported only by cmd/devseed, so none of it links into the
// shipped binary.
//
// Release names here are synthetic: structurally faithful to what the parser
// sees, and not scraped from any indexer.
package devdata

import (
	"time"

	"github.com/matthewdias/transpondarr/internal/core/domain"
)

const (
	day  = 24 * time.Hour
	week = 7 * day
)

type profile struct {
	name            string
	resolutionOrder string
	preferredSource string
	subPref         string
	codecPref       string
	preferDualAudio bool
	hardExcludes    string
	minScore        int64
	upgrades        bool
	cutoffScore     int64
	groups          []profileGroup
}

type profileGroup struct {
	name    string
	rank    int64
	blocked bool
}

type title struct {
	providerID      int64
	name            string
	altNames        []string
	format          domain.Format
	year            int
	status          string
	episodes        int // 0 when the provider publishes no count
	cover           string
	monitored       bool
	profile         string // profile name; empty uses the built-in Default
	pinnedGroup     string
	monitorNewFrom  int // 0 leaves the schema default
	scheduleChecked bool
	items           []item
	blocklist       []blockEntry
	releases        []release
}

type item struct {
	number    int
	inLibrary bool
	// unmonitored rather than monitored, so the zero value is the common case.
	unmonitored bool
	// airsIn is the offset from the seed clock; dated says whether there is a
	// date at all, since a null air date is normal operation rather than an error.
	airsIn time.Duration
	dated  bool
	held   string
	grab   *grab
	pass   *passOutcome
}

// passOutcome is what the last pass decided about this item (#181), which is
// what the Missing screen's reason column reads.
type passOutcome struct {
	outcome string
	source  string
	release string
	detail  string
	// heldFor is a pin delay still running; 0 leaves held_until null.
	heldFor time.Duration
	agedBy  time.Duration
}

type grab struct {
	status     string
	release    string
	hash       string
	agedBy     time.Duration
	missingFor time.Duration
	stalledFor time.Duration
	lastError  string
	events     []event
}

type event struct {
	kind   string
	detail string
	agedBy time.Duration
}

type blockEntry struct {
	release string
	// hash is the failed grab row's own info hash, which is what
	// blocklist.Record is given; empty is a Torznab that published none.
	hash      string
	reason    string
	failures  int
	expiresIn time.Duration // 0 with permanent false means already expired
	permanent bool
}

// release is what the Torznab stub serves; covers names the item numbers it
// claims, so a search answers with something that fits the seeded run.
type release struct {
	title   string
	group   string
	covers  []int
	ageDays int
}

func profiles() []profile {
	return []profile{
		{
			name:            "Anime 1080p",
			resolutionOrder: `["1080p","720p"]`,
			subPref:         "softsub",
			codecPref:       "h265",
			preferDualAudio: true,
			hardExcludes:    `["hardsub"]`,
			minScore:        800,
			upgrades:        true,
			cutoffScore:     2400,
			groups: []profileGroup{
				{name: "SubGroupA", rank: 1},
				{name: "SubGroupB", rank: 2},
				{name: "RipCrew", blocked: true},
			},
		},
		{
			name:            "Archive 720p",
			resolutionOrder: `["720p","480p"]`,
			preferredSource: "bd",
			subPref:         "softsub",
			minScore:        300,
		},
		{
			name:            "Everything",
			resolutionOrder: `["2160p","1080p","720p","480p"]`,
			hardExcludes:    `[]`,
			upgrades:        true,
			cutoffScore:     2600,
		},
	}
}

// fixtures is the seeded world: roughly one title per case the screens have to
// render, rather than volume for its own sake.
func fixtures() []title {
	return []title{
		partlyFilled(),
		complete(),
		nothingYet(),
		undatedRun(),
		gapFilledRun(),
		neverSynced(),
		unmonitoredWithDates(),
		singleEpisodeOVA(),
		movie(),
		nullYearMovie(),
		longRunner(),
		deferredImport(),
	}
}

func partlyFilled() title {
	t := title{
		providerID: 990101, name: "Placeholder Frontier",
		altNames: []string{"Placeholder Frontier Season 1"},
		format:   domain.FormatTV, year: 2026, status: "RELEASING", episodes: 12,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990101.jpg",
		monitored: true, profile: "Anime 1080p", pinnedGroup: "SubGroupA",
		scheduleChecked: true,
	}
	for n := 1; n <= 12; n++ {
		it := item{number: n, airsIn: time.Duration(n-7)*week + 6*time.Hour, dated: true}
		switch {
		case n <= 4:
			it.inLibrary = true
		case n == 6:
			// SubGroupA is pinned, so the pin delay is still running against a
			// rival release group's release.
			it.pass = &passOutcome{
				outcome: "pin_held", source: "sweep",
				release: "[SubGroupB] Placeholder Frontier - 06 [1080p]",
				heldFor: 5 * time.Hour, agedBy: 40 * time.Minute,
			}
		case n == 5:
			it.grab = &grab{
				status: "grabbed", hash: "aa01000000000000000000000000000000000001",
				release: "[SubGroupA] Placeholder Frontier - 05 [1080p][HEVC][Dual Audio]",
				agedBy:  4 * time.Hour, stalledFor: 3 * time.Hour,
				events: []event{{kind: "grabbed", detail: "SubGroupA 1080p", agedBy: 4 * time.Hour}},
			}
		}
		t.items = append(t.items, it)
	}
	t.releases = []release{
		{title: "[SubGroupA] Placeholder Frontier - 06 [1080p][HEVC][Dual Audio]", group: "SubGroupA", covers: []int{6}},
		{title: "[SubGroupB] Placeholder Frontier - 06 [1080p]", group: "SubGroupB", covers: []int{6}, ageDays: 1},
		{title: "[SubGroupB] Placeholder Frontier - 06 [720p]", group: "SubGroupB", covers: []int{6}, ageDays: 1},
		{title: "[RipCrew] Placeholder Frontier - 06 [1080p]", group: "RipCrew", covers: []int{6}, ageDays: 2},
		{title: "[SubGroupA] Placeholder Frontier - 06v2 [1080p][HEVC][Dual Audio]", group: "SubGroupA", covers: []int{6}},
		{title: "[SubGroupA] Placeholder Frontier - 01-06 [1080p][Batch]", group: "SubGroupA", covers: []int{1, 2, 3, 4, 5, 6}, ageDays: 3},
	}
	return t
}

func complete() title {
	t := title{
		providerID: 990102, name: "Placeholder Chronicle",
		format: domain.FormatTV, year: 2025, status: "FINISHED", episodes: 24,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990102.jpg",
		monitored: true, profile: "Anime 1080p", scheduleChecked: true,
	}
	for n := 1; n <= 24; n++ {
		it := item{number: n, inLibrary: true, airsIn: time.Duration(n-30) * week, dated: true}
		// An upgrade candidate: had, and still worth offering a release for (#97).
		if n == 24 {
			it.held = "[SubGroupB] Placeholder Chronicle - 24 [720p]"
			it.grab = &grab{
				status: "imported", hash: "aa02000000000000000000000000000000000002",
				release: "[SubGroupB] Placeholder Chronicle - 24 [720p]",
				agedBy:  6 * week,
				events: []event{
					{kind: "grabbed", detail: "SubGroupB 720p", agedBy: 6*week + time.Hour},
					{kind: "imported", detail: "Placeholder Chronicle/Season 01", agedBy: 6 * week},
				},
			}
		}
		t.items = append(t.items, it)
	}
	t.releases = []release{
		{title: "[SubGroupA] Placeholder Chronicle - 24 [1080p][HEVC][Dual Audio]", group: "SubGroupA", covers: []int{24}, ageDays: 4},
	}
	return t
}

func nothingYet() title {
	t := title{
		providerID: 990103, name: "Placeholder Horizon",
		format: domain.FormatTV, year: 2026, status: "RELEASING", episodes: 12,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990103.jpg",
		monitored: true, profile: "Everything", scheduleChecked: true,
	}
	for n := 1; n <= 12; n++ {
		it := item{number: n, airsIn: time.Duration(n-3)*week + 12*time.Hour, dated: true}
		switch n {
		case 1:
			// Settling a grab row sets last_error to NULL, so a failed one never has
			// a reason attached; it is in the event row and the blocklist entry below.
			it.grab = &grab{
				status: "failed", hash: "aa03000000000000000000000000000000000003",
				release: "[RipCrew] Placeholder Horizon - 01 [1080p]",
				agedBy:  2 * day,
				events: []event{
					{kind: "grabbed", detail: "RipCrew 1080p", agedBy: 2*day + time.Hour},
					{kind: "failed", detail: "download client reported an error", agedBy: 2 * day},
				},
			}
		case 2:
			// The only shape that renders stuck: still grabbed, with the error the
			// importer writes when a completed payload will not import.
			it.grab = &grab{
				status: "grabbed", hash: "aa04000000000000000000000000000000000004",
				release:   "[SubGroupA] Placeholder Horizon - 02 [1080p][HEVC]",
				agedBy:    9 * time.Hour,
				lastError: "import failed: link into the library: permission denied",
				events:    []event{{kind: "grabbed", detail: "SubGroupA 1080p", agedBy: 9 * time.Hour}},
			}
		}
		t.items = append(t.items, it)
	}
	t.blocklist = []blockEntry{
		{release: "[RipCrew] Placeholder Horizon - 01 [1080p]", hash: "aa03000000000000000000000000000000000003", reason: "download client reported an error", failures: 1, expiresIn: day},
		{release: "[RipCrew] Placeholder Horizon - 02 [1080p]", reason: "no peers had the data", failures: 2, expiresIn: week},
		{release: "[LowSeed] Placeholder Horizon - 01-12 [480p][Batch]", reason: "payload did not contain what it claimed", failures: 3, permanent: true},
		{release: "[OldGrp] Placeholder Horizon - 03 [1080p]", reason: "download client reported an error", failures: 1, expiresIn: -2 * day},
	}
	t.releases = []release{
		{title: "[SubGroupA] Placeholder Horizon - 04 [1080p][HEVC]", group: "SubGroupA", covers: []int{4}},
		{title: "[LowSeed] Placeholder Horizon - 04 [480p]", group: "LowSeed", covers: []int{4}, ageDays: 2},
		{title: "[RipCrew] Placeholder Horizon - 01 [1080p]", group: "RipCrew", covers: []int{1}, ageDays: 3},
	}
	return t
}

// undatedRun is the pre-2015 case: AniList was asked and published no schedule
// at all, which the calendar footer must be able to say.
func undatedRun() title {
	t := title{
		providerID: 990104, name: "Placeholder Drift",
		format: domain.FormatTV, year: 2014, status: "FINISHED", episodes: 13,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990104.jpg",
		monitored: true, profile: "Archive 720p", scheduleChecked: true,
	}
	for n := 1; n <= 13; n++ {
		it := item{number: n, inLibrary: n <= 9}
		if n == 10 {
			it.pass = &passOutcome{outcome: "no_match", source: "sweep", agedBy: 6 * time.Hour}
		}
		t.items = append(t.items, it)
	}
	t.releases = []release{
		{title: "[ArchiveGrp] Placeholder Drift - 10 [720p][BD]", group: "ArchiveGrp", covers: []int{10}, ageDays: 30},
		{title: "[ArchiveGrp] Placeholder Drift - 01-13 [720p][BD][Batch]", group: "ArchiveGrp", covers: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}, ageDays: 40},
	}
	return t
}

// gapFilledRun is a null-count title whose schedule reads 1, 3, 4: episode 2
// shared a broadcast slot, so it exists undated and nothing else creates it (#152).
func gapFilledRun() title {
	t := title{
		providerID: 990105, name: "Placeholder Ember",
		format: domain.FormatTV, year: 2026, status: "RELEASING", episodes: 0,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990105.jpg",
		monitored: true, profile: "Anime 1080p", scheduleChecked: true,
		items: []item{
			{number: 1, inLibrary: true, airsIn: -2 * week, dated: true},
			{number: 2},
			{number: 3, airsIn: -1 * day, dated: true, pass: &passOutcome{
				outcome: "declined", source: "sweep",
				release: "[SubGroupA] Placeholder Ember - 03 [1080p][HEVC]",
				detail:  "score 700 is below the profile minimum 800",
				agedBy:  2 * time.Hour,
			}},
			{number: 4, airsIn: 6 * day, dated: true},
		},
	}
	t.releases = []release{
		{title: "[SubGroupA] Placeholder Ember - 03 [1080p][HEVC]", group: "SubGroupA", covers: []int{3}},
	}
	return t
}

// neverSynced has been added but not yet reached by the airing job, which is the
// footer's other absence and is briefly true of every title. Its item rows are
// what make it reachable: ListUnscheduledTitles joins wanted_items, so a title
// with none is never returned, whatever its airing stamp is set to.
func neverSynced() title {
	t := title{
		providerID: 990106, name: "Placeholder Vigil",
		format: domain.FormatTV, year: 2026, status: "NOT_YET_RELEASED", episodes: 12,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990106.jpg",
		monitored: true, profile: "Anime 1080p",
	}
	for n := 1; n <= 12; n++ {
		t.items = append(t.items, item{number: n})
	}
	return t
}

func unmonitoredWithDates() title {
	t := title{
		providerID: 990107, name: "Placeholder Solace",
		format: domain.FormatTV, year: 2024, status: "FINISHED", episodes: 12,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990107.jpg",
		monitored: false, profile: "Archive 720p", scheduleChecked: true,
	}
	for n := 1; n <= 12; n++ {
		t.items = append(t.items, item{number: n, airsIn: time.Duration(n-60) * week, dated: true})
	}
	return t
}

func singleEpisodeOVA() title {
	return title{
		providerID: 990108, name: "Placeholder Cadence",
		format: domain.FormatOVA, year: 2019, status: "FINISHED", episodes: 1,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990108.jpg",
		monitored: true, profile: "Archive 720p", scheduleChecked: true,
		items: []item{{number: 1, inLibrary: true}},
		releases: []release{
			{title: "[ArchiveGrp] Placeholder Cadence OVA [720p][BD]", group: "ArchiveGrp", covers: []int{1}, ageDays: 20},
		},
	}
}

func movie() title {
	return title{
		providerID: 990109, name: "Placeholder Legend: The Final",
		altNames: []string{"Placeholder Legend Movie"},
		format:   domain.FormatMovie, year: 2021, status: "FINISHED", episodes: 1,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990109.jpg",
		monitored: true, profile: "Everything", scheduleChecked: true,
		items: []item{{number: 1, inLibrary: true, airsIn: -80 * week, dated: true}},
		releases: []release{
			{title: "Placeholder Legend The Final 2021 1080p BluRay x264-GRP", group: "GRP", covers: []int{1}, ageDays: 12},
			{title: "[SubGroupB] Placeholder Legend - The Final (2021) [1080p][HEVC]", group: "SubGroupB", covers: []int{1}, ageDays: 12},
		},
	}
}

// nullYearMovie is the title-level ineligible reason: automation must not grab
// it, while a manual grab still can (#209).
func nullYearMovie() title {
	return title{
		providerID: 990110, name: "Placeholder Requiem",
		format: domain.FormatMovie, year: 0, status: "NOT_YET_RELEASED", episodes: 1,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990110.jpg",
		monitored: true, profile: "Everything", scheduleChecked: true,
		items: []item{{number: 1}},
		releases: []release{
			{title: "Placeholder Requiem 1080p WEB-DL x264-UNKNOWN", group: "UNKNOWN", covers: []int{1}, ageDays: 1},
		},
	}
}

// longRunner drains a back catalogue: a null count, a monitor cut partway up the
// run, and a grab missing from the download client inside its grace period.
func longRunner() title {
	t := title{
		providerID: 990111, name: "Placeholder Odyssey",
		format: domain.FormatTV, year: 2003, status: "RELEASING", episodes: 0,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990111.jpg",
		monitored: true, profile: "Archive 720p", monitorNewFrom: 1046, scheduleChecked: true,
	}
	for n := 1040; n <= 1052; n++ {
		it := item{number: n, unmonitored: n < 1046}
		switch {
		case n <= 1044:
			it.inLibrary = true
		case n == 1047:
			it.airsIn, it.dated = -3*day, true
			it.grab = &grab{
				status: "grabbed", hash: "aa11000000000000000000000000000000000011",
				release:    "[ArchiveGrp] Placeholder Odyssey - 1047 [720p]",
				agedBy:     3 * day,
				missingFor: 2 * time.Minute,
				events:     []event{{kind: "grabbed", detail: "ArchiveGrp 720p", agedBy: 3 * day}},
			}
		case n >= 1048:
			it.airsIn, it.dated = time.Duration(n-1047)*week, true
		}
		t.items = append(t.items, it)
	}
	t.releases = []release{
		{title: "[ArchiveGrp] Placeholder Odyssey - 1045 [720p]", group: "ArchiveGrp", covers: []int{1045}, ageDays: 5},
		{title: "[ArchiveGrp] Placeholder Odyssey - 1046 [720p]", group: "ArchiveGrp", covers: []int{1046}, ageDays: 5},
	}
	return t
}

// deferredImport is the settled-but-unfinished case: a payload we examined whose
// episode is inside an archive nobody has extracted (#135).
func deferredImport() title {
	t := title{
		providerID: 990112, name: "Placeholder Tide",
		format: domain.FormatONA, year: 2026, status: "RELEASING", episodes: 12,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990112.jpg",
		monitored: true, profile: "Anime 1080p", scheduleChecked: true,
	}
	for n := 1; n <= 12; n++ {
		it := item{number: n, airsIn: time.Duration(n-4)*week + 3*time.Hour, dated: true}
		switch n {
		case 1:
			it.inLibrary = true
		case 2:
			// The deferral reason is stored in the event row: settling to
			// import_deferred sets last_error back to NULL.
			it.grab = &grab{
				status: "import_deferred", hash: "aa12000000000000000000000000000000000012",
				release: "[SubGroupB] Placeholder Tide - 02 [1080p]",
				agedBy:  36 * time.Hour,
				events: []event{
					{kind: "grabbed", detail: "SubGroupB 1080p", agedBy: 37 * time.Hour},
					{kind: "import_deferred", detail: "extract Placeholder.Tide.02.part1.rar to import this episode", agedBy: 36 * time.Hour},
				},
			}
		case 3:
			// What notify-only records instead of grabbing.
			it.pass = &passOutcome{
				outcome: "would_grab", source: "feed",
				release: "[SubGroupA] Placeholder Tide - 03 [1080p][HEVC][Dual Audio]",
				agedBy:  25 * time.Minute,
			}
		}
		t.items = append(t.items, it)
	}
	t.releases = []release{
		{title: "[SubGroupB] Placeholder Tide - 03 [1080p]", group: "SubGroupB", covers: []int{3}},
		{title: "[SubGroupA] Placeholder Tide - 03 [1080p][HEVC][Dual Audio]", group: "SubGroupA", covers: []int{3}},
	}
	return t
}

// addable is served by the stubs and deliberately not seeded, so there is a
// title left to add offline; nothing else distinguishes the two sets. The ids
// are above AniList's own range, so a forgotten TRANSPONDARR_ANILIST_ENDPOINT
// makes the lookup fail instead of returning someone else's real title.
func addable() []title {
	return []title{
		{
			providerID: 990201, name: "Placeholder Beacon",
			altNames: []string{"Placeholder Beacon Season 1"},
			format:   domain.FormatTV, year: 2026, status: "RELEASING", episodes: 12,
			cover: "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990201.jpg",
			items: datedRun(12, -3*week+9*time.Hour, week),
			releases: []release{
				{title: "[SubGroupA] Placeholder Beacon - 03 [1080p][HEVC][Dual Audio]", group: "SubGroupA", covers: []int{3}},
				{title: "[SubGroupB] Placeholder Beacon - 03 [720p]", group: "SubGroupB", covers: []int{3}, ageDays: 1},
				{title: "[SubGroupA] Placeholder Beacon - 01-03 [1080p][Batch]", group: "SubGroupA", covers: []int{1, 2, 3}, ageDays: 2},
			},
		},
		{
			providerID: 990202, name: "Placeholder Sonata",
			format: domain.FormatMovie, year: 2023, status: "FINISHED", episodes: 1,
			cover: "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990202.jpg",
			items: datedRun(1, -60*week, 0),
			releases: []release{
				{title: "Placeholder Sonata 2023 1080p BluRay x264-GRP", group: "GRP", covers: []int{1}, ageDays: 9},
			},
		},
		{
			// A null count, so the add exercises #151's "no episodes on record" path.
			providerID: 990203, name: "Placeholder Lantern",
			format: domain.FormatONA, year: 2022, status: "FINISHED", episodes: 0,
			cover: "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-990203.jpg",
			items: datedRun(6, -80*week, week),
			releases: []release{
				{title: "[ArchiveGrp] Placeholder Lantern - 01-06 [1080p][Batch]", group: "ArchiveGrp", covers: []int{1, 2, 3, 4, 5, 6}, ageDays: 50},
			},
		},
	}
}

// served is every title the stubs answer for. The seeder reads fixtures()
// alone. That is what leaves the addable set unseeded.
func served() []title {
	return append(fixtures(), addable()...)
}

func datedRun(n int, first, interval time.Duration) []item {
	out := make([]item, 0, n)
	for i := range n {
		out = append(out, item{number: i + 1, airsIn: first + time.Duration(i)*interval, dated: true})
	}
	return out
}
