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
// blank password inherits the stored secret rather than wiping it. The follow-up
// stays on the saved host, which the trailing slash exercises: the URL is stored
// as written but is the same destination, so the inheritance still applies (#259).
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

	if err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://qb:8080/", User: "operator"}); err != nil {
		t.Fatalf("update 2: %v", err)
	}
	snap := svc.Snapshot()
	if snap.Download.Password != "secret" {
		t.Fatalf("blank password should keep the stored secret, got %q", snap.Download.Password)
	}
	if snap.Download.URL != "http://qb:8080/" {
		t.Fatalf("url not updated, got %q", snap.Download.URL)
	}
	if snap.Download.User != "operator" {
		t.Fatalf("user not updated, got %q", snap.Download.User)
	}
}

// Categories are normalized on the way in, cleared by a blank field (they are
// not a secret, so the API key's inherit-on-blank rule does not apply), and a
// non-numeric segment is refused without touching persisted or live state.
func TestUpdateIndexerCategories(t *testing.T) {
	svc, reg, st := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateIndexer(ctx, IndexerConfig{URL: "http://prowlarr:9696/1/api", APIKey: "k", Categories: " 5070, 127720 ,"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if reg.Indexer() == nil {
		t.Fatal("indexer not swapped into registry")
	}
	if got, _ := st.Q.GetSetting(ctx, keyTorznabCategories); got != "5070,127720" {
		t.Fatalf("categories not persisted normalized, got %q", got)
	}
	if got := svc.Snapshot().Indexer.Categories; got != "5070,127720" {
		t.Fatalf("snapshot categories = %q, want 5070,127720", got)
	}

	before := svc.Snapshot().Indexer
	if err := svc.UpdateIndexer(ctx, IndexerConfig{URL: "http://prowlarr:9696/1/api", Categories: "5070,abc"}); err == nil {
		t.Fatal("expected an error for a non-numeric category")
	}
	if got := svc.Snapshot().Indexer; got != before {
		t.Fatalf("state changed despite an invalid update: %+v", got)
	}
	if got, _ := st.Q.GetSetting(ctx, keyTorznabCategories); got != "5070,127720" {
		t.Fatalf("persisted categories changed despite an invalid update, got %q", got)
	}

	if err := svc.UpdateIndexer(ctx, IndexerConfig{URL: "http://prowlarr:9696/1/api"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := svc.Snapshot().Indexer.Categories; got != "" {
		t.Fatalf("blank should clear the filter, got %q", got)
	}
	if snap := svc.Snapshot().Indexer; snap.APIKey != "k" {
		t.Fatalf("blank api key should still inherit, got %q", snap.APIKey)
	}
}

// The env baseline is pass-through like every other TRANSPONDARR_* value; the
// strict check belongs to the paths a user types into.
func TestIndexerCategoriesFromEnvBaseline(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })
	base := &config.Config{TorznabURL: "http://prowlarr:9696/1/api", TorznabCategories: "5070"}
	svc, err := New(context.Background(), st, base, clients.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if got := svc.Snapshot().Indexer.Categories; got != "5070" {
		t.Fatalf("env categories = %q, want 5070", got)
	}
}

func TestNormalizeCategories(t *testing.T) {
	ok := map[string]string{
		"":                 "",
		"  ":               "",
		"5070":             "5070",
		" 5070 , 127720 ":  "5070,127720",
		"5070,,127720":     "5070,127720",
		"5070,127720,\n":   "5070,127720",
		"5000,5070,140679": "5000,5070,140679",
		"+5070":            "5070",
		"007":              "7",
	}
	for in, want := range ok {
		got, err := NormalizeCategories(in)
		if err != nil {
			t.Errorf("NormalizeCategories(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeCategories(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"anime", "5070,abc", "-1", "0", "5070;127720", "50.70", "0x10"} {
		if got, err := NormalizeCategories(in); err == nil {
			t.Errorf("NormalizeCategories(%q) = %q, want an error", in, got)
		}
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
