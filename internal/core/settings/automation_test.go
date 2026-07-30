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
