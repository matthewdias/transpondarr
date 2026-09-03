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
	release   string
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
		providerID: 101, name: "Placeholder Frontier",
		altNames: []string{"Placeholder Frontier Season 1"},
		format:   domain.FormatTV, year: 2026, status: "RELEASING", episodes: 12,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-101.jpg",
		monitored: true, profile: "Anime 1080p", pinnedGroup: "SubGroupA",
		scheduleChecked: true,
	}
	for n := 1; n <= 12; n++ {
		it := item{number: n, airsIn: time.Duration(n-5)*week + 6*time.Hour, dated: true}
		switch {
		case n <= 4:
			it.inLibrary = true
		case n == 5:
			it.grab = &grab{
				status: "grabbed", hash: "aa01000000000000000000000000000000000001",
				release: "[SubGroupA] Placeholder Frontier - 05 [1080p][HEVC][Dual Audio].mkv",
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
		providerID: 102, name: "Placeholder Chronicle",
		format: domain.FormatTV, year: 2025, status: "FINISHED", episodes: 24,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-102.jpg",
		monitored: true, profile: "Anime 1080p", scheduleChecked: true,
	}
	for n := 1; n <= 24; n++ {
		it := item{number: n, inLibrary: true, airsIn: time.Duration(n-30) * week, dated: true}
		// An upgrade candidate: had, and still worth offering a release for (#97).
		if n == 24 {
			it.held = "[SubGroupB] Placeholder Chronicle - 24 [720p]"
			it.grab = &grab{
				status: "imported", hash: "aa02000000000000000000000000000000000002",
				release: "[SubGroupB] Placeholder Chronicle - 24 [720p].mkv",
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
		providerID: 103, name: "Placeholder Horizon",
		format: domain.FormatTV, year: 2026, status: "RELEASING", episodes: 12,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-103.jpg",
		monitored: true, profile: "Everything", scheduleChecked: true,
	}
	for n := 1; n <= 12; n++ {
		it := item{number: n, airsIn: time.Duration(n-3)*week + 12*time.Hour, dated: true}
		if n == 1 {
			it.grab = &grab{
				status: "failed", hash: "aa03000000000000000000000000000000000003",
				release:   "[RipCrew] Placeholder Horizon - 01 [1080p].mkv",
				agedBy:    2 * day,
				lastError: "torrent reported an error: files missing from the download client",
				events: []event{
					{kind: "grabbed", detail: "RipCrew 1080p", agedBy: 2*day + time.Hour},
					{kind: "failed", detail: "download client reported an error", agedBy: 2 * day},
				},
			}
		}
		t.items = append(t.items, it)
	}
	t.blocklist = []blockEntry{
		{release: "[RipCrew] Placeholder Horizon - 01 [1080p]", reason: "download client reported an error", failures: 1, expiresIn: day},
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
		providerID: 104, name: "Placeholder Drift",
		format: domain.FormatTV, year: 2014, status: "FINISHED", episodes: 13,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-104.jpg",
		monitored: true, profile: "Archive 720p", scheduleChecked: true,
	}
	for n := 1; n <= 13; n++ {
		t.items = append(t.items, item{number: n, inLibrary: n <= 9})
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
		providerID: 105, name: "Placeholder Ember",
		format: domain.FormatTV, year: 2026, status: "RELEASING", episodes: 0,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-105.jpg",
		monitored: true, profile: "Anime 1080p", scheduleChecked: true,
		items: []item{
			{number: 1, inLibrary: true, airsIn: -2 * week, dated: true},
			{number: 2},
			{number: 3, airsIn: -1 * day, dated: true},
			{number: 4, airsIn: 6 * day, dated: true},
		},
	}
	t.releases = []release{
		{title: "[SubGroupA] Placeholder Ember - 03 [1080p][HEVC]", group: "SubGroupA", covers: []int{3}},
	}
	return t
}

// neverSynced has been added but not yet reached by the airing job, which is the
// footer's other absence and is briefly true of every title.
func neverSynced() title {
	return title{
		providerID: 106, name: "Placeholder Vigil",
		format: domain.FormatTV, year: 2026, status: "NOT_YET_RELEASED", episodes: 0,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-106.jpg",
		monitored: true, profile: "Anime 1080p",
	}
}

func unmonitoredWithDates() title {
	t := title{
		providerID: 107, name: "Placeholder Solace",
		format: domain.FormatTV, year: 2024, status: "FINISHED", episodes: 12,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-107.jpg",
		monitored: false, profile: "Archive 720p", scheduleChecked: true,
	}
	for n := 1; n <= 12; n++ {
		t.items = append(t.items, item{number: n, airsIn: time.Duration(n-60) * week, dated: true})
	}
	return t
}

func singleEpisodeOVA() title {
	return title{
		providerID: 108, name: "Placeholder Cadence",
		format: domain.FormatOVA, year: 2019, status: "FINISHED", episodes: 1,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-108.jpg",
		monitored: true, profile: "Archive 720p", scheduleChecked: true,
		items: []item{{number: 1, inLibrary: true}},
		releases: []release{
			{title: "[ArchiveGrp] Placeholder Cadence OVA [720p][BD]", group: "ArchiveGrp", covers: []int{1}, ageDays: 20},
		},
	}
}

func movie() title {
	return title{
		providerID: 109, name: "Placeholder Legend: The Final",
		altNames: []string{"Placeholder Legend Movie"},
		format:   domain.FormatMovie, year: 2021, status: "FINISHED", episodes: 1,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-109.jpg",
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
		providerID: 110, name: "Placeholder Requiem",
		format: domain.FormatMovie, year: 0, status: "NOT_YET_RELEASED", episodes: 1,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-110.jpg",
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
		providerID: 111, name: "Placeholder Odyssey",
		format: domain.FormatTV, year: 2003, status: "RELEASING", episodes: 0,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-111.jpg",
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
				release:    "[ArchiveGrp] Placeholder Odyssey - 1047 [720p].mkv",
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
		providerID: 112, name: "Placeholder Tide",
		format: domain.FormatONA, year: 2026, status: "RELEASING", episodes: 12,
		cover:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/placeholder-112.jpg",
		monitored: true, profile: "Anime 1080p", scheduleChecked: true,
	}
	for n := 1; n <= 12; n++ {
		it := item{number: n, airsIn: time.Duration(n-4)*week + 3*time.Hour, dated: true}
		switch n {
		case 1:
			it.inLibrary = true
		case 2:
			it.grab = &grab{
				status: "import_deferred", hash: "aa12000000000000000000000000000000000012",
				release:   "[SubGroupB] Placeholder Tide - 02 [1080p].mkv",
				agedBy:    36 * time.Hour,
				lastError: "extract Placeholder.Tide.02.part1.rar to import this episode",
				events: []event{
					{kind: "grabbed", detail: "SubGroupB 1080p", agedBy: 37 * time.Hour},
					{kind: "import_deferred", detail: "unextracted archive", agedBy: 36 * time.Hour},
				},
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
