package settings

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })
	return st
}

func seedSetting(t *testing.T, st *store.Store, key, value string) {
	t.Helper()
	if err := st.Q.UpsertSetting(context.Background(), db.UpsertSettingParams{Key: key, Value: value}); err != nil {
		t.Fatalf("seed setting %s: %v", key, err)
	}
}

// newServiceOver builds a service over an already-seeded store, for tests that
// exercise the startup overlay rather than an update.
func newServiceOver(t *testing.T, st *store.Store) (*Service, *clients.Registry) {
	t.Helper()
	reg := clients.New()
	svc, err := New(context.Background(), st, &config.Config{}, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc, reg
}

// A fresh install has no adapters configured, so the registry must report no
// dispatcher rather than one with zero routes.
func TestNotifyUnconfiguredMeansNilDispatcher(t *testing.T) {
	_, reg, _ := newTestService(t)
	if reg.Notify() != nil {
		t.Fatal("Notify() should be nil when no adapter is configured")
	}
}

func TestUpdateNotifyPersistsAndSwapsDispatcher(t *testing.T) {
	svc, reg, st := newTestService(t)
	ctx := context.Background()

	in := NotifyConfig{
		DiscordURL:    "https://discord.example/api/webhooks/1/abc",
		DiscordEvents: NotifyEvents{Grabbed: true, Imported: true, Stuck: true, GrabFailed: true, TitleAdded: true},
		NtfyTopic:     "transpondarr",
		NtfyToken:     "tk_secret",
		NtfyEvents:    NotifyEvents{Imported: true, Stuck: true},
	}
	if err := svc.UpdateNotify(ctx, in); err != nil {
		t.Fatalf("update: %v", err)
	}

	if reg.Notify() == nil {
		t.Fatal("dispatcher not swapped into registry")
	}
	for key, want := range map[string]string{
		"notify.discord.url":             "https://discord.example/api/webhooks/1/abc",
		"notify.discord.on_grabbed":      "true",
		"notify.webhook.url":             "",
		"notify.webhook.on_imported":     "false",
		"notify.ntfy.server":             "https://ntfy.sh",
		"notify.ntfy.topic":              "transpondarr",
		"notify.ntfy.token":              "tk_secret",
		"notify.ntfy.on_imported":        "true",
		"notify.ntfy.on_grabbed":         "false",
		"notify.discord.on_series_added": "true",
		"notify.discord.on_stuck":        "true",
		"notify.discord.on_grab_failed":  "true",
		"notify.discord.on_imported":     "true",
		"notify.ntfy.on_stuck":           "true",
		"notify.ntfy.on_grab_failed":     "false",
		"notify.ntfy.on_series_added":    "false",
		"notify.webhook.on_grabbed":      "false",
		"notify.webhook.on_stuck":        "false",
		"notify.webhook.on_grab_failed":  "false",
		"notify.webhook.on_series_added": "false",
	} {
		if got, err := st.Q.GetSetting(ctx, key); err != nil || got != want {
			t.Errorf("setting %s = %q (err %v), want %q", key, got, err, want)
		}
	}

	snap := svc.Snapshot()
	if snap.Notify.NtfyServer != "https://ntfy.sh" {
		t.Errorf("ntfy server = %q, want the default", snap.Notify.NtfyServer)
	}
	if !snap.Notify.NtfyEvents.Imported || snap.Notify.NtfyEvents.Grabbed {
		t.Errorf("ntfy events = %+v, want imported on and grabbed off", snap.Notify.NtfyEvents)
	}
}

// Clearing every adapter must drop the dispatcher back to nil.
func TestUpdateNotifyClearingAllAdaptersDropsDispatcher(t *testing.T) {
	svc, reg, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.UpdateNotify(ctx, NotifyConfig{WebhookURL: "https://hooks.example/x"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if reg.Notify() == nil {
		t.Fatal("dispatcher should exist after configuring the webhook")
	}
	if err := svc.UpdateNotify(ctx, NotifyConfig{}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if reg.Notify() != nil {
		t.Fatal("dispatcher should be nil after clearing every adapter")
	}
}

func TestUpdateNotifyBlankTokenInheritsStored(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.UpdateNotify(ctx, NotifyConfig{NtfyTopic: "transpondarr", NtfyToken: "tk_secret"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.UpdateNotify(ctx, NotifyConfig{NtfyTopic: "renamed"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	snap := svc.Snapshot()
	if snap.Notify.NtfyToken != "tk_secret" {
		t.Errorf("blank token should keep the stored secret, got %q", snap.Notify.NtfyToken)
	}
	if snap.Notify.NtfyTopic != "renamed" {
		t.Errorf("topic not updated, got %q", snap.Notify.NtfyTopic)
	}
}

// A pre-toggle install (or a hand-edited DB) has adapter URLs but no toggle
// rows; absent keys mean enabled, so configuring an adapter notifies by default.
func TestNotifyUnsetTogglesParseEnabled(t *testing.T) {
	st := newTestStore(t)
	seedSetting(t, st, "notify.discord.url", "https://discord.example/api/webhooks/1/abc")
	seedSetting(t, st, "notify.discord.on_imported", "false")

	svc, reg := newServiceOver(t, st)
	if reg.Notify() == nil {
		t.Fatal("dispatcher should be built from persisted settings")
	}
	ev := svc.Snapshot().Notify.DiscordEvents
	if !ev.Grabbed || !ev.Stuck || !ev.GrabFailed || !ev.TitleAdded {
		t.Errorf("unset toggles should parse enabled, got %+v", ev)
	}
	if ev.Imported {
		t.Error("an explicit false toggle should parse disabled")
	}
}

// The stored toggle key is a literal, so #207's rename of the event kind must
// not disturb settings saved before it. Seeded under the old key, read out
// against the new kind.
func TestNotifyStoredSeriesAddedKeySurvivesTheTitleRename(t *testing.T) {
	for _, tc := range []struct {
		stored string
		want   bool
	}{{"true", true}, {"false", false}} {
		st := newTestStore(t)
		seedSetting(t, st, "notify.discord.url", "https://discord.example/api/webhooks/1/abc")
		seedSetting(t, st, "notify.discord.on_series_added", tc.stored)

		svc, _ := newServiceOver(t, st)
		ev := svc.Snapshot().Notify.DiscordEvents
		if got := notifyKinds(ev)[notify.Kind("title_added")]; got != tc.want {
			t.Errorf("stored on_series_added=%s routed title_added=%t, want %t", tc.stored, got, tc.want)
		}
	}
}

func TestTestNotifyValidatesRequiredFields(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.TestNotifyDiscord(ctx, NotifyConfig{}); err == nil {
		t.Error("TestNotifyDiscord should reject a blank URL")
	}
	if err := svc.TestNotifyWebhook(ctx, NotifyConfig{}); err == nil {
		t.Error("TestNotifyWebhook should reject a blank URL")
	}
	if err := svc.TestNotifyNtfy(ctx, NotifyConfig{}); err == nil {
		t.Error("TestNotifyNtfy should reject a blank topic")
	}
}

func TestTestNotifyNtfyInheritsStoredToken(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	var auth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	if err := svc.UpdateNotify(ctx, NotifyConfig{NtfyServer: ts.URL, NtfyTopic: "transpondarr", NtfyToken: "tk_secret"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.TestNotifyNtfy(ctx, NotifyConfig{NtfyServer: ts.URL, NtfyTopic: "transpondarr"}); err != nil {
		t.Fatalf("test: %v", err)
	}
	if auth != "Bearer tk_secret" {
		t.Errorf("authorization = %q, want the stored token", auth)
	}
}
