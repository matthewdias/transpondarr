package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/airing"
	"github.com/matthewdias/transpondarr/internal/core/auth"
	"github.com/matthewdias/transpondarr/internal/core/browse"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/importer"
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/core/metadata/anilist"
	"github.com/matthewdias/transpondarr/internal/core/metadata/dbcache"
	"github.com/matthewdias/transpondarr/internal/core/refresh"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/privdrop"
	"github.com/matthewdias/transpondarr/internal/server"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
	"github.com/matthewdias/transpondarr/internal/version"
)

// importPollInterval is how often the importer polls the download client for
// completed grabs.
const importPollInterval = 15 * time.Second

// shutdownTimeout is the whole budget for draining the HTTP server and any
// in-flight background work.
const shutdownTimeout = 10 * time.Second

// sessionCleanupInterval is how often expired session rows are swept; daily is
// plenty for a 30-day session TTL.
const sessionCleanupInterval = 24 * time.Hour

// airingSyncInterval ticks often so a newly added series gets its air dates
// within minutes. What each pass actually fetches is throttled by the per-series
// staleness cutoffs, so a tick with nothing due costs one query and no requests.
const airingSyncInterval = 15 * time.Minute

// metadataRefreshInterval ticks on the same rhythm as the airing sync and for
// the same reason: per-series TTL cutoffs decide what actually gets fetched, so
// an idle tick costs one query and no requests.
const metadataRefreshInterval = 15 * time.Minute

// seasonRefreshInterval ticks on the same rhythm again: per-season TTL cutoffs
// decide what actually gets fetched, so an idle tick costs one query and no
// requests.
const seasonRefreshInterval = 15 * time.Minute

func main() {
	// `transpondarrd openapi` prints the OpenAPI spec to stdout and exits — used by
	// the frontend's type generation (`make gen-api`), no server or DB required.
	if len(os.Args) > 1 && os.Args[1] == "openapi" {
		spec, err := server.OpenAPIYAML()
		if err != nil {
			fmt.Fprintln(os.Stderr, "openapi:", err)
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(spec)
		return
	}

	// `transpondarrd healthcheck` probes the running server's public health
	// endpoint and exits 0/1 — the container HEALTHCHECK, since the distroless
	// runtime image has no shell or curl to probe with.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// The Docker image starts as root only so this can fix ownership of /config
	// (Docker creates a missing bind-mount dir root-owned); privileges are shed
	// to PUID/PGID before anything else runs. No-op outside the container.
	if uid, gid, err := privdrop.Drop(cfg.DataDir); err != nil {
		return err
	} else if uid >= 0 {
		logger.Info("fixed data dir ownership and dropped privileges", "uid", uid, "gid", gid)
	}

	if err := ensureWritable(cfg.DataDir); err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	// leaveStoreOpen skips the close when a job overruns the shutdown deadline:
	// SQLite recovers an unclosed store on next open, whereas closing it under
	// the straggling work would fail its writes.
	var leaveStoreOpen bool
	defer func() {
		if !leaveStoreOpen {
			_ = st.DB.Close()
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := resolveAPIKey(ctx, st, cfg, logger); err != nil {
		return err
	}

	// The registry holds the live download/indexer/library clients; the settings
	// service builds them from the env baseline overlaid with persisted overrides
	// and swaps them on a config change. Handlers and the importer read the
	// current client from the registry each time, so edits take effect live.
	reg := clients.New()
	settingsSvc, err := settings.New(ctx, st, cfg, reg, logger)
	if err != nil {
		return err
	}

	// Forms auth: admin account + login sessions (the API key stays for machines).
	authSvc, err := auth.New(ctx, st, cfg)
	if err != nil {
		return err
	}
	// One provider process-wide: it carries the AniList rate limiter, so a second
	// instance would put two independent callers inside one budget.
	provider := metadata.Cached(anilist.New(logger), dbcache.New(st.Q))

	runner := jobs.New(logger)
	runner.Add(jobs.Job{
		Name:       "session-cleanup",
		Interval:   sessionCleanupInterval,
		RunAtStart: true,
		Run:        authSvc.CleanupExpired,
	})
	runner.Add(jobs.Job{
		Name:       "airing-sync",
		Interval:   airingSyncInterval,
		RunAtStart: true,
		Run:        airing.New(st, provider, logger).SyncOnce,
	})
	runner.Add(jobs.Job{
		Name:       "metadata-refresh",
		Interval:   metadataRefreshInterval,
		RunAtStart: true,
		Run:        refresh.New(st, provider, logger).RefreshOnce,
	})
	runner.Add(jobs.Job{
		Name:       "season-refresh",
		Interval:   seasonRefreshInterval,
		RunAtStart: true,
		Run:        browse.New(st, provider, logger).RefreshOnce,
	})
	// The import scan always runs; each scan it reads the current download client
	// and library from the registry and no-ops when either is unconfigured — so
	// enabling both via Settings activates importing without a restart.
	runner.Add(jobs.Job{
		Name:       "import-scan",
		Interval:   importPollInterval,
		RunAtStart: true,
		Run:        importer.New(st, reg, logger).ScanOnce,
	})
	jobsDone := runner.Start(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(cfg, st, logger, provider, reg, settingsSvc, authSvc, runner),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("transpondarr listening", "addr", cfg.Addr, "version", version.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "err", err)
		}
	}()

	<-ctx.Done()
	stop() // a second signal now kills the process instead of being swallowed during the drain

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Drained concurrently so they share the one budget; the store must outlive
	// whatever is still writing to it.
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Shutdown(shutdownCtx) }()
	select {
	case <-jobsDone:
	case <-shutdownCtx.Done():
		leaveStoreOpen = true
		logger.Warn("background work still running at the shutdown deadline; leaving the store open for it",
			"timeout", shutdownTimeout, "still_running", straggling(runner))
	}
	return <-srvErr
}

// straggling names what is still running at the shutdown deadline, so the warning
// says which job to blame rather than only that something overran.
func straggling(runner *jobs.Runner) []string {
	var names []string
	for _, s := range runner.Status() {
		if s.Running {
			names = append(names, s.Name)
		}
	}
	return names
}

// ensureWritable fails fast with an actionable message when the data dir isn't
// writable — the classic case is a root-owned bind mount with the container
// running under --user — instead of a cryptic SQLite error at first write.
func ensureWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".writecheck")
	if err != nil {
		return fmt.Errorf(
			"data dir %s is not writable by uid %d — chown it on the host to this uid, "+
				"or (Docker) drop the --user flag so the container can fix ownership itself: %w",
			dir, os.Getuid(), err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// healthcheck GETs /api/v1/health on the configured listen address and returns
// a process exit code. It reads the same env as the server, so it probes
// whatever port this container's server actually listens on.
func healthcheck() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: bad addr %q: %v\n", cfg.Addr, err)
		return 1
	}
	// A wildcard listen address isn't dialable — probe via loopback.
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/api/v1/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}

// resolveAPIKey fills cfg.APIKey. Precedence: an explicit TRANSPONDARR_API_KEY,
// then a key persisted in the store, then a freshly generated key which is
// persisted (and logged once) so it survives restarts without operator setup.
func resolveAPIKey(ctx context.Context, st *store.Store, cfg *config.Config, logger *slog.Logger) error {
	if cfg.APIKey != "" {
		return nil // explicit env key — operator-managed, not persisted
	}

	switch v, err := st.Q.GetSetting(ctx, settings.APIKeySettingKey); {
	case err == nil && v != "":
		cfg.APIKey = v
		return nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read persisted api key: %w", err)
	}

	key, err := auth.NewAPIKey()
	if err != nil {
		return err
	}
	if err := st.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: settings.APIKeySettingKey, Value: key}); err != nil {
		return fmt.Errorf("persist api key: %w", err)
	}
	cfg.APIKey = key
	// Don't log the key itself — a full-access credential in logs is a leak. It is
	// viewable (authenticated) in Settings → API access, or pin one via the env var.
	logger.Info("generated and persisted a new API key (survives restarts); " +
		"view it in Settings → API access, or set TRANSPONDARR_API_KEY to override")
	return nil
}
