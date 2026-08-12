package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/matthewdias/transpondarr/internal/core/auth"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/version"
)

// ── DTOs ────────────────────────────────────────────────────────────────────
//
// Secrets are never returned. Instead a *_set boolean reports whether one is
// stored, and on update an empty secret field means "keep the stored value".

type downloadSettingsDTO struct {
	Configured  bool   `json:"configured"`
	URL         string `json:"url"`
	User        string `json:"user"`
	Category    string `json:"category"`
	PasswordSet bool   `json:"password_set"`
}

type indexerSettingsDTO struct {
	Configured bool   `json:"configured"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	APIKeySet  bool   `json:"apikey_set"`
	Categories string `json:"categories" doc:"Comma-separated Newznab category IDs; empty = no filter"`
}

type librarySettingsDTO struct {
	Configured bool   `json:"configured"`
	Dir        string `json:"dir"`
	Mode       string `json:"mode"`
}

type automationSettingsDTO struct {
	Mode          string `json:"mode" enum:"off,notify_only,on" doc:"notify_only rehearses: search and decide, notify, never grab"`
	PinDelayHours int    `json:"pin_delay_hours"`
}

type generalSettingsDTO struct {
	Version string `json:"version"`
	Addr    string `json:"addr"`
	DataDir string `json:"data_dir"`
	DBPath  string `json:"db_path"`
	APIKey  string `json:"api_key" doc:"Current API key (visible to authenticated callers)"`
}

type authSettingsDTO struct {
	Configured bool   `json:"configured"`
	Username   string `json:"username"`
	Required   string `json:"required" doc:"enabled or local"`
}

type notifyAdapterDTO struct {
	Configured   bool   `json:"configured"`
	URL          string `json:"url"`
	OnGrabbed    bool   `json:"on_grabbed"`
	OnImported   bool   `json:"on_imported"`
	OnStuck      bool   `json:"on_stuck"`
	OnGrabFailed bool   `json:"on_grab_failed"`
	OnTitleAdded bool   `json:"on_title_added"`
	OnRehearsal  bool   `json:"on_rehearsal"`
}

type ntfySettingsDTO struct {
	Configured   bool   `json:"configured"`
	Server       string `json:"server"`
	Topic        string `json:"topic"`
	TokenSet     bool   `json:"token_set"`
	OnGrabbed    bool   `json:"on_grabbed"`
	OnImported   bool   `json:"on_imported"`
	OnStuck      bool   `json:"on_stuck"`
	OnGrabFailed bool   `json:"on_grab_failed"`
	OnTitleAdded bool   `json:"on_title_added"`
	OnRehearsal  bool   `json:"on_rehearsal"`
}

type notificationsSettingsDTO struct {
	Discord notifyAdapterDTO `json:"discord"`
	Webhook notifyAdapterDTO `json:"webhook"`
	Ntfy    ntfySettingsDTO  `json:"ntfy"`
}

type settingsDTO struct {
	Download      downloadSettingsDTO      `json:"download"`
	Indexer       indexerSettingsDTO       `json:"indexer"`
	Library       librarySettingsDTO       `json:"library"`
	Automation    automationSettingsDTO    `json:"automation"`
	Notifications notificationsSettingsDTO `json:"notifications"`
	Auth          authSettingsDTO          `json:"auth"`
	General       generalSettingsDTO       `json:"general"`
}

func downloadDTO(c settings.DownloadConfig) downloadSettingsDTO {
	return downloadSettingsDTO{
		Configured:  c.URL != "",
		URL:         c.URL,
		User:        c.User,
		Category:    c.Category,
		PasswordSet: c.Password != "",
	}
}

func indexerDTO(c settings.IndexerConfig) indexerSettingsDTO {
	return indexerSettingsDTO{
		Configured: c.URL != "",
		Name:       c.Name,
		URL:        c.URL,
		APIKeySet:  c.APIKey != "",
		Categories: c.Categories,
	}
}

func libraryDTO(c settings.LibraryConfig) librarySettingsDTO {
	return librarySettingsDTO{Configured: c.Dir != "", Dir: c.Dir, Mode: c.Mode}
}

func automationDTO(c settings.AutomationConfig) automationSettingsDTO {
	return automationSettingsDTO{Mode: string(c.Mode), PinDelayHours: c.PinDelayHours}
}

func notifyAdapterDTOFrom(url string, ev settings.NotifyEvents) notifyAdapterDTO {
	return notifyAdapterDTO{
		Configured:   url != "",
		URL:          url,
		OnGrabbed:    ev.Grabbed,
		OnImported:   ev.Imported,
		OnStuck:      ev.Stuck,
		OnGrabFailed: ev.GrabFailed,
		OnTitleAdded: ev.TitleAdded,
		OnRehearsal:  ev.Rehearsal,
	}
}

func notificationsDTO(c settings.NotifyConfig) notificationsSettingsDTO {
	return notificationsSettingsDTO{
		Discord: notifyAdapterDTOFrom(c.DiscordURL, c.DiscordEvents),
		Webhook: notifyAdapterDTOFrom(c.WebhookURL, c.WebhookEvents),
		Ntfy: ntfySettingsDTO{
			Configured:   c.NtfyTopic != "",
			Server:       c.NtfyServer,
			Topic:        c.NtfyTopic,
			TokenSet:     c.NtfyToken != "",
			OnGrabbed:    c.NtfyEvents.Grabbed,
			OnImported:   c.NtfyEvents.Imported,
			OnStuck:      c.NtfyEvents.Stuck,
			OnGrabFailed: c.NtfyEvents.GrabFailed,
			OnTitleAdded: c.NtfyEvents.TitleAdded,
			OnRehearsal:  c.NtfyEvents.Rehearsal,
		},
	}
}

func snapshotDTO(s settings.Snapshot) settingsDTO {
	return settingsDTO{
		Download:      downloadDTO(s.Download),
		Indexer:       indexerDTO(s.Indexer),
		Library:       libraryDTO(s.Library),
		Automation:    automationDTO(s.Automation),
		Notifications: notificationsDTO(s.Notify),
		General: generalSettingsDTO{
			Version: version.Version,
			Addr:    s.Addr,
			DataDir: s.DataDir,
			DBPath:  s.DBPath,
			APIKey:  s.APIKey,
		},
	}
}

// Input bodies

// All fields are optional (omitempty) so the same body works for a full save, a
// partial test, or clearing a section (empty url/dir disables it). Secrets left
// empty keep the stored value; the service applies defaults for name/category/mode.
type downloadInput struct {
	Body struct {
		URL      string `json:"url,omitempty"`
		User     string `json:"user,omitempty"`
		Password string `json:"password,omitempty" doc:"Leave empty to keep the stored password"`
		Category string `json:"category,omitempty"`
	}
}

type indexerInput struct {
	Body struct {
		Name   string `json:"name,omitempty"`
		URL    string `json:"url,omitempty"`
		APIKey string `json:"apikey,omitempty" doc:"Leave empty to keep the stored API key"`
		// Unlike the API key, empty clears the filter: this is not a secret.
		Categories string `json:"categories,omitempty" doc:"Comma-separated Newznab category IDs; empty = no filter"`
	}
}

type libraryInput struct {
	Body struct {
		Dir  string `json:"dir,omitempty"`
		Mode string `json:"mode,omitempty" enum:"auto,hardlink,copy"`
	}
}

// Both fields are required, unlike the sections above: the delay has no "unset"
// encoding, so omitempty would make "leave the delay alone" and "set it to 0"
// the same request. The service clamps the hour count.
type automationInput struct {
	Body struct {
		Mode          string `json:"mode" enum:"off,notify_only,on" doc:"notify_only rehearses: search and decide, notify, never grab"`
		PinDelayHours int    `json:"pin_delay_hours" doc:"Global pinned-group wait; 0 disables waiting"`
	}
}

// notifyAdapterInput mirrors notifyAdapterDTO for writes. Toggles are required
// (the automationInput precedent — bools have no unset encoding); an empty URL
// disables the adapter.
type notifyAdapterInput struct {
	URL          string `json:"url,omitempty"`
	OnGrabbed    bool   `json:"on_grabbed"`
	OnImported   bool   `json:"on_imported"`
	OnStuck      bool   `json:"on_stuck"`
	OnGrabFailed bool   `json:"on_grab_failed"`
	OnTitleAdded bool   `json:"on_title_added"`
	OnRehearsal  bool   `json:"on_rehearsal"`
}

type ntfyInput struct {
	Server       string `json:"server,omitempty"`
	Topic        string `json:"topic,omitempty"`
	Token        string `json:"token,omitempty" doc:"Leave empty to keep the stored token"`
	OnGrabbed    bool   `json:"on_grabbed"`
	OnImported   bool   `json:"on_imported"`
	OnStuck      bool   `json:"on_stuck"`
	OnGrabFailed bool   `json:"on_grab_failed"`
	OnTitleAdded bool   `json:"on_title_added"`
	OnRehearsal  bool   `json:"on_rehearsal"`
}

// One body serves the save and all three per-adapter tests, so the UI sends the
// whole section either way.
type notificationsInput struct {
	Body struct {
		Discord notifyAdapterInput `json:"discord"`
		Webhook notifyAdapterInput `json:"webhook"`
		Ntfy    ntfyInput          `json:"ntfy"`
	}
}

type settingsOutput struct {
	Body settingsDTO
}

type testOutput struct {
	Body struct {
		Status string `json:"status" example:"ok"`
	}
}

type apiKeyOutput struct {
	Body struct {
		APIKey string `json:"api_key"`
	}
}

// ── Routes ──────────────────────────────────────────────────────────────────

// settingsHandler owns the runtime-config endpoints' dependencies: the settings
// service (persists edits and rebuilds live clients) and the auth service (backs
// the auth section of the settings snapshot).
type settingsHandler struct {
	settings *settings.Service
	auth     *auth.Service
}

func newSettingsHandler(deps routeDeps) *settingsHandler {
	return &settingsHandler{settings: deps.settings, auth: deps.auth}
}

func registerSettingsRoutes(api huma.API, deps routeDeps) {
	h := newSettingsHandler(deps)

	huma.Register(api, huma.Operation{
		OperationID: "get-settings",
		Method:      http.MethodGet,
		Path:        "/api/v1/settings",
		Summary:     "Get the effective runtime configuration (secrets masked)",
		Tags:        []string{"settings"},
	}, h.getSettings)

	// Download client -----------------------------------------------------------
	huma.Register(api, huma.Operation{
		OperationID: "update-download-settings",
		Method:      http.MethodPut,
		Path:        "/api/v1/settings/download",
		Summary:     "Update the qBittorrent download client (rebuilds it live)",
		Tags:        []string{"settings"},
	}, h.updateDownload)

	huma.Register(api, huma.Operation{
		OperationID: "test-download-settings",
		Method:      http.MethodPost,
		Path:        "/api/v1/settings/download/test",
		Summary:     "Test qBittorrent connectivity for the given (unsaved) values",
		Tags:        []string{"settings"},
	}, h.testDownload)

	// Indexer -------------------------------------------------------------------
	huma.Register(api, huma.Operation{
		OperationID: "update-indexer-settings",
		Method:      http.MethodPut,
		Path:        "/api/v1/settings/indexer",
		Summary:     "Update the Torznab indexer (rebuilds it live)",
		Tags:        []string{"settings"},
	}, h.updateIndexer)

	huma.Register(api, huma.Operation{
		OperationID: "test-indexer-settings",
		Method:      http.MethodPost,
		Path:        "/api/v1/settings/indexer/test",
		Summary:     "Test Torznab connectivity for the given (unsaved) values",
		Tags:        []string{"settings"},
	}, h.testIndexer)

	// Library -------------------------------------------------------------------
	huma.Register(api, huma.Operation{
		OperationID: "update-library-settings",
		Method:      http.MethodPut,
		Path:        "/api/v1/settings/library",
		Summary:     "Update the library import target (rebuilds it live)",
		Tags:        []string{"settings"},
	}, h.updateLibrary)

	// Automation ----------------------------------------------------------------
	huma.Register(api, huma.Operation{
		OperationID: "update-automation-settings",
		Method:      http.MethodPut,
		Path:        "/api/v1/settings/automation",
		Summary:     "Update the global automation policy (applies on the next job tick)",
		Tags:        []string{"settings"},
	}, h.updateAutomation)

	// Notifications -------------------------------------------------------------
	huma.Register(api, huma.Operation{
		OperationID: "update-notifications-settings",
		Method:      http.MethodPut,
		Path:        "/api/v1/settings/notifications",
		Summary:     "Update the notification adapters (rebuilds the dispatcher live)",
		Tags:        []string{"settings"},
	}, h.updateNotifications)

	huma.Register(api, huma.Operation{
		OperationID: "test-notify-discord",
		Method:      http.MethodPost,
		Path:        "/api/v1/settings/notifications/discord/test",
		Summary:     "Send a test notification to the given (unsaved) Discord webhook",
		Tags:        []string{"settings"},
	}, h.testNotifyDiscord)

	huma.Register(api, huma.Operation{
		OperationID: "test-notify-webhook",
		Method:      http.MethodPost,
		Path:        "/api/v1/settings/notifications/webhook/test",
		Summary:     "Send a test notification to the given (unsaved) webhook URL",
		Tags:        []string{"settings"},
	}, h.testNotifyWebhook)

	huma.Register(api, huma.Operation{
		OperationID: "test-notify-ntfy",
		Method:      http.MethodPost,
		Path:        "/api/v1/settings/notifications/ntfy/test",
		Summary:     "Send a test notification to the given (unsaved) ntfy topic",
		Tags:        []string{"settings"},
	}, h.testNotifyNtfy)

	// API key ------------------------------------------------------------------
	huma.Register(api, huma.Operation{
		OperationID: "regenerate-api-key",
		Method:      http.MethodPost,
		Path:        "/api/v1/settings/apikey/regenerate",
		Summary:     "Generate a new API key (invalidates the old one)",
		Tags:        []string{"settings"},
	}, h.regenerateAPIKey)

	huma.Register(api, huma.Operation{
		OperationID: "test-library-settings",
		Method:      http.MethodPost,
		Path:        "/api/v1/settings/library/test",
		Summary:     "Test that the given library directory exists and is writable",
		Tags:        []string{"settings"},
	}, h.testLibrary)
}

// respond builds the full settings body. Every handler here goes through it,
// including the update paths: the client caches the response as the whole
// settings object, so a section save that omitted auth would blank that card.
func (h *settingsHandler) respond() *settingsOutput {
	out := &settingsOutput{}
	out.Body = snapshotDTO(h.settings.Snapshot())
	out.Body.Auth = authSettingsDTO{
		Configured: h.auth.Configured(),
		Username:   h.auth.Username(),
		Required:   h.auth.Required(),
	}
	return out
}

func (h *settingsHandler) getSettings(_ context.Context, _ *struct{}) (*settingsOutput, error) {
	return h.respond(), nil
}

func (h *settingsHandler) updateDownload(ctx context.Context, in *downloadInput) (*settingsOutput, error) {
	if err := h.settings.UpdateDownload(ctx, settings.DownloadConfig{
		URL:      in.Body.URL,
		User:     in.Body.User,
		Password: in.Body.Password,
		Category: in.Body.Category,
	}); err != nil {
		return nil, huma.Error500InternalServerError("failed to save download settings", err)
	}
	return h.respond(), nil
}

func (h *settingsHandler) testDownload(ctx context.Context, in *downloadInput) (*testOutput, error) {
	if err := h.settings.TestDownload(ctx, settings.DownloadConfig{
		URL:      in.Body.URL,
		User:     in.Body.User,
		Password: in.Body.Password,
	}); err != nil {
		return nil, huma.Error502BadGateway("download client test failed", err)
	}
	out := &testOutput{}
	out.Body.Status = "ok"
	return out, nil
}

func (h *settingsHandler) updateIndexer(ctx context.Context, in *indexerInput) (*settingsOutput, error) {
	if _, err := settings.NormalizeCategories(in.Body.Categories); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if err := h.settings.UpdateIndexer(ctx, settings.IndexerConfig{
		Name:       in.Body.Name,
		URL:        in.Body.URL,
		APIKey:     in.Body.APIKey,
		Categories: in.Body.Categories,
	}); err != nil {
		return nil, huma.Error500InternalServerError("failed to save indexer settings", err)
	}
	return h.respond(), nil
}

func (h *settingsHandler) testIndexer(ctx context.Context, in *indexerInput) (*testOutput, error) {
	if _, err := settings.NormalizeCategories(in.Body.Categories); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if err := h.settings.TestIndexer(ctx, settings.IndexerConfig{
		Name:       in.Body.Name,
		URL:        in.Body.URL,
		APIKey:     in.Body.APIKey,
		Categories: in.Body.Categories,
	}); err != nil {
		return nil, huma.Error502BadGateway("indexer test failed", err)
	}
	out := &testOutput{}
	out.Body.Status = "ok"
	return out, nil
}

func (h *settingsHandler) updateLibrary(ctx context.Context, in *libraryInput) (*settingsOutput, error) {
	if in.Body.Mode != "" && !settings.ValidImportMode(in.Body.Mode) {
		return nil, huma.Error422UnprocessableEntity("invalid import mode (want auto, hardlink or copy)")
	}
	if err := h.settings.UpdateLibrary(ctx, settings.LibraryConfig{
		Dir:  in.Body.Dir,
		Mode: in.Body.Mode,
	}); err != nil {
		return nil, huma.Error500InternalServerError("failed to save library settings", err)
	}
	return h.respond(), nil
}

func (h *settingsHandler) updateAutomation(ctx context.Context, in *automationInput) (*settingsOutput, error) {
	if err := h.settings.UpdateAutomation(ctx, settings.AutomationConfig{
		Mode:          settings.AutomationMode(in.Body.Mode),
		PinDelayHours: in.Body.PinDelayHours,
	}); err != nil {
		return nil, huma.Error500InternalServerError("failed to save automation settings", err)
	}
	return h.respond(), nil
}

func notifyConfigFrom(in *notificationsInput) settings.NotifyConfig {
	events := func(a notifyAdapterInput) settings.NotifyEvents {
		return settings.NotifyEvents{
			Grabbed:    a.OnGrabbed,
			Imported:   a.OnImported,
			Stuck:      a.OnStuck,
			GrabFailed: a.OnGrabFailed,
			TitleAdded: a.OnTitleAdded,
			Rehearsal:  a.OnRehearsal,
		}
	}
	return settings.NotifyConfig{
		DiscordURL:    in.Body.Discord.URL,
		DiscordEvents: events(in.Body.Discord),
		WebhookURL:    in.Body.Webhook.URL,
		WebhookEvents: events(in.Body.Webhook),
		NtfyServer:    in.Body.Ntfy.Server,
		NtfyTopic:     in.Body.Ntfy.Topic,
		NtfyToken:     in.Body.Ntfy.Token,
		NtfyEvents: settings.NotifyEvents{
			Grabbed:    in.Body.Ntfy.OnGrabbed,
			Imported:   in.Body.Ntfy.OnImported,
			Stuck:      in.Body.Ntfy.OnStuck,
			GrabFailed: in.Body.Ntfy.OnGrabFailed,
			TitleAdded: in.Body.Ntfy.OnTitleAdded,
			Rehearsal:  in.Body.Ntfy.OnRehearsal,
		},
	}
}

func (h *settingsHandler) updateNotifications(ctx context.Context, in *notificationsInput) (*settingsOutput, error) {
	if err := h.settings.UpdateNotify(ctx, notifyConfigFrom(in)); err != nil {
		return nil, huma.Error500InternalServerError("failed to save notification settings", err)
	}
	return h.respond(), nil
}

func (h *settingsHandler) testNotifyDiscord(ctx context.Context, in *notificationsInput) (*testOutput, error) {
	if strings.TrimSpace(in.Body.Discord.URL) == "" {
		return nil, huma.Error422UnprocessableEntity("a Discord webhook URL is required")
	}
	return notifyTestResult(h.settings.TestNotifyDiscord(ctx, notifyConfigFrom(in)))
}

func (h *settingsHandler) testNotifyWebhook(ctx context.Context, in *notificationsInput) (*testOutput, error) {
	if strings.TrimSpace(in.Body.Webhook.URL) == "" {
		return nil, huma.Error422UnprocessableEntity("a webhook URL is required")
	}
	return notifyTestResult(h.settings.TestNotifyWebhook(ctx, notifyConfigFrom(in)))
}

func (h *settingsHandler) testNotifyNtfy(ctx context.Context, in *notificationsInput) (*testOutput, error) {
	if strings.TrimSpace(in.Body.Ntfy.Topic) == "" {
		return nil, huma.Error422UnprocessableEntity("an ntfy topic is required")
	}
	return notifyTestResult(h.settings.TestNotifyNtfy(ctx, notifyConfigFrom(in)))
}

func notifyTestResult(err error) (*testOutput, error) {
	if err != nil {
		return nil, huma.Error502BadGateway("notification test failed", err)
	}
	out := &testOutput{}
	out.Body.Status = "ok"
	return out, nil
}

func (h *settingsHandler) regenerateAPIKey(ctx context.Context, _ *struct{}) (*apiKeyOutput, error) {
	key, err := h.settings.RegenerateAPIKey(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("failed to regenerate api key", err)
	}
	out := &apiKeyOutput{}
	out.Body.APIKey = key
	return out, nil
}

func (h *settingsHandler) testLibrary(ctx context.Context, in *libraryInput) (*testOutput, error) {
	if err := h.settings.TestLibrary(ctx, settings.LibraryConfig{
		Dir:  in.Body.Dir,
		Mode: in.Body.Mode,
	}); err != nil {
		return nil, huma.Error422UnprocessableEntity("library check failed: " + err.Error())
	}
	out := &testOutput{}
	out.Body.Status = "ok"
	return out, nil
}
