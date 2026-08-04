package importer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seriesEvents returns the grab events recorded for a series, newest first.
func seriesEvents(t *testing.T, st *store.Store, seriesID int64) []db.GrabEvent {
	t.Helper()
	events, err := st.Q.ListSeriesGrabEvents(context.Background(), seriesID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return events
}

func TestImportAppendsImportedEvent(t *testing.T) {
	st := coretest.NewStore(t)
	itemID, seriesID := seedGrab(t, st, "abc")
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: src},
	}}
	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	events := seriesEvents(t, st, seriesID)
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}
	e := events[0]
	if e.Event != "imported" || e.Detail != "" {
		t.Errorf("event = %s/%q, want imported with no detail", e.Event, e.Detail)
	}
	if e.WantedItemID != itemID || e.ItemNumber != 5 || e.ItemKind != "episode" || e.InfoHash != "abc" || e.ReleaseTitle != "rel" {
		t.Errorf("event row = %+v, want item %d / number 5 / abc / rel", e, itemID)
	}
}

// A deferral now means one covered item's file could not be picked out, and the
// event carries the reason a human needs to fix it from the Activity queue.
func TestDeferAppendsDeferredEventWithDetail(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	// Nothing claims episode 5, and files are left over: fixable by hand.
	dir := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 04 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 06 [1080p].mkv",
	)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	events := seriesEvents(t, st, seriesID)
	if len(events) != 1 || events[0].Event != "import_deferred" {
		t.Fatalf("events = %+v, want one import_deferred", events)
	}
	if !strings.Contains(events[0].Detail, "no file matched episode 5") {
		t.Errorf("detail = %q, want it to name the episode nothing matched", events[0].Detail)
	}
}

func TestClientErrorAppendsFailedEventWithDetail(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}
	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	events := seriesEvents(t, st, seriesID)
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}
	if events[0].Event != "failed" || events[0].Detail != "the download client reported an error" {
		t.Errorf("event = %s/%q, want failed with the client-error detail", events[0].Event, events[0].Detail)
	}
}

func TestVanishedGrabAppendsFailedEventWithDetail(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	backdateMissingSince(t, st, "abc", time.Hour)
	dl := &coretest.FakeDownload{} // client reports nothing at all
	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	events := seriesEvents(t, st, seriesID)
	if len(events) != 1 {
		t.Fatalf("recorded %d events, want 1", len(events))
	}
	if events[0].Event != "failed" || events[0].Detail != "the download vanished from the client" {
		t.Errorf("event = %s/%q, want failed with the vanished detail", events[0].Event, events[0].Detail)
	}
}

// An import failure records no event, mirroring #118: it stays grabbed and
// retries, so history would only accumulate noise for a path-mapping gap.
func TestPlaceFailureAppendsNoEvent(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: src},
	}}
	target := &coretest.FakeLibrary{DestErr: errors.New("mkdir /library: permission denied")}
	if err := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if events := seriesEvents(t, st, seriesID); len(events) != 0 {
		t.Errorf("events = %+v, want none for a Place failure", events)
	}
}
