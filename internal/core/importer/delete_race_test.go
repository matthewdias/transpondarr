package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/library"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

// A series delete landing between the grab-row read and the post-Place writes
// still places the file — the same outcome as the import finishing one tick
// before the delete — and then no-ops the cascaded-away rows: both writes are
// :exec, so zero rows is silent, nothing survives, and nothing retries.
func TestScanSurvivesSeriesDeletedMidImport(t *testing.T) {
	st := coretest.NewStore(t)
	_, seriesID := seedGrab(t, st, "abc")
	src := filepath.Join(t.TempDir(), "raw.mkv")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateComplete, ContentPath: src},
	}}
	target := &coretest.FakeLibrary{}
	target.PlaceHook = func(library.ImportRequest) {
		if _, err := st.Q.DeleteSeries(ctx, seriesID); err != nil {
			t.Errorf("delete series mid-import: %v", err)
		}
	}
	im := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce: %v — a mid-import delete must not fail the scan", err)
	}
	if n := len(target.Placed); n != 1 {
		t.Fatalf("Place called %d times, want 1 — the file still lands in the library", n)
	}
	for _, table := range []string{"grabs", "wanted_items"} {
		var count int
		if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 0 {
			t.Errorf("%d %s rows survive the cascade, want 0", count, table)
		}
	}

	if err := im.ScanOnce(ctx); err != nil {
		t.Fatalf("second ScanOnce: %v — nothing must be left to retry", err)
	}
	if n := len(target.Placed); n != 1 {
		t.Errorf("Place called %d times after the second scan, want still 1", n)
	}
}
