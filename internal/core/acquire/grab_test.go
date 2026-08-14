package acquire_test

import (
	"context"
	"errors"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

// grabMatch runs a search for the fixture release and returns the matched
// candidate plus the loaded items, so a Grab test starts from a real match.
func grabMatch(t *testing.T, svc *acquire.Service, id int64) acquire.Match {
	t.Helper()
	m, err := svc.MatchTitle(context.Background(), id)
	if err != nil {
		t.Fatalf("MatchSeries: %v", err)
	}
	if len(m.Candidates) == 0 || !m.Candidates[0].Matched {
		t.Fatalf("candidates = %+v, want a matched release", m.Candidates)
	}
	return m
}

// Every item a release covers gets its own grab row keyed on the one info hash.
func TestGrabRecordsOneGrabPerCoveredItem(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 04 [1080p]",
			DownloadURL: "magnet:?xt=urn:btih:dd04", Seeders: 40},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hash4", Outcome: download.AddSuccess}}
	st := coretest.NewStore(t)
	svc, reg := newService(t, st, idx, fakeTitles{})
	reg.SetDownload(dl)
	id := seedTitle(t, st, "Placeholder Saga", 12)

	m := grabMatch(t, svc, id)
	res, err := svc.Grab(context.Background(), id, m.Candidates[0], m.Items, false)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if res.InfoHash != "hash4" || string(res.Outcome) != "success" {
		t.Errorf("result = %+v, want hash4/success", res)
	}
	if len(res.Items) != 1 || res.Items[0] != 4 {
		t.Errorf("grabbed items = %v, want [4]", res.Items)
	}

	grabs, err := st.Q.ListGrabsByTitle(context.Background(), id)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].InfoHash != "hash4" || grabs[0].Status != "grabbed" {
		t.Fatalf("recorded grabs = %+v, want one grabbed row on hash4", grabs)
	}
}

// A grab appends one grabbed event per covered item, giving history rows the
// upsert would otherwise erase on re-grab.
func TestGrabAppendsOneEventPerCoveredItem(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[Batchers] Placeholder Saga S1 (01-02) [1080p][Batch]",
			DownloadURL: "magnet:?xt=urn:btih:ab12", Seeders: 40},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hashbatch", Outcome: download.AddSuccess}}
	st := coretest.NewStore(t)
	svc, reg := newService(t, st, idx, fakeTitles{})
	reg.SetDownload(dl)
	id := seedTitle(t, st, "Placeholder Saga", 12)

	m := grabMatch(t, svc, id)
	if _, err := svc.Grab(context.Background(), id, m.Candidates[0], m.Items, false); err != nil {
		t.Fatalf("Grab: %v", err)
	}

	events, err := st.Q.ListTitleGrabEvents(context.Background(), id)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("recorded %d events, want 2", len(events))
	}
	numbers := map[int64]bool{}
	for _, e := range events {
		if e.Event != "grabbed" {
			t.Errorf("event = %s, want grabbed", e.Event)
		}
		if e.SeriesID != id || e.InfoHash != "hashbatch" || e.ItemKind != "episode" {
			t.Errorf("event row = %+v, want series %d / hashbatch / episode", e, id)
		}
		if e.ReleaseTitle != "[Batchers] Placeholder Saga S1 (01-02) [1080p][Batch]" {
			t.Errorf("release title = %q", e.ReleaseTitle)
		}
		if e.WantedItemID == 0 {
			t.Errorf("event has zero wanted_item_id: %+v", e)
		}
		numbers[e.ItemNumber] = true
	}
	if !numbers[1] || !numbers[2] {
		t.Errorf("event item numbers = %v, want 1 and 2", numbers)
	}
}

// The download client is handed the configured category, which is what makes
// Transpondarr's torrents identifiable in the client UI.
func TestGrabAddsWithConfiguredCategory(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 05 [1080p]",
			DownloadURL: "magnet:?xt=urn:btih:ee05", Seeders: 40},
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hash5", Outcome: download.AddSuccess}}
	st := coretest.NewStore(t)
	reg := newRegistry(idx, dl)
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{category: "anime"}, discardLogger(), nil)
	id := seedTitle(t, st, "Placeholder Saga", 12)

	m := grabMatch(t, svc, id)
	if _, err := svc.Grab(context.Background(), id, m.Candidates[0], m.Items, true); err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if len(dl.Adds) != 1 {
		t.Fatalf("download Add called %d times, want 1", len(dl.Adds))
	}
	if dl.Adds[0].Category != "anime" || !dl.Adds[0].Paused {
		t.Errorf("Add opts = %+v, want category anime and paused", dl.Adds[0])
	}
}

// A failing add is reported as its own sentinel and records nothing.
func TestGrabDownloadAddFailureRecordsNothing(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 06 [1080p]",
			DownloadURL: "magnet:?xt=urn:btih:ff06", Seeders: 40},
	}}
	dl := &coretest.FakeDownload{Err: errors.New("qbit: connection refused")}
	st := coretest.NewStore(t)
	reg := newRegistry(idx, dl)
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{}, discardLogger(), nil)
	id := seedTitle(t, st, "Placeholder Saga", 12)

	m := grabMatch(t, svc, id)
	if _, err := svc.Grab(context.Background(), id, m.Candidates[0], m.Items, false); !errors.Is(err, acquire.ErrDownloadAdd) {
		t.Fatalf("Grab error = %v, want ErrDownloadAdd", err)
	}
	grabs, _ := st.Q.ListGrabsByTitle(context.Background(), id)
	if len(grabs) != 0 {
		t.Errorf("recorded %d grabs after a failed add, want 0", len(grabs))
	}
}

// An unconfigured download client is a sentinel, not a panic.
func TestGrabWithoutDownloadClient(t *testing.T) {
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		{Title: "[ExampleSubs] Placeholder Saga - 07 [1080p]",
			DownloadURL: "magnet:?xt=urn:btih:0007", Seeders: 40},
	}}
	st := coretest.NewStore(t)
	svc, _ := newService(t, st, idx, fakeTitles{})
	id := seedTitle(t, st, "Placeholder Saga", 12)

	m := grabMatch(t, svc, id)
	if _, err := svc.Grab(context.Background(), id, m.Candidates[0], m.Items, false); !errors.Is(err, acquire.ErrNoDownloadClient) {
		t.Fatalf("Grab error = %v, want ErrNoDownloadClient", err)
	}
}
