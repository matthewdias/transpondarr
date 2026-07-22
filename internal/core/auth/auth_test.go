package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
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
