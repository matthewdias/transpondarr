package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/core/auth"
)

// hostileOrigin is what a page on another website sends. Every rejection below has a
// paired admission of the same request shape without it, because a POST that expects
// a non-2xx passes for a dozen reasons unrelated to this check.
const hostileOrigin = "https://evil.example"

// write issues a request with an optional body and headers and returns the
// status and the body, so a test can assert on both.
func write(t *testing.T, ts *httptest.Server, method, path string, body []byte, headers map[string]string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// authRequired reads the stored auth required-mode back through the API, so the
// assertion is about what the server stored rather than about a service field.
func authRequired(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	var status struct {
		Required   string `json:"required"`
		Configured bool   `json:"configured"`
	}
	code, body := write(t, ts, http.MethodGet, "/api/v1/auth/status", nil, nil)
	if code != http.StatusOK {
		t.Fatalf("auth status: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatalf("decode auth status: %v", err)
	}
	return status.Required
}

// TestCrossOriginBodylessPostIsRefused checks the shape that needs no CORS preflight:
// a POST with no body has no Content-Type and no non-safelisted header, which makes it
// a simple request.
func TestCrossOriginBodylessPostIsRefused(t *testing.T) {
	h := newHarness(t, nil, nil)
	before := h.settings.APIKey()

	code, body := write(t, h.ts, http.MethodPost, "/api/v1/settings/apikey/regenerate", nil,
		map[string]string{"Origin": hostileOrigin})
	if code != http.StatusForbidden {
		t.Fatalf("cross-origin regenerate: status = %d, want 403 (%s)", code, body)
	}
	if h.settings.APIKey() != before {
		t.Fatal("cross-origin regenerate rotated the API key")
	}

	// The same request without an Origin still works, so the 403 above is the
	// guard and not the route.
	if code, body := write(t, h.ts, http.MethodPost, "/api/v1/settings/apikey/regenerate", nil, nil); code != http.StatusOK {
		t.Fatalf("regenerate with no Origin: status = %d, want 200 (%s)", code, body)
	}
	if h.settings.APIKey() == before {
		t.Fatal("regenerate with no Origin did not rotate the API key")
	}
}

// TestCrossOriginTextPlainPostIsRefused checks the plain-chi auth routes, which skip
// Huma's content negotiation: decodeJSON ignores Content-Type, and text/plain is
// CORS-safelisted.
func TestCrossOriginTextPlainPostIsRefused(t *testing.T) {
	h := newHarness(t, nil, nil)
	if got := authRequired(t, h.ts); got != auth.RequiredLocal {
		t.Fatalf("harness auth mode = %q, want %q", got, auth.RequiredLocal)
	}

	body := []byte(`{"required":"enabled"}`)
	code, out := write(t, h.ts, http.MethodPost, "/api/v1/auth/mode", body, map[string]string{
		"Content-Type": "text/plain;charset=UTF-8",
		"Origin":       hostileOrigin,
	})
	if code != http.StatusForbidden {
		t.Fatalf("cross-origin auth/mode: status = %d, want 403 (%s)", code, out)
	}
	if got := authRequired(t, h.ts); got != auth.RequiredLocal {
		t.Fatalf("cross-origin auth/mode changed the stored mode to %q", got)
	}

	if code, out := write(t, h.ts, http.MethodPost, "/api/v1/auth/mode", body, map[string]string{
		"Content-Type": "text/plain;charset=UTF-8",
	}); code != http.StatusOK {
		t.Fatalf("auth/mode with no Origin: status = %d, want 200 (%s)", code, out)
	}
	if got := authRequired(t, h.ts); got != auth.RequiredEnabled {
		t.Fatalf("auth/mode with no Origin left the mode at %q", got)
	}
}

// TestCrossOriginJSONPostWithNoContentTypeIsRefused checks the Huma routes that take a
// body: Huma reads a missing Content-Type as application/json, the one content type a
// browser can produce cross-origin without a preflight.
func TestCrossOriginJSONPostWithNoContentTypeIsRefused(t *testing.T) {
	h := newHarness(t, nil, nil)
	body, err := json.Marshal(map[string]string{
		"dir": t.TempDir(), "series_layout": "flat", "mode": "copy",
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}

	code, out := write(t, h.ts, http.MethodPost, "/api/v1/settings/library/test", body,
		map[string]string{"Origin": hostileOrigin})
	if code != http.StatusForbidden {
		t.Fatalf("cross-origin library test: status = %d, want 403 (%s)", code, out)
	}

	if code, out := write(t, h.ts, http.MethodPost, "/api/v1/settings/library/test", body, nil); code != http.StatusOK {
		t.Fatalf("library test with no Origin: status = %d, want 200 (%s)", code, out)
	}
}

// TestCrossOriginSetupIsRefusedInEveryMode checks the account-takeover chain:
// /auth/setup is exempt from auth in every auth required-mode and checks only whether
// an account exists, so a fresh enabled install is exposed before setup.
func TestCrossOriginSetupIsRefusedInEveryMode(t *testing.T) {
	ts, authSvc := newAuthServer(t, &config.Config{AuthRequired: auth.RequiredEnabled})
	body := []byte(`{"username":"attacker","password":"attackerchosen"}`)

	code, out := write(t, ts, http.MethodPost, "/api/v1/auth/setup", body, map[string]string{
		"Content-Type": "text/plain;charset=UTF-8",
		"Origin":       hostileOrigin,
	})
	if code != http.StatusForbidden {
		t.Fatalf("cross-origin setup: status = %d, want 403 (%s)", code, out)
	}
	if authSvc.Configured() {
		t.Fatal("cross-origin setup created the admin account")
	}

	if code, out := write(t, ts, http.MethodPost, "/api/v1/auth/setup", body, map[string]string{
		"Content-Type": "text/plain;charset=UTF-8",
	}); code != http.StatusCreated {
		t.Fatalf("setup with no Origin: status = %d, want 201 (%s)", code, out)
	}
	if !authSvc.Configured() {
		t.Fatal("setup with no Origin did not create the admin account")
	}
}

// TestSameOriginWriteSucceeds is the SPA's own case: this server serves it, so its
// Origin is this server's, including on the two POSTs it sends with no body.
func TestSameOriginWriteSucceeds(t *testing.T) {
	h := newHarness(t, nil, nil)
	before := h.settings.APIKey()

	code, body := write(t, h.ts, http.MethodPost, "/api/v1/settings/apikey/regenerate", nil,
		map[string]string{"Origin": h.ts.URL})
	if code != http.StatusOK {
		t.Fatalf("same-origin regenerate: status = %d, want 200 (%s)", code, body)
	}
	if h.settings.APIKey() == before {
		t.Fatal("same-origin regenerate did not rotate the API key")
	}
}

// TestCrossOriginReadStillSucceeds states the accepted residual risk over HTTP: we
// don't check reads, so a cross-site GET still runs its handler.
func TestCrossOriginReadStillSucceeds(t *testing.T) {
	h := newHarness(t, nil, nil)
	if code, body := write(t, h.ts, http.MethodGet, "/api/v1/titles", nil,
		map[string]string{"Origin": hostileOrigin}); code != http.StatusOK {
		t.Fatalf("cross-origin GET: status = %d, want 200 (%s)", code, body)
	}
}
