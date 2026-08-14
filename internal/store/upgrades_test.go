package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/store/db"
	"github.com/pressly/goose/v3"
)

// seedUpgradeProfile creates a profile with upgrades enabled and returns its id.
func seedUpgradeProfile(t *testing.T, st *Store, name string) int64 {
	t.Helper()
	p, err := st.Q.CreateQualityProfile(context.Background(), db.CreateQualityProfileParams{
		Name: name, ResolutionOrder: `["1080p"]`, HardExcludes: `[]`,
	})
	if err != nil {
		t.Fatalf("create profile %q: %v", name, err)
	}
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE quality_profiles SET upgrades_enabled = 1, cutoff_score = 2400 WHERE id = ?`, p.ID); err != nil {
		t.Fatalf("enable upgrades on profile %q: %v", name, err)
	}
	return p.ID
}

func setTitleProfile(t *testing.T, st *Store, titleID, profileID int64) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET quality_profile_id = ? WHERE id = ?`, profileID, titleID); err != nil {
		t.Fatalf("assign profile %d to series %d: %v", profileID, titleID, err)
	}
}

// seedHeldItem inserts an item already in the library, with the release that
// holds it and the grab status that release settled at.
func seedHeldItem(t *testing.T, st *Store, titleID int64, number int, heldTitle, grabStatus string) int64 {
	t.Helper()
	id := seedSearchItem(t, st, titleID, number, 1, nil)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE wanted_items SET held_release_title = ? WHERE id = ?`, heldTitle, id); err != nil {
		t.Fatalf("set held_release_title on item %d: %v", number, err)
	}
	if grabStatus != "" {
		seedSearchGrab(t, st, id, grabStatus)
	}
	return id
}

func feedTitles(t *testing.T, st *Store, now time.Time) []string {
	t.Helper()
	rows, err := st.Q.ListTitlesWithWantedItems(context.Background(),
		sql.NullString{String: FormatTimestamp(now), Valid: true})
	if err != nil {
		t.Fatalf("list series with wanted items: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return out
}

// The feed's due predicate is the only one that widens for upgrades (#97): a
// complete title re-enters it when its profile opts in and it holds a release
// worth beating. The sweep's predicate deliberately does not move.
func TestListTitlesWithWantedItemsIncludesUpgradePool(t *testing.T) {
	st := tempStore(t)
	now := time.Now()
	past := now.Add(-24 * time.Hour)
	profile := seedUpgradeProfile(t, st, "upgrades")

	// Included: the ordinary wanted item, on a profile with upgrades off.
	seedSearchItem(t, st, seedSearchTitle(t, st, "wanted", 1), 1, 0, &past)

	// Included: complete, but holding a release the profile may beat.
	held := seedSearchTitle(t, st, "held-imported", 1)
	setTitleProfile(t, st, held, profile)
	seedHeldItem(t, st, held, 1, "[ExampleSubs] Some Show - 01 (480p)", "imported")

	// Included: a failed upgrade grab leaves the item held and back in the pool.
	failedUpgrade := seedSearchTitle(t, st, "held-failed-upgrade", 1)
	setTitleProfile(t, st, failedUpgrade, profile)
	seedHeldItem(t, st, failedUpgrade, 1, "[ExampleSubs] Some Show - 01 (480p)", "failed")

	// Excluded: the same shape on a profile that never opted in.
	off := seedSearchTitle(t, st, "upgrades-off", 1)
	seedHeldItem(t, st, off, 1, "[ExampleSubs] Some Show - 01 (480p)", "imported")

	// Excluded: nothing identifies what is held, so nothing can be compared to it.
	blank := seedSearchTitle(t, st, "no-held-title", 1)
	setTitleProfile(t, st, blank, profile)
	seedHeldItem(t, st, blank, 1, "", "imported")

	// Excluded: an upgrade is already in flight.
	inFlight := seedSearchTitle(t, st, "upgrade-in-flight", 1)
	setTitleProfile(t, st, inFlight, profile)
	seedHeldItem(t, st, inFlight, 1, "[ExampleSubs] Some Show - 01 (480p)", "grabbed")

	// Excluded: a deferred grab is settled but its payload is a human's to fix.
	deferred := seedSearchTitle(t, st, "upgrade-deferred", 1)
	setTitleProfile(t, st, deferred, profile)
	seedHeldItem(t, st, deferred, 1, "[ExampleSubs] Some Show - 01 (480p)", "import_deferred")

	// Excluded: unmonitored (#188). Both halves need the clause -- without it on
	// the upgrade half, a narrowed held item makes its title feed-due every poll.
	narrowedUpgrade := seedSearchTitle(t, st, "unmonitored-upgrade", 1)
	setTitleProfile(t, st, narrowedUpgrade, profile)
	unmonitorItem(t, st,
		seedHeldItem(t, st, narrowedUpgrade, 1, "[ExampleSubs] Some Show - 01 (480p)", "imported"))
	// Excluded: the wanted half, narrowed.
	unmonitorItem(t, st, seedSearchItem(t, st, seedSearchTitle(t, st, "unmonitored-wanted", 1), 1, 0, &past))

	got := feedTitles(t, st, now)
	for _, want := range []string{"wanted", "held-imported", "held-failed-upgrade"} {
		if !contains(got, want) {
			t.Errorf("feed set %v is missing %q", got, want)
		}
	}
	for _, unwanted := range []string{
		"upgrades-off", "no-held-title", "upgrade-in-flight", "upgrade-deferred",
		"unmonitored-upgrade", "unmonitored-wanted",
	} {
		if contains(got, unwanted) {
			t.Errorf("feed set %v wrongly includes %q", got, unwanted)
		}
	}
	if len(got) != 3 {
		t.Errorf("feed set = %v, want exactly the wanted series plus the two upgradable ones", got)
	}

	// The sweep spends a search per title, so upgrades ride the flat-cost feed
	// alone: its predicate must be unchanged.
	if due := dueTitles(t, st, now, 100); len(due) != 1 || due[0] != "wanted" {
		t.Errorf("due set = %v, want only the series with something still wanted", due)
	}
}

// held_release_title is the identity an upgrade compares against, so an existing
// library must arrive carrying it rather than sitting outside the pool forever.
func TestQualityUpgradesMigrationBackfillsHeldTitle(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	titleID := seedSearchTitle(t, st, "backfilled", 1)
	if err := goose.DownTo(st.DB, "migrations", 16); err != nil {
		t.Fatalf("roll back to the pre-upgrades schema: %v", err)
	}

	// Two held items: one whose grab still records the release that landed it, one
	// whose row was overwritten by a later failed grab.
	imported := seedPreUpgradeItem(t, st, titleID, 1, 1, "[ExampleSubs] Some Show - 01 (1080p)", "imported")
	overwritten := seedPreUpgradeItem(t, st, titleID, 2, 1, "[OtherSubs] Some Show - 02 (1080p)", "failed")
	// Not held at all: nothing to remember.
	wanted := seedPreUpgradeItem(t, st, titleID, 3, 0, "[ExampleSubs] Some Show - 03 (1080p)", "grabbed")

	if err := goose.Up(st.DB, "migrations"); err != nil {
		t.Fatalf("re-apply the upgrades migration: %v", err)
	}

	for _, tc := range []struct {
		name string
		id   int64
		want string
	}{
		{"an imported grab names what is held", imported, "[ExampleSubs] Some Show - 01 (1080p)"},
		{"an overwritten grab leaves it unknown", overwritten, ""},
		{"an item we do not hold has nothing to name", wanted, ""},
	} {
		var got string
		if err := st.DB.QueryRowContext(ctx,
			`SELECT held_release_title FROM wanted_items WHERE id = ?`, tc.id).Scan(&got); err != nil {
			t.Fatalf("%s: read held_release_title: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: held_release_title = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// seedPreUpgradeItem inserts an item and its grab with raw SQL, so it works on
// the rolled-back schema the migration test drives — where the library flag is
// still named have, as 00019 renames it only later.
func seedPreUpgradeItem(t *testing.T, st *Store, titleID int64, number int, have int, releaseTitle, status string) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := st.DB.ExecContext(ctx,
		`INSERT INTO wanted_items (series_id, kind, number, have) VALUES (?, 'episode', ?, ?)`,
		titleID, number, have)
	if err != nil {
		t.Fatalf("insert item %d: %v", number, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("item %d id: %v", number, err)
	}
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO grabs (wanted_item_id, info_hash, release_title, status) VALUES (?, 'hash', ?, ?)`,
		id, releaseTitle, status); err != nil {
		t.Fatalf("insert grab for item %d: %v", number, err)
	}
	return id
}
