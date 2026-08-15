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
	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/airing"
	"github.com/matthewdias/transpondarr/internal/core/auth"
	"github.com/matthewdias/transpondarr/internal/core/blocklist"
	"github.com/matthewdias/transpondarr/internal/core/browse"
	"github.com/matthewdias/transpondarr/internal/core/catalog"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/importer"
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/core/library"
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

// libraryTidyInterval and libraryStagingMaxAge govern the staging-file sweep (#132).
// It walks the library roots, so it gets its own slow cadence rather than riding
// the 15s import scan; the age is generous because nothing depends on it being
// prompt, and an interrupted transfer's staging file is orphaned for good.
const (
	libraryTidyInterval  = 6 * time.Hour
	libraryStagingMaxAge = 24 * time.Hour
)

// shutdownTimeout is the whole budget for draining the HTTP server and any
// in-flight background work.
const shutdownTimeout = 10 * time.Second

// sessionCleanupInterval is how often expired session rows are swept; daily is
// plenty for a 30-day session TTL.
const sessionCleanupInterval = 24 * time.Hour

// airingSyncInterval ticks often so a newly added title gets its air dates
// within minutes. What each pass actually fetches is throttled by the per-title
// staleness cutoffs, so a tick with nothing due costs one query and no requests.
const airingSyncInterval = 15 * time.Minute

// metadataRefreshInterval ticks on the same rhythm as the airing sync and for
// the same reason: per-title TTL cutoffs decide what actually gets fetched, so
// an idle tick costs one query and no requests.
const metadataRefreshInterval = 15 * time.Minute

// seasonRefreshInterval ticks on the same rhythm again: per-season TTL cutoffs
// decide what actually gets fetched, so an idle tick costs one query and no
// requests.
const seasonRefreshInterval = 15 * time.Minute

// wantedSearchInterval ticks on the same rhythm: the per-title backoff decides
// what a pass actually searches, and it is persisted, so running at start costs
// an idle tick rather than an indexer stampede after a restart loop.
const wantedSearchInterval = 15 * time.Minute

// feedPollInterval matches the sweep's tick but does far more with it: one
// request covers every title at once, where a sweep pass searches five and then
// backs each off for an hour or more. 15 minutes is also the floor indexers ask
// for — Sonarr's RSS sync defaults here and refuses to go below 10.
const feedPollInterval = 15 * time.Minute

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
	// One blocklist for the whole daemon: the sweep and the importer are the two
	// paths that record a failed release, and they must not each hold their own.
	blocklistSvc := blocklist.New(st, logger)

	// One acquire service for both entry points, so they share the one matcher and
	// the one blocklist. A second stateless catalog wrapper over the same shared
	// provider keeps the AniList rate-limit budget single.
	acquireSvc := acquire.New(st, reg, catalog.NewService(st, provider), settingsSvc, logger, blocklistSvc)

	// Both are always registered; each no-ops when automation is off or either
	// client is unconfigured, all read per run — so flipping the Settings toggle or
	// configuring an integration takes effect without a restart. The feed poll is
	// the hot path and the sweep is the safety net: the feed catches anything
	// published between passes for one request, and the sweep covers what scrolled
	// off it, plus every indexer with no feed at all.
	runner.Add(jobs.Job{
		Name:       "wanted-search",
		Interval:   wantedSearchInterval,
		RunAtStart: true,
		Run:        acquireSvc.SweepOnce,
	})
	runner.Add(jobs.Job{
		Name:       "feed-poll",
		Interval:   feedPollInterval,
		RunAtStart: true,
		Run:        acquireSvc.PollFeedOnce,
	})
	// The import scan always runs; each scan it reads the current download client
	// and library from the registry and no-ops when either is unconfigured — so
	// enabling both via Settings activates importing without a restart.
	// Built once and shared with the API: the manual import fix must serialize
	// with the scan, which it does by holding the same importer's mutex.
	importSvc := importer.New(st, reg, logger, blocklistSvc, acquireSvc,
		importer.WithStallPolicy(settingsSvc))
	runner.Add(jobs.Job{
		Name:       "import-scan",
		Interval:   importPollInterval,
		RunAtStart: true,
		Run:        importSvc.ScanOnce,
	})
	// Sweeping the library's staging orphans is an optional target capability, so a
	// target without one is a supported configuration and this tick simply passes.
	runner.Add(jobs.Job{
		Name:       "library-tidy",
		Interval:   libraryTidyInterval,
		RunAtStart: true,
		Run: func(ctx context.Context) error {
			sweeper, ok := reg.Library().(library.StagingSweeper)
			if !ok {
				logger.Debug("library-tidy: the configured library cannot sweep staging files")
				return nil
			}
			removed, err := sweeper.SweepStaging(ctx, libraryStagingMaxAge)
			if removed > 0 {
				logger.Info("library-tidy: removed stale staging files", "count", removed)
			}
			return err
		},
	})
	jobsDone := runner.Start(ctx)

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: server.New(server.Deps{
			Store:     st,
			Logger:    logger,
			Provider:  provider,
			Clients:   reg,
			Settings:  settingsSvc,
			Auth:      authSvc,
			Jobs:      runner,
			Blocklist: blocklistSvc,
			Acquire:   acquireSvc,
			Importer:  importSvc,
		}),
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
