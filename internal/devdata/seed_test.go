package devdata

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/metadata/dbcache"
	"github.com/matthewdias/transpondarr/internal/core/parser"
	"github.com/matthewdias/transpondarr/internal/store"
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
	if err := Seed(context.Background(), st, Options{Now: fixedNow, RNGSeed: 1}); err != nil {
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

func TestSeedProducesTheGrabDetailTheQueueRendersFrom(t *testing.T) {
	st := seeded(t)
	if n := count(t, st, `SELECT count(*) FROM grabs WHERE missing_since IS NOT NULL`); n == 0 {
		t.Error("no grab inside the missing-from-client grace period")
	}
	if n := count(t, st, `SELECT count(*) FROM grabs WHERE stalled_since IS NOT NULL`); n == 0 {
		t.Error("no grab with a stall clock running")
	}
	if n := count(t, st, `SELECT count(*) FROM grabs WHERE last_error IS NOT NULL AND last_error != ''`); n == 0 {
		t.Error("no grab carrying a last_error")
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

func TestSeedProducesBlocklistAtEveryRungIncludingExpired(t *testing.T) {
	st := seeded(t)
	stamp := func(d time.Duration) string { return store.FormatTimestamp(fixedNow.Add(d)) }

	if n := count(t, st, `SELECT count(*) FROM release_blocklist WHERE blocked_until IS NULL`); n == 0 {
		t.Error("no permanent blocklist entry")
	}
	if n := count(t, st, `SELECT count(*) FROM release_blocklist WHERE blocked_until > ? AND blocked_until <= ?`,
		stamp(0), stamp(48*time.Hour)); n == 0 {
		t.Error("no blocklist entry on the 24h rung")
	}
	if n := count(t, st, `SELECT count(*) FROM release_blocklist WHERE blocked_until > ?`, stamp(48*time.Hour)); n == 0 {
		t.Error("no blocklist entry on the 7d rung")
	}
	if n := count(t, st, `SELECT count(*) FROM release_blocklist WHERE blocked_until IS NOT NULL AND blocked_until <= ?`, stamp(0)); n == 0 {
		t.Error("no expired blocklist entry; expiry being filtered rather than deleted is invisible")
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

func TestSeedProducesBothCalendarAbsences(t *testing.T) {
	st := seeded(t)
	if n := count(t, st, `SELECT count(*) FROM series WHERE airing_synced_at IS NULL`); n == 0 {
		t.Error("no never-synced title; the footer's 'we have not asked' case is unreachable")
	}
	if n := count(t, st, `
		SELECT count(*) FROM series s
		WHERE s.airing_synced_at IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM wanted_items w WHERE w.series_id = s.id AND w.airs_at IS NOT NULL)`); n == 0 {
		t.Error("no synced-but-undated title; the footer's 'we asked and got nothing' case is unreachable")
	}
	if n := count(t, st, `
		SELECT count(*) FROM wanted_items w JOIN series s ON s.id = w.series_id
		WHERE w.airs_at IS NOT NULL AND s.monitored = 0`); n == 0 {
		t.Error("no dated item on an unmonitored title; #183's case is papered over")
	}
}

func TestSeedProducesPostersWithoutReachingAniList(t *testing.T) {
	st := seeded(t)
	cache := dbcache.New(st.Q)
	// Read it back the way the request path does, so the assertion survives a
	// change to how the snapshot is marshalled.
	for _, id := range []int64{101, 109, 111} {
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
		if err := Seed(context.Background(), st, Options{Now: fixedNow, RNGSeed: 7}); err != nil {
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
		t.Error("two seed runs with the same RNG seed and clock disagree; a bug found against a seeded library is not reproducible")
	}
}

// decide.Score works in the thousands, so a plausible-looking cutoff of 80
// silently empties the screen; this pins the scale, not the wording.
func TestHeldReleaseScoresBelowItsProfileCutoff(t *testing.T) {
	profileCutoffs := map[string]int{}
	for _, p := range profiles() {
		if p.upgrades {
			profileCutoffs[p.name] = int(p.cutoffScore)
		}
	}
	var checked int
	for _, ti := range fixtures() {
		cutoff, upgrading := profileCutoffs[ti.profile]
		if !upgrading {
			continue
		}
		for _, it := range ti.items {
			if it.held == "" {
				continue
			}
			checked++
			score, _ := decide.Score(parser.Parse(it.held), indexer.Release{Title: it.held}, domain.QualityProfile{
				Groups:          []string{"SubGroupA", "SubGroupB"},
				ResolutionOrder: []string{"1080p", "720p"},
				SubPref:         "softsub",
				CodecPref:       "h265",
				PreferDualAudio: true,
			})
			if score >= cutoff {
				t.Errorf("held release %q scores %d against %s's cutoff of %d; Cutoff Unmet will be empty",
					it.held, score, ti.profile, cutoff)
			}
		}
	}
	if checked == 0 {
		t.Error("no held release on an upgrading profile; Cutoff Unmet has nothing to list")
	}
}
