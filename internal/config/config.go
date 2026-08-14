// Package config loads runtime configuration from the environment with sane
// defaults. Everything is overridable via TRANSPONDARR_* environment variables.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds resolved server configuration.
type Config struct {
	Addr    string // listen address, e.g. ":9797"
	APIKey  string // required for /api/* (except health)
	DataDir string // where the DB and other state live
	DBPath  string // SQLite database path

	// Auth (forms-based login). Username/Password bootstrap the initial
	// admin account on first run when no user is configured yet; Required is
	// "enabled" (always) or "local" (skip auth for local/private addresses).
	AuthUsername string
	AuthPassword string
	AuthRequired string

	// qBittorrent download client (optional). When QbitURL is empty no download
	// client is wired, and the download endpoints report it as unconfigured.
	QbitURL      string // WebUI root, e.g. "http://localhost:8080"
	QbitUser     string
	QbitPassword string
	// QbitCategory tags Transpondarr-managed torrents so they are identifiable in
	// the client UI and routable to a save path. It is best-effort decoration, not
	// identity — the pipeline keys torrents on their info hash (see internal/store
	// grabs), so clients without a category concept still work.
	QbitCategory string

	// Torznab indexer (optional). A single endpoint for v1 — pointing it at a
	// Prowlarr aggregate feed already fans out across many trackers. When
	// TorznabURL is empty no indexer is wired.
	TorznabName   string // display name, defaults to "torznab"
	TorznabURL    string // Torznab API endpoint (Prowlarr/Jackett feed URL)
	TorznabAPIKey string
	// TorznabCategories is the comma-separated Newznab id list sent as cat= on
	// every search and recent-feed request. Empty means no filter.
	TorznabCategories string

	// Library import (optional). When LibraryDir is empty, completed downloads are
	// not imported (the pipeline still grabs). ImportMode is auto|hardlink|copy;
	// auto hardlinks and falls back to a copy across filesystems.
	LibraryDir string
	// LibraryMoviesDir is the root movies are placed into, Plex and Jellyfin both
	// wanting a Movies library separate from Shows. Empty is not a fallback into
	// LibraryDir: a movie import fails until one is set.
	LibraryMoviesDir string
	// LibrarySeriesLayout is season_folders|flat: the path shape inside the series
	// root. It says nothing about movies, which have one shape.
	LibrarySeriesLayout string
	ImportMode          string

	// Automation (the scheduled search sweep). Strings, like every other value
	// here, because the settings layer overlays persisted overrides on top and
	// those live in the DB as text; it parses both at startup.
	// AutomationEnabled ships "false": an install must configure its indexer and
	// download client before anything grabs on its own. Accepts the mode names
	// "off" / "notify_only" / "on" as well as bool spellings (#116).
	AutomationEnabled string
	// PinDelayHours is how long the sweep waits for a title's pinned group before
	// taking another group's release. "0" means no wait.
	PinDelayHours string
}

// Load reads configuration from the environment. A .env file in the working
// directory (if present) is loaded first as a local-dev convenience, without
// overriding variables already set in the real environment.
func Load() (*Config, error) {
	loadDotEnv(".env")

	c := &Config{
		Addr:    getenv("TRANSPONDARR_ADDR", ":9797"),
		DataDir: getenv("TRANSPONDARR_DATA_DIR", "./data"),
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	c.DBPath = getenv("TRANSPONDARR_DB", filepath.Join(c.DataDir, "transpondarr.db"))

	// APIKey may be empty here; when it is, main resolves a persisted key from the
	// store or generates and persists one, so it survives restarts.
	c.APIKey = os.Getenv("TRANSPONDARR_API_KEY")

	c.QbitURL = os.Getenv("TRANSPONDARR_QBIT_URL")
	c.QbitUser = os.Getenv("TRANSPONDARR_QBIT_USER")
	c.QbitPassword = os.Getenv("TRANSPONDARR_QBIT_PASSWORD")
	c.QbitCategory = getenv("TRANSPONDARR_QBIT_CATEGORY", "transpondarr")

	c.TorznabName = getenv("TRANSPONDARR_TORZNAB_NAME", "torznab")
	c.TorznabURL = os.Getenv("TRANSPONDARR_TORZNAB_URL")
	c.TorznabAPIKey = os.Getenv("TRANSPONDARR_TORZNAB_APIKEY")
	c.TorznabCategories = os.Getenv("TRANSPONDARR_TORZNAB_CATEGORIES")

	c.LibraryDir = os.Getenv("TRANSPONDARR_LIBRARY_DIR")
	c.LibraryMoviesDir = os.Getenv("TRANSPONDARR_LIBRARY_MOVIES_DIR")
	c.LibrarySeriesLayout = os.Getenv("TRANSPONDARR_LIBRARY_SERIES_LAYOUT")
	c.ImportMode = getenv("TRANSPONDARR_IMPORT_MODE", "auto")

	c.AutomationEnabled = getenv("TRANSPONDARR_AUTOMATION_ENABLED", "false")
	c.PinDelayHours = getenv("TRANSPONDARR_PIN_DELAY_HOURS", "0")

	c.AuthUsername = os.Getenv("TRANSPONDARR_AUTH_USERNAME")
	c.AuthPassword = os.Getenv("TRANSPONDARR_AUTH_PASSWORD")
	c.AuthRequired = getenv("TRANSPONDARR_AUTH_REQUIRED", "enabled")

	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadDotEnv loads KEY=VALUE pairs from a .env file into the process environment,
// skipping any variable that is already set (the real environment wins). Blank
// lines, "# comments", an optional leading "export", and surrounding quotes are
// tolerated. A missing file is not an error — it is a dev convenience only.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
