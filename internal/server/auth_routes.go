package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
func registerAuthRoutes(r *chi.Mux, a *auth.Service, apiKeyFn func() string, logger *slog.Logger) {
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
			writeProblem(w, http.StatusConflict, "An admin account already exists. Sign in instead.")
			return
		}
		var in credentials
		if err := decodeJSON(w, req, &in); err != nil {
			writeProblem(w, http.StatusBadRequest, "The request body isn't valid JSON.")
			return
		}
		if err := a.CreateUser(req.Context(), in.Username, in.Password); err != nil {
			credentialsError(w, req, logger, err)
			return
		}
		issueSession(w, req, a, logger, in.Username, http.StatusCreated)
	})

	r.With(passwordLimiter).Post("/api/v1/auth/login", func(w http.ResponseWriter, req *http.Request) {
		var in credentials
		if err := decodeJSON(w, req, &in); err != nil {
			writeProblem(w, http.StatusBadRequest, "The request body isn't valid JSON.")
			return
		}
		if !a.Verify(strings.TrimSpace(in.Username), in.Password) {
			writeProblem(w, http.StatusUnauthorized, "Wrong username or password.")
			return
		}
		issueSession(w, req, a, logger, strings.TrimSpace(in.Username), http.StatusOK)
	})

	r.Post("/api/v1/auth/logout", func(w http.ResponseWriter, req *http.Request) {
		if c, err := req.Cookie(auth.SessionCookieName); err == nil {
			a.DeleteSession(req.Context(), c.Value)
		}
		clearSessionCookie(w, req)
		w.WriteHeader(http.StatusNoContent)
	})

	// Change password (requires the current one). In "local" auth mode the middleware
	// admits any LAN peer uncredentialed, so the limiter is the only throttle here.
	r.With(passwordLimiter).Post("/api/v1/auth/password", func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if err := decodeJSON(w, req, &in); err != nil {
			writeProblem(w, http.StatusBadRequest, "The request body isn't valid JSON.")
			return
		}
		if !a.Verify(a.Username(), in.CurrentPassword) {
			writeProblem(w, http.StatusUnauthorized, "Your current password is wrong.")
			return
		}
		if len(in.NewPassword) < auth.MinPasswordLen {
			writeProblem(w, http.StatusBadRequest, passwordTooShortDetail)
			return
		}
		// CreateUser rotates the hash and drops existing sessions, so re-issue one
		// for this browser to keep it logged in.
		if err := a.CreateUser(req.Context(), a.Username(), in.NewPassword); err != nil {
			credentialsError(w, req, logger, err)
			return
		}
		issueSession(w, req, a, logger, a.Username(), http.StatusOK)
	})

	// Change the auth required-mode (enabled | local). Authed via the middleware.
	r.Post("/api/v1/auth/mode", func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			Required string `json:"required"`
		}
		if err := decodeJSON(w, req, &in); err != nil {
			writeProblem(w, http.StatusBadRequest, "The request body isn't valid JSON.")
			return
		}
		// The settings inputs' rule (#227) applies here too: the service reads an
		// absent required-mode as "enabled", which would lock a local install out.
		if !auth.ValidRequired(in.Required) {
			writeProblem(w, http.StatusBadRequest, `The authentication mode must be "enabled" or "local".`)
			return
		}
		if err := a.SetRequired(req.Context(), in.Required); err != nil {
			writeServerError(w, req, logger, "Couldn't save the authentication mode. The server log has the cause.", err)
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
func issueSession(w http.ResponseWriter, req *http.Request, a *auth.Service, logger *slog.Logger, username string, status int) {
	tok, exp, err := a.CreateSession(req.Context(), username)
	if err != nil {
		writeServerError(w, req, logger, "Couldn't sign you in. The server log has the cause.", err)
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

// isHTTPS reports whether the original request was over TLS, reading a reverse
// proxy's X-Forwarded-Proto so the Secure cookie flag is set on HTTPS deployments.
func isHTTPS(req *http.Request) bool {
	return req.TLS != nil || strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https")
}

func decodeJSON(w http.ResponseWriter, req *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(w, req.Body, maxAuthBodyBytes)).Decode(v)
}

// passwordAttemptLimiter throttles password verification per client; httprate
// counts every attempt in the window, not only failures. One call is one bucket.
func passwordAttemptLimiter() func(http.Handler) http.Handler {
	return httprate.LimitBy(
		passwordRateLimit, passwordRateWindow, keyByRemoteAddr,
		httprate.WithLimitHandler(func(w http.ResponseWriter, _ *http.Request) {
			writeProblem(w, http.StatusTooManyRequests, fmt.Sprintf(
				"Too many password attempts. Wait %d minutes, then try again.", int(passwordRateWindow.Minutes())))
		}),
	)
}

// keyByRemoteAddr keys the limiter on the peer address (host without port). It
// uses RemoteAddr, not forwarding headers (httprate.KeyByIP/KeyByRealIP read
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

var passwordTooShortDetail = fmt.Sprintf("Use a password of at least %d characters.", auth.MinPasswordLen)

// credentialsError answers a failed CreateUser: a rejected credential is the
// caller's to fix, and anything else is a store failure.
func credentialsError(w http.ResponseWriter, req *http.Request, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, auth.ErrCredentialsRequired):
		writeProblem(w, http.StatusBadRequest, "Enter a username and a password.")
	case errors.Is(err, auth.ErrPasswordTooShort):
		writeProblem(w, http.StatusBadRequest, passwordTooShortDetail)
	default:
		writeServerError(w, req, logger, "Couldn't save the account. The server log has the cause.", err)
	}
}
