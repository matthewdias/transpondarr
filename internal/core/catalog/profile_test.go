package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

func seriesProfile(t *testing.T, st *store.Store, seriesID int64) int64 {
	t.Helper()
	var id int64
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT quality_profile_id FROM series WHERE id = ?`, seriesID).Scan(&id); err != nil {
		t.Fatalf("read quality_profile_id: %v", err)
	}
	return id
}

func profileService(t *testing.T) (*store.Store, *Service) {
	t.Helper()
	st := coretest.NewStore(t)
	prov := &fakeProvider{
		meta: metadata.TitleMeta{
			ProviderID: 42, Titles: metadata.Titles{Romaji: "Placeholder Saga"}, Format: "TV",
		},
		items: []metadata.ItemMeta{{Number: 1}, {Number: 2}},
	}
	return st, NewService(st, prov)
}

// The profile is an add-time choice, so it has to be assignable in the same
// write as the series rather than by a follow-up call the user has to remember.
func TestAddSeriesAppliesTheChosenProfile(t *testing.T) {
	st, svc := profileService(t)
	row, err := st.DB.ExecContext(context.Background(),
		`INSERT INTO quality_profiles (name) VALUES ('Sharper')`)
	if err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	profileID, err := row.LastInsertId()
	if err != nil {
		t.Fatalf("profile id: %v", err)
	}

	title, err := svc.AddSeries(context.Background(), "fake", 42, true, MonitorAll, profileID)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if got := seriesProfile(t, st, title.ID); got != profileID {
		t.Errorf("quality_profile_id = %d, want %d", got, profileID)
	}
}

// An omitted profile is not a choice: the column default (the seeded is-default
// profile) has to survive, or every Discovery add would need one.
func TestAddSeriesWithoutAProfileKeepsTheDefault(t *testing.T) {
	st, svc := profileService(t)

	title, err := svc.AddSeries(context.Background(), "fake", 42, true, MonitorAll, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	var want int64
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT id FROM quality_profiles WHERE is_default = 1`).Scan(&want); err != nil {
		t.Fatalf("read the default profile: %v", err)
	}
	if got := seriesProfile(t, st, title.ID); got != want {
		t.Errorf("quality_profile_id = %d, want the default %d", got, want)
	}
}

// The assignment shares the add's transaction, so a bad profile id leaves
// nothing behind rather than a series on a profile the caller never asked for.
func TestAddSeriesRejectsAnUnknownProfileAndPersistsNothing(t *testing.T) {
	st, svc := profileService(t)

	_, err := svc.AddSeries(context.Background(), "fake", 42, true, MonitorAll, 9999)
	if !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("AddSeries with an unknown profile = %v, want ErrUnknownProfile", err)
	}
	var series int
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT count(*) FROM series`).Scan(&series); err != nil {
		t.Fatalf("count series: %v", err)
	}
	if series != 0 {
		t.Errorf("%d series persisted, want the transaction rolled back", series)
	}
}
