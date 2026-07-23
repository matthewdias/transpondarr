package auth

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

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

// RunCleanup must sweep immediately on entry (the interval here is far longer
// than the test) and leave live sessions alone.
func TestRunCleanupSweepsExpiredSessions(t *testing.T) {
	svc, st := newTestAuth(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
		ExpiresAt: time.Now().UTC().Add(-time.Hour).Format(sqliteTimeLayout),
	}); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RunCleanup(ctx, time.Hour, discardLogger())
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var n int
		if err := st.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&n); err != nil {
			t.Fatalf("count sessions: %v", err)
		}
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected the expired session swept and the live one kept, have %d rows", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := svc.ValidateSession(ctx, liveTok); !ok {
		t.Fatal("live session did not survive the sweep")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunCleanup did not stop on context cancellation")
	}
}

// A failing sweep must be logged: the ticker is the only thing bounding the
// sessions table on a long-lived instance, so silence would reproduce issue #4.
func TestRunCleanupLogsFailedSweep(t *testing.T) {
	svc, st := newTestAuth(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_ = st.DB.Close()

	w := &syncWriter{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RunCleanup(ctx, time.Hour, slog.New(slog.NewTextHandler(w, nil)))
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(w.String(), "session cleanup failed") {
		if time.Now().After(deadline) {
			t.Fatalf("no warning logged for a failed sweep; log output: %q", w.String())
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunCleanup did not stop on context cancellation")
	}
}

// syncWriter is a goroutine-safe log sink: the test polls it while RunCleanup writes.
type syncWriter struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
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
