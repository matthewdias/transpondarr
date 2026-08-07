package airing_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// Throwaway: measures autocommit vs. one transaction for the sync's write shape.
func TestTmpMeasureWriteCost(t *testing.T) {
	const n = 1000
	st := coretest.NewStore(t)
	ctx := context.Background()
	a := seedSeries(t, st, 900, 0)
	b := seedSeries(t, st, 901, 0)

	param := func(series int64, i int) db.UpsertWantedItemParams {
		return db.UpsertWantedItemParams{
			SeriesID: series, Kind: "episode",
			Number:    sql.NullInt64{Int64: int64(i), Valid: true},
			Monitored: 1,
		}
	}

	start := time.Now()
	for i := 1; i <= n; i++ {
		if _, err := st.Q.UpsertWantedItem(ctx, param(a, i)); err != nil {
			t.Fatal(err)
		}
	}
	auto := time.Since(start)

	start = time.Now()
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	q := st.Q.WithTx(tx)
	for i := 1; i <= n; i++ {
		if _, err := q.UpsertWantedItem(ctx, param(b, i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	wrapped := time.Since(start)

	t.Logf("%d upserts: autocommit=%v transaction=%v (%.0fx)", n, auto.Round(time.Millisecond),
		wrapped.Round(100*time.Microsecond), float64(auto)/float64(wrapped))
}
