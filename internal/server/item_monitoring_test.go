package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

type setItemsMonitoredResponse struct {
	Updated      int `json:"updated"`
	SeriesQueued int `json:"series_queued"`
}

// setMonitoredParams seeds the flag directly, so a test can arrive at the state
// the endpoint under test is meant to leave behind.
func setMonitoredParams(monitored int64, ids []int64) db.SetWantedItemsMonitoredParams {
	return db.SetWantedItemsMonitoredParams{Monitored: monitored, Ids: ids}
}

func declinedOutcome() db.UpsertPassOutcomeParams {
	return db.UpsertPassOutcomeParams{
		Outcome: "declined", Source: "sweep",
		ReleaseTitle: "[SynthSubs] Placeholder Saga - 01 [720p]",
		Detail:       "below the profile floor",
		RecordedAt:   store.FormatTimestamp(time.Now().Add(-2 * time.Hour)),
	}
}

func itemMonitored(t *testing.T, st *store.Store, id int64) int64 {
	t.Helper()
	var got int64
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT monitored FROM wanted_items WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("read monitored for item %d: %v", id, err)
	}
	return got
}

func searchEpoch(t *testing.T, st *store.Store, seriesID int64) int64 {
	t.Helper()
	var got int64
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT search_epoch FROM series WHERE id = ?`, seriesID).Scan(&got); err != nil {
		t.Fatalf("read search_epoch for series %d: %v", seriesID, err)
	}
	return got
}

// One route for one state-setter: a single item is a one-element array, and both
// call sites (the Episodes table and the cross-series Wanted page) need the
// batch form anyway.
func TestSetItemsMonitoredUnmonitorsWithoutTouchingTheSearchQueue(t *testing.T) {
	h := wantedHarness(t)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	first := itemID(t, h.store, seriesID, 1)
	before := searchEpoch(t, h.store, seriesID)

	var out setItemsMonitoredResponse
	code := do(t, h, http.MethodPatch, "/api/v1/wanted/items",
		map[string]any{"item_ids": []int64{first}, "monitored": false}, &out)
	if code != http.StatusOK {
		t.Fatalf("PATCH items = %d, want 200", code)
	}
	if out.Updated != 1 {
		t.Errorf("updated = %d, want 1", out.Updated)
	}
	if itemMonitored(t, h.store, first) != 0 {
		t.Error("item 1 is still monitored")
	}
	if got := searchEpoch(t, h.store, seriesID); got != before {
		t.Errorf("search_epoch = %d, want %d -- unmonitoring queues nothing", got, before)
	}
	if out.SeriesQueued != 0 {
		t.Errorf("series_queued = %d, want 0", out.SeriesQueued)
	}
}

// Re-monitoring resets the sweep cadence once per distinct series: the feed's
// dedupe is one-shot, so only a fresh search finds a release that already exists.
func TestSetItemsMonitoredResetsEachSeriesOnce(t *testing.T) {
	h := wantedHarness(t)
	first := seedSeries(t, h.store, "Placeholder Saga", 3)
	second := seedSeries(t, h.store, "Another Show", 2)
	ids := []int64{
		itemID(t, h.store, first, 1),
		itemID(t, h.store, first, 2),
		itemID(t, h.store, second, 1),
	}
	if _, err := h.store.Q.SetWantedItemsMonitored(context.Background(),
		setMonitoredParams(0, ids)); err != nil {
		t.Fatalf("seed unmonitored items: %v", err)
	}
	firstBefore, secondBefore := searchEpoch(t, h.store, first), searchEpoch(t, h.store, second)

	var out setItemsMonitoredResponse
	code := do(t, h, http.MethodPatch, "/api/v1/wanted/items",
		map[string]any{"item_ids": ids, "monitored": true}, &out)
	if code != http.StatusOK {
		t.Fatalf("PATCH items = %d, want 200", code)
	}
	if out.Updated != 3 || out.SeriesQueued != 2 {
		t.Errorf("response = %+v, want 3 updated across 2 series", out)
	}
	if got := searchEpoch(t, h.store, first); got != firstBefore+1 {
		t.Errorf("series %d epoch = %d, want %d -- two items, one reset", first, got, firstBefore+1)
	}
	if got := searchEpoch(t, h.store, second); got != secondBefore+1 {
		t.Errorf("series %d epoch = %d, want %d", second, got, secondBefore+1)
	}
}

// Re-monitoring something already monitored changes nothing, so it must not
// spend a reset: the series would lose accumulated backoff for free.
func TestSetItemsMonitoredDoesNotResetWhenNothingChanged(t *testing.T) {
	h := wantedHarness(t)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	ids := []int64{itemID(t, h.store, seriesID, 1), itemID(t, h.store, seriesID, 2)}
	before := searchEpoch(t, h.store, seriesID)

	var out setItemsMonitoredResponse
	code := do(t, h, http.MethodPatch, "/api/v1/wanted/items",
		map[string]any{"item_ids": ids, "monitored": true}, &out)
	if code != http.StatusOK {
		t.Fatalf("PATCH items = %d, want 200", code)
	}
	if got := searchEpoch(t, h.store, seriesID); got != before {
		t.Errorf("search_epoch = %d, want %d -- nothing moved, so nothing is queued", got, before)
	}
	if out.SeriesQueued != 0 {
		t.Errorf("series_queued = %d, want 0", out.SeriesQueued)
	}
}

// A selection straddling both states resets once, on the strength of the item
// that actually moved.
func TestSetItemsMonitoredResetsOnTheItemThatMoved(t *testing.T) {
	h := wantedHarness(t)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	stillOn := itemID(t, h.store, seriesID, 1)
	turnedOff := itemID(t, h.store, seriesID, 2)
	if _, err := h.store.Q.SetWantedItemsMonitored(context.Background(),
		setMonitoredParams(0, []int64{turnedOff})); err != nil {
		t.Fatalf("seed an unmonitored item: %v", err)
	}
	before := searchEpoch(t, h.store, seriesID)

	var out setItemsMonitoredResponse
	code := do(t, h, http.MethodPatch, "/api/v1/wanted/items",
		map[string]any{"item_ids": []int64{stillOn, turnedOff}, "monitored": true}, &out)
	if code != http.StatusOK {
		t.Fatalf("PATCH items = %d, want 200", code)
	}
	if got := searchEpoch(t, h.store, seriesID); got != before+1 {
		t.Errorf("search_epoch = %d, want %d -- exactly one reset", got, before+1)
	}
	if out.SeriesQueued != 1 {
		t.Errorf("series_queued = %d, want 1", out.SeriesQueued)
	}
}

// A hand-built selection must survive a series deleted in another tab: for a
// state-setter a missing id is vacuous -- the item is gone, so "stop wanting it"
// is already true -- which is why this diverges from resetSelected's 404.
func TestSetItemsMonitoredSkipsUnknownIDs(t *testing.T) {
	h := wantedHarness(t)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 2)
	known := itemID(t, h.store, seriesID, 1)

	var out setItemsMonitoredResponse
	code := do(t, h, http.MethodPatch, "/api/v1/wanted/items",
		map[string]any{"item_ids": []int64{known, 987654}, "monitored": false}, &out)
	if code != http.StatusOK {
		t.Fatalf("PATCH items = %d, want 200 with the rest applied", code)
	}
	if out.Updated != 1 {
		t.Errorf("updated = %d, want 1", out.Updated)
	}
	if itemMonitored(t, h.store, known) != 0 {
		t.Error("the known item was not unmonitored")
	}
}

// The listing is a display filter, not an exclusion (#188, decisions 5 and 6):
// the row has to stay reachable, or the click that hid it cannot be undone.
func TestMissingHidesUnmonitoredItemsBehindTheToggle(t *testing.T) {
	h := wantedHarness(t)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	if _, err := h.store.Q.SetWantedItemsMonitored(context.Background(),
		setMonitoredParams(0, []int64{itemID(t, h.store, seriesID, 1)})); err != nil {
		t.Fatalf("unmonitor item 1: %v", err)
	}

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing", &out); code != http.StatusOK {
		t.Fatalf("GET missing = %d, want 200", code)
	}
	if len(out.Groups) != 1 {
		t.Fatalf("groups = %+v, want the one group", out.Groups)
	}
	if out.Groups[0].Missing != 2 || len(out.Groups[0].Items) != 2 {
		t.Errorf("group = %+v, want the count and the rows to agree at 2", out.Groups[0])
	}
	for _, it := range out.items() {
		if it.Number == 1 {
			t.Error("episode 1 is unmonitored and must be withheld by default")
		}
	}

	if code := h.get(t, "/api/v1/wanted/missing?unmonitored=true", &out); code != http.StatusOK {
		t.Fatalf("GET missing?unmonitored = %d, want 200", code)
	}
	if out.Groups[0].Missing != 3 || len(out.Groups[0].Items) != 3 {
		t.Errorf("group = %+v, want all three once unmonitored is asked for", out.Groups[0])
	}
	for _, it := range out.items() {
		if it.Number != 1 {
			continue
		}
		if it.Reason != "unmonitored" || it.Monitored {
			t.Errorf("episode 1 = %+v, want reason unmonitored and monitored false", it)
		}
	}
}

// The suppression half of decision 9: a stored refusal on an unmonitored item is
// never revisited, so the row must not keep showing it.
func TestUnmonitoredSuppressesAStoredPassAnswer(t *testing.T) {
	h := wantedHarness(t)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 1)
	recordPassOutcome(t, h.store, seriesID, 1, declinedOutcome())
	if _, err := h.store.Q.SetWantedItemsMonitored(context.Background(),
		setMonitoredParams(0, []int64{itemID(t, h.store, seriesID, 1)})); err != nil {
		t.Fatalf("unmonitor item 1: %v", err)
	}

	var out missingResponse
	if code := h.get(t, "/api/v1/wanted/missing?unmonitored=true", &out); code != http.StatusOK {
		t.Fatalf("GET missing?unmonitored = %d, want 200", code)
	}
	items := out.items()
	if len(items) != 1 {
		t.Fatalf("items = %+v, want the one unmonitored row", items)
	}
	if items[0].Reason != "unmonitored" || items[0].LastPass != nil {
		t.Errorf("item = %+v, want unmonitored with no dated pass answer", items[0])
	}
}
