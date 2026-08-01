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
	m, err := svc.MatchSeries(context.Background(), id)
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
	id := seedSeries(t, st, "Placeholder Saga", 12)

	m := grabMatch(t, svc, id)
	res, err := svc.Grab(context.Background(), m.Candidates[0], m.Items, false)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if res.InfoHash != "hash4" || string(res.Outcome) != "success" {
		t.Errorf("result = %+v, want hash4/success", res)
	}
	if len(res.Items) != 1 || res.Items[0] != 4 {
		t.Errorf("grabbed items = %v, want [4]", res.Items)
	}

	grabs, err := st.Q.ListGrabsBySeries(context.Background(), id)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].InfoHash != "hash4" || grabs[0].Status != "grabbed" {
		t.Fatalf("recorded grabs = %+v, want one grabbed row on hash4", grabs)
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
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{category: "anime"}, discardLogger())
	id := seedSeries(t, st, "Placeholder Saga", 12)

	m := grabMatch(t, svc, id)
	if _, err := svc.Grab(context.Background(), m.Candidates[0], m.Items, true); err != nil {
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
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{}, discardLogger())
	id := seedSeries(t, st, "Placeholder Saga", 12)

	m := grabMatch(t, svc, id)
	if _, err := svc.Grab(context.Background(), m.Candidates[0], m.Items, false); !errors.Is(err, acquire.ErrDownloadAdd) {
		t.Fatalf("Grab error = %v, want ErrDownloadAdd", err)
	}
	grabs, _ := st.Q.ListGrabsBySeries(context.Background(), id)
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
	id := seedSeries(t, st, "Placeholder Saga", 12)

	m := grabMatch(t, svc, id)
	if _, err := svc.Grab(context.Background(), m.Candidates[0], m.Items, false); !errors.Is(err, acquire.ErrNoDownloadClient) {
		t.Fatalf("Grab error = %v, want ErrNoDownloadClient", err)
	}
}
