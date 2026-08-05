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

type missingResponse struct {
	Items []struct {
		ID              int64  `json:"id"`
		SeriesID        int64  `json:"series_id"`
		SeriesTitle     string `json:"series_title"`
		Monitored       bool   `json:"monitored"`
		Number          int    `json:"number"`
		AirsAt          string `json:"airs_at"`
		Reason          string `json:"reason"`
		ReasonDetail    string `json:"reason_detail"`
		BlockedReleases int    `json:"blocked_releases"`
		NextSearchAt    string `json:"next_search_at"`
	} `json:"items"`
	NextCursor string `json:"next_cursor"`
}

type cutoffResponse struct {
	Items []struct {
		ID          int64  `json:"id"`
		SeriesID    int64  `json:"series_id"`
		SeriesTitle string `json:"series_title"`
		Number      int    `json:"number"`
		Status      string `json:"status"`
		HeldRelease string `json:"held_release"`
		Score       int    `json:"score"`
		CutoffScore int    `json:"cutoff_score"`
		ProfileName string `json:"profile_name"`
	} `json:"items"`
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
	got := map[int]string{}
	for _, it := range out.Items {
		got[it.Number] = it.Reason
	}
	if len(got) != 2 || got[3] == "" || got[4] == "" {
		t.Fatalf("items = %+v, want only episodes 3 (failed) and 4 (never grabbed)", out.Items)
	}
	if got[3] != "grab_failed" {
		t.Errorf("episode 3 reason = %q, want grab_failed", got[3])
	}
	for _, it := range out.Items {
		if it.Number == 3 && it.ReasonDetail != "torrent vanished from the client" {
			t.Errorf("episode 3 detail = %q, want the grab's last error", it.ReasonDetail)
		}
		if it.SeriesID != seriesID || it.SeriesTitle != "Placeholder Saga" || !it.Monitored {
			t.Errorf("item = %+v, want the series joined through", it)
		}
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
	if len(out.Items) != 2 {
		t.Fatalf("items = %+v, want the aired and the unscheduled one", out.Items)
	}
	for _, it := range out.Items {
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
	if len(out.Items) != 3 {
		t.Fatalf("items = %+v, want all three once unaired is asked for", out.Items)
	}
	for _, it := range out.Items {
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
	if len(out.Items) != 0 {
		t.Fatalf("items = %+v, want none: the series is unmonitored", out.Items)
	}
	if code := h.get(t, "/api/v1/wanted/missing?unmonitored=true", &out); code != http.StatusOK {
		t.Fatalf("GET missing?unmonitored = %d, want 200", code)
	}
	if len(out.Items) != 1 || out.Items[0].Reason != "unmonitored" {
		t.Fatalf("items = %+v, want the one item reading unmonitored", out.Items)
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
	bySeries := map[int64]string{}
	for _, it := range out.Items {
		bySeries[it.SeriesID] = it.Reason
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
		if bySeries[tc.id] != tc.want {
			t.Errorf("series %d reason = %q, want %q", tc.id, bySeries[tc.id], tc.want)
		}
	}
	for _, it := range out.Items {
		if it.SeriesID == blocked && it.BlockedReleases != 1 {
			t.Errorf("blocked_releases = %d, want 1", it.BlockedReleases)
		}
		if it.SeriesID == backoff && it.NextSearchAt == "" {
			t.Error("want next_search_at on a backed-off row")
		}
	}
}

// Recent gaps first, back catalogue after -- and the back catalogue reads
// forwards, since a series with no schedule at all is a run to drain in order.
func TestMissingOrdersRecentFirstThenTheBackCatalogue(t *testing.T) {
	h := wantedHarness(t)
	seriesID := seedSeries(t, h.store, "Long Runner", 4)
	setAirsAt(t, h.store, seriesID, 3, store.FormatTimestamp(time.Now().Add(-24*time.Hour)))
	setAirsAt(t, h.store, seriesID, 4, store.FormatTimestamp(time.Now().Add(-2*time.Hour)))
	// episodes 1 and 2 have no air date

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	got := make([]int, 0, len(out.Items))
	for _, it := range out.Items {
		got = append(got, it.Number)
	}
	want := []int{4, 3, 1, 2}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// A missing set is unbounded, so it pages: every item appears exactly once
// across pages and the last page carries no cursor.
func TestMissingPaginates(t *testing.T) {
	h := wantedHarness(t)
	seriesID := seedSeries(t, h.store, "Long Runner", 5)

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
		for _, it := range out.Items {
			if seen[it.ID] {
				t.Fatalf("item %d returned on two pages", it.ID)
			}
			seen[it.ID] = true
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
	if len(seen) != 5 {
		t.Fatalf("saw %d distinct items across %d pages, want 5", len(seen), pages)
	}
	if seriesID == 0 {
		t.Fatal("unreachable")
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
	if len(out.Items) != 1 {
		t.Fatalf("items = %+v, want only the sub-cutoff item", out.Items)
	}
	got := out.Items[0]
	if got.Number != 1 || got.Status != "have" || got.ProfileName != "Upgrading" {
		t.Errorf("item = %+v, want episode 1, held, on the Upgrading profile", got)
	}
	if got.Score >= got.CutoffScore || got.CutoffScore != 2300 {
		t.Errorf("score %d vs cutoff %d, want a score below a cutoff of 2300", got.Score, got.CutoffScore)
	}
	if got.HeldRelease == "" {
		t.Error("want the held release title carried through")
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
