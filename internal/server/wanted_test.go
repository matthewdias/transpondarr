package server_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

type lastPass struct {
	ReleaseTitle string `json:"release_title"`
	Source       string `json:"source"`
	At           string `json:"at"`
	HeldUntil    string `json:"held_until"`
}

type missingItem struct {
	ID           int64     `json:"id"`
	Number       int       `json:"number"`
	Monitored    bool      `json:"monitored"`
	AirsAt       string    `json:"airs_at"`
	Reason       string    `json:"reason"`
	ReasonDetail string    `json:"reason_detail"`
	LastPass     *lastPass `json:"last_pass"`
}

type missingGroup struct {
	TitleID         int64         `json:"title_id"`
	Title           string        `json:"title"`
	Format          string        `json:"format"`
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

// items flattens the title groups for tests that only care which items are present.
func (r missingResponse) items() []missingItem {
	var out []missingItem
	for _, g := range r.Groups {
		out = append(out, g.Items...)
	}
	return out
}

type cutoffResponse struct {
	Groups []struct {
		TitleID     int64  `json:"title_id"`
		Title       string `json:"title"`
		Format      string `json:"format"`
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
	TitlesQueued int    `json:"titles_queued"`
	Automation   string `json:"automation"`
	RunTriggered bool   `json:"run_triggered"`
}

// wantedHarness is a server with an indexer configured and automation on, so a
// reason reflects the row under test rather than a global blocker.
func wantedHarness(t *testing.T) *harness {
	t.Helper()
	return wantedHarnessWithDownload(t, nil)
}

// wantedHarnessWithDownload is wantedHarness with a download client, for a test
// that settles a grab by running the import scan instead of writing the status.
func wantedHarnessWithDownload(t *testing.T, dl *coretest.FakeDownload) *harness {
	t.Helper()
	h := newHarness(t, &coretest.FakeIndexer{}, dl)
	if err := h.settings.UpdateAutomation(t.Context(), settings.AutomationConfig{
		Mode: settings.AutomationOn,
	}); err != nil {
		t.Fatalf("enable automation: %v", err)
	}
	return h
}

func searchedAt(t *testing.T, st *store.Store, titleID int64, last, next string) {
	t.Helper()
	if _, err := st.DB.ExecContext(t.Context(),
		`UPDATE series SET last_searched_at = ?, next_search_at = ? WHERE id = ?`,
		nullable(last), nullable(next), titleID); err != nil {
		t.Fatalf("set search cadence: %v", err)
	}
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Missing is the search sweep's own predicate lifted library-wide: a held item and one
// with a live grab are both absent, a failed grab puts its item back in.
func TestMissingListsOnlyWhatIsStillWanted(t *testing.T) {
	h := wantedHarness(t)
	ctx := t.Context()
	titleID := seedTitle(t, h.store, "Placeholder Saga", 4)
	// 1 is held, 2 is downloading, 3 failed and is wanted again, 4 unchanged.
	if err := h.store.Q.SetWantedItemHeld(ctx, db.SetWantedItemHeldParams{
		InLibrary: 1, HeldReleaseTitle: "[ExampleSubs] Placeholder Saga - 01 [1080p]", ID: itemID(t, h.store, titleID, 1),
	}); err != nil {
		t.Fatalf("hold item 1: %v", err)
	}
	grabItem(t, h.store, titleID, 2, "grabbed", "")
	grabItem(t, h.store, titleID, 3, "failed", "torrent vanished from the client")

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if len(out.Groups) != 1 {
		t.Fatalf("groups = %+v, want one for the title", out.Groups)
	}
	g := out.Groups[0]
	if g.TitleID != titleID || g.Title != "Placeholder Saga" || !g.Monitored || g.Missing != 2 {
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
		t.Errorf("episode 4 reason = %q, want none: the group carries the title's story", got[4].Reason)
	}
	if out.GlobalReason != "" {
		t.Errorf("global_reason = %q, want none: automation is on and an indexer is set", out.GlobalReason)
	}
}

// A settled grab's last_error is cleared by the same statement that settles it
// (#273), so the Missing screen's detail has to come from the history row
// settle() wrote at the same moment. This drives the real failure -- the client
// reporting an error, through the scan -- rather than writing the end state,
// because a fixture that wrote last_error by hand made the old dead read look
// alive.
func TestMissingGrabFailedDetailSurvivesSettling(t *testing.T) {
	dl := &coretest.FakeDownload{}
	h := wantedHarnessWithDownload(t, dl)
	ctx := t.Context()
	titleID := seedTitle(t, h.store, "Placeholder Saga", 4)
	if _, err := h.store.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: itemID(t, h.store, titleID, 3),
		InfoHash:     "hashF",
		ReleaseTitle: "[ExampleSubs] Placeholder Saga - 03 [1080p]",
		Status:       "grabbed",
	}); err != nil {
		t.Fatalf("record grab: %v", err)
	}
	dl.Statuses = []download.Status{{Hash: "hashF", State: download.StateError}}
	if err := h.importer.ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	grabs, err := h.store.Q.ListGrabsByTitle(ctx, titleID)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].Status != "failed" {
		t.Fatalf("grabs = %+v, want the one grab settled as failed", grabs)
	}
	// Without this the test could pass on the column the screen used to read.
	if grabs[0].LastError.Valid {
		t.Fatalf("last_error = %q, want NULL: settling clears it", grabs[0].LastError.String)
	}

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	var failed missingItem
	for _, g := range out.Groups {
		for _, it := range g.Items {
			if it.Number == 3 {
				failed = it
			}
		}
	}
	if failed.Reason != "grab_failed" {
		t.Fatalf("episode 3 = %+v, want grab_failed", failed)
	}
	if failed.ReasonDetail != "the download client reported an error" {
		t.Errorf("episode 3 detail = %q, want the reason settle() recorded", failed.ReasonDetail)
	}
}

// An item can fail, revert to wanted and fail again, and the reason shown has to
// be the current grab's. An earlier attempt's sentence presented as this one's is
// the same confusion SetGrabStatus's clearing prevents (#273). Both failures are
// settled by the scan, so the two history rows are the ones settle() wrote.
func TestMissingGrabFailedDetailIsTheCurrentGrabs(t *testing.T) {
	dl := &coretest.FakeDownload{}
	h := wantedHarnessWithDownload(t, dl)
	ctx := t.Context()
	titleID := seedTitle(t, h.store, "Placeholder Saga", 4)
	id := itemID(t, h.store, titleID, 3)

	failGrab := func(hash, release string, state download.State) {
		t.Helper()
		if _, err := h.store.Q.UpsertGrab(ctx, db.UpsertGrabParams{
			WantedItemID: id, InfoHash: hash, ReleaseTitle: release, Status: "grabbed",
		}); err != nil {
			t.Fatalf("record grab %s: %v", hash, err)
		}
		dl.Statuses = []download.Status{{Hash: hash, State: state}}
		if err := h.importer.ScanOnce(ctx); err != nil {
			t.Fatalf("scan %s: %v", hash, err)
		}
	}
	failGrab("hashOld", "[ExampleSubs] Placeholder Saga - 03 [1080p]", download.StateError)
	// Both timestamps are SQLite's datetime('now'), which resolves to the second,
	// so the re-grab has to land in a later one or nothing can tell the attempts
	// apart. A real install takes minutes to get here.
	time.Sleep(1100 * time.Millisecond)
	failGrab("hashNew", "[OtherSubs] Placeholder Saga - 03 [720p]", download.StateDataMissing)

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	var failed missingItem
	for _, g := range out.Groups {
		for _, it := range g.Items {
			if it.Number == 3 {
				failed = it
			}
		}
	}
	if failed.Reason != "grab_failed" {
		t.Fatalf("episode 3 = %+v, want grab_failed", failed)
	}
	if failed.ReasonDetail != "the download client no longer has the data" {
		t.Errorf("episode 3 detail = %q, want the second grab's reason, not the first's", failed.ReasonDetail)
	}
}

// The Calendar is responsible for the forward-looking view, so an unaired item
// is withheld until asked for; an item with no schedule is not unaired
// and always shows, matching how the search sweep reads a null air date.
func TestMissingUnairedToggle(t *testing.T) {
	h := wantedHarness(t)
	titleID := seedTitle(t, h.store, "Airing Show", 3)
	setAirsAt(t, h.store, titleID, 1, store.FormatTimestamp(time.Now().Add(-48*time.Hour)))
	setAirsAt(t, h.store, titleID, 2, store.FormatTimestamp(time.Now().Add(48*time.Hour)))
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

// An episode's air date is when it becomes acquirable, so withholding it until
// then is complete information. A film's is only the earliest it could be: the
// date AniList publishes is the theatrical premiere, months ahead of anything
// grabbable. Hiding it there would delete the whole title from the Wanted page a user
// tracks it on, where hiding one episode still leaves its title listed.
func TestMissingKeepsAnAnnouncedFilmVisible(t *testing.T) {
	h := wantedHarness(t)
	movieID := seedMovie(t, h.store, "Announced Film", 2027)
	setAirsAt(t, h.store, movieID, 1, store.FormatTimestamp(time.Now().Add(90*24*time.Hour)))

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if len(out.items()) != 1 {
		t.Fatalf("items = %+v, want the announced film without asking for unaired", out.items())
	}
	if len(out.Groups) != 1 || out.Groups[0].Missing != 1 {
		t.Fatalf("groups = %+v, want the film counted in its group", out.Groups)
	}
}

func recordPassOutcome(t *testing.T, st *store.Store, titleID int64, number int, p db.UpsertPassOutcomeParams) {
	t.Helper()
	p.WantedItemID = itemID(t, st, titleID, number)
	if err := st.Q.UpsertPassOutcome(t.Context(), p); err != nil {
		t.Fatalf("record pass outcome for item %d: %v", number, err)
	}
}

// #181's pass reason: what the last pass decided appears on the row, dated, with the
// release it acted on -- and only when that reason won, since an
// "as of" stamped on a freshly derived answer would misrepresent it.
func TestMissingSurfacesTheLastPassOutcome(t *testing.T) {
	h := wantedHarness(t)
	now := time.Now()
	titleID := seedTitle(t, h.store, "Placeholder Saga", 4)
	for n := 1; n <= 4; n++ {
		setAirsAt(t, h.store, titleID, n, store.FormatTimestamp(now.Add(-48*time.Hour)))
	}
	setAirsAt(t, h.store, titleID, 2, store.FormatTimestamp(now.Add(48*time.Hour)))

	recordPassOutcome(t, h.store, titleID, 1, db.UpsertPassOutcomeParams{
		Outcome: "declined", Source: "sweep",
		ReleaseTitle: "[SynthSubs] Placeholder Saga - 01 [720p]",
		Detail:       "below the profile minimum",
		RecordedAt:   store.FormatTimestamp(now.Add(-2 * time.Hour)),
	})
	recordPassOutcome(t, h.store, titleID, 2, db.UpsertPassOutcomeParams{
		Outcome: "no_match", Source: "sweep",
		RecordedAt: store.FormatTimestamp(now.Add(-2 * time.Hour)),
	})
	recordPassOutcome(t, h.store, titleID, 3, db.UpsertPassOutcomeParams{
		Outcome: "pin_held", Source: "feed",
		ReleaseTitle: "[OtherSubs] Placeholder Saga - 03 [1080p]",
		Detail:       `waiting for the pinned group "PinnedSubs"`,
		HeldUntil:    sql.NullString{String: store.FormatTimestamp(now.Add(4 * time.Hour)), Valid: true},
		RecordedAt:   store.FormatTimestamp(now.Add(-30 * time.Minute)),
	})
	// The refusal predates the grab that has since failed, so the grab wins.
	recordPassOutcome(t, h.store, titleID, 4, db.UpsertPassOutcomeParams{
		Outcome: "declined", Source: "sweep",
		ReleaseTitle: "[SynthSubs] Placeholder Saga - 04 [720p]",
		Detail:       "below the profile minimum",
		RecordedAt:   store.FormatTimestamp(now.Add(-6 * time.Hour)),
	})
	grabItem(t, h.store, titleID, 4, "failed", "torrent vanished from the client")

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing?unaired=true", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	got := map[int]missingItem{}
	for _, it := range out.items() {
		got[it.Number] = it
	}
	if len(got) != 4 {
		t.Fatalf("items = %+v, want all four", out.items())
	}

	declined := got[1]
	if declined.Reason != "declined" || declined.ReasonDetail != "below the profile minimum" {
		t.Errorf("episode 1 = %+v, want declined with the refusal reason", declined)
	}
	if declined.LastPass == nil {
		t.Fatal("episode 1 carries no last_pass; a stored answer has to be dated")
	}
	if declined.LastPass.ReleaseTitle != "[SynthSubs] Placeholder Saga - 01 [720p]" ||
		declined.LastPass.Source != "sweep" || declined.LastPass.At == "" {
		t.Errorf("episode 1 last_pass = %+v", declined.LastPass)
	}
	if declined.LastPass.HeldUntil != "" {
		t.Errorf("episode 1 carries held_until %q on a decline", declined.LastPass.HeldUntil)
	}

	// The pass reason never outranks a fact computed fresh, and an unaired row
	// must not be dated as though it were the pass's answer.
	if unaired := got[2]; unaired.Reason != "unaired" || unaired.LastPass != nil {
		t.Errorf("episode 2 = %+v, want unaired with no last_pass", unaired)
	}

	held := got[3]
	if held.Reason != "pin_held" || held.LastPass == nil {
		t.Fatalf("episode 3 = %+v, want pin_held with its window", held)
	}
	if held.LastPass.HeldUntil == "" || held.LastPass.Source != "feed" {
		t.Errorf("episode 3 last_pass = %+v, want the hold window and the feed source", held.LastPass)
	}

	stale := got[4]
	if stale.Reason != "grab_failed" || stale.LastPass != nil {
		t.Errorf("episode 4 = %+v, want the failure: the refusal predates the grab", stale)
	}
	if stale.ReasonDetail != "torrent vanished from the client" {
		t.Errorf("episode 4 detail = %q, want the grab's error", stale.ReasonDetail)
	}
}

func TestMissingUnmonitoredToggle(t *testing.T) {
	h := wantedHarness(t)
	titleID := seedTitle(t, h.store, "Quiet Show", 1)
	if _, err := h.store.DB.ExecContext(t.Context(),
		`UPDATE series SET monitored = 0 WHERE id = ?`, titleID); err != nil {
		t.Fatalf("unmonitor: %v", err)
	}

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if len(out.Groups) != 0 {
		t.Fatalf("groups = %+v, want none: the title is unmonitored", out.Groups)
	}
	if code := h.get(t, "/api/v1/wanted/missing?unmonitored=true", &out); code != http.StatusOK {
		t.Fatalf("GET missing?unmonitored = %d, want 200", code)
	}
	if len(out.Groups) != 1 || out.Groups[0].Reason != "unmonitored" {
		t.Fatalf("groups = %+v, want the one group reading unmonitored", out.Groups)
	}
}

// The reason is re-derived from stored database state on every request, so the search sweep's
// cadence columns and the blocklist show through without a write anywhere.
func TestMissingReasonReadsStoredState(t *testing.T) {
	h := wantedHarness(t)
	ctx := t.Context()
	never := seedTitle(t, h.store, "Never Searched", 1)
	backoff := seedTitle(t, h.store, "Backing Off", 1)
	searchedAt(t, h.store, backoff, store.FormatTimestamp(time.Now().Add(-2*time.Hour)),
		store.FormatTimestamp(time.Now().Add(4*time.Hour)))
	due := seedTitle(t, h.store, "Due Now", 1)
	searchedAt(t, h.store, due, store.FormatTimestamp(time.Now().Add(-2*time.Hour)), "")
	blocked := seedTitle(t, h.store, "Blocklisted", 1)
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
	byTitle := map[int64]missingGroup{}
	for _, g := range out.Groups {
		byTitle[g.TitleID] = g
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
		if byTitle[tc.id].Reason != tc.want {
			t.Errorf("title %d reason = %q, want %q", tc.id, byTitle[tc.id].Reason, tc.want)
		}
	}
	if byTitle[blocked].BlockedReleases != 1 {
		t.Errorf("blocked_releases = %d, want 1", byTitle[blocked].BlockedReleases)
	}
	if byTitle[backoff].NextSearchAt == "" {
		t.Error("want next_search_at on a backed-off group")
	}
}

// Title groups order by their newest missing broadcast, an all-undated title last;
// inside a group episodes enumerate forwards regardless of their dates, since
// that is how a run reads and how a back catalogue drains.
func TestMissingOrdersRecentGroupsFirstAndEpisodesForwards(t *testing.T) {
	h := wantedHarness(t)
	older := seedTitle(t, h.store, "Older Gap", 1)
	setAirsAt(t, h.store, older, 1, store.FormatTimestamp(time.Now().Add(-72*time.Hour)))
	undated := seedTitle(t, h.store, "Back Catalogue", 3)
	current := seedTitle(t, h.store, "Long Runner", 4)
	setAirsAt(t, h.store, current, 3, store.FormatTimestamp(time.Now().Add(-24*time.Hour)))
	setAirsAt(t, h.store, current, 4, store.FormatTimestamp(time.Now().Add(-2*time.Hour)))
	// Long Runner's episodes 1 and 2 have no air date

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if len(out.Groups) != 3 {
		t.Fatalf("groups = %+v, want three titles", out.Groups)
	}
	if out.Groups[0].TitleID != current || out.Groups[1].TitleID != older || out.Groups[2].TitleID != undated {
		t.Fatalf("group order = %v %v %v, want newest broadcast first and the undated title last",
			out.Groups[0].Title, out.Groups[1].Title, out.Groups[2].Title)
	}
	var numbers []int
	for _, it := range out.Groups[0].Items {
		numbers = append(numbers, it.Number)
	}
	if len(numbers) != 4 || numbers[0] != 1 || numbers[1] != 2 || numbers[2] != 3 || numbers[3] != 4 {
		t.Fatalf("episode order = %v, want 1 2 3 4: a group enumerates forwards", numbers)
	}
}

// A title group past the cap still reports its full size: the header count is the
// back-catalog progress display, the listed rows are only the front of the run.
func TestMissingCapsItemsPerGroupButNotTheCount(t *testing.T) {
	h := wantedHarness(t)
	seedTitle(t, h.store, "Very Long Runner", 60)

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

// A results page's weight is rows, not title groups: it closes early once its groups would
// list about 200 items, so a run of capped back-catalog groups cannot stack
// into one giant paint. The cursor resumes without a skip or an overlap.
func TestMissingPageClosesOnTheItemBudget(t *testing.T) {
	h := wantedHarness(t)
	// Six title of 50 missing items each: the budget admits four (200 shown),
	// well under the 50-group limit.
	for _, title := range []string{"Bulk A", "Bulk B", "Bulk C", "Bulk D", "Bulk E", "Bulk F"} {
		seedTitle(t, h.store, title, 50)
	}

	seen := map[int64]bool{}
	cursor, pages := "", 0
	for {
		var out missingResponse
		path := "/api/v1/wanted/missing"
		if cursor != "" {
			path += "?cursor=" + cursor
		}
		if code := h.get(t, path, &out); code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, code)
		}
		shown := 0
		for _, g := range out.Groups {
			if seen[g.TitleID] {
				t.Fatalf("title %d returned on two pages", g.TitleID)
			}
			seen[g.TitleID] = true
			shown += len(g.Items)
		}
		if shown > 200 {
			t.Fatalf("page lists %d items, want the budget to hold it to 200", shown)
		}
		pages++
		if out.NextCursor == "" {
			break
		}
		if pages > 4 {
			t.Fatal("pagination did not terminate")
		}
		cursor = out.NextCursor
	}
	if len(seen) != 6 || pages != 2 {
		t.Fatalf("saw %d groups across %d pages, want all 6 across 2", len(seen), pages)
	}
}

// The Wanted page-level reason: what stops any search running is reported once, not
// stamped on every row.
func TestMissingReportsTheGlobalReason(t *testing.T) {
	h := wantedHarness(t) // automation on, indexer set
	seedTitle(t, h.store, "Quiet Library", 1)

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if out.GlobalReason != "" {
		t.Errorf("global_reason = %q, want none", out.GlobalReason)
	}

	if err := h.settings.UpdateAutomation(t.Context(), settings.AutomationConfig{
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

	bare := newHarness(t, nil, nil) // no indexer
	seedTitle(t, bare.store, "Unsearchable", 1)
	if code := bare.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if out.GlobalReason != "no_indexer" {
		t.Errorf("global_reason = %q, want no_indexer to outrank automation state", out.GlobalReason)
	}
}

// The pagination unit is the title group, so a title never splits across a results page
// boundary: every group appears exactly once, whole, and the last page has
// no cursor.
func TestMissingPaginatesByGroup(t *testing.T) {
	h := wantedHarness(t)
	for i, title := range []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon"} {
		id := seedTitle(t, h.store, title, 2)
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
			if seen[g.TitleID] {
				t.Fatalf("title %d returned on two pages", g.TitleID)
			}
			seen[g.TitleID] = true
			if len(g.Items) != 2 {
				t.Fatalf("group %s arrived split: %d items, want its whole 2", g.Title, len(g.Items))
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

// Cutoff Unmet is queried from stored database state: the held release is re-scored under
// the title's current profile, so the row reports the numbers behind the claim.
func TestCutoffUnmetRoute(t *testing.T) {
	h := wantedHarness(t)
	ctx := t.Context()
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
	titleID := seedTitle(t, h.store, "Placeholder Saga", 2)
	if _, err := h.store.Q.SetTitleProfile(ctx, db.SetTitleProfileParams{
		QualityProfileID: profile.ID, ID: titleID, ID_2: profile.ID,
	}); err != nil {
		t.Fatalf("set title profile: %v", err)
	}
	holdItem(t, h.store, titleID, 1, "[MidSubs] Placeholder Saga - 01 [720p]")  // below 2300
	holdItem(t, h.store, titleID, 2, "[TopSubs] Placeholder Saga - 02 [1080p]") // above

	var out cutoffResponse
	if code := h.get(t, "/api/v1/wanted/cutoff-unmet", &out); code != http.StatusOK {
		t.Fatalf("GET cutoff-unmet = %d, want 200", code)
	}
	if len(out.Groups) != 1 {
		t.Fatalf("groups = %+v, want one for the title", out.Groups)
	}
	g := out.Groups[0]
	if g.TitleID != titleID || g.ProfileName != "Upgrading" || g.CutoffScore != 2300 || g.Below != 1 {
		t.Errorf("group = %+v, want the profile and cutoff hoisted to the header", g)
	}
	// Format is on the title group so the Wanted page can word a film's row without labelling
	// it an episode (#215); it comes through acquire.CutoffGroup, not the
	// row's own struct, which is the join this asserts.
	if g.Format != "TV" {
		t.Errorf("group format = %q, want TV", g.Format)
	}
	if len(g.Items) != 1 {
		t.Fatalf("items = %+v, want only the sub-cutoff item", g.Items)
	}
	got := g.Items[0]
	if got.Number != 1 || got.Status != "in_library" {
		t.Errorf("item = %+v, want episode 1, held", got)
	}
	if got.Score >= g.CutoffScore {
		t.Errorf("score %d vs cutoff %d, want a score below the cutoff", got.Score, g.CutoffScore)
	}
	if got.HeldRelease == "" {
		t.Error("want the held release title carried through")
	}
	// The held [MidSubs] 720p under TopSubs>MidSubs at 1080p>720p leaves the top
	// profile group and top resolution unawarded, 100 points each.
	goals := map[string]int{}
	for _, g := range got.UnmetGoals {
		goals[g.Label] = g.Points
	}
	if len(goals) != 2 || goals["group TopSubs"] != 100 || goals["resolution 1080p"] != 100 {
		t.Errorf("unmet_goals = %v, want the group and resolution gaps at 100 each", goals)
	}
}

// Search is expressed as a cadence reset plus a triggered run, never as N
// synchronous indexer requests: titlesPerPass is the budget that bounds it.
func TestQueueSearchResetsCadenceAndTriggersTheSweep(t *testing.T) {
	h := wantedHarness(t)
	ctx := t.Context()
	// The daemon registers this job; the harness runner is empty, so the route's
	// trigger finds no job to run until the test registers one.
	h.jobs.Add(jobs.Job{Name: "wanted-search", Interval: time.Hour,
		Run: func(context.Context) error { return nil }})
	one := seedTitle(t, h.store, "One", 1)
	two := seedTitle(t, h.store, "Two", 1)
	future := store.FormatTimestamp(time.Now().Add(6 * time.Hour))
	searchedAt(t, h.store, one, store.FormatTimestamp(time.Now()), future)
	searchedAt(t, h.store, two, store.FormatTimestamp(time.Now()), future)

	body := struct {
		TitleIDs []int64 `json:"title_ids"`
	}{TitleIDs: []int64{one}}
	var out queueSearchResponse
	if code := h.postJSON(t, "/api/v1/wanted/search", body, &out); code != http.StatusAccepted {
		t.Fatalf("POST wanted/search = %d, want 202", code)
	}
	if out.TitlesQueued != 1 || out.Automation != "on" || !out.RunTriggered {
		t.Fatalf("response = %+v, want 1 title queued, automation on, run triggered", out)
	}
	if got := nextSearchAt(t, h.store, one); got != "" {
		t.Errorf("title one next_search_at = %q, want cleared", got)
	}
	if got := nextSearchAt(t, h.store, two); got == "" {
		t.Error("title two was not selected and must keep its backoff")
	}
	if len(h.idx.Queries) != 0 {
		t.Errorf("the endpoint issued %d indexer searches; it must only queue", len(h.idx.Queries))
	}

	// An explicit empty array means the whole library, which is what "Search
	// all" sends. Omitting the field is rejected instead, so a
	// mis-serialized request cannot discard every title's backoff by accident.
	if code := h.postJSON(t, "/api/v1/wanted/search", struct{}{}, &out); code != http.StatusUnprocessableEntity {
		t.Fatalf("POST wanted/search with no series_ids = %d, want 422", code)
	}
	if code := h.postJSON(t, "/api/v1/wanted/search", struct {
		TitleIDs []int64 `json:"title_ids"`
	}{TitleIDs: []int64{}}, &out); code != http.StatusAccepted {
		t.Fatalf("POST wanted/search (all) = %d, want 202", code)
	}
	if out.TitlesQueued != -1 {
		t.Errorf("titles_queued = %d, want -1 for a library-wide reset", out.TitlesQueued)
	}
	if got := nextSearchAt(t, h.store, two); got != "" {
		t.Errorf("title two next_search_at = %q, want cleared by the library-wide reset", got)
	}
	if _, err := h.store.Q.GetTitle(ctx, one); err != nil {
		t.Fatalf("title one vanished: %v", err)
	}

	// An unknown id in the selection rejects the whole request, and the reset is
	// one transaction, so a partial selection is never left half-queued.
	searchedAt(t, h.store, one, store.FormatTimestamp(time.Now()), future)
	var missing queueSearchResponse
	if code := h.postJSON(t, "/api/v1/wanted/search", struct {
		TitleIDs []int64 `json:"title_ids"`
	}{TitleIDs: []int64{one, 9999}}, &missing); code != http.StatusNotFound {
		t.Errorf("POST wanted/search with an unknown title = %d, want 404", code)
	}
	if got := nextSearchAt(t, h.store, one); got == "" {
		t.Error("the known title in a rejected selection must keep its backoff")
	}
}

// Notify-only is reported rather than hidden: the run happens and rehearses, so
// the response states that nothing will be sent to the download client.
func TestQueueSearchReportsNotifyOnly(t *testing.T) {
	h := wantedHarness(t)
	if err := h.settings.UpdateAutomation(t.Context(), settings.AutomationConfig{
		Mode: settings.AutomationNotifyOnly,
	}); err != nil {
		t.Fatalf("set notify-only: %v", err)
	}
	seedTitle(t, h.store, "Rehearsed", 1)

	var out queueSearchResponse
	if code := h.postJSON(t, "/api/v1/wanted/search", struct {
		TitleIDs []int64 `json:"title_ids"`
	}{TitleIDs: []int64{}}, &out); code != http.StatusAccepted {
		t.Fatalf("POST wanted/search = %d, want 202", code)
	}
	if out.Automation != "notify_only" {
		t.Errorf("automation = %q, want notify_only", out.Automation)
	}
}

func nextSearchAt(t *testing.T, st *store.Store, titleID int64) string {
	t.Helper()
	var next sql.NullString
	if err := st.DB.QueryRowContext(t.Context(),
		`SELECT next_search_at FROM series WHERE id = ?`, titleID).Scan(&next); err != nil {
		t.Fatalf("read next_search_at: %v", err)
	}
	return next.String
}

func holdItem(t *testing.T, st *store.Store, titleID int64, number int, releaseTitle string) {
	t.Helper()
	ctx := t.Context()
	id := itemID(t, st, titleID, number)
	if err := st.Q.SetWantedItemHeld(ctx, db.SetWantedItemHeldParams{
		InLibrary: 1, HeldReleaseTitle: releaseTitle, ID: id,
	}); err != nil {
		t.Fatalf("hold item %d: %v", number, err)
	}
	if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: id, InfoHash: releaseTitle, ReleaseTitle: releaseTitle, Status: "imported",
	}); err != nil {
		t.Fatalf("record grab for item %d: %v", number, err)
	}
}

// grabItem records a grab already in the state under test, with reason as why
// the attempt has gone wrong. Where the reason is stored follows the status,
// because the two are stored in different places: a grabbed row keeps its import
// error in last_error, and a settled one cannot, since the statement that settles
// it clears the column. So a settled reason goes where settle() puts it (#273).
func grabItem(t *testing.T, st *store.Store, titleID int64, number int, status, reason string) {
	t.Helper()
	ctx := t.Context()
	id := itemID(t, st, titleID, number)
	g, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: id, InfoHash: "hash", ReleaseTitle: "[ExampleSubs] release", Status: status,
	})
	if err != nil {
		t.Fatalf("record grab for item %d: %v", number, err)
	}
	switch {
	case reason == "":
		return
	case status == "grabbed":
		if err := st.Q.SetGrabLastError(ctx, db.SetGrabLastErrorParams{
			LastError: sql.NullString{String: reason, Valid: true}, ID: g.ID,
		}); err != nil {
			t.Fatalf("set last_error for item %d: %v", number, err)
		}
	default:
		if err := st.Q.AppendGrabEvent(ctx, db.AppendGrabEventParams{
			SeriesID: titleID, WantedItemID: id, ItemNumber: int64(number), ItemKind: "episode",
			InfoHash: "hash", ReleaseTitle: "[ExampleSubs] release", Event: status, Detail: reason,
		}); err != nil {
			t.Fatalf("append %s event for item %d: %v", status, number, err)
		}
	}
}
