package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

func newTestAuth(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st := coretest.NewStore(t)
	svc, err := New(context.Background(), st, &config.Config{})
	if err != nil {
		t.Fatalf("new auth: %v", err)
	}
	return svc, st
}

func TestVerifyRejectsWrongCredentials(t *testing.T) {
	svc, _ := newTestAuth(t)
	ctx := context.Background()

	if svc.Configured() {
		t.Fatal("service should start unconfigured")
	}
	if err := svc.CreateUser(ctx, "admin", "correcthorse"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if !svc.Configured() {
		t.Fatal("service should be configured after CreateUser")
	}

	if !svc.Verify("admin", "correcthorse") {
		t.Error("correct credentials rejected")
	}
	if svc.Verify("admin", "wrongpassword") {
		t.Error("wrong password accepted")
	}
	if svc.Verify("intruder", "correcthorse") {
		t.Error("wrong username accepted")
	}
}

// New hashes must be argon2id.
func TestHashPasswordUsesArgon2id(t *testing.T) {
	h, err := hashPassword("correcthorse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("expected argon2id hash, got %q", h)
	}
	if !verifyPassword("correcthorse", h) {
		t.Error("argon2id hash failed to verify")
	}
	if verifyPassword("wrong", h) {
		t.Error("argon2id hash verified a wrong password")
	}
}

func TestCreateUserRejectsShortPassword(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.CreateUser(context.Background(), "admin", "short"); err == nil {
		t.Fatalf("expected rejection of password shorter than %d chars", MinPasswordLen)
	}
}

func TestSessionLifecycle(t *testing.T) {
	svc, _ := newTestAuth(t)
	ctx := context.Background()
	if err := svc.CreateUser(ctx, "admin", "correcthorse"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	tok, _, err := svc.CreateSession(ctx, "admin")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if u, ok := svc.ValidateSession(ctx, tok); !ok || u != "admin" {
		t.Fatalf("valid session not recognised: user=%q ok=%v", u, ok)
	}

	svc.DeleteSession(ctx, tok)
	if _, ok := svc.ValidateSession(ctx, tok); ok {
		t.Fatal("session still valid after logout")
	}
}

// The sweep must remove expired rows and leave live sessions alone.
func TestCleanupExpiredRemovesOnlyExpiredSessions(t *testing.T) {
	svc, st := newTestAuth(t)
	ctx := context.Background()

	if err := svc.CreateUser(ctx, "admin", "correcthorse"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	liveTok, _, err := svc.CreateSession(ctx, "admin")
	if err != nil {
		t.Fatalf("create live session: %v", err)
	}
	if err := st.Q.CreateSession(ctx, db.CreateSessionParams{
		Token:     "expired-token",
		Username:  "admin",
		ExpiresAt: store.FormatTimestamp(time.Now().Add(-time.Hour)),
	}); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}

	if err := svc.CleanupExpired(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	var n int
	if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if n != 1 {
		t.Errorf("have %d session rows, want only the live one", n)
	}
	if _, ok := svc.ValidateSession(ctx, liveTok); !ok {
		t.Error("live session did not survive the sweep")
	}
}

// The sweep must report failure rather than swallow it: it is the only thing
// bounding the sessions table on a long-lived instance, so a silent failure
// would reproduce issue #4. The job runner logs what this returns.
func TestCleanupExpiredReportsStoreFailure(t *testing.T) {
	svc, st := newTestAuth(t)
	_ = st.DB.Close()

	if err := svc.CleanupExpired(context.Background()); err == nil {
		t.Fatal("cleanup on a closed store returned nil, want an error")
	}
}

// A password change must revoke existing sessions so other logged-in clients are
// logged out.
func TestChangePasswordRevokesSessions(t *testing.T) {
	svc, _ := newTestAuth(t)
	ctx := context.Background()
	if err := svc.CreateUser(ctx, "admin", "correcthorse"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	tok, _, err := svc.CreateSession(ctx, "admin")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := svc.CreateUser(ctx, "admin", "newpassphrase"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, ok := svc.ValidateSession(ctx, tok); ok {
		t.Fatal("old session survived a password change")
	}
}

// lockProbe records, per settings write, whether the writer lock was free and
// which writer hold covered it. Both matter: a write outside the lock and a write
// under a hold the field update does not share diverge the same way (#258).
type lockProbe struct {
	svc      *Service
	inner    settingsWriter
	unlocked []string
	holds    []uint64
}

func (p *lockProbe) UpsertSetting(ctx context.Context, arg db.UpsertSettingParams) error {
	if p.svc.mu.TryLock() {
		p.svc.mu.Unlock()
		p.unlocked = append(p.unlocked, arg.Key)
	}
	p.holds = append(p.holds, p.svc.mu.holds.Load())
	return p.inner.UpsertSetting(ctx, arg)
}

func newProbedAuth(t *testing.T) (*Service, *store.Store, *lockProbe) {
	t.Helper()
	svc, st := newTestAuth(t)
	probe := &lockProbe{svc: svc, inner: svc.settings}
	svc.settings = probe
	return svc, st, probe
}

// assertOneHold reports that every settings write shared the single writer hold
// still current when the call returned — the hold the field update ran under.
func assertOneHold(t *testing.T, svc *Service, probe *lockProbe) {
	t.Helper()
	if len(probe.unlocked) > 0 {
		t.Fatalf("settings written with the writer lock free: %v", probe.unlocked)
	}
	if len(probe.holds) == 0 {
		t.Fatal("no settings write reached the store")
	}
	current := svc.mu.holds.Load()
	for i, h := range probe.holds {
		if h != current {
			t.Fatalf("settings write %d ran under writer hold %d, but the call ended on hold %d: the store write and the field update are not one uninterrupted hold", i+1, h, current)
		}
	}
}

// The stored mode and the in-memory one must move together: divergence survives
// until the next restart, which resolves to the DB, so a runtime "enabled" over a
// stored "local" silently stops enforcing auth at the next boot.
func TestSetRequiredWritesSettingsUnderLock(t *testing.T) {
	svc, st, probe := newProbedAuth(t)
	ctx := context.Background()

	if err := svc.SetRequired(ctx, RequiredLocal); err != nil {
		t.Fatalf("set required: %v", err)
	}
	assertOneHold(t, svc, probe)
	if got := svc.Required(); got != RequiredLocal {
		t.Fatalf("in-memory mode = %q, want %q", got, RequiredLocal)
	}
	stored, err := st.Q.GetSetting(ctx, keyRequired)
	if err != nil {
		t.Fatalf("read stored mode: %v", err)
	}
	if stored != RequiredLocal {
		t.Fatalf("stored mode = %q, want %q", stored, RequiredLocal)
	}
}

// CreateUser has the same divergence with a wider window: two settings writes and
// then one lock, so a concurrent pair can leave the stored hash and the live one
// disagreeing.
func TestCreateUserWritesSettingsUnderLock(t *testing.T) {
	svc, st, probe := newProbedAuth(t)
	ctx := context.Background()

	if err := svc.CreateUser(ctx, "admin", "correcthorse"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	assertOneHold(t, svc, probe)
	if !svc.Verify("admin", "correcthorse") {
		t.Fatal("credentials unusable after CreateUser")
	}
	stored, err := st.Q.GetSetting(ctx, keyUsername)
	if err != nil {
		t.Fatalf("read stored username: %v", err)
	}
	if stored != "admin" {
		t.Fatalf("stored username = %q, want %q", stored, "admin")
	}
}
