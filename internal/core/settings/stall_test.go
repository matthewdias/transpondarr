package settings

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/store"
)

// An install that has never set the key gets the default, not zero -- zero is
// the deliberate "never give up" and must be reachable only on purpose.
func TestStallTimeoutDefaultsWhenUnset(t *testing.T) {
	svc, _, _ := newTestService(t)

	if got, want := svc.StallTimeout(), domain.DefaultStallHours*time.Hour; got != want {
		t.Errorf("StallTimeout() = %v, want %v", got, want)
	}
	if got := svc.Snapshot().Download.StallHours; got != domain.DefaultStallHours {
		t.Errorf("snapshot hours = %d, want %d", got, domain.DefaultStallHours)
	}
}

// The saved value applies live and survives a restart, and 0 disables rather
// than meaning "immediately".
func TestUpdateDownloadPersistsStallHours(t *testing.T) {
	svc, _, st := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://qb:8080", StallHours: 12}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got, want := svc.StallTimeout(), 12*time.Hour; got != want {
		t.Errorf("StallTimeout() = %v, want %v", got, want)
	}
	if got, _ := st.Q.GetSetting(ctx, keyDownloadStallHours); got != "12" {
		t.Errorf("persisted %q, want %q", got, "12")
	}

	if err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://qb:8080", StallHours: 0}); err != nil {
		t.Fatalf("update to zero: %v", err)
	}
	if got := svc.StallTimeout(); got != 0 {
		t.Errorf("StallTimeout() = %v, want 0: zero never gives up", got)
	}

	// A restart reads the stored 0 back as 0, not as the default.
	restarted, err := New(ctx, st, &config.Config{}, clients.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got := restarted.StallTimeout(); got != 0 {
		t.Errorf("StallTimeout() after restart = %v, want 0", got)
	}
}

// An hour count past the duration ceiling would wrap int64 and turn the longest
// possible wait into none, so it is clamped before it is stored.
func TestStallHoursAreClampedBeforePersisting(t *testing.T) {
	svc, _, st := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://qb:8080", StallHours: 999999999}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got, want := svc.StallTimeout(), domain.MaxStallHours*time.Hour; got != want {
		t.Errorf("StallTimeout() = %v, want the clamped %v", got, want)
	}
	// Stored clamped, so a reload agrees with the live state rather than re-clamping.
	if got, _ := st.Q.GetSetting(ctx, keyDownloadStallHours); got != "8760" {
		t.Errorf("persisted %q, want the clamped %q", got, "8760")
	}
}

// The env baseline is the floor the DB override sits on, and an unparseable one
// degrades to the default rather than taking the daemon down.
func TestStallHoursFromEnvBaseline(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want time.Duration
	}{
		{"2", 2 * time.Hour},
		{"0", 0},
		{"nonsense", domain.DefaultStallHours * time.Hour},
	} {
		t.Run(tc.env, func(t *testing.T) {
			store, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() { _ = store.DB.Close() })
			svc, err := New(context.Background(), store, &config.Config{StallTimeoutHours: tc.env},
				clients.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatalf("new service: %v", err)
			}
			if got := svc.StallTimeout(); got != tc.want {
				t.Errorf("StallTimeout() = %v, want %v", got, tc.want)
			}
		})
	}
}
