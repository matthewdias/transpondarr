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
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// newServiceWith builds a service over a store pre-seeded with the given
// persisted overrides, on top of the given env baseline.
func newServiceWith(t *testing.T, base *config.Config, persisted map[string]string) *Service {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })
	for k, v := range persisted {
		if err := st.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v}); err != nil {
			t.Fatalf("seed setting %s: %v", k, err)
		}
	}
	svc, err := New(ctx, st, base, clients.New(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc
}

// Automation ships off: an install that has never configured an indexer or a
// download client must not start grabbing on its own.
func TestAutomationDefaultsOff(t *testing.T) {
	svc := newServiceWith(t, &config.Config{}, nil)
	if svc.AutomationEnabled() {
		t.Error("automation is on by default; it must ship off until configured")
	}
	if d := svc.PinDelayDefault(); d != 0 {
		t.Errorf("default pin delay = %v, want 0 (no wait)", d)
	}
}

func TestAutomationReadsEnvBaseline(t *testing.T) {
	svc := newServiceWith(t, &config.Config{AutomationEnabled: "true", PinDelayHours: "6"}, nil)
	if !svc.AutomationEnabled() {
		t.Error("automation off despite the env baseline enabling it")
	}
	if got := svc.PinDelayDefault(); got != 6*time.Hour {
		t.Errorf("pin delay = %v, want 6h", got)
	}
}

// A persisted override wins over the env baseline, matching every other setting.
func TestAutomationPersistedOverrideWinsOverEnv(t *testing.T) {
	svc := newServiceWith(t, &config.Config{AutomationEnabled: "true", PinDelayHours: "6"}, map[string]string{
		keyAutomationEnabled:  "false",
		keyAutomationPinDelay: "12",
	})
	if svc.AutomationEnabled() {
		t.Error("persisted override did not turn automation off")
	}
	if got := svc.PinDelayDefault(); got != 12*time.Hour {
		t.Errorf("pin delay = %v, want the persisted 12h", got)
	}
}

// #116: the toggle carries a third state. The stored value stays one key whose
// domain widened, so every legacy "true"/"false" — persisted or env — must keep
// meaning what it always did.
func TestAutomationModeParsing(t *testing.T) {
	for _, tc := range []struct {
		stored     string
		want       AutomationMode
		enabled    bool
		notifyOnly bool
	}{
		{"on", AutomationOn, true, false},
		{"off", AutomationOff, false, false},
		{"notify_only", AutomationNotifyOnly, true, true},
		{"true", AutomationOn, true, false},
		{"false", AutomationOff, false, false},
		{"yes-please", AutomationOff, false, false},
	} {
		svc := newServiceWith(t, &config.Config{}, map[string]string{keyAutomationEnabled: tc.stored})
		if got := svc.Snapshot().Automation.Mode; got != tc.want {
			t.Errorf("stored %q: mode = %q, want %q", tc.stored, got, tc.want)
		}
		if got := svc.AutomationEnabled(); got != tc.enabled {
			t.Errorf("stored %q: AutomationEnabled = %t, want %t", tc.stored, got, tc.enabled)
		}
		if got := svc.NotifyOnly(); got != tc.notifyOnly {
			t.Errorf("stored %q: NotifyOnly = %t, want %t", tc.stored, got, tc.notifyOnly)
		}
	}
}

// Notify-only through the env baseline, for installs configured entirely by env.
func TestAutomationNotifyOnlyFromEnv(t *testing.T) {
	svc := newServiceWith(t, &config.Config{AutomationEnabled: "notify_only"}, nil)
	if !svc.AutomationEnabled() || !svc.NotifyOnly() {
		t.Error("notify_only env baseline did not yield an enabled, notify-only service")
	}
}

func TestUpdateAutomationRejectsUnknownMode(t *testing.T) {
	svc := newServiceWith(t, &config.Config{}, nil)
	if err := svc.UpdateAutomation(context.Background(), AutomationConfig{Mode: "loud"}); err == nil {
		t.Error("an unknown automation mode was accepted")
	}
}

// An unparseable value degrades to the default rather than failing startup: a
// typo in one setting must not take the whole daemon down.
func TestAutomationUnparseableValueDegradesToDefault(t *testing.T) {
	svc := newServiceWith(t, &config.Config{}, map[string]string{
		keyAutomationEnabled:  "yes-please",
		keyAutomationPinDelay: "soon",
	})
	if svc.AutomationEnabled() {
		t.Error("an unparseable automation flag must degrade to off")
	}
	if got := svc.PinDelayDefault(); got != 0 {
		t.Errorf("pin delay = %v, want the 0 default", got)
	}
}

// A negative delay is nonsense, not a time machine.
func TestAutomationNegativePinDelayDegradesToZero(t *testing.T) {
	svc := newServiceWith(t, &config.Config{PinDelayHours: "-3"}, nil)
	if got := svc.PinDelayDefault(); got != 0 {
		t.Errorf("pin delay = %v, want 0", got)
	}
}

// Multiplied out raw, an hour count this size wraps int64 into a negative
// duration, which every caller reads as "no delay at all".
func TestAutomationOverlongPinDelayClamps(t *testing.T) {
	svc := newServiceWith(t, &config.Config{PinDelayHours: "3000000"}, nil)
	want := domain.MaxPinDelayHours * time.Hour
	if got := svc.PinDelayDefault(); got != want {
		t.Errorf("pin delay = %v, want the %v ceiling", got, want)
	}
}

// #102's acceptance criterion: the sweep reads the switch per run, so a save has
// to be visible to the very next read without anything being rebuilt.
func TestUpdateAutomationAppliesLive(t *testing.T) {
	ctx := context.Background()
	svc := newServiceWith(t, &config.Config{}, nil)

	if err := svc.UpdateAutomation(ctx, AutomationConfig{Mode: AutomationOn, PinDelayHours: 6}); err != nil {
		t.Fatalf("enable automation: %v", err)
	}
	if !svc.AutomationEnabled() {
		t.Error("automation still off after being enabled")
	}
	if got := svc.PinDelayDefault(); got != 6*time.Hour {
		t.Errorf("pin delay = %v, want 6h", got)
	}

	if err := svc.UpdateAutomation(ctx, AutomationConfig{Mode: AutomationNotifyOnly, PinDelayHours: 6}); err != nil {
		t.Fatalf("switch to notify-only: %v", err)
	}
	if !svc.AutomationEnabled() || !svc.NotifyOnly() {
		t.Error("notify-only did not read as enabled-but-rehearsing on the next read")
	}

	if err := svc.UpdateAutomation(ctx, AutomationConfig{Mode: AutomationOff, PinDelayHours: 6}); err != nil {
		t.Fatalf("disable automation: %v", err)
	}
	if svc.AutomationEnabled() || svc.NotifyOnly() {
		t.Error("automation still on after being disabled")
	}
}

func TestUpdateAutomationPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.DB.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := New(ctx, st, &config.Config{}, clients.New(), log)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if err := svc.UpdateAutomation(ctx, AutomationConfig{Mode: AutomationNotifyOnly, PinDelayHours: 9}); err != nil {
		t.Fatalf("update automation: %v", err)
	}

	// The env baseline still says off; only the persisted override carries the save.
	restarted, err := New(ctx, st, &config.Config{}, clients.New(), log)
	if err != nil {
		t.Fatalf("restart service: %v", err)
	}
	if !restarted.AutomationEnabled() || !restarted.NotifyOnly() {
		t.Error("notify-only lost after a restart; the save did not persist")
	}
	if got := restarted.PinDelayDefault(); got != 9*time.Hour {
		t.Errorf("pin delay after restart = %v, want 9h", got)
	}
}

// The HTTP layer hands through whatever a client sent, so the clamp has to hold
// on the write path too, not only when parsing a stored value.
func TestUpdateAutomationClampsPinDelay(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		hours int
		want  time.Duration
	}{
		{-3, 0},
		{0, 0},
		{6, 6 * time.Hour},
		{3_000_000, domain.MaxPinDelayHours * time.Hour},
	} {
		svc := newServiceWith(t, &config.Config{}, nil)
		if err := svc.UpdateAutomation(ctx, AutomationConfig{Mode: AutomationOff, PinDelayHours: tc.hours}); err != nil {
			t.Fatalf("update automation with %d hours: %v", tc.hours, err)
		}
		if got := svc.PinDelayDefault(); got != tc.want {
			t.Errorf("PinDelayHours %d stored as %v, want %v", tc.hours, got, tc.want)
		}
	}
}

// The Settings UI renders from the snapshot, so a control cannot show its own
// current value unless the snapshot carries it.
func TestSnapshotCarriesAutomation(t *testing.T) {
	svc := newServiceWith(t, &config.Config{AutomationEnabled: "true", PinDelayHours: "12"}, nil)
	got := svc.Snapshot().Automation
	if want := (AutomationConfig{Mode: AutomationOn, PinDelayHours: 12}); got != want {
		t.Errorf("snapshot automation = %+v, want %+v", got, want)
	}
}
