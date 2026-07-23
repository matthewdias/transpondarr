package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/auth"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/server"
)

// passwordAttemptBudget mirrors the unexported passwordRateLimit; a change to one
// without the other fails the rate-limit tests rather than weakening them.
const passwordAttemptBudget = 5

// newAuthServer builds a server with the given auth config and returns the
// running test server plus the auth service (so a test can create the admin
// account directly rather than through the setup endpoint).
func newAuthServer(t *testing.T, cfg *config.Config) (*httptest.Server, *auth.Service) {
	t.Helper()
	ctx := context.Background()
	st := coretest.NewStore(t)
	reg := clients.New()

	settingsSvc, err := settings.New(ctx, st, cfg, reg)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	authSvc, err := auth.New(ctx, st, cfg)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	ts := httptest.NewServer(server.New(cfg, st, discardLogger(), reg, settingsSvc, authSvc))
	t.Cleanup(ts.Close)
	return ts, authSvc
}

// getWith issues a GET to a protected route with an optional request mutator
// (headers, Host), and returns the status code.
func getWith(t *testing.T, ts *httptest.Server, client *http.Client, mutate func(*http.Request)) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/series", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if mutate != nil {
		mutate(req)
	}
	if client == nil {
		client = ts.Client()
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestAuthAPIKeyPath covers the machine-client admission path: a correct
// X-Api-Key header is authorized, a wrong one is not, and — by design — the key
// is only read from the header, never a query parameter.
func TestAuthAPIKeyPath(t *testing.T) {
	const key = "secret-api-key"
	ts, _ := newAuthServer(t, &config.Config{AuthRequired: auth.RequiredEnabled, APIKey: key})

	if code := getWith(t, ts, nil, func(r *http.Request) { r.Header.Set("X-Api-Key", key) }); code != http.StatusOK {
		t.Errorf("correct API key: status = %d, want 200", code)
	}
	if code := getWith(t, ts, nil, func(r *http.Request) { r.Header.Set("X-Api-Key", "wrong") }); code != http.StatusUnauthorized {
		t.Errorf("wrong API key: status = %d, want 401", code)
	}
	// The key must not be accepted as a query parameter (it would leak into logs).
	if code := getWith(t, ts, nil, func(r *http.Request) { r.URL.RawQuery = "apikey=" + key }); code != http.StatusUnauthorized {
		t.Errorf("API key in query string: status = %d, want 401 (header-only)", code)
	}
}

// TestAuthSessionPath covers the browser admission path: logging in issues a
// session cookie that authorizes subsequent requests, a bad password does not,
// and a client without the cookie is rejected.
func TestAuthSessionPath(t *testing.T) {
	ts, authSvc := newAuthServer(t, &config.Config{AuthRequired: auth.RequiredEnabled})
	if err := authSvc.CreateUser(context.Background(), "admin", "correcthorse"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// Wrong password: 401 and no session cookie.
	if code := login(t, ts, client, "admin", "wrongpass"); code != http.StatusUnauthorized {
		t.Errorf("bad login: status = %d, want 401", code)
	}
	// Without a session, the protected route is 401.
	if code := getWith(t, ts, client, nil); code != http.StatusUnauthorized {
		t.Errorf("no session: status = %d, want 401", code)
	}

	// Correct login issues the cookie (stored in the jar).
	if code := login(t, ts, client, "admin", "correcthorse"); code != http.StatusOK {
		t.Fatalf("good login: status = %d, want 200", code)
	}
	if !hasSessionCookie(client, ts) {
		t.Fatal("login did not set a session cookie")
	}
	// The jar-carried cookie now authorizes the protected route.
	if code := getWith(t, ts, client, nil); code != http.StatusOK {
		t.Errorf("with session: status = %d, want 200", code)
	}
}

// TestAuthLocalModeBypassAndGuards covers the local-address admission path and
// the two things that must defeat it: a non-literal Host (DNS-rebinding guard)
// and any proxy-forwarding header.
func TestAuthLocalModeBypassAndGuards(t *testing.T) {
	ts, _ := newAuthServer(t, &config.Config{AuthRequired: auth.RequiredLocal})

	// Loopback peer + IP-literal Host (the httptest default) is admitted with no
	// credentials.
	if code := getWith(t, ts, nil, nil); code != http.StatusOK {
		t.Errorf("local loopback: status = %d, want 200", code)
	}

	// DNS-rebinding guard: a registrable-domain Host cannot be a local literal, so
	// the bypass must not apply even though the peer is loopback.
	if code := getWith(t, ts, nil, func(r *http.Request) { r.Host = "attacker.example.com" }); code != http.StatusUnauthorized {
		t.Errorf("rebinding Host: status = %d, want 401", code)
	}

	// A proxy-forwarding header means the request was proxied, so the local bypass
	// must not apply.
	if code := getWith(t, ts, nil, func(r *http.Request) { r.Header.Set("X-Forwarded-For", "203.0.113.7") }); code != http.StatusUnauthorized {
		t.Errorf("proxied request: status = %d, want 401", code)
	}
}

// TestAuthEnabledModeIgnoresLocalAddress confirms local admission is gated on the
// required-mode: in "enabled" mode a loopback request with no credentials is
// still rejected, while the public health endpoint stays open.
func TestAuthEnabledModeIgnoresLocalAddress(t *testing.T) {
	ts, _ := newAuthServer(t, &config.Config{AuthRequired: auth.RequiredEnabled})
	if code := getWith(t, ts, nil, nil); code != http.StatusUnauthorized {
		t.Errorf("enabled mode, loopback, no creds: status = %d, want 401", code)
	}

	// The health check is allow-listed as public and must not require auth.
	resp, err := ts.Client().Get(ts.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET health: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("public health: status = %d, want 200", resp.StatusCode)
	}
}

// TestAuthPasswordChangeIsRateLimited covers the change-password endpoint in
// "local" mode, where the middleware admits any LAN peer with no credential at
// all: without the limiter it is an unmetered password-guessing oracle.
func TestAuthPasswordChangeIsRateLimited(t *testing.T) {
	ts, authSvc := newAuthServer(t, &config.Config{AuthRequired: auth.RequiredLocal})
	if err := authSvc.CreateUser(context.Background(), "admin", "correcthorse"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	for i := range passwordAttemptBudget {
		if code := changePassword(t, ts, "wrongpass", "brandnewpassword"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, code)
		}
	}
	if code := changePassword(t, ts, "wrongpass", "brandnewpassword"); code != http.StatusTooManyRequests {
		t.Errorf("attempt past the budget: status = %d, want 429", code)
	}
	// The lockout must not be bypassable by supplying the correct password.
	if code := changePassword(t, ts, "correcthorse", "brandnewpassword"); code != http.StatusTooManyRequests {
		t.Errorf("correct password while limited: status = %d, want 429", code)
	}
}

// TestAuthPasswordLimiterSharesBucket pins the deliberate choice of one bucket for
// both password-verifying endpoints: spending the budget on login must leave none
// for change-password, since both guess at the same admin credential.
func TestAuthPasswordLimiterSharesBucket(t *testing.T) {
	ts, authSvc := newAuthServer(t, &config.Config{AuthRequired: auth.RequiredLocal})
	if err := authSvc.CreateUser(context.Background(), "admin", "correcthorse"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	for i := range passwordAttemptBudget {
		if code := login(t, ts, ts.Client(), "admin", "wrongpass"); code != http.StatusUnauthorized {
			t.Fatalf("login attempt %d: status = %d, want 401", i+1, code)
		}
	}
	if code := changePassword(t, ts, "wrongpass", "brandnewpassword"); code != http.StatusTooManyRequests {
		t.Errorf("change-password after login budget spent: status = %d, want 429", code)
	}
}

// TestAuthPasswordLimiterIsAtomic guards the limiter's check-then-act step against
// concurrent attempts: httprate holds its mutex across the read and the increment,
// so a burst must not slip more than the budget through to verification.
func TestAuthPasswordLimiterIsAtomic(t *testing.T) {
	ts, authSvc := newAuthServer(t, &config.Config{AuthRequired: auth.RequiredEnabled})
	if err := authSvc.CreateUser(context.Background(), "admin", "correcthorse"); err != nil {
		t.Fatalf("create user: %v", err)
	}

	const attempts = 50
	codes := make([]int, attempts)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() {
			<-start
			codes[i] = login(t, ts, ts.Client(), "admin", "wrongpass")
		})
	}
	close(start)
	wg.Wait()

	verified := 0
	for _, c := range codes {
		switch c {
		case http.StatusUnauthorized:
			verified++
		case http.StatusTooManyRequests:
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if verified > passwordAttemptBudget {
		t.Errorf("%d of %d concurrent attempts reached verification, want <= %d", verified, attempts, passwordAttemptBudget)
	}
}

func changePassword(t *testing.T, ts *httptest.Server, current, next string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"current_password": current, "new_password": next})
	resp, err := ts.Client().Post(ts.URL+"/api/v1/auth/password", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("password POST: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func login(t *testing.T, ts *httptest.Server, client *http.Client, user, pass string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := client.Post(ts.URL+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login POST: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func hasSessionCookie(client *http.Client, ts *httptest.Server) bool {
	u, _ := url.Parse(ts.URL)
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			return true
		}
	}
	return false
}
