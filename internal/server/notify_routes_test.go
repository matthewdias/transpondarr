package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

type notifyAdapterJSON struct {
	Configured    bool   `json:"configured"`
	URL           string `json:"url"`
	OnGrabbed     bool   `json:"on_grabbed"`
	OnImported    bool   `json:"on_imported"`
	OnStuck       bool   `json:"on_stuck"`
	OnGrabFailed  bool   `json:"on_grab_failed"`
	OnSeriesAdded bool   `json:"on_series_added"`
	OnRehearsal   bool   `json:"on_rehearsal"`
}

type ntfyJSON struct {
	Configured    bool   `json:"configured"`
	Server        string `json:"server"`
	Topic         string `json:"topic"`
	TokenSet      bool   `json:"token_set"`
	OnGrabbed     bool   `json:"on_grabbed"`
	OnImported    bool   `json:"on_imported"`
	OnStuck       bool   `json:"on_stuck"`
	OnGrabFailed  bool   `json:"on_grab_failed"`
	OnSeriesAdded bool   `json:"on_series_added"`
	OnRehearsal   bool   `json:"on_rehearsal"`
}

type notificationsJSON struct {
	Discord notifyAdapterJSON `json:"discord"`
	Webhook notifyAdapterJSON `json:"webhook"`
	Ntfy    ntfyJSON          `json:"ntfy"`
}

type notifySettingsJSON struct {
	Notifications notificationsJSON `json:"notifications"`
}

func adapterBody(url string) map[string]any {
	return map[string]any{
		"url": url, "on_grabbed": true, "on_imported": true,
		"on_stuck": true, "on_grab_failed": true, "on_series_added": true,
		"on_rehearsal": true,
	}
}

func notificationsBody(discordURL, webhookURL, ntfyServer, ntfyTopic, ntfyToken string) map[string]any {
	return map[string]any{
		"discord": adapterBody(discordURL),
		"webhook": adapterBody(webhookURL),
		"ntfy": map[string]any{
			"server": ntfyServer, "topic": ntfyTopic, "token": ntfyToken,
			"on_grabbed": true, "on_imported": false, "on_stuck": true,
			"on_grab_failed": true, "on_series_added": true, "on_rehearsal": true,
		},
	}
}

// The Settings UI caches every PUT's response as the whole settings object, so
// a notifications save must return the full DTO — and never the ntfy token.
func TestNotificationsSettingsRoundTrip(t *testing.T) {
	h := newHarness(t, nil, nil)

	var initial notifySettingsJSON
	if code := do(t, h, http.MethodGet, "/api/v1/settings", nil, &initial); code != http.StatusOK {
		t.Fatalf("GET /settings = %d, want 200", code)
	}
	if initial.Notifications.Discord.Configured || initial.Notifications.Ntfy.Configured {
		t.Errorf("fresh install reports configured adapters: %+v", initial.Notifications)
	}
	if initial.Notifications.Ntfy.Server != "https://ntfy.sh" {
		t.Errorf("ntfy server = %q, want the default", initial.Notifications.Ntfy.Server)
	}

	var saved notifySettingsJSON
	code := do(t, h, http.MethodPut, "/api/v1/settings/notifications",
		notificationsBody("https://discord.example/api/webhooks/1/abc", "", "", "transpondarr", "tk_secret"), &saved)
	if code != http.StatusOK {
		t.Fatalf("PUT /settings/notifications = %d, want 200", code)
	}
	n := saved.Notifications
	if !n.Discord.Configured || n.Discord.URL != "https://discord.example/api/webhooks/1/abc" {
		t.Errorf("discord = %+v, want configured with the saved URL", n.Discord)
	}
	if n.Webhook.Configured {
		t.Errorf("webhook = %+v, want unconfigured", n.Webhook)
	}
	if !n.Ntfy.Configured || !n.Ntfy.TokenSet || n.Ntfy.Topic != "transpondarr" {
		t.Errorf("ntfy = %+v, want configured with token_set", n.Ntfy)
	}
	if n.Ntfy.OnImported || !n.Ntfy.OnGrabbed {
		t.Errorf("ntfy toggles = %+v, want imported off and grabbed on", n.Ntfy)
	}

	var got notifySettingsJSON
	if code := do(t, h, http.MethodGet, "/api/v1/settings", nil, &got); code != http.StatusOK {
		t.Fatalf("GET /settings = %d, want 200", code)
	}
	if got.Notifications != saved.Notifications {
		t.Errorf("read-back %+v != save response %+v", got.Notifications, saved.Notifications)
	}
	if h.reg.Notify() == nil {
		t.Error("dispatcher not live in the registry after the save")
	}
}

func TestNotificationTestEndpoints(t *testing.T) {
	h := newHarness(t, nil, nil)

	// Missing required field → 422.
	var out struct{}
	if code := do(t, h, http.MethodPost, "/api/v1/settings/notifications/discord/test",
		notificationsBody("", "", "", "", ""), &out); code != http.StatusUnprocessableEntity {
		t.Errorf("discord test with no URL = %d, want 422", code)
	}
	if code := do(t, h, http.MethodPost, "/api/v1/settings/notifications/webhook/test",
		notificationsBody("", "", "", "", ""), &out); code != http.StatusUnprocessableEntity {
		t.Errorf("webhook test with no URL = %d, want 422", code)
	}
	if code := do(t, h, http.MethodPost, "/api/v1/settings/notifications/ntfy/test",
		notificationsBody("", "", "", "", ""), &out); code != http.StatusUnprocessableEntity {
		t.Errorf("ntfy test with no topic = %d, want 422", code)
	}

	// A refusing endpoint → 502.
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(refuse.Close)
	if code := do(t, h, http.MethodPost, "/api/v1/settings/notifications/webhook/test",
		notificationsBody("", refuse.URL, "", "", ""), &out); code != http.StatusBadGateway {
		t.Errorf("webhook test against a refusing endpoint = %d, want 502", code)
	}

	// A healthy endpoint → ok.
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(okSrv.Close)
	var ok struct {
		Status string `json:"status"`
	}
	if code := do(t, h, http.MethodPost, "/api/v1/settings/notifications/ntfy/test",
		notificationsBody("", "", okSrv.URL, "transpondarr", ""), &ok); code != http.StatusOK || ok.Status != "ok" {
		t.Errorf("ntfy test = %d %+v, want 200 ok", code, ok)
	}
}

func TestAddSeriesDispatchesSeriesAdded(t *testing.T) {
	provider := variantProvider{meta: metadata.TitleMeta{
		Titles: metadata.Titles{Romaji: "Placeholder Saga"}, Format: "TV",
	}}
	h := newHarnessWithProvider(t, nil, nil, provider)
	fn := coretest.NewFakeNotifier()
	h.reg.SetNotify(notify.NewDispatcher(discardLogger(),
		notify.Route{Notifier: fn, Kinds: map[notify.Kind]bool{notify.KindSeriesAdded: true}}))

	var out struct{}
	if code := do(t, h, http.MethodPost, "/api/v1/series",
		map[string]any{"provider": "anilist", "provider_id": 42}, &out); code != http.StatusCreated {
		t.Fatalf("POST /series = %d, want 201", code)
	}

	select {
	case ev := <-fn.Events:
		if ev.Kind != notify.KindSeriesAdded || ev.SeriesTitle != "Placeholder Saga" {
			t.Errorf("event = %+v, want series_added for Placeholder Saga", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the series_added event")
	}
}
