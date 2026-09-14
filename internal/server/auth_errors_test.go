package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/auth"
)

// postAuthProblem posts body to an auth endpoint and decodes the problem+json reply.
func postAuthProblem(t *testing.T, ts *httptest.Server, path string, body map[string]string) (*http.Response, problemBody) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := ts.Client().Post(ts.URL+path, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("POST %s: Content-Type = %q, want application/problem+json", path, ct)
	}
	var p problemBody
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("POST %s: decode problem: %v", path, err)
	}
	if p.Status != resp.StatusCode {
		t.Fatalf("POST %s: body status %d, response status %d", path, p.Status, resp.StatusCode)
	}
	return resp, p
}

func TestAuthSetupShortPasswordIsProblemJSON(t *testing.T) {
	ts, _ := newAuthServer(t, &config.Config{AuthRequired: auth.RequiredEnabled})

	_, p := postAuthProblem(t, ts, "/api/v1/auth/setup", map[string]string{"username": "admin", "password": "short"})
	if p.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", p.Status)
	}
	if !strings.Contains(p.Detail, "at least 8 characters") {
		t.Errorf("detail = %q, want the minimum length", p.Detail)
	}
}

func TestAuthLoginErrorsAreProblemJSON(t *testing.T) {
	ts, authSvc := newAuthServer(t, &config.Config{AuthRequired: auth.RequiredEnabled})
	if err := authSvc.CreateUser(t.Context(), "admin", "correcthorse"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	creds := map[string]string{"username": "admin", "password": "wrongpass"}

	for range passwordAttemptBudget {
		_, p := postAuthProblem(t, ts, "/api/v1/auth/login", creds)
		if p.Status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", p.Status)
		}
		if !strings.Contains(p.Detail, "username or password") {
			t.Fatalf("detail = %q, want a wrong-credentials message", p.Detail)
		}
	}

	resp, p := postAuthProblem(t, ts, "/api/v1/auth/login", creds)
	if p.Status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", p.Status)
	}
	if !strings.Contains(p.Detail, "15 minutes") {
		t.Errorf("detail = %q, want how long to wait", p.Detail)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 has no Retry-After header")
	}
}
