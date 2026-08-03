// Package settings is the runtime-configuration layer. It resolves the effective
// integration config as the TRANSPONDARR_* environment baseline overlaid with
// per-key overrides persisted in the settings table, builds the download /
// indexer / library clients from it into a clients.Registry, and — on an update
// from the Settings UI — persists the change, rebuilds the affected client, and
// swaps it into the registry so it takes effect without a restart.
//
// Secrets (qBittorrent password, Torznab API key) live in the settings table in
// plaintext, the same trust model as the rest of the single-file SQLite DB.
package settings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/auth"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/download/qbittorrent"
	"github.com/matthewdias/transpondarr/internal/core/indexer"
	"github.com/matthewdias/transpondarr/internal/core/indexer/torznab"
	"github.com/matthewdias/transpondarr/internal/core/library"
	"github.com/matthewdias/transpondarr/internal/core/library/mediaserver"
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/core/notify/discord"
	"github.com/matthewdias/transpondarr/internal/core/notify/ntfy"
	"github.com/matthewdias/transpondarr/internal/core/notify/webhook"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// APIKeySettingKey is the settings-table key under which the API key is stored.
// Exported so the composition root can reuse it when resolving the key at start.
const APIKeySettingKey = "api.key"

// Setting keys persisted in the settings table.
const (
	keyQbitURL       = "qbit.url"
	keyQbitUser      = "qbit.user"
	keyQbitPassword  = "qbit.password"
	keyQbitCategory  = "qbit.category"
	keyTorznabName   = "torznab.name"
	keyTorznabURL    = "torznab.url"
	keyTorznabAPIKey = "torznab.apikey"
	keyLibraryDir    = "library.dir"
	keyLibraryMode   = "library.import_mode"

	keyAutomationEnabled  = "automation.enabled"
	keyAutomationPinDelay = "automation.pin_delay_hours"

	keyNotifyDiscordURL = "notify.discord.url"
	keyNotifyWebhookURL = "notify.webhook.url"
	keyNotifyNtfyServer = "notify.ntfy.server"
	keyNotifyNtfyTopic  = "notify.ntfy.topic"
	keyNotifyNtfyToken  = "notify.ntfy.token"
)

// notifyToggleKey names one adapter's per-event switch, e.g.
// notify.discord.on_imported. Absent means enabled; only "false" disables.
func notifyToggleKey(adapter, event string) string {
	return "notify." + adapter + ".on_" + event
}

// Defaults for values that must never be empty.
const (
	defaultCategory    = "transpondarr"
	defaultIndexerName = "torznab"
	defaultImportMode  = "auto"
	defaultNtfyServer  = "https://ntfy.sh"
)

// DownloadConfig is the qBittorrent client configuration.
type DownloadConfig struct {
	URL      string
	User     string
	Password string
	Category string
}

// IndexerConfig is the Torznab indexer configuration.
type IndexerConfig struct {
	Name   string
	URL    string
	APIKey string
}

// LibraryConfig is the library import target configuration.
type LibraryConfig struct {
	Dir  string
	Mode string // auto | hardlink | copy
}

// AutomationConfig is the global automation policy: the kill switch every
// unattended job reads, and the pinned-group wait for series not overriding it.
type AutomationConfig struct {
	Enabled       bool
	PinDelayHours int
}

// NotifyEvents is one adapter's per-event switches (the Sonarr model): a
// configured adapter defaults to all-on, each kind toggleable off.
type NotifyEvents struct {
	Grabbed     bool
	Imported    bool
	Stuck       bool
	GrabFailed  bool
	SeriesAdded bool
}

// NotifyConfig is the notification adapters' configuration. An adapter is
// configured when its URL (Discord/webhook) or topic (ntfy) is non-empty.
type NotifyConfig struct {
	DiscordURL    string
	DiscordEvents NotifyEvents
	WebhookURL    string
	WebhookEvents NotifyEvents
	NtfyServer    string
	NtfyTopic     string
	NtfyToken     string
	NtfyEvents    NotifyEvents
}

func (c *DownloadConfig) applyDefaults() {
	if strings.TrimSpace(c.Category) == "" {
		c.Category = defaultCategory
	}
}

func (c *IndexerConfig) applyDefaults() {
	if strings.TrimSpace(c.Name) == "" {
		c.Name = defaultIndexerName
	}
}

func (c *LibraryConfig) applyDefaults() {
	if strings.TrimSpace(c.Mode) == "" {
		c.Mode = defaultImportMode
	}
}

func (c *NotifyConfig) applyDefaults() {
	if strings.TrimSpace(c.NtfyServer) == "" {
		c.NtfyServer = defaultNtfyServer
	}
}

// Snapshot is the effective configuration for display (secrets not masked here;
// the HTTP layer masks them before serialization).
type Snapshot struct {
	Download   DownloadConfig
	Indexer    IndexerConfig
	Library    LibraryConfig
	Automation AutomationConfig
	Notify     NotifyConfig
	APIKey     string
	DataDir    string
	DBPath     string
	Addr       string
}

// state is the effective configuration as an immutable value. It is only ever
// replaced wholesale via cur.Store, never mutated in place, so any reader that
// loads it sees a consistent snapshot without locking.
type state struct {
	dl  DownloadConfig
	idx IndexerConfig
	lib LibraryConfig
	ntf NotifyConfig

	// Parsed once at startup: settings are strings at rest, but every read of
	// these is on a job tick, so the typed form lives here. Hours, not a
	// duration: the stored unit is what Snapshot reports, so nothing truncates.
	automationEnabled bool
	pinDelayHours     int

	apiKey string

	dataDir string
	dbPath  string
	addr    string
}

// Service resolves and mutates the effective runtime configuration.
type Service struct {
	// updateMu serializes the read-modify-write of an update (resolve blank
	// secrets → persist → swap) so two concurrent saves can't clobber each other.
	// Reads need no lock: they load the immutable state pointer via cur.
	updateMu sync.Mutex
	cur      atomic.Pointer[state]

	store *store.Store
	reg   *clients.Registry
	log   *slog.Logger
}

// New builds the service from the environment baseline, overlays any persisted
// overrides, and populates the registry with the resulting clients.
func New(ctx context.Context, st *store.Store, base *config.Config, reg *clients.Registry, log *slog.Logger) (*Service, error) {
	cfg := &state{
		apiKey:  base.APIKey,
		dataDir: base.DataDir,
		dbPath:  base.DBPath,
		addr:    base.Addr,
		dl:      DownloadConfig{URL: base.QbitURL, User: base.QbitUser, Password: base.QbitPassword, Category: base.QbitCategory},
		idx:     IndexerConfig{Name: base.TorznabName, URL: base.TorznabURL, APIKey: base.TorznabAPIKey},
		lib:     LibraryConfig{Dir: base.LibraryDir, Mode: base.ImportMode},
	}

	rows, err := st.Q.ListSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("load settings: %w", err)
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		m[r.Key] = r.Value
	}
	overlay(m, keyQbitURL, &cfg.dl.URL)
	overlay(m, keyQbitUser, &cfg.dl.User)
	overlay(m, keyQbitPassword, &cfg.dl.Password)
	overlay(m, keyQbitCategory, &cfg.dl.Category)
	overlay(m, keyTorznabName, &cfg.idx.Name)
	overlay(m, keyTorznabURL, &cfg.idx.URL)
	overlay(m, keyTorznabAPIKey, &cfg.idx.APIKey)
	overlay(m, keyLibraryDir, &cfg.lib.Dir)
	overlay(m, keyLibraryMode, &cfg.lib.Mode)
	overlay(m, keyNotifyDiscordURL, &cfg.ntf.DiscordURL)
	overlay(m, keyNotifyWebhookURL, &cfg.ntf.WebhookURL)
	overlay(m, keyNotifyNtfyServer, &cfg.ntf.NtfyServer)
	overlay(m, keyNotifyNtfyTopic, &cfg.ntf.NtfyTopic)
	overlay(m, keyNotifyNtfyToken, &cfg.ntf.NtfyToken)
	cfg.ntf.DiscordEvents = overlayToggles(m, "discord")
	cfg.ntf.WebhookEvents = overlayToggles(m, "webhook")
	cfg.ntf.NtfyEvents = overlayToggles(m, "ntfy")

	automation, pinDelay := base.AutomationEnabled, base.PinDelayHours
	overlay(m, keyAutomationEnabled, &automation)
	overlay(m, keyAutomationPinDelay, &pinDelay)

	cfg.dl.applyDefaults()
	cfg.idx.applyDefaults()
	cfg.lib.applyDefaults()
	cfg.ntf.applyDefaults()
	cfg.automationEnabled = parseBool(automation, log)
	cfg.pinDelayHours = parseHours(pinDelay, log)

	s := &Service{store: st, reg: reg, log: log}
	s.cur.Store(cfg)

	reg.SetDownload(buildDownload(cfg.dl))
	reg.SetIndexer(buildIndexer(cfg.idx))
	reg.SetLibrary(buildLibrary(cfg.lib))
	reg.SetNotify(s.buildNotify(cfg.ntf))
	return s, nil
}

// overlayToggles reads one adapter's per-event switches: an absent key is
// enabled, so a freshly configured adapter notifies on everything.
func overlayToggles(m map[string]string, adapter string) NotifyEvents {
	on := func(event string) bool {
		v, ok := m[notifyToggleKey(adapter, event)]
		if !ok {
			return true
		}
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		return err != nil || b
	}
	return NotifyEvents{
		Grabbed:     on("grabbed"),
		Imported:    on("imported"),
		Stuck:       on("stuck"),
		GrabFailed:  on("grab_failed"),
		SeriesAdded: on("series_added"),
	}
}

func overlay(m map[string]string, key string, dst *string) {
	if v, ok := m[key]; ok {
		*dst = v
	}
}

// Snapshot returns the current effective configuration.
func (s *Service) Snapshot() Snapshot {
	c := s.cur.Load()
	return Snapshot{
		Download:   c.dl,
		Indexer:    c.idx,
		Library:    c.lib,
		Automation: AutomationConfig{Enabled: c.automationEnabled, PinDelayHours: c.pinDelayHours},
		Notify:     c.ntf,
		APIKey:     c.apiKey,
		DataDir:    c.dataDir,
		DBPath:     c.dbPath,
		Addr:       c.addr,
	}
}

// DownloadCategory returns the category applied to grabbed torrents.
func (s *Service) DownloadCategory() string { return s.cur.Load().dl.Category }

// AutomationEnabled reports whether unattended work may act. Every job reads it
// per run, so UpdateAutomation takes effect on the next tick without a restart.
func (s *Service) AutomationEnabled() bool { return s.cur.Load().automationEnabled }

// PinDelayDefault is how long the sweep waits for a series' pinned group before
// taking another group's release, for series that do not override it.
func (s *Service) PinDelayDefault() time.Duration {
	return domain.PinDelay(int64(s.cur.Load().pinDelayHours))
}

// parseBool and parseHours degrade a bad value to the zero default rather than
// failing startup: one mistyped setting must not take the daemon down.
func parseBool(v string, log *slog.Logger) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil && strings.TrimSpace(v) != "" {
		log.Warn("ignoring unparseable automation setting", "key", keyAutomationEnabled, "value", v)
	}
	return err == nil && b
}

func parseHours(v string, log *slog.Logger) int {
	h, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil && strings.TrimSpace(v) != "" {
		log.Warn("ignoring unparseable automation setting", "key", keyAutomationPinDelay, "value", v)
	}
	if err != nil {
		return 0
	}
	// Clamped, not stored raw: an hour count past the duration ceiling wraps
	// int64 when multiplied out and turns the longest possible wait into none.
	if h > domain.MaxPinDelayHours {
		log.Warn("clamping automation setting to its maximum",
			"key", keyAutomationPinDelay, "value", v, "max_hours", domain.MaxPinDelayHours)
	}
	return int(domain.ClampPinDelayHours(int64(h)))
}

// APIKey returns the current API key. The auth middleware reads this on each
// request so a regenerated key takes effect without a restart.
func (s *Service) APIKey() string { return s.cur.Load().apiKey }

// RegenerateAPIKey issues, persists, and swaps in a new API key, returning it.
// Any client still using the old key must re-authenticate.
func (s *Service) RegenerateAPIKey(ctx context.Context) (string, error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	key, err := auth.NewAPIKey()
	if err != nil {
		return "", err
	}
	if err := s.store.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: APIKeySettingKey, Value: key}); err != nil {
		return "", fmt.Errorf("persist api key: %w", err)
	}
	// Copy-on-write: mutate a copy and swap the pointer, never the live state
	// (see the state type). The other Update* methods follow the same pattern.
	next := *s.cur.Load()
	next.apiKey = key
	s.cur.Store(&next)
	return key, nil
}

// UpdateDownload saves the qBittorrent config and swaps in the rebuilt client.
// An empty Password keeps the stored one; an empty URL disables the client.
// Persisting before the swap leaves live state untouched if the save fails.
func (s *Service) UpdateDownload(ctx context.Context, in DownloadConfig) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	cur := s.cur.Load()
	if in.Password == "" {
		in.Password = cur.dl.Password
	}
	in.applyDefaults()

	if err := s.persist(ctx, map[string]string{
		keyQbitURL:      in.URL,
		keyQbitUser:     in.User,
		keyQbitPassword: in.Password,
		keyQbitCategory: in.Category,
	}); err != nil {
		return err
	}

	next := *cur
	next.dl = in
	s.cur.Store(&next)
	s.reg.SetDownload(buildDownload(in))
	return nil
}

// UpdateIndexer saves the Torznab config and swaps in the rebuilt indexer.
// An empty APIKey keeps the stored one; an empty URL disables the indexer.
// Persisting before the swap leaves live state untouched if the save fails.
func (s *Service) UpdateIndexer(ctx context.Context, in IndexerConfig) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	cur := s.cur.Load()
	if in.APIKey == "" {
		in.APIKey = cur.idx.APIKey
	}
	in.applyDefaults()

	if err := s.persist(ctx, map[string]string{
		keyTorznabName:   in.Name,
		keyTorznabURL:    in.URL,
		keyTorznabAPIKey: in.APIKey,
	}); err != nil {
		return err
	}

	next := *cur
	next.idx = in
	s.cur.Store(&next)
	s.reg.SetIndexer(buildIndexer(in))
	return nil
}

// UpdateLibrary saves the library config and swaps in the rebuilt target.
// An empty Dir disables import. Persisting before the swap leaves live state
// untouched if the save fails.
func (s *Service) UpdateLibrary(ctx context.Context, in LibraryConfig) error {
	in.applyDefaults()
	if !ValidImportMode(in.Mode) {
		return fmt.Errorf("invalid import mode %q (want auto, hardlink or copy)", in.Mode)
	}

	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	if err := s.persist(ctx, map[string]string{
		keyLibraryDir:  in.Dir,
		keyLibraryMode: in.Mode,
	}); err != nil {
		return err
	}

	next := *s.cur.Load()
	next.lib = in
	s.cur.Store(&next)
	s.reg.SetLibrary(buildLibrary(in))
	return nil
}

// UpdateAutomation saves the global automation policy. Nothing is rebuilt or
// torn down: the jobs stay registered and read the switch per run, so disabling
// and re-enabling are both restart-free. The clamped hour count is what gets
// persisted, so a reload agrees with the live state rather than re-clamping.
func (s *Service) UpdateAutomation(ctx context.Context, in AutomationConfig) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	hours := int(domain.ClampPinDelayHours(int64(in.PinDelayHours)))
	if err := s.persist(ctx, map[string]string{
		keyAutomationEnabled:  strconv.FormatBool(in.Enabled),
		keyAutomationPinDelay: strconv.Itoa(hours),
	}); err != nil {
		return err
	}

	next := *s.cur.Load()
	next.automationEnabled = in.Enabled
	next.pinDelayHours = hours
	s.cur.Store(&next)
	return nil
}

// UpdateNotify saves the notification config and swaps in the rebuilt
// dispatcher. An empty NtfyToken keeps the stored one; clearing every adapter
// drops the dispatcher to nil. Persisting before the swap leaves live state
// untouched if the save fails.
func (s *Service) UpdateNotify(ctx context.Context, in NotifyConfig) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	cur := s.cur.Load()
	if in.NtfyToken == "" {
		in.NtfyToken = cur.ntf.NtfyToken
	}
	in.applyDefaults()

	kv := map[string]string{
		keyNotifyDiscordURL: in.DiscordURL,
		keyNotifyWebhookURL: in.WebhookURL,
		keyNotifyNtfyServer: in.NtfyServer,
		keyNotifyNtfyTopic:  in.NtfyTopic,
		keyNotifyNtfyToken:  in.NtfyToken,
	}
	persistToggles(kv, "discord", in.DiscordEvents)
	persistToggles(kv, "webhook", in.WebhookEvents)
	persistToggles(kv, "ntfy", in.NtfyEvents)
	if err := s.persist(ctx, kv); err != nil {
		return err
	}

	next := *cur
	next.ntf = in
	s.cur.Store(&next)
	s.reg.SetNotify(s.buildNotify(in))
	return nil
}

func persistToggles(kv map[string]string, adapter string, ev NotifyEvents) {
	kv[notifyToggleKey(adapter, "grabbed")] = strconv.FormatBool(ev.Grabbed)
	kv[notifyToggleKey(adapter, "imported")] = strconv.FormatBool(ev.Imported)
	kv[notifyToggleKey(adapter, "stuck")] = strconv.FormatBool(ev.Stuck)
	kv[notifyToggleKey(adapter, "grab_failed")] = strconv.FormatBool(ev.GrabFailed)
	kv[notifyToggleKey(adapter, "series_added")] = strconv.FormatBool(ev.SeriesAdded)
}

func notifyKinds(ev NotifyEvents) map[notify.Kind]bool {
	return map[notify.Kind]bool{
		notify.KindGrabbed:     ev.Grabbed,
		notify.KindImported:    ev.Imported,
		notify.KindImportStuck: ev.Stuck,
		notify.KindGrabFailed:  ev.GrabFailed,
		notify.KindSeriesAdded: ev.SeriesAdded,
	}
}

// buildNotify returns a dispatcher over the configured adapters, or nil when
// none is — a concrete *Dispatcher, so nil-checking it never hits the typed-nil
// interface gotcha buildLibrary documents.
func (s *Service) buildNotify(c NotifyConfig) *notify.Dispatcher {
	var routes []notify.Route
	if strings.TrimSpace(c.DiscordURL) != "" {
		routes = append(routes, notify.Route{Notifier: discord.New(c.DiscordURL), Kinds: notifyKinds(c.DiscordEvents)})
	}
	if strings.TrimSpace(c.WebhookURL) != "" {
		routes = append(routes, notify.Route{Notifier: webhook.New(c.WebhookURL), Kinds: notifyKinds(c.WebhookEvents)})
	}
	if strings.TrimSpace(c.NtfyTopic) != "" {
		cc := c
		cc.applyDefaults()
		routes = append(routes, notify.Route{Notifier: ntfy.New(cc.NtfyServer, cc.NtfyTopic, cc.NtfyToken), Kinds: notifyKinds(c.NtfyEvents)})
	}
	if len(routes) == 0 {
		return nil
	}
	return notify.NewDispatcher(s.log, routes...)
}

// TestNotifyDiscord sends a test event to the given (unsaved) Discord webhook.
func (s *Service) TestNotifyDiscord(ctx context.Context, in NotifyConfig) error {
	if strings.TrimSpace(in.DiscordURL) == "" {
		return errors.New("a Discord webhook URL is required")
	}
	return discord.New(in.DiscordURL).Send(ctx, notify.Event{Kind: notify.KindTest})
}

// TestNotifyWebhook sends a test event to the given (unsaved) webhook URL.
func (s *Service) TestNotifyWebhook(ctx context.Context, in NotifyConfig) error {
	if strings.TrimSpace(in.WebhookURL) == "" {
		return errors.New("a webhook URL is required")
	}
	return webhook.New(in.WebhookURL).Send(ctx, notify.Event{Kind: notify.KindTest})
}

// TestNotifyNtfy sends a test event to the given (unsaved) ntfy topic, filling
// in the stored token when the field is blank.
func (s *Service) TestNotifyNtfy(ctx context.Context, in NotifyConfig) error {
	if strings.TrimSpace(in.NtfyTopic) == "" {
		return errors.New("an ntfy topic is required")
	}
	if in.NtfyToken == "" {
		in.NtfyToken = s.cur.Load().ntf.NtfyToken
	}
	in.applyDefaults()
	return ntfy.New(in.NtfyServer, in.NtfyTopic, in.NtfyToken).Send(ctx, notify.Event{Kind: notify.KindTest})
}

// TestDownload verifies connectivity for the given (unsaved) values, filling in
// the stored password when the field is blank.
func (s *Service) TestDownload(ctx context.Context, in DownloadConfig) error {
	if strings.TrimSpace(in.URL) == "" {
		return errors.New("a qBittorrent URL is required")
	}
	if in.Password == "" {
		in.Password = s.cur.Load().dl.Password
	}
	return qbittorrent.New(in.URL, in.User, in.Password).Test(ctx)
}

// TestIndexer verifies the given (unsaved) indexer values by issuing a probe
// search, filling in the stored API key when the field is blank.
func (s *Service) TestIndexer(ctx context.Context, in IndexerConfig) error {
	if strings.TrimSpace(in.URL) == "" {
		return errors.New("a Torznab URL is required")
	}
	if in.APIKey == "" {
		in.APIKey = s.cur.Load().idx.APIKey
	}
	in.applyDefaults()
	_, err := torznab.New(in.Name, in.URL, in.APIKey).Search(ctx, indexer.Query{Term: "test"})
	return err
}

// TestLibrary verifies the library directory exists and is writable.
func (s *Service) TestLibrary(_ context.Context, in LibraryConfig) error {
	dir := strings.TrimSpace(in.Dir)
	if dir == "" {
		return errors.New("a library directory is required")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("cannot access %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}
	probe := filepath.Join(dir, ".transpondarr-write-test")
	f, err := os.Create(probe)
	if err != nil {
		return fmt.Errorf("%q is not writable: %w", dir, err)
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return nil
}

// persist writes each key/value to the settings table in a single transaction,
// so a mid-write failure never leaves a section half-updated (e.g. a new URL
// paired with an old password).
func (s *Service) persist(ctx context.Context, kv map[string]string) error {
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin settings tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit
	q := s.store.Q.WithTx(tx)
	for k, v := range kv {
		if err := q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			return fmt.Errorf("persist %s: %w", k, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit settings: %w", err)
	}
	return nil
}

// ValidImportMode reports whether m is a recognised import mode.
func ValidImportMode(m string) bool {
	switch m {
	case "auto", "hardlink", "copy":
		return true
	default:
		return false
	}
}

func buildDownload(c DownloadConfig) download.Client {
	if strings.TrimSpace(c.URL) == "" {
		return nil
	}
	return qbittorrent.New(c.URL, c.User, c.Password)
}

func buildIndexer(c IndexerConfig) indexer.Indexer {
	if strings.TrimSpace(c.URL) == "" {
		return nil
	}
	c.applyDefaults()
	return torznab.New(c.Name, c.URL, c.APIKey)
}

// buildLibrary returns a library.Target (interface) so an unconfigured library
// is a true nil interface — returning a typed nil *mediaserver.Target would read
// as non-nil through the interface and defeat the importer's nil check.
func buildLibrary(c LibraryConfig) library.Target {
	if strings.TrimSpace(c.Dir) == "" {
		return nil
	}
	c.applyDefaults()
	return mediaserver.New(c.Dir, c.Mode)
}
