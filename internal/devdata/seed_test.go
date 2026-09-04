package devdata

import (
	"context"
	"database/sql"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/catalog"
	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/metadata/dbcache"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// fixedNow keeps every relative air date and grab age reproducible.
var fixedNow = time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

func seeded(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "dev.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })
	if err := Seed(context.Background(), st, Options{Now: fixedNow}); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	return st
}

func queryStrings(t *testing.T, st *store.Store, q string, args ...any) []string {
	t.Helper()
	rows, err := st.DB.Query(q, args...)
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, v.String)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func count(t *testing.T, st *store.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func TestSeedCoversEveryGrabStatus(t *testing.T) {
	st := seeded(t)
	got := map[string]bool{}
	for _, s := range queryStrings(t, st, `SELECT DISTINCT status FROM grabs`) {
		got[s] = true
	}
	for _, want := range []string{"grabbed", "imported", "failed", "import_deferred"} {
		if !got[want] {
			t.Errorf("no grab with status %q; the Activity screen cannot render it", want)
		}
	}
}

// The queue reads ListOpenGrabs and derives each row's status through
// deriveItemState with in_library false, so these three predicates are that
// function's reachable outcomes for a queue row. Counting statuses in grabs
// would miss stuck entirely, since stuck is grabbed plus an error.
func TestSeedProducesEveryStateTheActivityQueueRenders(t *testing.T) {
	st := seeded(t)
	rows, err := st.Q.ListOpenGrabs(context.Background())
	if err != nil {
		t.Fatalf("ListOpenGrabs: %v", err)
	}
	var downloading, stuck, deferred int
	for _, r := range rows {
		switch {
		case r.Status == "import_deferred":
			deferred++
		case r.Status == "grabbed" && r.LastError.String != "":
			stuck++
		case r.Status == "grabbed":
			downloading++
		}
	}
	if downloading == 0 {
		t.Errorf("%d open grabs, none downloading", len(rows))
	}
	if stuck == 0 {
		t.Errorf("%d open grabs, none grabbed-with-an-error; the queue cannot render stuck", len(rows))
	}
	if deferred == 0 {
		t.Errorf("%d open grabs, none deferred", len(rows))
	}
}

// SetGrabStatus writes last_error = NULL, and the only SetGrabLastError call
// site runs on rows that stay grabbed, so any other pairing is a state the
// seeder invented and no install can reach.
func TestSeedNeverWritesALastErrorAStatusWouldClear(t *testing.T) {
	st := seeded(t)
	got := queryStrings(t, st, `
		SELECT status || ': ' || release_title FROM grabs
		WHERE last_error IS NOT NULL AND last_error != '' AND status != 'grabbed'`)
	for _, row := range got {
		t.Errorf("grab row has a last_error its own status would have set to NULL: %s", row)
	}
}

func TestSeedProducesTheGrabDetailTheQueueRendersFrom(t *testing.T) {
	st := seeded(t)
	if n := count(t, st, `SELECT count(*) FROM grabs WHERE missing_since IS NOT NULL`); n == 0 {
		t.Error("no grab inside the missing-from-client grace period")
	}
	if n := count(t, st, `SELECT count(*) FROM grabs WHERE stalled_since IS NOT NULL`); n == 0 {
		t.Error("no grab with a stall clock running")
	}
	if n := count(t, st, `SELECT count(*) FROM grab_events`); n < 5 {
		t.Errorf("grab_events = %d rows, want enough for History to have depth", n)
	}
}

func TestSeedProducesNullAirDateItemsBesideDatedOnes(t *testing.T) {
	st := seeded(t)
	n := count(t, st, `
		SELECT count(*) FROM wanted_items w
		WHERE w.airs_at IS NULL
		  AND EXISTS (SELECT 1 FROM wanted_items o WHERE o.series_id = w.series_id AND o.airs_at IS NOT NULL)`)
	if n == 0 {
		t.Error("no undated item on a title that has dated ones; the normal pre-2015 case is unrepresented")
	}
}

func TestSeedProducesAGapFilledRun(t *testing.T) {
	st := seeded(t)
	// A schedule reading 1, 3, 4: the gap-filled item exists but carries no date,
	// which is the shape #152 creates and nothing else would produce.
	n := count(t, st, `
		SELECT count(*) FROM wanted_items gap
		WHERE gap.airs_at IS NULL
		  AND EXISTS (SELECT 1 FROM wanted_items lo WHERE lo.series_id = gap.series_id AND lo.number < gap.number AND lo.airs_at IS NOT NULL)
		  AND EXISTS (SELECT 1 FROM wanted_items hi WHERE hi.series_id = gap.series_id AND hi.number > gap.number AND hi.airs_at IS NOT NULL)`)
	if n == 0 {
		t.Error("no undated item bracketed by dated ones; there is no gap-filled run")
	}
}

func TestSeedProducesHeldButStillGrabbableItem(t *testing.T) {
	st := seeded(t)
	n := count(t, st, `
		SELECT count(*) FROM wanted_items w
		JOIN series s ON s.id = w.series_id
		JOIN quality_profiles p ON p.id = s.quality_profile_id
		WHERE w.in_library = 1 AND w.held_release_title != '' AND p.upgrades_enabled = 1`)
	if n == 0 {
		t.Error("no held item on an upgrades-enabled profile; the Upgrades screen has nothing to show")
	}
}

// Read through the query the Blocklist screen lists from, and check the failure
// count as well as the expiry: blocked_until is written directly, so asserting
// on it alone leaves the repeat-upsert loop behind each expiry unexercised.
func TestSeedProducesBlocklistAtEveryRungIncludingExpired(t *testing.T) {
	st := seeded(t)
	ctx := context.Background()

	stored := map[string]db.ReleaseBlocklist{}
	for _, id := range queryStrings(t, st, `SELECT id FROM series`) {
		titleID, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			t.Fatalf("series id %q: %v", id, err)
		}
		rows, err := st.Q.ListBlocklistByTitle(ctx, titleID)
		if err != nil {
			t.Fatalf("ListBlocklistByTitle(%d): %v", titleID, err)
		}
		for _, r := range rows {
			stored[r.NormalizedTitle] = r
		}
	}

	var permanent, expired, active int
	for _, ti := range fixtures() {
		for _, b := range ti.blocklist {
			key := decide.NormalizeReleaseTitle(b.release)
			row, ok := stored[key]
			if !ok {
				t.Errorf("blocklist entry %q is not in what the screen lists", b.release)
				continue
			}
			// blocklist.Record is given the failed grab row's own info hash, so an
			// entry naming a hash no grab row holds is a pairing no install writes;
			// empty is the case where Torznab published none.
			if row.InfoHash != "" && count(t, st, `SELECT count(*) FROM grabs WHERE info_hash = ?`, row.InfoHash) == 0 {
				t.Errorf("blocklist entry %q names info hash %q, which no grab row holds", b.release, row.InfoHash)
			}
			// remember() passes one grab row's hash and its release name together,
			// so an entry naming a hash must name that row's release too.
			if row.InfoHash != "" {
				for _, got := range queryStrings(t, st, `SELECT release_title FROM grabs WHERE info_hash = ?`, row.InfoHash) {
					if decide.NormalizeReleaseTitle(got) != row.NormalizedTitle {
						t.Errorf("blocklist entry %q shares a hash with grab row %q but not its release name", b.release, got)
					}
				}
			}
			if int(row.Failures) != b.failures {
				t.Errorf("blocklist entry %q reports %d failures, want %d; the stored failure count is not the one the fixture asked for",
					b.release, row.Failures, b.failures)
			}
			switch {
			case b.permanent:
				permanent++
				if row.BlockedUntil.Valid {
					t.Errorf("blocklist entry %q is meant to be permanent but expires at %s", b.release, row.BlockedUntil.String)
				}
			case b.expiresIn <= 0:
				expired++
				if !row.BlockedUntil.Valid || row.BlockedUntil.String > store.FormatTimestamp(fixedNow) {
					t.Errorf("blocklist entry %q is meant to have expired, blocked_until = %q", b.release, row.BlockedUntil.String)
				}
			default:
				active++
			}
		}
	}
	if permanent == 0 || expired == 0 || active == 0 {
		t.Errorf("blocklist covers %d permanent, %d expired, %d active entries; each has to be reachable", permanent, expired, active)
	}
}

func TestSeedProducesTheSeriesListPopulation(t *testing.T) {
	st := seeded(t)
	if n := count(t, st, `SELECT count(*) FROM series`); n < 8 {
		t.Errorf("series = %d, want enough rows for sorting and the sidebar count to mean something", n)
	}
	if n := count(t, st, `SELECT count(*) FROM series WHERE monitored = 0`); n == 0 {
		t.Error("no unmonitored title")
	}
	if n := count(t, st, `SELECT count(*) FROM series WHERE pinned_group IS NOT NULL`); n == 0 {
		t.Error("no title with a pinned release group")
	}
	if n := count(t, st, `SELECT count(DISTINCT quality_profile_id) FROM series`); n < 2 {
		t.Errorf("titles use %d profiles, want more than one in use", n)
	}
	if n := count(t, st, `SELECT count(*) FROM quality_profiles`); n < 3 {
		t.Errorf("quality_profiles = %d, want a second and third with non-default axes", n)
	}
	if n := count(t, st, `SELECT count(*) FROM series WHERE format = 'MOVIE'`); n == 0 {
		t.Error("no movie title; format is the discriminator everywhere and is untested by the seed")
	}
	if n := count(t, st, `SELECT count(*) FROM wanted_items WHERE kind = 'movie'`); n == 0 {
		t.Error("movie title has no movie-kind item")
	}
}

// The footer renders ListUnscheduledTitles, which inner-joins wanted_items, so
// a never-synced title with no items counts in series and still cannot appear.
func TestSeedProducesBothCalendarAbsences(t *testing.T) {
	st := seeded(t)
	rows, err := st.Q.ListUnscheduledTitles(context.Background(), int64(0))
	if err != nil {
		t.Fatalf("ListUnscheduledTitles: %v", err)
	}
	var asked, unasked []string
	for _, r := range rows {
		if r.ScheduleChecked {
			asked = append(asked, r.Title)
		} else {
			unasked = append(unasked, r.Title)
		}
	}
	if len(unasked) == 0 {
		t.Errorf("ListUnscheduledTitles returned %d rows, none unchecked; the footer's 'we have not asked' case is unreachable", len(rows))
	}
	if len(asked) == 0 {
		t.Errorf("ListUnscheduledTitles returned %d rows, none checked; the footer's 'we asked and got nothing' case is unreachable", len(rows))
	}
}

// #183's case, read through the grid's own query rather than the base tables:
// an unmonitored title is absent from the grid until the filter is switched on,
// and the seed must not paper over that by leaving it undated.
func TestSeedProducesADatedItemOnAnUnmonitoredTitle(t *testing.T) {
	st := seeded(t)
	items, err := st.Q.ListCalendarItems(context.Background(), db.ListCalendarItemsParams{
		AirsAt:   sql.NullString{String: store.FormatTimestamp(fixedNow.Add(-100 * week)), Valid: true},
		AirsAt_2: sql.NullString{String: store.FormatTimestamp(fixedNow.Add(100 * week)), Valid: true},
	})
	if err != nil {
		t.Fatalf("ListCalendarItems: %v", err)
	}
	var monitored, unmonitored int
	for _, r := range items {
		if r.TitleMonitored == 1 {
			monitored++
		} else {
			unmonitored++
		}
	}
	if monitored == 0 {
		t.Error("the calendar grid is empty for the default filter; no monitored title has a dated item")
	}
	if unmonitored == 0 {
		t.Errorf("%d calendar rows, none on an unmonitored title; #183's case is papered over", len(items))
	}
}

func TestSeedProducesPostersWithoutReachingAniList(t *testing.T) {
	st := seeded(t)
	cache := dbcache.New(st.Q)
	// Read it back the way the request path does, so the assertion survives a
	// change to how the snapshot is marshalled.
	for _, id := range []int64{990101, 990109, 990111} {
		snap, _, ok, err := cache.Get(context.Background(), "anilist", id)
		if err != nil || !ok {
			t.Fatalf("cache.Get(%d) ok=%v err=%v, want a cached snapshot", id, ok, err)
		}
		if snap.Title.CoverURL == "" {
			t.Errorf("title %d has no cover URL; the list page has no images", id)
		}
	}
	rows := count(t, st, `SELECT count(*) FROM metadata_cache`)
	if titles := count(t, st, `SELECT count(*) FROM series`); rows < titles {
		t.Errorf("metadata_cache rows = %d for %d titles, want every title cached", rows, titles)
	}
}

func TestSeedIsDeterministic(t *testing.T) {
	dump := func() string {
		st, err := store.Open(filepath.Join(t.TempDir(), "dev.db"))
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer func() { _ = st.DB.Close() }()
		if err := Seed(context.Background(), st, Options{Now: fixedNow}); err != nil {
			t.Fatalf("Seed: %v", err)
		}
		var b strings.Builder
		for _, q := range []string{
			`SELECT provider_id || '|' || title || '|' || format || '|' || monitored || '|' || year FROM series ORDER BY provider_id`,
			`SELECT s.provider_id || '|' || w.kind || '|' || coalesce(w.number,-1) || '|' || w.in_library || '|' || w.monitored || '|' || coalesce(w.airs_at,'') FROM wanted_items w JOIN series s ON s.id = w.series_id ORDER BY s.provider_id, w.number`,
			`SELECT info_hash || '|' || release_title || '|' || status FROM grabs ORDER BY info_hash`,
			`SELECT normalized_title || '|' || reason || '|' || coalesce(blocked_until,'never') FROM release_blocklist ORDER BY normalized_title`,
		} {
			for _, row := range queryStrings(t, st, q) {
				b.WriteString(row)
				b.WriteByte('\n')
			}
		}
		return b.String()
	}
	if a, b := dump(), dump(); a != b {
		t.Error("two seed runs on the same clock disagree; a bug found against a seeded library is not reproducible")
	}
}

// decide.Score works in the thousands, so a plausible-looking cutoff empties the
// screen silently. Scoring here goes through the service the screen calls, under
// the seeded profile: a hand-built profile literal moves with neither, so
// re-ranking profiles() would leave it passing against an empty screen.
// CutoffUnmet reads only the store, which is why the other five deps are nil.
func TestCutoffUnmetListsAGroup(t *testing.T) {
	st := seeded(t)
	// The zero cursor is this listing's own top: it orders by title ascending,
	// where Missing descends from QueueCursorTop.
	page, err := acquire.New(st, nil, nil, nil, nil, nil).CutoffUnmet(context.Background(), acquire.CutoffUnmetParams{Limit: 10})
	if err != nil {
		t.Fatalf("CutoffUnmet: %v", err)
	}
	if len(page.Groups) == 0 {
		t.Fatal("Cutoff Unmet is empty; no seeded held release scores below its own profile's cutoff")
	}
	for _, g := range page.Groups {
		if len(g.Items) == 0 {
			t.Errorf("group %q lists no items", g.TitleName)
			continue
		}
		for _, it := range g.Items {
			if it.Score >= g.CutoffScore {
				t.Errorf("%s item %d scores %d against a cutoff of %d, so it should not be listed",
					g.TitleName, it.Number, it.Score, g.CutoffScore)
			}
			// Without this a profile with no axes at all would list everything
			// while the screen had nothing to say about why.
			if len(it.UnmetGoals) == 0 {
				t.Errorf("%s item %d is listed with no unmet goals; the row has no reason to show",
					g.TitleName, it.Number)
			}
		}
	}
}

// A fixture naming a profile that does not exist used to be skipped, so the
// title silently ran on the built-in Default and the fixture's own axes were
// never applied.
func TestSeedRefusesAFixtureNamingAnUnknownProfile(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "dev.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })

	profileIDs, err := seedProfiles(context.Background(), st)
	if err != nil {
		t.Fatalf("seedProfiles: %v", err)
	}
	bad := []title{{providerID: 1, name: "Nonexistent Profile", format: domain.FormatTV, profile: "Nope"}}
	err = seedTitles(context.Background(), st, dbcache.New(st.Q), bad, profileIDs, fixedNow)
	if err == nil {
		t.Fatal("seedTitles accepted a fixture naming an unknown profile; the title would run on the default instead")
	}
	if !strings.Contains(err.Error(), "Nope") {
		t.Errorf("error %q does not name the profile that is missing", err)
	}
}

// The reason column ranks a stored pass outcome against the grab beside it, so
// the rows have to arrive through ListMissingItemsByTitle rather than be counted
// in pass_outcomes: an outcome on an item the page never lists shows nothing.
func TestSeedProducesTheMissingScreensReasonColumn(t *testing.T) {
	st := seeded(t)
	ctx := context.Background()

	titles, err := st.Q.ListMissingTitlesPage(ctx, db.ListMissingTitlesPageParams{
		Column1:  0,
		Column2:  0,
		AirsAt:   sql.NullString{String: store.FormatTimestamp(fixedNow), Valid: true},
		AirsAt_2: sql.NullString{String: "~", Valid: true},
		AirsAt_3: sql.NullString{String: "~", Valid: true},
		ID:       0,
		Limit:    50,
	})
	if err != nil {
		t.Fatalf("ListMissingTitlesPage: %v", err)
	}
	if len(titles) == 0 {
		t.Fatal("the Missing screen has no groups")
	}
	ids := make([]int64, 0, len(titles))
	for _, s := range titles {
		ids = append(ids, s.ID)
	}
	items, err := st.Q.ListMissingItemsByTitle(ctx, db.ListMissingItemsByTitleParams{
		TitleIds: ids,
		Column2:  0,
		Column3:  0,
		AirsAt:   sql.NullString{String: store.FormatTimestamp(fixedNow), Valid: true},
	})
	if err != nil {
		t.Fatalf("ListMissingItemsByTitle: %v", err)
	}
	got := map[string]bool{}
	for _, r := range items {
		if r.PassOutcome.Valid {
			got[r.PassOutcome.String] = true
		}
	}
	for _, want := range []string{acquire.OutcomeNoMatch, acquire.OutcomeDeclined, acquire.OutcomePinHeld, acquire.OutcomeWouldGrab} {
		if !got[want] {
			t.Errorf("no listed missing item stores the %q outcome; the reason column cannot render it", want)
		}
	}
}

// Variants are Romaji, English and Native deduped (catalog.dedupeNonEmpty), so a
// fixture altName that reaches none of them leaves #107's variant-fallback
// search unexercisable and makes altNames look load-bearing when it is not.
func TestASeededTitleHasMoreThanOneNameVariant(t *testing.T) {
	svc := catalog.NewService(seeded(t), anilistStub(t))
	ctx := context.Background()

	var withAlts int
	for _, ti := range fixtures() {
		if len(ti.altNames) == 0 {
			continue
		}
		withAlts++
		got, err := svc.TitleVariants(ctx, ti.providerID)
		if err != nil {
			t.Fatalf("TitleVariants(%d): %v", ti.providerID, err)
		}
		if len(got) < 2 {
			t.Errorf("TitleVariants(%d) = %v, want the fixture's alt name too", ti.providerID, got)
		}
	}
	if withAlts == 0 {
		t.Error("no fixture declares an alt name; the variant fallback has nothing to search with")
	}
}

// A stored pass detail is a snapshot of what decide once said, so a number in it
// that disagrees with the profile puts a threshold on the Missing screen that the
// profile editor contradicts.
func TestAStoredRefusalDetailAgreesWithItsOwnProfile(t *testing.T) {
	minScores := map[string]int64{}
	for _, p := range profiles() {
		minScores[p.name] = p.minScore
	}
	pattern := regexp.MustCompile(`below the profile minimum (\d+)`)

	var checked int
	for _, ti := range fixtures() {
		for _, it := range ti.items {
			if it.pass == nil {
				continue
			}
			m := pattern.FindStringSubmatch(it.pass.detail)
			if m == nil {
				continue
			}
			checked++
			got, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil {
				t.Fatalf("detail %q: %v", it.pass.detail, err)
			}
			if want := minScores[ti.profile]; got != want {
				t.Errorf("%s item %d stores %q, but its profile %q sets a minimum of %d",
					ti.name, it.number, it.pass.detail, ti.profile, want)
			}
		}
	}
	if checked == 0 {
		t.Error("no stored refusal names a profile minimum; the screen has no threshold to be wrong about")
	}
}
