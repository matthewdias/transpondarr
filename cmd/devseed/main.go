// Command devseed builds a local development database and serves the stubs that
// stand in for AniList and a Torznab indexer, so every screen has something on
// it with no network access and no real credentials.
//
// It is a development tool: it is never built into transpondarrd, and nothing it
// writes is meant to reach a real install.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/devdata"
	"github.com/matthewdias/transpondarr/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "devseed:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		reset        = flag.Bool("reset", false, "wipe the existing database before seeding")
		force        = flag.Bool("force", false, "allow --reset to wipe a database outside the working directory")
		seedOnly     = flag.Bool("seed-only", false, "seed and exit instead of serving the stubs")
		torznabAddr  = flag.String("torznab-addr", "127.0.0.1:0", "listen address for the Torznab stub")
		anilistAddr  = flag.String("anilist-addr", "127.0.0.1:0", "listen address for the AniList stub")
		rngSeed      = flag.Int64("rng-seed", 1, "seed for the generated part of the fixtures")
		writeEnvFile = flag.Bool("write-env-local", false, "write the stub endpoints to ./.env.local instead of printing them")
	)
	flag.Parse()

	if err := validateFlags(*seedOnly, *writeEnvFile); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}
	if err := devdata.CheckTarget(cfg.DBPath, wd, *reset, *force); err != nil {
		return err
	}
	if *writeEnvFile {
		if err := devdata.CheckEnvWritable(envLocalName); err != nil {
			return fmt.Errorf("%w (merge the endpoints by hand instead)", err)
		}
	}
	if *reset {
		if err := removeDatabase(cfg.DBPath); err != nil {
			return err
		}
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = st.DB.Close() }()

	now := time.Now()
	if err := devdata.Seed(context.Background(), st, devdata.Options{Now: now, RNGSeed: *rngSeed}); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	fmt.Printf("seeded %s (rng seed %d)\n", cfg.DBPath, *rngSeed)
	if *seedOnly {
		return nil
	}

	torznabURL, stopTorznab, err := serve(*torznabAddr, devdata.TorznabHandler(now, *rngSeed), "/api")
	if err != nil {
		return err
	}
	defer stopTorznab()
	anilistURL, stopAnilist, err := serve(*anilistAddr, devdata.AnilistHandler(now), "/graphql")
	if err != nil {
		return err
	}
	defer stopAnilist()

	env := stubEnv(torznabURL, anilistURL)
	if *writeEnvFile {
		if err := writeEnvLocal(env); err != nil {
			return err
		}
		fmt.Println("wrote .env.local")
	} else {
		fmt.Printf("\nstubs are up. Put these where this checkout can see them (.env.local, not .env):\n\n%s\n", env)
	}
	fmt.Println("ctrl-c to stop")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return nil
}

// stubEnv is the block the command prints or writes. It blanks the download
// client deliberately, because .env.local outranks .env: otherwise the importer
// scans a real qBittorrent, finds none of the seeded info hashes, and fails
// every seeded grab row after the five-minute grace period.
func stubEnv(torznabURL, anilistURL string) string {
	return fmt.Sprintf("TRANSPONDARR_TORZNAB_URL=%s\nTRANSPONDARR_TORZNAB_APIKEY=dev\nTRANSPONDARR_ANILIST_ENDPOINT=%s\nTRANSPONDARR_QBIT_URL=\n", torznabURL, anilistURL)
}

// serve binds and reports the address it got, because port 0 is what lets two
// worktrees run their stubs at once.
func serve(addr string, h http.Handler, path string) (string, func(), error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "devseed: serve:", err)
		}
	}()
	url := "http://" + ln.Addr().String() + path
	return url, func() { _ = srv.Close() }, nil
}

// removeDatabase takes the sidecar files too: leaving a -wal behind would replay
// the wiped database's tail into the fresh one.
func removeDatabase(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	return nil
}

const envLocalName = ".env.local"

// validateFlags rejects --seed-only with --write-env-local: there are no stub
// endpoints to write when the stubs never start, so it would silently do nothing.
func validateFlags(seedOnly, writeEnvFile bool) error {
	if seedOnly && writeEnvFile {
		return errors.New("--write-env-local needs the stubs, so it cannot be combined with --seed-only")
	}
	return nil
}

// writeEnvLocal re-checks before writing, the up-front check having been made
// before the stubs bound their ports.
func writeEnvLocal(env string) error {
	if err := devdata.CheckEnvWritable(envLocalName); err != nil {
		return fmt.Errorf("%w; merge these by hand:\n\n%s", err, env)
	}
	header := "# Written by devseed. Per-checkout overrides; never committed.\n"
	return os.WriteFile(filepath.Clean(envLocalName), []byte(header+strings.TrimLeft(env, "\n")), 0o600)
}
