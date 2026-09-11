# Migrations and the sqlc layer (`internal/store`)

What a schema change costs, and the three traps that make one fail quietly.

- A DB change = a goose migration under `internal/store/migrations` + queries in
  `internal/store/queries` + `make gen`.
  - **`make gen` fails on any sqlc but the `mise.toml` pin.** The generated
    layer is committed and CI diffs it, so a mismatched sqlc shows up as drift that
    looks like your change rather than the toolchain's.
  - **Keep comments in `internal/store/queries/*.sql` ASCII-only.** sqlc's sqlite
    codegen miscounts byte vs. rune offsets: a doc comment between `-- name:` and
    the SQL body containing a multi-byte character — an em dash, which this repo's
    prose style uses everywhere — silently truncates the *emitted* SQL by that many
    bytes. The result compiles, `make gen` reports no error, and the query fails
    only at runtime. Also note `sqlc.arg(name)` is rejected by this dialect
    (`extraneous input '?1'`) — use positional `?` params.
  - **Migration numbers are a shared sequence — check `main` before claiming one.**
    Two branches that each add `000NN_*.sql` merge without a git conflict (different
    filenames) and leave a migration set goose rejects as a duplicate version. Renumber
    on rebase; never merge past a collision.
  - **A table rebuild is one `-- +goose StatementBegin` block, never loose
    statements** (`00020_provider_identity.sql` is the only one, and the recipe).
    SQLite has no DROP CONSTRAINT, so changing one means create-copy-drop-rename —
    and `DROP TABLE series` with foreign keys on **cascade-deletes the user's whole
    library**, since `db.go` enables them in the DSN for every pooled connection.
    `PRAGMA foreign_keys` is a silent no-op inside a transaction (hence `-- +goose
    NO TRANSACTION`) *and* is per-connection, so the pragma and the DROP must run
    on the same connection. One statement block is one `Exec` is one pooled
    connection — that is the guarantee; loose statements have none. Wrap the DDL in
    an explicit `BEGIN`/`COMMIT` so a failure rolls back, and restore
    `PRAGMA foreign_keys = on` inside the same block, because the DSN pragma is
    applied only at connection open. A migration test seeding every cascade child
    and asserting it is still there is the acceptance criterion, not a nicety.
    - **Re-check the keys before `COMMIT`, and make the check able to fail.** With
      enforcement off, a mis-copied id orphans children silently. A bare `PRAGMA
      foreign_key_check` cannot catch it — it *returns* offending rows, and `Exec`
      discards them — so land the count somewhere that rejects it:
      `CREATE TABLE fk_violations (n INTEGER NOT NULL CHECK (n = 0)); INSERT INTO
      fk_violations (n) SELECT count(*) FROM pragma_foreign_key_check; DROP TABLE
      fk_violations;`.
    - **A failure mid-block returns a connection to the pool with foreign keys
      off**, since the restoring pragma never runs. Contained today only because
      `store.Open` propagates the error and the process exits — do not build
      anything that keeps running past a failed migration.
