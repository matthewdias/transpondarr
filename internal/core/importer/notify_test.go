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
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

// notifyingSource wires a real dispatcher over a FakeNotifier, so the tests
// exercise the same async fan-out production does.
func notifyingSource(dl download.Client, lib *coretest.FakeLibrary, fn *coretest.FakeNotifier) fakeSource {
	kinds := map[notify.Kind]bool{
		notify.KindGrabbed: true, notify.KindImported: true, notify.KindImportStuck: true,
		notify.KindGrabFailed: true, notify.KindTitleAdded: true,
	}
	d := notify.NewDispatcher(discardLogger(), notify.Route{Notifier: fn, Kinds: kinds})
	return fakeSource{dl: dl, lib: lib, ntf: d}
}

func waitEvent(t *testing.T, fn *coretest.FakeNotifier) notify.Event {
	t.Helper()
	select {
	case ev := <-fn.Events:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a notification")
		return notify.Event{}
	}
}

func expectNoEvent(t *testing.T, fn *coretest.FakeNotifier) {
	t.Helper()
	select {
	case ev := <-fn.Events:
		t.Fatalf("unexpected notification %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestImportDispatchesImportedEvent(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: src},
	}}
	fn := coretest.NewFakeNotifier()
	im := New(st, notifyingSource(dl, &coretest.FakeLibrary{}, fn), discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	ev := waitEvent(t, fn)
	if ev.Kind != notify.KindImported {
		t.Fatalf("kind = %s, want imported", ev.Kind)
	}
	if ev.Title != "Placeholder Saga" || ev.ItemNumber != 5 || ev.ReleaseTitle != "rel" {
		t.Errorf("event = %+v, want series/item/release from the grab", ev)
	}
	if ev.Path != "/library/placed.mkv" {
		t.Errorf("path = %q, want the library destination", ev.Path)
	}
}

// A pack landing six episodes is one arrival, not six: one event carrying the
// numbers, so a season import cannot spam a phone.
func TestMultiItemImportDispatchesOneEvent(t *testing.T) {
	st := coretest.NewStore(t)
	seedBatchGrab(t, st, "abc", 3)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 02 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 03 [1080p].mkv",
	)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: dir},
	}}
	fn := coretest.NewFakeNotifier()
	im := New(st, notifyingSource(dl, &coretest.FakeLibrary{}, fn), discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	ev := waitEvent(t, fn)
	if ev.Kind != notify.KindImported {
		t.Fatalf("kind = %s, want imported", ev.Kind)
	}
	if len(ev.Items) != 3 || ev.Items[0] != 1 || ev.Items[2] != 3 {
		t.Errorf("items = %v, want the three episodes sorted", ev.Items)
	}
	if ev.ItemNumber != 0 {
		t.Errorf("item = %d, want 0 for a multi-item release", ev.ItemNumber)
	}
	if ev.Path != "/library" {
		t.Errorf("path = %q, want the directory the episodes landed in", ev.Path)
	}
	expectNoEvent(t, fn)
}

// A single-item import keeps its shape: one number, the file's own destination.
func TestSingleItemImportKeepsItsEventShape(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: src},
	}}
	fn := coretest.NewFakeNotifier()
	im := New(st, notifyingSource(dl, &coretest.FakeLibrary{}, fn), discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	ev := waitEvent(t, fn)
	if ev.ItemNumber != 5 || len(ev.Items) != 0 || ev.Path != "/library/placed.mkv" {
		t.Errorf("event = %+v, want item 5, no items list, the file's destination", ev)
	}
}

// A stuck import notifies once per incident — on the no-error → error
// transition — not once per distinct reason: a flaky mount alternating error
// strings tick-to-tick must not flap notifications.
func TestStuckImportNotifiesOncePerIncident(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: "/nonexistent/raw.mkv"},
	}}
	fn := coretest.NewFakeNotifier()
	im := New(st, notifyingSource(dl, &coretest.FakeLibrary{}, fn), discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	ev := waitEvent(t, fn)
	if ev.Kind != notify.KindImportStuck {
		t.Fatalf("kind = %s, want import_stuck", ev.Kind)
	}
	if ev.Title != "Placeholder Saga" || ev.Error == "" {
		t.Errorf("event = %+v, want the series and a reason", ev)
	}

	// Same failure next tick: the unchanged-message guard must also gate the event.
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan 2: %v", err)
	}
	expectNoEvent(t, fn)

	// A *different* reason on a still-stuck grab is the same incident: the DB row
	// updates, the notification does not repeat.
	dl.Statuses = []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: "/elsewhere/raw.mkv"},
	}
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan 3: %v", err)
	}
	expectNoEvent(t, fn)
	if g := grabByHash(t, st, "abc"); !g.LastError.Valid ||
		!strings.Contains(g.LastError.String, "/elsewhere/raw.mkv") {
		t.Errorf("last_error = %+v, want it updated to the new reason", g.LastError)
	}
}

// A batch failing in the client is one incident: one event, not one per row.
func TestFailedDownloadNotifiesOncePerRelease(t *testing.T) {
	st := coretest.NewStore(t)
	seedBatchGrab(t, st, "abc", 2)
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError, ContentPath: "/whatever"},
	}}
	fn := coretest.NewFakeNotifier()
	im := New(st, notifyingSource(dl, &coretest.FakeLibrary{}, fn), discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	ev := waitEvent(t, fn)
	if ev.Kind != notify.KindGrabFailed {
		t.Fatalf("kind = %s, want grab_failed", ev.Kind)
	}
	if ev.Title != "Placeholder Saga" || ev.ReleaseTitle != "[SynthSubs] Placeholder Saga - 01-03 [Batch]" || ev.Error == "" {
		t.Errorf("event = %+v, want series, release, and a reason", ev)
	}
	if ev.ItemNumber != 0 {
		t.Errorf("item = %d, want 0 for a multi-item release", ev.ItemNumber)
	}
	expectNoEvent(t, fn)
}

// The vanished-from-client path fires the same event as an in-client error, and
// a nil blocklist must not silence it.
func TestVanishedDownloadNotifiesGrabFailed(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	backdateMissingSince(t, st, "abc", 10*time.Minute)
	dl := &coretest.FakeDownload{} // reports nothing for the hash
	fn := coretest.NewFakeNotifier()
	im := New(st, notifyingSource(dl, &coretest.FakeLibrary{}, fn), discardLogger(), nil, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	ev := waitEvent(t, fn)
	if ev.Kind != notify.KindGrabFailed {
		t.Fatalf("kind = %s, want grab_failed", ev.Kind)
	}
	if ev.ItemNumber != 5 {
		t.Errorf("item = %d, want the single row's number", ev.ItemNumber)
	}
}

// A notifier failing must never fail the import — the file is placed, the grab
// settles, and the scan reports success.
func TestErroringNotifierDoesNotFailImport(t *testing.T) {
	st := coretest.NewStore(t)
	seedGrab(t, st, "abc")
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: src},
	}}
	fn := coretest.NewFakeNotifier()
	fn.Err = errors.New("endpoint down")
	im := New(st, notifyingSource(dl, &coretest.FakeLibrary{}, fn), discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	waitEvent(t, fn)
	if grabByHash(t, st, "abc").Status != "imported" {
		t.Error("grab not imported despite the notifier only failing after the fact")
	}
}
