package acquire_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// All fixtures use invented title/group names; only the naming structure under
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

// putOnProfile moves a seeded title onto a profile, which is what decides
// whether its held items are candidates at all.
func putOnProfile(t *testing.T, st *store.Store, titleID, profileID int64) {
	t.Helper()
	rows, err := st.Q.SetTitleProfile(context.Background(), db.SetTitleProfileParams{
		QualityProfileID: profileID, ID: titleID, ID_2: profileID,
	})
	if err != nil || rows != 1 {
		t.Fatalf("set series profile: %v (%d rows)", err, rows)
	}
}

// hold marks an item as held by a release, which is what the cutoff scores.
func hold(t *testing.T, st *store.Store, titleID int64, number int, releaseTitle string) {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := st.DB.QueryRowContext(ctx,
		`SELECT id FROM wanted_items WHERE series_id = ? AND number = ?`, titleID, number).Scan(&id); err != nil {
		t.Fatalf("look up item %d: %v", number, err)
	}
	if err := st.Q.SetWantedItemHeld(ctx, db.SetWantedItemHeldParams{
		InLibrary: 1, HeldReleaseTitle: releaseTitle, ID: id,
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
// one at or above it is out, and a title on a non-upgrading profile never
// appears at all. The cutoff and profile name live on the group, being the
// profile's rather than any one item's.
func TestCutoffUnmetMembership(t *testing.T) {
	st := coretest.NewStore(t)
	ctx := context.Background()
	titleID := seedTitle(t, st, "Placeholder Saga", 3)
	putOnProfile(t, st, titleID, upgradingProfile(t, st, "Upgrading", 2300))
	hold(t, st, titleID, 1, "[TopSubs] Placeholder Saga - 01 [720p]")  // 2300: met
	hold(t, st, titleID, 2, "[MidSubs] Placeholder Saga - 02 [720p]")  // 2200: unmet
	hold(t, st, titleID, 3, "[TopSubs] Placeholder Saga - 03 [1080p]") // 2400: met

	static, err := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "Static", ResolutionOrder: `["1080p","720p"]`, HardExcludes: `[]`, CutoffScore: 9000,
	})
	if err != nil {
		t.Fatalf("create static profile: %v", err)
	}
	other := seedTitle(t, st, "Static Show", 1)
	putOnProfile(t, st, other, static.ID)
	hold(t, st, other, 1, "[MidSubs] Static Show - 01 [720p]")

	page, err := cutoffService(t, st).CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 20})
	if err != nil {
		t.Fatalf("CutoffUnmet: %v", err)
	}
	if len(page.Groups) != 1 {
		t.Fatalf("groups = %+v, want only the upgrading series", page.Groups)
	}
	g := page.Groups[0]
	if g.TitleID != titleID || g.ProfileName != "Upgrading" || g.CutoffScore != 2300 || g.Below != 1 {
		t.Errorf("group = %+v, want Placeholder Saga on Upgrading at 2300 with 1 below", g)
	}
	if len(g.Items) != 1 || g.Items[0].Number != 2 {
		t.Fatalf("items = %+v, want only episode 2", g.Items)
	}
	got := g.Items[0]
	if got.Score >= g.CutoffScore {
		t.Errorf("score %d, cutoff %d: a listed item must score below its cutoff", got.Score, g.CutoffScore)
	}
	if got.HeldReleaseTitle == "" || len(got.UnmetGoals) == 0 {
		t.Errorf("item = %+v, want the held release and its unmet goals carried through", got)
	}
}

// holdWithStatus is hold with the grab left in a chosen state, which is what
// decides whether the upgrade pool can act on the item at all.
func holdWithStatus(t *testing.T, st *store.Store, titleID int64, number int, releaseTitle, status string) {
	t.Helper()
	hold(t, st, titleID, number, releaseTitle)
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE grabs SET status = ? WHERE wanted_item_id =
		     (SELECT id FROM wanted_items WHERE series_id = ? AND number = ?)`,
		status, titleID, number); err != nil {
		t.Fatalf("set grab status for item %d: %v", number, err)
	}
}

// Membership is the sweep's upgrade pool plus what it is already acting on.
// import_deferred is the one held state left out: its fix belongs to the
// Activity queue, and a grab from here would overwrite the deferred row and
// orphan the payload the episode is sitting in.
func TestCutoffUnmetMembershipMatchesTheUpgradePool(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedTitle(t, st, "Placeholder Saga", 4)
	putOnProfile(t, st, titleID, upgradingProfile(t, st, "Upgrading", 2300))
	// All four hold the same sub-cutoff release; only the grab state differs.
	const held = "[MidSubs] Placeholder Saga - %s [720p]"
	holdWithStatus(t, st, titleID, 1, fmt.Sprintf(held, "01"), "imported")
	holdWithStatus(t, st, titleID, 2, fmt.Sprintf(held, "02"), "failed")
	holdWithStatus(t, st, titleID, 3, fmt.Sprintf(held, "03"), "grabbed")
	holdWithStatus(t, st, titleID, 4, fmt.Sprintf(held, "04"), "import_deferred")

	page, err := cutoffService(t, st).CutoffUnmet(context.Background(), acquire.CutoffUnmetParams{Limit: 20})
	if err != nil {
		t.Fatalf("CutoffUnmet: %v", err)
	}
	if len(page.Groups) != 1 {
		t.Fatalf("groups = %+v, want one", page.Groups)
	}
	got := map[int]string{}
	for _, it := range page.Groups[0].Items {
		got[it.Number] = it.Grab.Status
	}
	if len(got) != 3 || got[1] != "imported" || got[2] != "failed" || got[3] != "grabbed" {
		t.Fatalf("items = %v, want the imported, failed and in-flight holds", got)
	}
	if _, ok := got[4]; ok {
		t.Error("a deferred grab is the Activity queue's to fix and must not be listed here")
	}
	if page.Groups[0].Below != 3 {
		t.Errorf("below = %d, want the count to exclude the deferred item too", page.Groups[0].Below)
	}
}

// A page of groups is filled by scanning past title whose held releases all
// meet their cutoff, and a title never splits across pages.
func TestCutoffUnmetPagesGroupsPastMetTitles(t *testing.T) {
	st := coretest.NewStore(t)
	ctx := context.Background()
	profileID := upgradingProfile(t, st, "Upgrading", 2300)
	// Titles sort A..F; the even ones hold sub-cutoff releases, the odd ones are
	// fully met and must be scanned over without becoming groups.
	titles := []string{"Alpha Saga", "Bravo Saga", "Charlie Saga", "Delta Saga", "Echo Saga", "Foxtrot Saga"}
	wantTitles := map[string]bool{"Bravo Saga": true, "Delta Saga": true, "Foxtrot Saga": true}
	for i, title := range titles {
		id := seedTitle(t, st, title, 2)
		putOnProfile(t, st, id, profileID)
		for n := 1; n <= 2; n++ {
			res, group := "1080p", "TopSubs"
			if wantTitles[title] {
				res, group = "720p", "MidSubs"
			}
			hold(t, st, id, n, "["+group+"] "+title+" - "+strconv.Itoa(n)+" ["+res+"]")
		}
		_ = i
	}

	svc := cutoffService(t, st)
	seen := map[string]int{}
	cursor, pages := acquire.QueueCursor{}, 0
	for {
		page, err := svc.CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("CutoffUnmet: %v", err)
		}
		for _, g := range page.Groups {
			seen[g.TitleName] += len(g.Items)
			if len(g.Items) != 2 || g.Below != 2 {
				t.Fatalf("group %s arrived split: %+v", g.TitleName, g)
			}
		}
		pages++
		if page.NextCursor == (acquire.QueueCursor{}) {
			break
		}
		if pages > 6 {
			t.Fatal("pagination did not terminate")
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("saw %v, want exactly the three sub-cutoff series", seen)
	}
	for title := range wantTitles {
		if seen[title] != 2 {
			t.Errorf("series %s items = %d, want its whole 2", title, seen[title])
		}
	}
}

// Unmonitored title are withheld unless asked for: the toggle mirrors the
// calendar's rather than inventing a second meaning.
func TestCutoffUnmetUnmonitoredToggle(t *testing.T) {
	st := coretest.NewStore(t)
	ctx := context.Background()
	titleID := seedTitle(t, st, "Quiet Show", 1)
	putOnProfile(t, st, titleID, upgradingProfile(t, st, "Upgrading", 2300))
	hold(t, st, titleID, 1, "[MidSubs] Quiet Show - 01 [720p]")
	if _, err := st.DB.ExecContext(ctx, `UPDATE series SET monitored = 0 WHERE id = ?`, titleID); err != nil {
		t.Fatalf("unmonitor: %v", err)
	}

	svc := cutoffService(t, st)
	page, err := svc.CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 20})
	if err != nil {
		t.Fatalf("CutoffUnmet: %v", err)
	}
	if len(page.Groups) != 0 {
		t.Fatalf("groups = %+v, want none: the series is unmonitored", page.Groups)
	}
	page, err = svc.CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 20, IncludeUnmonitored: true})
	if err != nil {
		t.Fatalf("CutoffUnmet unmonitored: %v", err)
	}
	if len(page.Groups) != 1 || page.Groups[0].Monitored {
		t.Fatalf("groups = %+v, want the unmonitored group once asked for", page.Groups)
	}
}

// A page also closes on the item budget: groups of capped size stop stacking
// at about 200 items, and the cursor resumes at the excluded title rather
// than after it, so nothing is skipped.
func TestCutoffUnmetPageClosesOnTheItemBudget(t *testing.T) {
	st := coretest.NewStore(t)
	profileID := upgradingProfile(t, st, "Upgrading", 2300)
	// Five title of 50 sub-cutoff holds each: the budget admits four (200).
	titles := []string{"Bulk A", "Bulk B", "Bulk C", "Bulk D", "Bulk E"}
	for _, title := range titles {
		id := seedTitle(t, st, title, 50)
		putOnProfile(t, st, id, profileID)
		for n := 1; n <= 50; n++ {
			hold(t, st, id, n, "[MidSubs] "+title+" - "+strconv.Itoa(n)+" [720p]")
		}
	}

	svc := cutoffService(t, st)
	ctx := context.Background()
	page, err := svc.CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 20})
	if err != nil {
		t.Fatalf("CutoffUnmet: %v", err)
	}
	if len(page.Groups) != 4 {
		t.Fatalf("page 1 groups = %d, want the budget to close at 4", len(page.Groups))
	}
	if page.NextCursor == (acquire.QueueCursor{}) {
		t.Fatal("want a cursor: one group remains")
	}

	rest, err := svc.CutoffUnmet(ctx, acquire.CutoffUnmetParams{Limit: 20, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("CutoffUnmet page 2: %v", err)
	}
	if len(rest.Groups) != 1 || rest.Groups[0].TitleName != "Bulk E" {
		t.Fatalf("page 2 groups = %+v, want exactly the excluded Bulk E", rest.Groups)
	}
	if rest.NextCursor != (acquire.QueueCursor{}) {
		t.Errorf("next_cursor = %+v, want none on the last page", rest.NextCursor)
	}
}

// A group past the cap still reports its full size: Below is the truth, Items
// is the front of the run.
func TestCutoffUnmetCapsItemsPerGroupButNotTheCount(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedTitle(t, st, "Very Held Saga", 60)
	putOnProfile(t, st, titleID, upgradingProfile(t, st, "Upgrading", 2300))
	for n := 1; n <= 60; n++ {
		hold(t, st, titleID, n, "[MidSubs] Very Held Saga - "+strconv.Itoa(n)+" [720p]")
	}

	page, err := cutoffService(t, st).CutoffUnmet(context.Background(), acquire.CutoffUnmetParams{Limit: 5})
	if err != nil {
		t.Fatalf("CutoffUnmet: %v", err)
	}
	if len(page.Groups) != 1 {
		t.Fatalf("groups = %+v, want one", page.Groups)
	}
	g := page.Groups[0]
	if g.Below != 60 || len(g.Items) != 50 {
		t.Fatalf("below = %d with %d items, want the count at 60 and the listing capped at 50", g.Below, len(g.Items))
	}
	if g.Items[0].Number != 1 || g.Items[49].Number != 50 {
		t.Errorf("cap kept %d..%d, want the front of the run", g.Items[0].Number, g.Items[49].Number)
	}
}
