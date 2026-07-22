-- +goose Up
-- Browser login sessions (forms-based auth). The opaque token is the
-- cookie value; the server looks it up here. Rows are deleted on logout, on a
-- password change (DeleteSessionsForUser), and lazily once expired. Machine API
-- clients use the API key instead and never create sessions.
CREATE TABLE sessions (
    token      TEXT PRIMARY KEY,
    username   TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL
);

CREATE INDEX idx_sessions_expires ON sessions (expires_at);

-- +goose Down
DROP TABLE sessions;
