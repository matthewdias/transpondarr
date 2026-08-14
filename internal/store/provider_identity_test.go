package store

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
)

// The provider-identity migration rebuilds title to shed the single-column
// UNIQUE on anilist_id, and title has three ON DELETE CASCADE children. A
// rebuild that lets the cascade fire empties the user's library silently, so the
// survival of every child is the acceptance criterion, not a nicety.
func TestProviderIdentityMigrationKeepsCascadeChildren(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	if err := goose.DownTo(st.DB, "migrations", 18); err != nil {
		t.Fatalf("roll back to the anilist_id schema: %v", err)
	}

	var titleID int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO series (anilist_id, title, format, monitored, quality_profile_id, pinned_group, search_backoff)
		 VALUES (4321, 'Populated Show', 'TV', 1, 7, 'ExampleSubs', 3) RETURNING id`).Scan(&titleID); err != nil {
		t.Fatalf("seed series: %v", err)
	}
	// A second row with no provider id at all: the CHECK must accept it, and the
	// rebuild must not invent a provider for it.
	var untrackedID int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO series (title, format, monitored) VALUES ('No Provider', 'TV', 1) RETURNING id`).Scan(&untrackedID); err != nil {
		t.Fatalf("seed provider-less series: %v", err)
	}

	var itemID int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO wanted_items (series_id, kind, number, have) VALUES (?, 'episode', 1, 1) RETURNING id`,
		titleID).Scan(&itemID); err != nil {
		t.Fatalf("seed wanted item: %v", err)
	}
	for _, seed := range []struct {
		name string
		sql  string
		args []any
	}{
		{"grab", `INSERT INTO grabs (wanted_item_id, info_hash, release_title, status) VALUES (?, 'abc', '[ExampleSubs] Show - 01', 'imported')`, []any{itemID}},
		{"pass outcome", `INSERT INTO pass_outcomes (wanted_item_id, outcome, source, recorded_at) VALUES (?, 'no_match', 'sweep', '2026-01-01T00:00:00Z')`, []any{itemID}},
		{"grab event", `INSERT INTO grab_events (series_id, wanted_item_id, info_hash, event) VALUES (?, ?, 'abc', 'grabbed')`, []any{titleID, itemID}},
		{"blocklist entry", `INSERT INTO release_blocklist (series_id, info_hash, release_title, normalized_title, reason) VALUES (?, 'def', '[Other] Show - 01', 'other show 01', 'import failed')`, []any{titleID}},
	} {
		if _, err := st.DB.ExecContext(ctx, seed.sql, seed.args...); err != nil {
			t.Fatalf("seed %s: %v", seed.name, err)
		}
	}

	if err := goose.Up(st.DB, "migrations"); err != nil {
		t.Fatalf("re-apply the provider-identity migration: %v", err)
	}

	for _, want := range []struct {
		name  string
		query string
	}{
		{"series", `SELECT COUNT(*) FROM series`},
		{"wanted_items", `SELECT COUNT(*) FROM wanted_items`},
		{"grabs", `SELECT COUNT(*) FROM grabs`},
		{"pass_outcomes", `SELECT COUNT(*) FROM pass_outcomes`},
		{"grab_events", `SELECT COUNT(*) FROM grab_events`},
		{"release_blocklist", `SELECT COUNT(*) FROM release_blocklist`},
	} {
		var n int
		if err := st.DB.QueryRowContext(ctx, want.query).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", want.name, err)
		}
		if want.name == "series" {
			if n != 2 {
				t.Errorf("%s rows after migration = %d, want 2", want.name, n)
			}
			continue
		}
		if n != 1 {
			t.Errorf("%s rows after migration = %d, want 1 (the cascade fired)", want.name, n)
		}
	}

	// Every other column the eight accumulated ALTERs added must survive the
	// rebuild with its value, not just its name.
	var (
		provider      string
		providerID    int64
		title         string
		profileID     int64
		pinnedGroup   string
		searchBackoff int64
	)
	if err := st.DB.QueryRowContext(ctx,
		`SELECT provider, provider_id, title, quality_profile_id, pinned_group, search_backoff FROM series WHERE id = ?`,
		titleID).Scan(&provider, &providerID, &title, &profileID, &pinnedGroup, &searchBackoff); err != nil {
		t.Fatalf("read migrated series: %v", err)
	}
	if provider != "anilist" || providerID != 4321 {
		t.Errorf("identity = (%q, %d), want (anilist, 4321)", provider, providerID)
	}
	if title != "Populated Show" || profileID != 7 || pinnedGroup != "ExampleSubs" || searchBackoff != 3 {
		t.Errorf("carried columns = (%q, %d, %q, %d), want (Populated Show, 7, ExampleSubs, 3)",
			title, profileID, pinnedGroup, searchBackoff)
	}

	var nulls int
	if err := st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM series WHERE id = ? AND provider IS NULL AND provider_id IS NULL`,
		untrackedID).Scan(&nulls); err != nil {
		t.Fatalf("read provider-less series: %v", err)
	}
	if nulls != 1 {
		t.Error("the provider-less row did not migrate to (NULL, NULL)")
	}

	// The cascade must still be wired after the rebuild, or a deleted title
	// leaves orphans forever.
	if _, err := st.DB.ExecContext(ctx, `DELETE FROM series WHERE id = ?`, titleID); err != nil {
		t.Fatalf("delete series: %v", err)
	}
	for _, table := range []string{"wanted_items", "grabs", "pass_outcomes", "grab_events", "release_blocklist"} {
		var n int
		if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Errorf("%s kept %d rows after the series was deleted; the cascade was lost in the rebuild", table, n)
		}
	}
}

// The pair is the identity: the same id in two provider spaces is two titles,
// which the old single-column UNIQUE could not express.
func TestTitleIdentityIsThePair(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	const insert = `INSERT INTO series (provider, provider_id, title) VALUES (?, ?, 'Show')`
	if _, err := st.DB.ExecContext(ctx, insert, "anilist", 123); err != nil {
		t.Fatalf("insert anilist 123: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, insert, "mal", 123); err != nil {
		t.Errorf("the same id in another provider space was rejected: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx, insert, "anilist", 123); err == nil {
		t.Error("a duplicate (provider, provider_id) was accepted")
	}
}

// Half an identity is not an identity: a provider with no id names an id space
// and nothing in it, and an id with no provider is the ambiguity this change
// exists to remove.
func TestTitleIdentityIsBothOrNeither(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO series (provider, title) VALUES ('anilist', 'Show')`); err == nil {
		t.Error("a provider with no id was accepted")
	}
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO series (provider_id, title) VALUES (123, 'Show')`); err == nil {
		t.Error("an id with no provider was accepted")
	}
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO series (title) VALUES ('Untracked')`); err != nil {
		t.Errorf("an untracked series with neither was rejected: %v", err)
	}
}
