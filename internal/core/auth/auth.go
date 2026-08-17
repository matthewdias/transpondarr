// Package auth implements forms-based authentication: a single admin
// account (username + argon2id-hashed password) and opaque server-side login
// sessions carried in an httpOnly cookie. It is deliberately separate from the
// API key — the key authenticates machines (dashboards, scripts), while humans
// log in and get a session. Credentials and the required-mode live in the
// settings table; sessions live in their own table.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/argon2id"

	"github.com/matthewdias/transpondarr/internal/config"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

const (
	RequiredEnabled = "enabled" // always require a session or API key
	RequiredLocal   = "local"   // skip auth for local/private-network addresses
)

const SessionCookieName = "transpondarr_session"
const MinPasswordLen = 8
const SessionTTL = 30 * 24 * time.Hour

// Settings-table keys.
const (
	keyUsername = "auth.username"
	keyPassword = "auth.password_hash"
	keyRequired = "auth.required"
)

// argon2idParams governs new password hashes. Argon2id is the OWASP-preferred
// KDF; these settings (64 MiB, t=2, p=1) clear the OWASP minimum with margin.
// Parallelism is pinned (not runtime.NumCPU, argon2id.DefaultParams' default) so
// a hash costs the same regardless of the host it was created on.
var argon2idParams = &argon2id.Params{
	Memory:      64 * 1024,
	Iterations:  2,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

// Service holds the admin credentials and required-mode and manages sessions.
type Service struct {
	mu       sync.RWMutex
	store    *store.Store
	username string
	hash     string
	required string
}

// New loads auth state from the store and, when no user exists yet, bootstraps
// one from TRANSPONDARR_AUTH_USERNAME/PASSWORD if both are provided.
func New(ctx context.Context, st *store.Store, cfg *config.Config) (*Service, error) {
	s := &Service{store: st, required: normalizeRequired(cfg.AuthRequired)}

	if v, err := getSetting(ctx, st, keyUsername); err != nil {
		return nil, err
	} else {
		s.username = v
	}
	if v, err := getSetting(ctx, st, keyPassword); err != nil {
		return nil, err
	} else {
		s.hash = v
	}
	if v, err := getSetting(ctx, st, keyRequired); err != nil {
		return nil, err
	} else if v != "" {
		s.required = normalizeRequired(v)
	}

	if s.username == "" && cfg.AuthUsername != "" && cfg.AuthPassword != "" {
		if err := s.CreateUser(ctx, cfg.AuthUsername, cfg.AuthPassword); err != nil {
			return nil, fmt.Errorf("bootstrap auth user: %w", err)
		}
	}
	return s, nil
}

func getSetting(ctx context.Context, st *store.Store, key string) (string, error) {
	v, err := st.Q.GetSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("load %s: %w", key, err)
	}
	return v, nil
}

func normalizeRequired(m string) string {
	if strings.EqualFold(strings.TrimSpace(m), RequiredLocal) {
		return RequiredLocal
	}
	return RequiredEnabled
}

// ValidRequired reports whether m is a recognised required-mode. Exact, unlike
// normalizeRequired, which reads what a stored value or an env var may hold.
func ValidRequired(m string) bool {
	switch m {
	case RequiredEnabled, RequiredLocal:
		return true
	default:
		return false
	}
}

// Configured reports whether an admin account exists.
func (s *Service) Configured() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.username != "" && s.hash != ""
}

// Username returns the admin username (empty when unconfigured).
func (s *Service) Username() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.username
}

// Required returns the current required-mode.
func (s *Service) Required() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.required
}

// Verify checks a username/password pair against the stored credentials.
func (s *Service) Verify(username, password string) bool {
	s.mu.RLock()
	u, h := s.username, s.hash
	s.mu.RUnlock()
	if u == "" || h == "" {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(u)) == 1
	passOK := verifyPassword(password, h)
	return userOK && passOK
}

// CreateUser sets or replaces the admin credentials and invalidates any existing
// sessions (so a password change logs other clients out).
func (s *Service) CreateUser(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return errors.New("username and password are required")
	}
	if len(password) < MinPasswordLen {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	h, err := hashPassword(password)
	if err != nil {
		return err
	}
	if err := s.persist(ctx, keyUsername, username); err != nil {
		return err
	}
	if err := s.persist(ctx, keyPassword, h); err != nil {
		return err
	}
	s.mu.Lock()
	s.username = username
	s.hash = h
	s.mu.Unlock()
	_ = s.store.Q.DeleteSessionsForUser(ctx, username)
	return nil
}

// SetRequired updates the auth-required mode.
func (s *Service) SetRequired(ctx context.Context, mode string) error {
	mode = normalizeRequired(mode)
	if err := s.persist(ctx, keyRequired, mode); err != nil {
		return err
	}
	s.mu.Lock()
	s.required = mode
	s.mu.Unlock()
	return nil
}

func (s *Service) persist(ctx context.Context, k, v string) error {
	if err := s.store.Q.UpsertSetting(ctx, db.UpsertSettingParams{Key: k, Value: v}); err != nil {
		return fmt.Errorf("persist %s: %w", k, err)
	}
	return nil
}

// CreateSession issues a new session token for username and returns it with its
// expiry (for the cookie's Max-Age).
func (s *Service) CreateSession(ctx context.Context, username string) (string, time.Time, error) {
	tok, err := randomToken()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := time.Now().Add(SessionTTL)
	if err := s.store.Q.CreateSession(ctx, db.CreateSessionParams{
		Token:     tok,
		Username:  username,
		ExpiresAt: store.FormatTimestamp(exp),
	}); err != nil {
		return "", time.Time{}, err
	}
	return tok, exp, nil
}

// ValidateSession returns the username for a valid, unexpired session token.
func (s *Service) ValidateSession(ctx context.Context, token string) (string, bool) {
	if token == "" {
		return "", false
	}
	u, err := s.store.Q.GetSession(ctx, token)
	if err != nil {
		return "", false
	}
	return u, true
}

// DeleteSession revokes a session token (logout).
func (s *Service) DeleteSession(ctx context.Context, token string) {
	if token != "" {
		_ = s.store.Q.DeleteSession(ctx, token)
	}
}

// CleanupExpired removes expired session rows.
func (s *Service) CleanupExpired(ctx context.Context) error {
	return s.store.Q.DeleteExpiredSessions(ctx)
}

// ── password hashing (argon2id) ───────────────────────────────────────────────

func hashPassword(pw string) (string, error) {
	return argon2id.CreateHash(pw, argon2idParams)
}

// verifyPassword reports whether pw matches the stored argon2id hash (PHC-encoded,
// "$argon2id$…"). argon2id.ComparePasswordAndHash is constant-time internally.
func verifyPassword(pw, encoded string) bool {
	match, err := argon2id.ComparePasswordAndHash(pw, encoded)
	return err == nil && match
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// NewAPIKey returns a fresh random 32-hex-character API key. The key
// authenticates machine clients (see the package doc); it is minted here at
// first run and whenever the settings UI rotates it.
func NewAPIKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return hex.EncodeToString(b), nil
}
