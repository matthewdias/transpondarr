package settings

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/store"
)

func newTestService(t *testing.T) (*Service, *clients.Registry, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })
	reg := clients.New()
	svc, err := New(context.Background(), st, &config.Config{}, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, reg, st
}

// A save persists to the DB and swaps the live client; a follow-up save with a
// blank password inherits the stored secret rather than wiping it.
func TestUpdateDownloadPersistsAndKeepsBlankPassword(t *testing.T) {
	svc, reg, st := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://qb:8080", User: "admin", Password: "secret"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if reg.Download() == nil {
		t.Fatal("download client not swapped into registry")
	}
	if got, _ := st.Q.GetSetting(ctx, keyQbitPassword); got != "secret" {
		t.Fatalf("password not persisted, got %q", got)
	}

	if err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://qb:9090", User: "admin"}); err != nil {
		t.Fatalf("update 2: %v", err)
	}
	snap := svc.Snapshot()
	if snap.Download.Password != "secret" {
		t.Fatalf("blank password should keep the stored secret, got %q", snap.Download.Password)
	}
	if snap.Download.URL != "http://qb:9090" {
		t.Fatalf("url not updated, got %q", snap.Download.URL)
	}
}

// A persist failure must leave both the in-memory config and the live client
// untouched — the DB write happens before the swap, so there is no torn state.
func TestUpdateDownloadPersistFailureLeavesStateUnchanged(t *testing.T) {
	svc, reg, st := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://qb:8080", Password: "secret"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := svc.Snapshot().Download
	client := reg.Download()

	// Closing the DB forces the next persist (BeginTx/Commit) to fail.
	_ = st.DB.Close()

	if err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://evil:1234", Password: "new"}); err == nil {
		t.Fatal("expected an error when the DB is closed")
	}
	if got := svc.Snapshot().Download; got != before {
		t.Fatalf("in-memory config changed despite persist failure: %+v", got)
	}
	if reg.Download() != client {
		t.Fatal("registry client swapped despite persist failure")
	}
}
