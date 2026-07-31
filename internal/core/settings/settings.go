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
)

// Defaults for values that must never be empty.
const (
	defaultCategory    = "transpondarr"
	defaultIndexerName = "torznab"
	defaultImportMode  = "auto"
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

// Snapshot is the effective configuration for display (secrets not masked here;
// the HTTP layer masks them before serialization).
type Snapshot struct {
	Download DownloadConfig
	Indexer  IndexerConfig
	Library  LibraryConfig
	APIKey   string
	DataDir  string
	DBPath   string
	Addr     string
}

// state is the effective configuration as an immutable value. It is only ever
// replaced wholesale via cur.Store, never mutated in place, so any reader that
// loads it sees a consistent snapshot without locking.
type state struct {
	dl  DownloadConfig
	idx IndexerConfig
	lib LibraryConfig

	// Parsed once at startup: settings are strings at rest, but every read of
	// these is on a job tick, so the typed form lives here.
	automationEnabled bool
	pinDelayDefault   time.Duration

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

	automation, pinDelay := base.AutomationEnabled, base.PinDelayHours
	overlay(m, keyAutomationEnabled, &automation)
	overlay(m, keyAutomationPinDelay, &pinDelay)

	cfg.dl.applyDefaults()
	cfg.idx.applyDefaults()
	cfg.lib.applyDefaults()
	cfg.automationEnabled = parseBool(automation, log)
	cfg.pinDelayDefault = parseHours(pinDelay, log)

	s := &Service{store: st, reg: reg}
	s.cur.Store(cfg)

	reg.SetDownload(buildDownload(cfg.dl))
	reg.SetIndexer(buildIndexer(cfg.idx))
	reg.SetLibrary(buildLibrary(cfg.lib))
	return s, nil
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
		Download: c.dl,
		Indexer:  c.idx,
		Library:  c.lib,
		APIKey:   c.apiKey,
		DataDir:  c.dataDir,
		DBPath:   c.dbPath,
		Addr:     c.addr,
	}
}

// DownloadCategory returns the category applied to grabbed torrents.
func (s *Service) DownloadCategory() string { return s.cur.Load().dl.Category }

// AutomationEnabled reports whether the scheduled search sweep may grab. It is
// read per run, so a future toggle (#102) takes effect without a restart.
func (s *Service) AutomationEnabled() bool { return s.cur.Load().automationEnabled }

// PinDelayDefault is how long the sweep waits for a series' pinned group before
// taking another group's release, for series that do not override it.
func (s *Service) PinDelayDefault() time.Duration { return s.cur.Load().pinDelayDefault }

// parseBool and parseHours degrade a bad value to the zero default rather than
// failing startup: one mistyped setting must not take the daemon down.
func parseBool(v string, log *slog.Logger) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil && strings.TrimSpace(v) != "" {
		log.Warn("ignoring unparseable automation setting", "key", keyAutomationEnabled, "value", v)
	}
	return err == nil && b
}

func parseHours(v string, log *slog.Logger) time.Duration {
	h, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil && strings.TrimSpace(v) != "" {
		log.Warn("ignoring unparseable automation setting", "key", keyAutomationPinDelay, "value", v)
	}
	if err != nil {
		return 0
	}
	// Clamped, not multiplied raw: an hour count past the duration ceiling wraps
	// int64 and turns the longest possible wait into none.
	if h > domain.MaxPinDelayHours {
		log.Warn("clamping automation setting to its maximum",
			"key", keyAutomationPinDelay, "value", v, "max_hours", domain.MaxPinDelayHours)
	}
	return domain.PinDelay(int64(h))
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
