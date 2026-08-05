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

// Nothing here unpacks a RAR set, so the deferral has to name what it found and
// what to do with it -- otherwise the row is a dead end.
func TestDefersAnArchivePayloadNamingTheArchive(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	dir := writeTree(t,
		"placeholder.saga.s01e05.1080p.web.h264-example.rar",
		"placeholder.saga.s01e05.1080p.web.h264-example.r00",
		"placeholder.saga.s01e05.1080p.web.h264-example.r01",
		"placeholder.saga.s01e05.1080p.web.h264-example.sfv",
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
	if !strings.Contains(events[0].Detail, "3-part archive set") {
		t.Errorf("detail = %q, want it to name the archive set and its size", events[0].Detail)
	}
	if !strings.Contains(events[0].Detail, "Fix import") {
		t.Errorf("detail = %q, want it to point at the manual path", events[0].Detail)
	}
}

// A payload holding one loose episode and an archive covering another must not
// fail the second: the bytes are right there, so it is a human's to fix.
func TestDefersTheItemAnArchiveCoversBesideALooseFile(t *testing.T) {
	st := coretest.NewStore(t)
	seedBatchGrab(t, st, "abc", 2)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"placeholder.saga.s01e02.1080p.web.h264-synth.rar",
		"placeholder.saga.s01e02.1080p.web.h264-synth.r00",
	)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	rows, err := st.Q.ListGrabsByInfoHash(context.Background(), "abc")
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	got := map[int64]string{}
	for _, r := range rows {
		got[r.ItemNumber.Int64] = r.Status
	}
	if got[1] != statusImported {
		t.Errorf("episode 1 status = %q, want the loose file imported", got[1])
	}
	if got[2] != statusDeferred {
		t.Errorf("episode 2 status = %q, want the archive to hold it deferred", got[2])
	}
}

// The two deferrals stay distinguishable: an empty payload has nothing to
// extract, so telling a human to extract it would be a wrong instruction.
func TestDefersAPayloadWithNeitherVideoNorArchive(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	dir := writeTree(t,
		"placeholder.saga.s01e05.1080p.web.h264-example.nfo",
		"placeholder.saga.s01e05.1080p.web.h264-example.sfv",
	)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	if err := New(st, fakeSource{dl: dl, lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil).ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	events := seriesEvents(t, st, seriesID)
	if len(events) != 1 || events[0].Detail != "the payload holds no video file" {
		t.Fatalf("events = %+v, want the plain no-video deferral", events)
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
