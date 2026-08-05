package acquire_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// All fixtures use invented series/group names; only the naming structure under
// test is real. The profile ranks TopSubs above MidSubs at 1080p/720p, so a held
// release scores predictably: group 2000/1900, resolution 400/300.
func upgradingProfile(t *testing.T, st *store.Store, name string, cutoff int64) int64 {
	t.Helper()
	ctx := context.Background()
	p, err := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name:                 name,
		ResolutionOrder:      `["1080p","720p"]`,
		HardExcludes:         `[]`,
		UpgradesEnabled:      1,
		CutoffScore:          cutoff,
		UpgradeV2AboveCutoff: 1,
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	for rank, g := range []string{"TopSubs", "MidSubs"} {
		if _, err := st.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
			ProfileID: p.ID, GroupName: g, Rank: int64(rank),
		}); err != nil {
			t.Fatalf("add group %s: %v", g, err)
		}
	}
	return p.ID
}

// putOnProfile moves a seeded series onto a profile, which is what decides
// whether its held items are candidates at all.
func putOnProfile(t *testing.T, st *store.Store, seriesID, profileID int64) {
	t.Helper()
	rows, err := st.Q.SetSeriesProfile(context.Background(), db.SetSeriesProfileParams{
		QualityProfileID: profileID, ID: seriesID, ID_2: profileID,
	})
	if err != nil || rows != 1 {
		t.Fatalf("set series profile: %v (%d rows)", err, rows)
	}
}

// hold marks an item as held by a release, which is what the cutoff scores.
func hold(t *testing.T, st *store.Store, seriesID int64, number int, releaseTitle string) {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := st.DB.QueryRowContext(ctx,
		`SELECT id FROM wanted_items WHERE series_id = ? AND number = ?`, seriesID, number).Scan(&id); err != nil {
		t.Fatalf("look up item %d: %v", number, err)
	}
	if err := st.Q.SetWantedItemHeld(ctx, db.SetWantedItemHeldParams{
		Have: 1, HeldReleaseTitle: releaseTitle, ID: id,
	}); err != nil {
		t.Fatalf("hold item %d: %v", number, err)
	}
	if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: id, InfoHash: "hash" + strconv.Itoa(number), ReleaseTitle: releaseTitle, Status: "imported",
	}); err != nil {
		t.Fatalf("record grab for item %d: %v", number, err)
	}
}

func cutoffService(t *testing.T, st *store.Store) *acquire.Service {
	t.Helper()
	return acquire.New(st, newRegistry(nil, nil), fakeTitles{}, fakeConfig{}, discardLogger(), nil)
}

// Membership is exact: a held release scoring below its profile's cutoff is in,
// one at or above it is out, and a series on a non-upgrading profile never
// appears at all.
func TestCutoffUnmetMembership(t *testing.T) {
	st := coretest.NewStore(t)
	ctx := context.Background()
	seriesID := seedSeries(t, st, "Placeholder Saga", 3)
	putOnProfile(t, st, seriesID, upgradingProfile(t, st, "Upgrading", 2300))
	hold(t, st, seriesID, 1, "[TopSubs] Placeholder Saga - 01 [720p]")  // 2300: met
	hold(t, st, seriesID, 2, "[MidSubs] Placeholder Saga - 02 [720p]")  // 2200: unmet
	hold(t, st, seriesID, 3, "[TopSubs] Placeholder Saga - 03 [1080p]") // 2400: met

	static, err := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "Static", ResolutionOrder: `["1080p","720p"]`, HardExcludes: `[]`, CutoffScore: 9000,
	})
	if err != nil {
		t.Fatalf("create static profile: %v", err)
	}
	other := seedSeries(t, st, "Static Show", 1)
	putOnProfile(t, st, other, static.ID)
	hold(t, st, other, 1, "[MidSubs] Static Show - 01 [720p]")

	page, err := cutoffService(t, st).CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 20})
	if err != nil {
		t.Fatalf("CutoffUnmet: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %+v, want only the sub-cutoff item on the upgrading profile", page.Items)
	}
	got := page.Items[0]
	if got.Number != 2 || got.SeriesID != seriesID {
		t.Errorf("item = %+v, want episode 2 of Placeholder Saga", got)
	}
	if got.Score >= got.CutoffScore {
		t.Errorf("score %d, cutoff %d: a listed item must score below its cutoff", got.Score, got.CutoffScore)
	}
	if got.ProfileName != "Upgrading" || got.HeldReleaseTitle == "" {
		t.Errorf("item = %+v, want the profile name and held release carried through", got)
	}
}

// A page is filled to its limit even though membership is decided in Go: the
// scan reads past the rows it rejects, so "load more" is never a page of
// nothing with a cursor attached.
func TestCutoffUnmetPageIsFilledPastRejects(t *testing.T) {
	st := coretest.NewStore(t)
	seriesID := seedSeries(t, st, "Placeholder Saga", 12)
	putOnProfile(t, st, seriesID, upgradingProfile(t, st, "Upgrading", 2300))
	// Every odd item meets the cutoff, so a single fetch of limit+1 rows would
	// hand back half a page.
	for n := 1; n <= 12; n++ {
		res, group := "720p", "MidSubs"
		if n%2 == 1 {
			res, group = "1080p", "TopSubs"
		}
		hold(t, st, seriesID, n, "["+group+"] Placeholder Saga - "+strconv.Itoa(n)+" ["+res+"]")
	}

	ctx := context.Background()
	svc := cutoffService(t, st)
	page, err := svc.CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 4})
	if err != nil {
		t.Fatalf("CutoffUnmet: %v", err)
	}
	if len(page.Items) != 4 {
		t.Fatalf("items = %d, want a full page of 4", len(page.Items))
	}
	if page.NextCursor == (acquire.QueueCursor{}) {
		t.Fatal("want a cursor: two unmet items remain")
	}

	rest, err := svc.CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 4, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("CutoffUnmet page 2: %v", err)
	}
	if len(rest.Items) != 2 {
		t.Fatalf("page 2 items = %d, want the remaining 2", len(rest.Items))
	}
	if rest.NextCursor != (acquire.QueueCursor{}) {
		t.Errorf("next_cursor = %+v, want none on the last page", rest.NextCursor)
	}
	seen := map[int]bool{}
	for _, it := range append(page.Items, rest.Items...) {
		if seen[it.Number] {
			t.Fatalf("item %d listed twice across pages", it.Number)
		}
		seen[it.Number] = true
	}
	if len(seen) != 6 {
		t.Fatalf("distinct items = %d, want the 6 even-numbered ones", len(seen))
	}
}

// Unmonitored series are withheld unless asked for: the toggle mirrors the
// calendar's rather than inventing a second meaning.
func TestCutoffUnmetUnmonitoredToggle(t *testing.T) {
	st := coretest.NewStore(t)
	ctx := context.Background()
	seriesID := seedSeries(t, st, "Quiet Show", 1)
	putOnProfile(t, st, seriesID, upgradingProfile(t, st, "Upgrading", 2300))
	hold(t, st, seriesID, 1, "[MidSubs] Quiet Show - 01 [720p]")
	if _, err := st.DB.ExecContext(ctx, `UPDATE series SET monitored = 0 WHERE id = ?`, seriesID); err != nil {
		t.Fatalf("unmonitor: %v", err)
	}

	svc := cutoffService(t, st)
	page, err := svc.CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 20})
	if err != nil {
		t.Fatalf("CutoffUnmet: %v", err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("items = %+v, want none: the series is unmonitored", page.Items)
	}
	page, err = svc.CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 20, IncludeUnmonitored: true})
	if err != nil {
		t.Fatalf("CutoffUnmet unmonitored: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %+v, want the unmonitored item once asked for", page.Items)
	}
}
