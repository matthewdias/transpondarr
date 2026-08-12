package server_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

// upgradeProfileJSON is profileJSON plus the upgrade policy, so a round-trip
// test can assert the new fields survive create, update and read.
type upgradeProfileJSON struct {
	profileJSON
	UpgradesEnabled      bool `json:"upgrades_enabled"`
	CutoffScore          int  `json:"cutoff_score"`
	UpgradeV2AboveCutoff bool `json:"upgrade_v2_above_cutoff"`
}

// hold marks an item as held by a named release, the state an upgrade acts on.
func hold(t *testing.T, h *harness, seriesID int64, number int, release string) {
	t.Helper()
	if _, err := h.store.DB.ExecContext(context.Background(),
		`UPDATE wanted_items SET in_library = 1, held_release_title = ? WHERE series_id = ? AND number = ?`,
		release, seriesID, number); err != nil {
		t.Fatalf("hold item %d: %v", number, err)
	}
}

// The upgrade policy is profile data, so it has to survive the same round trip
// the rest of the profile does.
func TestProfileUpgradePolicyRoundTrips(t *testing.T) {
	h := newHarness(t, nil, nil)
	in := map[string]any{
		"name":                    "Upgrading",
		"resolution_order":        []string{"1080p", "720p"},
		"upgrades_enabled":        true,
		"cutoff_score":            2400,
		"upgrade_v2_above_cutoff": true,
	}
	var created upgradeProfileJSON
	if code := do(t, h, "POST", "/api/v1/profiles", in, &created); code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", code)
	}
	if !created.UpgradesEnabled || created.CutoffScore != 2400 || !created.UpgradeV2AboveCutoff {
		t.Fatalf("created = %+v, want the upgrade policy echoed", created)
	}

	in["cutoff_score"] = 2000
	in["upgrade_v2_above_cutoff"] = false
	var updated upgradeProfileJSON
	if code := do(t, h, "PUT", fmt.Sprintf("/api/v1/profiles/%d", created.ID), in, &updated); code != http.StatusOK {
		t.Fatalf("update status = %d, want 200", code)
	}
	if !updated.UpgradesEnabled || updated.CutoffScore != 2000 || updated.UpgradeV2AboveCutoff {
		t.Errorf("updated = %+v, want the edited policy", updated)
	}

	var list struct {
		Profiles []upgradeProfileJSON `json:"profiles"`
	}
	if code := do(t, h, "GET", "/api/v1/profiles", nil, &list); code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", code)
	}
	for _, p := range list.Profiles {
		if p.ID != created.ID {
			// The seeded Default must stay opted out.
			if p.UpgradesEnabled {
				t.Errorf("profile %q reads as opted in", p.Name)
			}
			continue
		}
		if !p.UpgradesEnabled || p.CutoffScore != 2000 {
			t.Errorf("listed profile = %+v, want the stored policy", p)
		}
	}

	// A negative cutoff is meaningless: scores are never negative.
	in["cutoff_score"] = -1
	if code := do(t, h, "PUT", fmt.Sprintf("/api/v1/profiles/%d", created.ID), in, nil); code != http.StatusUnprocessableEntity {
		t.Errorf("negative cutoff status = %d, want 422", code)
	}
}

// A release that only covers items we hold is a match now, so a manual grab of
// it succeeds in one request like every other manual grab (PR #57).
func TestGrabOfAHeldOnlyReleaseSucceeds(t *testing.T) {
	const matchURL = "magnet:?xt=urn:btih:0000000000000000000000000000000000000003"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 03 [1080p]", DownloadURL: matchURL, Seeders: 100},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hash3", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	hold(t, h, seriesID, 3, "[ExampleSubs] Placeholder Saga - 03 [480p]")

	var searchOut struct {
		Results []struct {
			Matched        bool  `json:"matched"`
			Items          []int `json:"items"`
			UpgradeItems   []int `json:"upgrade_items"`
			UpgradeBlocked []struct {
				Item   int    `json:"item"`
				Reason string `json:"reason"`
			} `json:"upgrade_blocked"`
		} `json:"results"`
	}
	if code := h.get(t, fmt.Sprintf("/api/v1/titles/%d/search", seriesID), &searchOut); code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", code)
	}
	if len(searchOut.Results) != 1 || !searchOut.Results[0].Matched {
		t.Fatalf("results = %+v, want the release matched to the held item", searchOut.Results)
	}
	r := searchOut.Results[0]
	if len(r.Items) != 1 || r.Items[0] != 3 {
		t.Errorf("items = %v, want [3]", r.Items)
	}
	// The default profile never opted in, so automation would refuse it — which
	// the Releases tab shows rather than enforces.
	if len(r.UpgradeItems) != 0 {
		t.Errorf("upgrade_items = %v, want none while the profile is opted out", r.UpgradeItems)
	}
	if len(r.UpgradeBlocked) != 1 || r.UpgradeBlocked[0].Item != 3 || r.UpgradeBlocked[0].Reason == "" {
		t.Errorf("upgrade_blocked = %+v, want item 3 with a reason", r.UpgradeBlocked)
	}

	var grabOut struct {
		Items []int `json:"items"`
	}
	if code := h.postJSON(t, fmt.Sprintf("/api/v1/titles/%d/grab", seriesID),
		map[string]any{"download_url": matchURL}, &grabOut); code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201 — a manual grab is never refused", code)
	}
	if len(grabOut.Items) != 1 || grabOut.Items[0] != 3 {
		t.Errorf("grabbed items = %v, want [3]", grabOut.Items)
	}
}

// An upgrade in flight is a held item with an open grab, a combination the
// status vocabulary could not previously produce: it reads as downloading, so
// the queue does not show the episode as simply had.
func TestHeldItemWithAnOpenGrabReadsAsDownloading(t *testing.T) {
	const matchURL = "magnet:?xt=urn:btih:0000000000000000000000000000000000000003"
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 03 [1080p]", DownloadURL: matchURL, Seeders: 100},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hash3", Outcome: download.AddSuccess}}
	h := newHarness(t, idx, dl)
	seriesID := seedSeries(t, h.store, "Placeholder Saga", 3)
	hold(t, h, seriesID, 3, "[ExampleSubs] Placeholder Saga - 03 [480p]")

	if got := itemStatus(t, h, seriesID, 3); got != "in_library" {
		t.Fatalf("held item status = %q, want in_library before the upgrade", got)
	}
	if code := h.postJSON(t, fmt.Sprintf("/api/v1/titles/%d/grab", seriesID),
		map[string]any{"download_url": matchURL}, nil); code != http.StatusCreated {
		t.Fatalf("grab status = %d, want 201", code)
	}
	if got := itemStatus(t, h, seriesID, 3); got != "downloading" {
		t.Errorf("held item status = %q, want downloading while the upgrade is in flight", got)
	}
}
