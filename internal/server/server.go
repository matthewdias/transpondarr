// Package server wires the HTTP layer: a chi router, the Huma (OpenAPI 3.1)
// API, API-key auth, and the embedded single-page frontend.
package server

import (
	"crypto/subtle"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/matthewdias/transpondarr/internal/core/acquire"
	"github.com/matthewdias/transpondarr/internal/core/auth"
	"github.com/matthewdias/transpondarr/internal/core/blocklist"
	"github.com/matthewdias/transpondarr/internal/core/browse"
	"github.com/matthewdias/transpondarr/internal/core/catalog"
	"github.com/matthewdias/transpondarr/internal/core/clients"
	"github.com/matthewdias/transpondarr/internal/core/importer"
	"github.com/matthewdias/transpondarr/internal/core/jobs"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/core/settings"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/version"
	"github.com/matthewdias/transpondarr/web"
)

func init() {
	huma.DefaultArrayNullable = false
}

// Deps is everything New wires into the HTTP layer. Instances are shared with
// the daemon's background jobs on purpose: Provider brings its rate limiter,
// Blocklist its breaker (so it sees every failure path), Acquire its in-flight
// claims (covering manual grabs and the jobs alike), and Importer is the very
// instance the scan job runs on — a manual import fix and the scan serialize on
// its mutex instead of racing over one payload.
type Deps struct {
	Store     *store.Store
	Logger    *slog.Logger
	Provider  metadata.Provider
	Clients   *clients.Registry // live download/indexer/library clients; any may be nil when unconfigured
	Settings  *settings.Service
	Auth      *auth.Service
	Jobs      *jobs.Runner
	Blocklist *blocklist.Service
	Acquire   *acquire.Service
	Importer  *importer.Importer
}

// New builds the top-level HTTP handler.
func New(d Deps) http.Handler {
	r := chi.NewMux()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(authMiddleware(d.Auth, d.Settings.APIKey))

	d.Logger.Info("auth: forms login", "required", d.Auth.Required(), "configured", d.Auth.Configured())
	registerAuthRoutes(r, d.Auth, d.Settings.APIKey)

	api := humachi.New(r, apiConfig())
	registerRoutes(api, routeDeps{
		store:     d.Store,
		catalog:   catalog.NewService(d.Store, d.Provider),
		browse:    browse.New(d.Store, d.Provider, d.Logger),
		clients:   d.Clients,
		settings:  d.Settings,
		auth:      d.Auth,
		jobs:      d.Jobs,
		acquire:   d.Acquire,
		blocklist: d.Blocklist,
		importer:  d.Importer,
	})

	r.NotFound(spaHandler())
	return r
}

// OpenAPIYAML renders the API's OpenAPI 3.1 document without starting a server or
// touching any live dependency — route registration only defines operations and
// schemas, so zero-value deps are safe. Used by `transpondarrd openapi` to feed
// frontend type generation (see `make gen-api`).
func OpenAPIYAML() ([]byte, error) {
	api := humachi.New(chi.NewMux(), apiConfig())
	registerRoutes(api, routeDeps{})
	return api.OpenAPI().YAML()
}

// apiConfig builds the Huma config shared by the live API and the OpenAPI-dump
// path, so the served spec and the generated spec are byte-identical. It declares
// the two auth schemes — the API key and the browser cookie flow (which is
// same-origin) — because the OpenAPI contract must still show that /api/* requires
// auth and how a client supplies it. The local-address bypass is a deployment
// convenience, not a spec-level scheme, so it isn't declared here.
func apiConfig() huma.Config {
	cfg := huma.DefaultConfig("Transpondarr API", version.Version)
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"apiKey": {
			Type:        "apiKey",
			In:          "header",
			Name:        "X-Api-Key",
			Description: "Machine-client API key.",
		},
		"session": {
			Type:        "apiKey",
			In:          "cookie",
			Name:        auth.SessionCookieName,
			Description: "Browser session cookie issued by the forms-login flow.",
		},
	}
	// Separate maps = OR: a request satisfies auth with either scheme. Individual
	// public operations (e.g. health) override this with an empty security list.
	cfg.Security = []map[string][]string{
		{"apiKey": {}},
		{"session": {}},
	}
	return cfg
}

// authMiddleware guards /api/* except the public endpoints (health, and the auth
// status/setup/login/logout endpoints the SPA needs before it has a session).
func authMiddleware(a *auth.Service, apiKeyFn func() string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if !requiresAuth(req.URL.Path) || authorized(req, a, apiKeyFn) {
				next.ServeHTTP(w, req)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}

// authorized reports whether a request may proceed: a valid machine API key, a
// valid browser session, or — in "local" mode — a request from a local address.
func authorized(req *http.Request, a *auth.Service, apiKeyFn func() string) bool {
	// Constant-time compare so the key can't be recovered by timing (matches the
	// password path in the auth package).
	if key := apiKeyFn(); key != "" &&
		subtle.ConstantTimeCompare([]byte(providedKey(req)), []byte(key)) == 1 {
		return true
	}
	if hasValidSession(req, a) {
		return true
	}
	if a.Required() == auth.RequiredLocal && isLocalRequest(req) {
		return true
	}
	return false
}

// hasValidSession reports whether the request carries a valid browser login
// session (as opposed to being admitted by the API key or the local-address
// bypass). The auth-status endpoint surfaces this so the UI knows when there is
// an actual session to sign out of — in "local" mode a loopback client is
// authorized with no session, so a "Sign out" action would otherwise be a no-op.
func hasValidSession(req *http.Request, a *auth.Service) bool {
	c, err := req.Cookie(auth.SessionCookieName)
	if err != nil {
		return false
	}
	_, ok := a.ValidateSession(req.Context(), c.Value)
	return ok
}

// providedKey reads the machine API key from the request header only. It is
// deliberately not accepted as a URL query parameter: a long-lived credential in a
// query string leaks into proxy/access logs and browser history.
func providedKey(req *http.Request) string {
	return req.Header.Get("X-Api-Key")
}

func requiresAuth(p string) bool {
	if !strings.HasPrefix(p, "/api/") {
		return false
	}
	switch p {
	case "/api/v1/health",
		"/api/v1/auth/status",
		"/api/v1/auth/setup",
		"/api/v1/auth/login",
		"/api/v1/auth/logout":
		return false
	}
	return true
}

// proxyHeaders are set by reverse proxies. If a request carries any of them it was
// proxied, so the local-address bypass must not apply — otherwise a remote client
// behind a same-host or LAN proxy that forwards under some other header name would
// be mistaken for a local client and skip auth.
var proxyHeaders = []string{
	"X-Forwarded-For", "X-Real-IP", "Forwarded",
	"X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Server", "Via",
}

// isLocalRequest reports whether the request came from a loopback or private
// address with no proxy-forwarding headers. Note: in "local" mode this trusts the
// whole private range, not just the host — see SECURITY.md before exposing it.
func isLocalRequest(req *http.Request) bool {
	for _, h := range proxyHeaders {
		if req.Header.Get(h) != "" {
			return false
		}
	}
	// DNS-rebinding guard: a private RemoteAddr only proves the TCP peer is on the
	// LAN, not that the browser meant to reach a local host. A rebinding page
	// (attacker.com re-pointed at this private IP) still connects from a private
	// address, so it would otherwise clear the check above and skip auth. Requiring
	// the Host header to be an IP literal or "localhost" — a value the attacker
	// can't force into the victim's browser via DNS — defeats that. Reaching the
	// UI by a hostname in local mode therefore needs a real login (session/API key).
	if !hostIsLocalLiteral(req.Host) {
		return false
	}
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// hostIsLocalLiteral reports whether the request's Host header names the server
// by an IP literal or "localhost" rather than a registrable domain name. DNS
// rebinding needs a domain name, so an IP/localhost Host cannot be a rebinding
// target.
func hostIsLocalLiteral(hostHeader string) bool {
	h := hostHeader
	if host, _, err := net.SplitHostPort(hostHeader); err == nil {
		h = host
	}
	if h == "localhost" {
		return true
	}
	return net.ParseIP(h) != nil
}

// spaHandler serves the embedded frontend, falling back to index.html so
// client-side routes resolve.
func spaHandler() http.HandlerFunc {
	sub, err := web.DistFS()
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, statErr := fs.Stat(sub, name); statErr != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	}
}
