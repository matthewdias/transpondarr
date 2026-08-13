package acquire_test

import (
	"context"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

// withNotifier routes every kind through a FakeNotifier on the registry.
func withNotifier(reg *clients.Registry) *coretest.FakeNotifier {
	fn := coretest.NewFakeNotifier()
	kinds := map[notify.Kind]bool{
		notify.KindGrabbed: true, notify.KindImported: true, notify.KindImportStuck: true,
		notify.KindGrabFailed: true, notify.KindTitleAdded: true, notify.KindRehearsal: true,
	}
	reg.SetNotify(notify.NewDispatcher(discardLogger(), notify.Route{Notifier: fn, Kinds: kinds}))
	return fn
}

// An automatic grab is what unattended operation most wants visibility into:
// the sweep's grabPass dispatches a grabbed event with the series and release.
func TestSweepGrabDispatchesGrabbedEvent(t *testing.T) {
	st := coretest.NewStore(t)
	seedSweep(t, st, "Placeholder Saga", true, sweepItem{number: 5})
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{episodeRelease("Placeholder Saga", 5)}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hash5", Outcome: download.AddSuccess}}
	reg := newRegistry(idx, dl)
	fn := withNotifier(reg)
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{}, discardLogger(), nil)

	if err := svc.SweepOnce(context.Background()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	select {
	case ev := <-fn.Events:
		if ev.Kind != notify.KindGrabbed {
			t.Fatalf("kind = %s, want grabbed", ev.Kind)
		}
		if ev.Title != "Placeholder Saga" {
			t.Errorf("title = %q", ev.Title)
		}
		if ev.ReleaseTitle != "[ExampleSubs] Placeholder Saga - 05 [1080p]" {
			t.Errorf("release = %q", ev.ReleaseTitle)
		}
		if ev.ItemNumber != 5 {
			t.Errorf("item = %d, want the single covered episode", ev.ItemNumber)
		}
		// The kind is what keeps an adapter from labelling a movie "Episode 1"; a
		// series must still carry the episode kind that earns the label.
		if ev.ItemKind != domain.KindEpisode {
			t.Errorf("item kind = %q, want episode", ev.ItemKind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the grabbed event")
	}
}

// A manual grab is the user clicking a button — no notification.
func TestManualGrabDoesNotDispatch(t *testing.T) {
	st := coretest.NewStore(t)
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{episodeRelease("Placeholder Saga", 5)}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "hash5", Outcome: download.AddSuccess}}
	reg := newRegistry(idx, dl)
	fn := withNotifier(reg)
	svc := acquire.New(st, reg, fakeTitles{}, fakeConfig{}, discardLogger(), nil)
	id := seedSeries(t, st, "Placeholder Saga", 12)

	m := grabMatch(t, svc, id)
	if _, err := svc.Grab(context.Background(), id, m.Candidates[0], m.Items, false); err != nil {
		t.Fatalf("Grab: %v", err)
	}
	select {
	case ev := <-fn.Events:
		t.Fatalf("manual grab dispatched %+v; explicit user intent needs no notification", ev)
	case <-time.After(100 * time.Millisecond):
	}
}
