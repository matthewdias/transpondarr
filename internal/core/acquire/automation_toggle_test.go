package acquire_test

import (
	"context"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

// #102's acceptance criterion, against the real settings service rather than a
// double: the same running sweep must obey a toggle written after it was built,
// in both directions, with nothing rebuilt in between.
func TestSweepObeysLiveAutomationToggle(t *testing.T) {
	ctx := context.Background()
	st := coretest.NewStore(t)
	idx := &coretest.FakeIndexer{Releases: []indexer.Release{
		episodeRelease("Placeholder Saga", 3),
		episodeRelease("Placeholder Saga", 5),
	}}
	dl := &coretest.FakeDownload{Result: download.AddResult{Hash: "swept", Outcome: download.AddSuccess}}
	reg := newRegistry(nil, nil)

	// settings.New installs clients built from the (empty) config, so the fakes go
	// in afterwards or they are overwritten by nils.
	cfg, err := settings.New(ctx, st, &config.Config{}, reg, discardLogger())
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	reg.SetIndexer(idx)
	reg.SetDownload(dl)

	svc := acquire.New(st, reg, fakeTitles{}, cfg, discardLogger())
	past := time.Now().Add(-2 * time.Hour)
	id := seedSweep(t, st, "Placeholder Saga", true,
		sweepItem{number: 3, airsAt: &past}, sweepItem{number: 5, airsAt: &past})

	// Off until configured: the default ships inert.
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce while off: %v", err)
	}
	if len(idx.Queries) != 0 {
		t.Fatalf("disabled sweep issued %d searches, want none", len(idx.Queries))
	}

	if err := cfg.UpdateAutomation(ctx, settings.AutomationConfig{Enabled: true}); err != nil {
		t.Fatalf("enable automation: %v", err)
	}
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce after enabling: %v", err)
	}
	if len(idx.Queries) == 0 {
		t.Fatal("sweep did not search after automation was enabled without a restart")
	}
	if got := grabbedItemNumbers(t, st, id); len(got) == 0 {
		t.Fatal("sweep grabbed nothing after automation was enabled")
	}

	if err := cfg.UpdateAutomation(ctx, settings.AutomationConfig{Enabled: false}); err != nil {
		t.Fatalf("disable automation: %v", err)
	}
	searches, adds := len(idx.Queries), len(dl.Adds)
	if err := svc.SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce after disabling: %v", err)
	}
	if len(idx.Queries) != searches || len(dl.Adds) != adds {
		t.Errorf("sweep kept working after the toggle was turned off: searches %d->%d, adds %d->%d",
			searches, len(idx.Queries), adds, len(dl.Adds))
	}
}
