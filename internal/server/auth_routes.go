package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"

	"github.com/matthewdias/transpondarr/internal/core/auth"
)

// maxAuthBodyBytes caps the request body on the unauthenticated auth endpoints so
// a large payload can't exhaust memory before JSON decoding rejects it.
const maxAuthBodyBytes = 64 << 10 // 64 KiB

// Password attempts are rate-limited per client to slow online guessing and blunt
// the (memory-hard, argon2id) CPU/RAM cost of repeated verification.
const (
	passwordRateLimit  = 5
	passwordRateWindow = 15 * time.Minute
)

// registerAuthRoutes wires the plain-chi authentication endpoints. They are chi
// (not Huma) handlers because they set and read the session cookie directly.
func registerAuthRoutes(r *chi.Mux, a *auth.Service, apiKeyFn func() string) {
	// Login and change-password verify the same admin password, so they share one
	// bucket: separate ones would double the guesses available per window.
	passwordLimiter := passwordAttemptLimiter()

	r.Get("/api/v1/auth/status", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"configured":    a.Configured(),
			"required":      a.Required(),
			"authenticated": authorized(req, a, apiKeyFn),
			"session":       hasValidSession(req, a),
			"username":      a.Username(),
			"local":         isLocalRequest(req),
		})
	})

	// First-run: create the admin account. Only works while none exists.
	r.Post("/api/v1/auth/setup", func(w http.ResponseWriter, req *http.Request) {
		if a.Configured() {
			http.Error(w, "already configured", http.StatusConflict)
			return
		}
		var in credentials
		if err := decodeJSON(w, req, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if err := a.CreateUser(req.Context(), in.Username, in.Password); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		issueSession(w, req, a, in.Username, http.StatusCreated)
	})

	r.With(passwordLimiter).Post("/api/v1/auth/login", func(w http.ResponseWriter, req *http.Request) {
		var in credentials
		if err := decodeJSON(w, req, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if !a.Verify(strings.TrimSpace(in.Username), in.Password) {
			http.Error(w, "invalid username or password", http.StatusUnauthorized)
			return
		}
		issueSession(w, req, a, strings.TrimSpace(in.Username), http.StatusOK)
	})

	r.Post("/api/v1/auth/logout", func(w http.ResponseWriter, req *http.Request) {
		if c, err := req.Cookie(auth.SessionCookieName); err == nil {
			a.DeleteSession(req.Context(), c.Value)
		}
		clearSessionCookie(w, req)
		w.WriteHeader(http.StatusNoContent)
	})

	// Change password (requires the current one). In "local" mode the middleware
	// admits any LAN peer uncredentialed, so the limiter is the only throttle here.
	r.With(passwordLimiter).Post("/api/v1/auth/password", func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if err := decodeJSON(w, req, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if !a.Verify(a.Username(), in.CurrentPassword) {
			http.Error(w, "current password is incorrect", http.StatusUnauthorized)
			return
		}
		if len(in.NewPassword) < auth.MinPasswordLen {
			http.Error(w, fmt.Sprintf("new password must be at least %d characters", auth.MinPasswordLen), http.StatusBadRequest)
			return
		}
		// CreateUser rotates the hash and drops existing sessions, so re-issue one
		// for this browser to keep it logged in.
		if err := a.CreateUser(req.Context(), a.Username(), in.NewPassword); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		issueSession(w, req, a, a.Username(), http.StatusOK)
	})

	// Change the required-mode (enabled | local). Authed via the middleware.
	r.Post("/api/v1/auth/mode", func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			Required string `json:"required"`
		}
		if err := decodeJSON(w, req, &in); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if err := a.SetRequired(req.Context(), in.Required); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"required": a.Required()})
	})
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// issueSession creates a session and sets the cookie, then returns the username.
func issueSession(w http.ResponseWriter, req *http.Request, a *auth.Service, username string, status int) {
	tok, exp, err := a.CreateSession(req.Context(), username)
	if err != nil {
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, req, tok, exp)
	writeJSON(w, status, map[string]string{"username": username})
}

func setSessionCookie(w http.ResponseWriter, req *http.Request, token string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  exp,
		MaxAge:   int(time.Until(exp).Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(req),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, req *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(req),
		SameSite: http.SameSiteLaxMode,
	})
}

// isHTTPS reports whether the original request was over TLS, honouring a reverse
// proxy's X-Forwarded-Proto so the Secure cookie flag is set on HTTPS deployments.
func isHTTPS(req *http.Request) bool {
	return req.TLS != nil || strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")
}

func decodeJSON(w http.ResponseWriter, req *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, req.Body, maxAuthBodyBytes)).Decode(v)
}

// passwordAttemptLimiter throttles password verification per client; httprate
// counts every attempt in the window, not just failures. One call is one bucket.
func passwordAttemptLimiter() func(http.Handler) http.Handler {
	return httprate.LimitBy(
		passwordRateLimit, passwordRateWindow, keyByRemoteAddr,
		httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "too many password attempts; try again later", http.StatusTooManyRequests)
		}),
	)
}

// keyByRemoteAddr keys the limiter on the peer address (host without port). It
// uses RemoteAddr, not forwarding headers (httprate.KeyByIP/KeyByRealIP trust
// those), since they're client-controllable; behind a reverse proxy this is
// coarse (the proxy's address), which is acceptable for a single-admin lockout.
func keyByRemoteAddr(req *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr, nil
	}
	return host, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
