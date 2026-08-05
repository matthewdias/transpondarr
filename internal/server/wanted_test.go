package server_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

type missingItem struct {
	ID           int64  `json:"id"`
	Number       int    `json:"number"`
	AirsAt       string `json:"airs_at"`
	Reason       string `json:"reason"`
	ReasonDetail string `json:"reason_detail"`
}

type missingGroup struct {
	SeriesID        int64         `json:"series_id"`
	SeriesTitle     string        `json:"series_title"`
	Monitored       bool          `json:"monitored"`
	Reason          string        `json:"reason"`
	BlockedReleases int           `json:"blocked_releases"`
	NextSearchAt    string        `json:"next_search_at"`
	Missing         int           `json:"missing"`
	Items           []missingItem `json:"items"`
}

type missingResponse struct {
	GlobalReason string         `json:"global_reason"`
	Groups       []missingGroup `json:"groups"`
	NextCursor   string         `json:"next_cursor"`
}

// items flattens the groups for tests that only care which items are present.
func (r missingResponse) items() []missingItem {
	var out []missingItem
	for _, g := range r.Groups {
		out = append(out, g.Items...)
	}
	return out
}

type cutoffResponse struct {
	Groups []struct {
		SeriesID    int64  `json:"series_id"`
		SeriesTitle string `json:"series_title"`
		ProfileName string `json:"profile_name"`
		CutoffScore int    `json:"cutoff_score"`
		Below       int    `json:"below"`
		Items       []struct {
			ID          int64  `json:"id"`
			Number      int    `json:"number"`
			Status      string `json:"status"`
			HeldRelease string `json:"held_release"`
			Score       int    `json:"score"`
			UnmetGoals  []struct {
				Label  string `json:"label"`
				Points int    `json:"points"`
			} `json:"unmet_goals"`
		} `json:"items"`
	} `json:"groups"`
	NextCursor string `json:"next_cursor"`
}

type queueSearchResponse struct {
	SeriesQueued int    `json:"series_queued"`
	Automation   string `json:"automation"`
	RunTriggered bool   `json:"run_triggered"`
}

// wantedHarness is a server with an indexer configured and automation on, so a
// reason reflects the row under test rather than a global blocker.
func wantedHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, &coretest.FakeIndexer{}, nil)
	if err := h.settings.UpdateAutomation(context.Background(), settings.AutomationConfig{
		Mode: settings.AutomationOn,
	}); err != nil {
		t.Fatalf("enable automation: %v", err)
	}
	return h
}

func searchedAt(t *testing.T, st *store.Store, seriesID int64, last, next string) {
	t.Helper()
	if _, err := st.DB.ExecContext(context.Background(),
		`UPDATE series SET last_searched_at = ?, next_search_at = ? WHERE id = ?`,
		nullable(last), nullable(next), seriesID); err != nil {
		t.Fatalf("set search cadence: %v", err)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Missing is the sweep's own predicate lifted library-wide: a held item and one
// with a live grab are both absent, a failed grab puts its item back in.
func TestMissingListsOnlyWhatIsStillWanted(t *testing.T) {
	h := wantedHarness(t)
	ctx := context.Background()
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 4)
	// 1 is held, 2 is downloading, 3 failed and is wanted again, 4 untouched.
	if err := h.store.Q.SetWantedItemHeld(ctx, db.SetWantedItemHeldParams{
		Have: 1, HeldReleaseTitle: "[ExampleSubs] Placeholder Saga - 01 [1080p]", ID: itemID(t, h.store, seriesID, 1),
	}); err != nil {
		t.Fatalf("hold item 1: %v", err)
	}
	grabItem(t, h.store, seriesID, 2, "grabbed", "")
	grabItem(t, h.store, seriesID, 3, "failed", "torrent vanished from the client")

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if len(out.Groups) != 1 {
		t.Fatalf("groups = %+v, want one for the series", out.Groups)
	}
	g := out.Groups[0]
	if g.SeriesID != seriesID || g.SeriesTitle != "Placeholder Saga" || !g.Monitored || g.Missing != 2 {
		t.Errorf("group = %+v, want Placeholder Saga with 2 missing", g)
	}
	got := map[int]missingItem{}
	for _, it := range g.Items {
		got[it.Number] = it
	}
	if len(got) != 2 {
		t.Fatalf("items = %+v, want only episodes 3 (failed) and 4 (never grabbed)", g.Items)
	}
	if got[3].Reason != "grab_failed" || got[3].ReasonDetail != "torrent vanished from the client" {
		t.Errorf("episode 3 = %+v, want grab_failed with the grab's last error", got[3])
	}
	if got[4].Reason != "" {
		t.Errorf("episode 4 reason = %q, want none: the group carries the series' story", got[4].Reason)
	}
	if out.GlobalReason != "" {
		t.Errorf("global_reason = %q, want none: automation is on and an indexer is set", out.GlobalReason)
	}
}

// The Calendar owns the forward-looking view, so an unaired item is withheld
// until asked for; an item with no schedule at all is not unaired and always
// shows, matching how the sweep reads a null air date.
func TestMissingUnairedToggle(t *testing.T) {
	h := wantedHarness(t)
	seriesID := seedSeries(t, h.store, "Airing Show", 3)
	setAirsAt(t, h.store, seriesID, 1, store.FormatTimestamp(time.Now().Add(-48*time.Hour)))
	setAirsAt(t, h.store, seriesID, 2, store.FormatTimestamp(time.Now().Add(48*time.Hour)))
	// episode 3 has no air date

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if items := out.items(); len(items) != 2 {
		t.Fatalf("items = %+v, want the aired and the unscheduled one", items)
	}
	if len(out.Groups) == 1 && out.Groups[0].Missing != 2 {
		t.Errorf("missing = %d, want the count to honour the filter too", out.Groups[0].Missing)
	}
	for _, it := range out.items() {
		if it.Number == 2 {
			t.Fatalf("episode 2 airs in the future and must be withheld by default")
		}
		if it.Reason == "unaired" {
			t.Errorf("item %d reason = unaired; a null air date is searchable", it.Number)
		}
	}

	if code := h.get(t, "/api/v1/wanted/missing?unaired=true", &out); code != http.StatusOK {
		t.Fatalf("GET missing?unaired = %d, want 200", code)
	}
	if items := out.items(); len(items) != 3 {
		t.Fatalf("items = %+v, want all three once unaired is asked for", items)
	}
	for _, it := range out.items() {
		if it.Number == 2 && it.Reason != "unaired" {
			t.Errorf("episode 2 reason = %q, want unaired", it.Reason)
		}
	}
}

func TestMissingUnmonitoredToggle(t *testing.T) {
	h := wantedHarness(t)
	seriesID := seedSeries(t, h.store, "Quiet Show", 1)
	if _, err := h.store.DB.ExecContext(context.Background(),
		`UPDATE series SET monitored = 0 WHERE id = ?`, seriesID); err != nil {
		t.Fatalf("unmonitor: %v", err)
	}

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if len(out.Groups) != 0 {
		t.Fatalf("groups = %+v, want none: the series is unmonitored", out.Groups)
	}
	if code := h.get(t, "/api/v1/wanted/missing?unmonitored=true", &out); code != http.StatusOK {
		t.Fatalf("GET missing?unmonitored = %d, want 200", code)
	}
	if len(out.Groups) != 1 || out.Groups[0].Reason != "unmonitored" {
		t.Fatalf("groups = %+v, want the one group reading unmonitored", out.Groups)
	}
}

// The reason is re-derived from stored state on every request, so the sweep's
// cadence columns and the blocklist show through without a write anywhere.
func TestMissingReasonReadsStoredState(t *testing.T) {
	h := wantedHarness(t)
	ctx := context.Background()
	never := seedSeries(t, h.store, "Never Searched", 1)
	backoff := seedSeries(t, h.store, "Backing Off", 1)
	searchedAt(t, h.store, backoff, store.FormatTimestamp(time.Now().Add(-2*time.Hour)),
		store.FormatTimestamp(time.Now().Add(4*time.Hour)))
	due := seedSeries(t, h.store, "Due Now", 1)
	searchedAt(t, h.store, due, store.FormatTimestamp(time.Now().Add(-2*time.Hour)), "")
	blocked := seedSeries(t, h.store, "Blocklisted", 1)
	searchedAt(t, h.store, blocked, store.FormatTimestamp(time.Now().Add(-2*time.Hour)), "")
	if _, err := h.store.Q.UpsertBlocklistEntry(ctx, db.UpsertBlocklistEntryParams{
		SeriesID: blocked, InfoHash: "deadbeef", ReleaseTitle: "[ExampleSubs] Blocklisted - 01 [1080p]",
		NormalizedTitle: "examplesubs blocklisted 01 1080p", Reason: "import failed",
		BlockedUntil: sql.NullString{String: store.FormatTimestamp(time.Now().Add(24 * time.Hour)), Valid: true},
	}); err != nil {
		t.Fatalf("blocklist a release: %v", err)
	}

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	bySeries := map[int64]missingGroup{}
	for _, g := range out.Groups {
		bySeries[g.SeriesID] = g
	}
	for _, tc := range []struct {
		id   int64
		want string
	}{
		{never, "never_searched"},
		{backoff, "search_backoff"},
		{due, "search_due"},
		{blocked, "blocklisted"},
	} {
		if bySeries[tc.id].Reason != tc.want {
			t.Errorf("series %d reason = %q, want %q", tc.id, bySeries[tc.id].Reason, tc.want)
		}
	}
	if bySeries[blocked].BlockedReleases != 1 {
		t.Errorf("blocked_releases = %d, want 1", bySeries[blocked].BlockedReleases)
	}
	if bySeries[backoff].NextSearchAt == "" {
		t.Error("want next_search_at on a backed-off group")
	}
}

// Groups order by their newest missing broadcast, an all-undated series last;
// inside a group episodes enumerate forwards regardless of their dates, since
// that is how a run reads and how a back catalogue drains.
func TestMissingOrdersRecentGroupsFirstAndEpisodesForwards(t *testing.T) {
	h := wantedHarness(t)
	older := seedSeries(t, h.store, "Older Gap", 1)
	setAirsAt(t, h.store, older, 1, store.FormatTimestamp(time.Now().Add(-72*time.Hour)))
	undated := seedSeries(t, h.store, "Back Catalogue", 3)
	current := seedSeries(t, h.store, "Long Runner", 4)
	setAirsAt(t, h.store, current, 3, store.FormatTimestamp(time.Now().Add(-24*time.Hour)))
	setAirsAt(t, h.store, current, 4, store.FormatTimestamp(time.Now().Add(-2*time.Hour)))
	// Long Runner's episodes 1 and 2 have no air date

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if len(out.Groups) != 3 {
		t.Fatalf("groups = %+v, want three series", out.Groups)
	}
	if out.Groups[0].SeriesID != current || out.Groups[1].SeriesID != older || out.Groups[2].SeriesID != undated {
		t.Fatalf("group order = %v %v %v, want newest broadcast first and the undated series last",
			out.Groups[0].SeriesTitle, out.Groups[1].SeriesTitle, out.Groups[2].SeriesTitle)
	}
	var numbers []int
	for _, it := range out.Groups[0].Items {
		numbers = append(numbers, it.Number)
	}
	if len(numbers) != 4 || numbers[0] != 1 || numbers[1] != 2 || numbers[2] != 3 || numbers[3] != 4 {
		t.Fatalf("episode order = %v, want 1 2 3 4: a group enumerates forwards", numbers)
	}
}

// A group past the cap still reports its full size: the header count is the
// back-catalog progress display, the listed rows are just the front of the run.
func TestMissingCapsItemsPerGroupButNotTheCount(t *testing.T) {
	h := wantedHarness(t)
	seedSeries(t, h.store, "Very Long Runner", 60)

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if len(out.Groups) != 1 {
		t.Fatalf("groups = %+v, want one", out.Groups)
	}
	g := out.Groups[0]
	if g.Missing != 60 || len(g.Items) != 50 {
		t.Fatalf("missing = %d with %d items, want the count at 60 and the listing capped at 50", g.Missing, len(g.Items))
	}
	if g.Items[0].Number != 1 || g.Items[49].Number != 50 {
		t.Errorf("cap kept %d..%d, want the front of the run", g.Items[0].Number, g.Items[49].Number)
	}
}

// The page-level tier: what stops any search running at all is said once, not
// stamped on every row.
func TestMissingReportsTheGlobalReason(t *testing.T) {
	h := wantedHarness(t) // automation on, indexer set
	seedSeries(t, h.store, "Quiet Library", 1)

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if out.GlobalReason != "" {
		t.Errorf("global_reason = %q, want none", out.GlobalReason)
	}

	if err := h.settings.UpdateAutomation(context.Background(), settings.AutomationConfig{
		Mode: settings.AutomationNotifyOnly,
	}); err != nil {
		t.Fatalf("set notify-only: %v", err)
	}
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if out.GlobalReason != "notify_only" {
		t.Errorf("global_reason = %q, want notify_only", out.GlobalReason)
	}

	bare := newHarness(t, nil, nil) // no indexer at all
	seedSeries(t, bare.store, "Unsearchable", 1)
	if code := bare.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if out.GlobalReason != "no_indexer" {
		t.Errorf("global_reason = %q, want no_indexer to outrank automation state", out.GlobalReason)
	}
}

// The pagination unit is the group, so a series never splits across a page
// boundary: every group appears exactly once, whole, and the last page carries
// no cursor.
func TestMissingPaginatesByGroup(t *testing.T) {
	h := wantedHarness(t)
	for i, title := range []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon"} {
		id := seedSeries(t, h.store, title, 2)
		// Distinct latest broadcasts keep the group order deterministic.
		setAirsAt(t, h.store, id, 2, store.FormatTimestamp(time.Now().Add(-time.Duration(i+1)*24*time.Hour)))
	}

	seen := map[int64]bool{}
	cursor, pages := "", 0
	for {
		var out missingResponse
		path := "/api/v1/wanted/missing?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		if code := h.get(t, path, &out); code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, code)
		}
		if len(out.Groups) > 2 {
			t.Fatalf("page of %d groups, want at most the limit", len(out.Groups))
		}
		for _, g := range out.Groups {
			if seen[g.SeriesID] {
				t.Fatalf("series %d returned on two pages", g.SeriesID)
			}
			seen[g.SeriesID] = true
			if len(g.Items) != 2 {
				t.Fatalf("group %s arrived split: %d items, want its whole 2", g.SeriesTitle, len(g.Items))
			}
		}
		pages++
		if out.NextCursor == "" {
			break
		}
		if pages > 5 {
			t.Fatal("pagination did not terminate")
		}
		cursor = out.NextCursor
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("saw %d distinct groups across %d pages, want 5 across 3", len(seen), pages)
	}

	var bad missingResponse
	if code := h.get(t, "/api/v1/wanted/missing?cursor=not-a-cursor", &bad); code != http.StatusBadRequest {
		t.Errorf("GET with a junk cursor = %d, want 400", code)
	}
}

// Cutoff Unmet is queried from stored state: the held release is re-scored under
// the series' current profile, so the row carries the numbers behind the claim.
func TestCutoffUnmetRoute(t *testing.T) {
	h := wantedHarness(t)
	ctx := context.Background()
	profile, err := h.store.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "Upgrading", ResolutionOrder: `["1080p","720p"]`, HardExcludes: `[]`,
		UpgradesEnabled: 1, CutoffScore: 2300,
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	for rank, g := range []string{"TopSubs", "MidSubs"} {
		if _, err := h.store.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
			ProfileID: profile.ID, GroupName: g, Rank: int64(rank),
		}); err != nil {
			t.Fatalf("add group: %v", err)
		}
	}
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 2)
	if _, err := h.store.Q.SetSeriesProfile(ctx, db.SetSeriesProfileParams{
		QualityProfileID: profile.ID, ID: seriesID, ID_2: profile.ID,
	}); err != nil {
		t.Fatalf("set series profile: %v", err)
	}
	holdItem(t, h.store, seriesID, 1, "[MidSubs] Placeholder Saga - 01 [720p]")  // below 2300
	holdItem(t, h.store, seriesID, 2, "[TopSubs] Placeholder Saga - 02 [1080p]") // above

	var out cutoffResponse
	if code := h.get(t, "/api/v1/wanted/cutoff-unmet", &out); code != http.StatusOK {
		t.Fatalf("GET cutoff-unmet = %d, want 200", code)
	}
	if len(out.Groups) != 1 {
		t.Fatalf("groups = %+v, want one for the series", out.Groups)
	}
	g := out.Groups[0]
	if g.SeriesID != seriesID || g.ProfileName != "Upgrading" || g.CutoffScore != 2300 || g.Below != 1 {
		t.Errorf("group = %+v, want the profile and cutoff hoisted to the header", g)
	}
	if len(g.Items) != 1 {
		t.Fatalf("items = %+v, want only the sub-cutoff item", g.Items)
	}
	got := g.Items[0]
	if got.Number != 1 || got.Status != "have" {
		t.Errorf("item = %+v, want episode 1, held", got)
	}
	if got.Score >= g.CutoffScore {
		t.Errorf("score %d vs cutoff %d, want a score below the cutoff", got.Score, g.CutoffScore)
	}
	if got.HeldRelease == "" {
		t.Error("want the held release title carried through")
	}
	// The held [MidSubs] 720p under TopSubs>MidSubs at 1080p>720p leaves the top
	// group and top resolution unearned, 100 points each.
	goals := map[string]int{}
	for _, g := range got.UnmetGoals {
		goals[g.Label] = g.Points
	}
	if len(goals) != 2 || goals["group TopSubs"] != 100 || goals["resolution 1080p"] != 100 {
		t.Errorf("unmet_goals = %v, want the group and resolution gaps at 100 each", goals)
	}
}

// Search is expressed as a cadence reset plus a triggered run, never as N
// synchronous indexer requests: seriesPerPass is the budget that bounds it.
func TestQueueSearchResetsCadenceAndTriggersTheSweep(t *testing.T) {
	h := wantedHarness(t)
	ctx := context.Background()
	// The daemon registers this job; the harness runner is empty, so the route's
	// trigger has nothing to reach until the test supplies it.
	h.jobs.Add(jobs.Job{Name: "wanted-search", Interval: time.Hour,
		Run: func(context.Context) error { return nil }})
	one := seedSeries(t, h.store, "One", 1)
	two := seedSeries(t, h.store, "Two", 1)
	future := store.FormatTimestamp(time.Now().Add(6 * time.Hour))
	searchedAt(t, h.store, one, store.FormatTimestamp(time.Now()), future)
	searchedAt(t, h.store, two, store.FormatTimestamp(time.Now()), future)

	body := struct {
		SeriesIDs []int64 `json:"series_ids"`
	}{SeriesIDs: []int64{one}}
	var out queueSearchResponse
	if code := h.postJSON(t, "/api/v1/wanted/search", body, &out); code != http.StatusAccepted {
		t.Fatalf("POST wanted/search = %d, want 202", code)
	}
	if out.SeriesQueued != 1 || out.Automation != "on" || !out.RunTriggered {
		t.Fatalf("response = %+v, want 1 series queued, automation on, run triggered", out)
	}
	if got := nextSearchAt(t, h.store, one); got != "" {
		t.Errorf("series one next_search_at = %q, want cleared", got)
	}
	if got := nextSearchAt(t, h.store, two); got == "" {
		t.Error("series two was not selected and must keep its backoff")
	}
	if len(h.idx.Queries) != 0 {
		t.Errorf("the endpoint issued %d indexer searches; it must only queue", len(h.idx.Queries))
	}

	// No ids means the whole library, which is what "Search all" sends.
	if code := h.postJSON(t, "/api/v1/wanted/search", struct{}{}, &out); code != http.StatusAccepted {
		t.Fatalf("POST wanted/search (all) = %d, want 202", code)
	}
	if out.SeriesQueued != -1 {
		t.Errorf("series_queued = %d, want -1 for a library-wide reset", out.SeriesQueued)
	}
	if got := nextSearchAt(t, h.store, two); got != "" {
		t.Errorf("series two next_search_at = %q, want cleared by the library-wide reset", got)
	}
	if _, err := h.store.Q.GetSeries(ctx, one); err != nil {
		t.Fatalf("series one vanished: %v", err)
	}

	var missing queueSearchResponse
	if code := h.postJSON(t, "/api/v1/wanted/search", struct {
		SeriesIDs []int64 `json:"series_ids"`
	}{SeriesIDs: []int64{9999}}, &missing); code != http.StatusNotFound {
		t.Errorf("POST wanted/search for an unknown series = %d, want 404", code)
	}
}

// Notify-only is reported rather than hidden: the run happens and rehearses, so
// the caller can say nothing will reach the download client.
func TestQueueSearchReportsNotifyOnly(t *testing.T) {
	h := wantedHarness(t)
	if err := h.settings.UpdateAutomation(context.Background(), settings.AutomationConfig{
		Mode: settings.AutomationNotifyOnly,
	}); err != nil {
		t.Fatalf("set notify-only: %v", err)
	}
	seedSeries(t, h.store, "Rehearsed", 1)

	var out queueSearchResponse
	if code := h.postJSON(t, "/api/v1/wanted/search", struct{}{}, &out); code != http.StatusAccepted {
		t.Fatalf("POST wanted/search = %d, want 202", code)
	}
	if out.Automation != "notify_only" {
		t.Errorf("automation = %q, want notify_only", out.Automation)
	}
}

func nextSearchAt(t *testing.T, st *store.Store, seriesID int64) string {
	t.Helper()
	var next sql.NullString
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT next_search_at FROM series WHERE id = ?`, seriesID).Scan(&next); err != nil {
		t.Fatalf("read next_search_at: %v", err)
	}
	return next.String
}

func holdItem(t *testing.T, st *store.Store, seriesID int64, number int, releaseTitle string) {
	t.Helper()
	ctx := context.Background()
	id := itemID(t, st, seriesID, number)
	if err := st.Q.SetWantedItemHeld(ctx, db.SetWantedItemHeldParams{
		Have: 1, HeldReleaseTitle: releaseTitle, ID: id,
	}); err != nil {
		t.Fatalf("hold item %d: %v", number, err)
	}
	if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: id, InfoHash: releaseTitle, ReleaseTitle: releaseTitle, Status: "imported",
	}); err != nil {
		t.Fatalf("record grab for item %d: %v", number, err)
	}
}

func grabItem(t *testing.T, st *store.Store, seriesID int64, number int, status, lastError string) {
	t.Helper()
	ctx := context.Background()
	id := itemID(t, st, seriesID, number)
	g, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: id, InfoHash: "hash", ReleaseTitle: "[ExampleSubs] release", Status: status,
	})
	if err != nil {
		t.Fatalf("record grab for item %d: %v", number, err)
	}
	if lastError == "" {
		return
	}
	if _, err := st.DB.ExecContext(ctx,
		`UPDATE grabs SET last_error = ? WHERE id = ?`, lastError, g.ID); err != nil {
		t.Fatalf("set last_error: %v", err)
	}
}
