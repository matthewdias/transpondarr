package store

import (
	"context"
	"testing"
)

// Foreign-key enforcement must come from the DSN pragma, not a one-off Exec: a
// plain `PRAGMA foreign_keys = ON` applies only to the single pooled connection
// that ran it. Hold several connections open at once (forcing distinct physical
// connections) and check the pragma on each.
func TestForeignKeysEnforcedOnEveryPooledConn(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	const conns = 4
	st.DB.SetMaxOpenConns(conns)
	for range conns {
		conn, err := st.DB.Conn(ctx)
		if err != nil {
			t.Fatalf("acquire conn: %v", err)
		}
		defer func() { _ = conn.Close() }()

		var fk int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("read pragma: %v", err)
		}
		if fk != 1 {
			t.Fatal("pooled connection has foreign_keys disabled")
		}
	}
}
