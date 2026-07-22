// Package store owns the SQLite database: connection, embedded goose migrations,
// and the sqlc-generated query layer (internal/store/db).
package store

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	DB *sql.DB
	Q  *db.Queries
}

// Open opens (creating if needed) the SQLite database at path and applies all
// pending migrations.
func Open(path string) (*Store, error) {
	// Pragmas must ride the DSN: database/sql pools connections, and a plain
	// `PRAGMA` Exec would apply to only the one connection that ran it, leaving
	// foreign keys unenforced on the rest. busy_timeout makes concurrent writers
	// (importer tick vs. request handlers) wait instead of failing SQLITE_BUSY.
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(conn, "migrations"); err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}

	return &Store{DB: conn, Q: db.New(conn)}, nil
}
