package store

import (
	"context"
	"testing"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

func TestDefaultProfileSeededAndAssignedToNewSeries(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	def, err := st.Q.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("get default profile: %v", err)
	}
	if def.Name != "Default" || def.IsDefault != 1 {
		t.Errorf("default profile = %q (is_default=%d), want seeded Default", def.Name, def.IsDefault)
	}

	series, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: "X", Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	if series.QualityProfileID != def.ID {
		t.Errorf("new series profile = %d, want default %d", series.QualityProfileID, def.ID)
	}
}

func TestDeleteQualityProfileRefusesDefault(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	def, err := st.Q.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("get default profile: %v", err)
	}
	rows, err := st.Q.DeleteQualityProfile(ctx, def.ID)
	if err != nil {
		t.Fatalf("delete default profile: %v", err)
	}
	if rows != 0 {
		t.Errorf("delete default reported %d rows, want 0 (refused)", rows)
	}
	if _, err := st.Q.GetQualityProfile(ctx, def.ID); err != nil {
		t.Error("default profile must survive a delete attempt")
	}
}

func TestReassignThenDeleteProfileCascadesGroups(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	prof, err := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name:            "Strict",
		ResolutionOrder: `["1080p"]`,
		HardExcludes:    `["hardsub"]`,
		MinScore:        100,
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if _, err := st.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
		ProfileID: prof.ID, Rank: 1, GroupName: "ExampleSubs",
	}); err != nil {
		t.Fatalf("add group: %v", err)
	}

	series, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: "X", Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	if _, err := st.Q.SetSeriesProfile(ctx, db.SetSeriesProfileParams{QualityProfileID: prof.ID, ID: series.ID, ID_2: prof.ID}); err != nil {
		t.Fatalf("set series profile: %v", err)
	}
	n, err := st.Q.CountSeriesByProfile(ctx, prof.ID)
	if err != nil || n != 1 {
		t.Fatalf("count by profile = %d (%v), want 1", n, err)
	}

	def, err := st.Q.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("get default profile: %v", err)
	}

	// The prompt-to-migrate delete flow: move the series, then delete.
	if err := st.Q.ReassignSeriesProfile(ctx, db.ReassignSeriesProfileParams{
		QualityProfileID: def.ID, QualityProfileID_2: prof.ID,
	}); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	rows, err := st.Q.DeleteQualityProfile(ctx, prof.ID)
	if err != nil {
		t.Fatalf("delete profile: %v", err)
	}
	if rows != 1 {
		t.Errorf("delete reported %d rows, want 1", rows)
	}
	groups, err := st.Q.ListProfileGroups(ctx, prof.ID)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("groups after profile delete = %d rows, want 0 (cascade)", len(groups))
	}
	got, err := st.Q.GetSeries(ctx, series.ID)
	if err != nil {
		t.Fatalf("get series: %v", err)
	}
	if got.QualityProfileID != def.ID {
		t.Errorf("series profile after reassign = %d, want default %d", got.QualityProfileID, def.ID)
	}
}

func TestDeleteQualityProfileRefusesWhileInUse(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	prof, err := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "InUse", ResolutionOrder: `["1080p"]`, HardExcludes: `[]`,
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	series, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: "X", Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	if _, err := st.Q.SetSeriesProfile(ctx, db.SetSeriesProfileParams{QualityProfileID: prof.ID, ID: series.ID, ID_2: prof.ID}); err != nil {
		t.Fatalf("set series profile: %v", err)
	}

	rows, err := st.Q.DeleteQualityProfile(ctx, prof.ID)
	if err != nil {
		t.Fatalf("delete in-use profile: %v", err)
	}
	if rows != 0 {
		t.Errorf("delete of in-use profile reported %d rows, want 0 (refused)", rows)
	}
	if _, err := st.Q.GetQualityProfile(ctx, prof.ID); err != nil {
		t.Error("a profile still assigned to a series must survive a delete attempt")
	}
}

func TestSetSeriesProfileRejectsMissingProfile(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	series, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: "X", Format: "TV", Monitored: 1})
	if err != nil {
		t.Fatalf("create series: %v", err)
	}
	rows, err := st.Q.SetSeriesProfile(ctx, db.SetSeriesProfileParams{QualityProfileID: 999, ID: series.ID, ID_2: 999})
	if err != nil {
		t.Fatalf("set series profile: %v", err)
	}
	if rows != 0 {
		t.Errorf("set to missing profile reported %d rows, want 0 (refused)", rows)
	}
	got, err := st.Q.GetSeries(ctx, series.ID)
	if err != nil {
		t.Fatalf("get series: %v", err)
	}
	if got.QualityProfileID == 999 {
		t.Error("series must not point at a profile that does not exist")
	}
}

func TestQualityProfileRejectsInvalidJSON(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	if _, err := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "BadRes", ResolutionOrder: `not json`, HardExcludes: `[]`,
	}); err == nil {
		t.Error("invalid resolution_order JSON should be rejected")
	}
	if _, err := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "BadExcludes", ResolutionOrder: `["1080p"]`, HardExcludes: ``,
	}); err == nil {
		t.Error("invalid hard_excludes JSON should be rejected")
	}
}

func TestOnlyOneDefaultProfile(t *testing.T) {
	st := tempStore(t)

	if _, err := st.DB.Exec(`INSERT INTO quality_profiles (name, is_default) VALUES ('Second', 1)`); err == nil {
		t.Error("a second default profile should be rejected by the partial unique index")
	}
}

func TestProfileGroupsOrderedUnblockedFirstThenByRank(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	def, err := st.Q.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("get default profile: %v", err)
	}
	add := func(rank int64, name string, blocked int64) {
		t.Helper()
		if _, err := st.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
			ProfileID: def.ID, Rank: rank, GroupName: name, Blocked: blocked,
		}); err != nil {
			t.Fatalf("add group %s: %v", name, err)
		}
	}
	add(1, "BlockedCorp", 1)
	add(2, "SecondChoice", 0)
	add(1, "FirstChoice", 0)

	groups, err := st.Q.ListProfileGroups(ctx, def.ID)
	if err != nil {
		t.Fatalf("list groups: %v", err)
	}
	var names []string
	for _, g := range groups {
		names = append(names, g.GroupName)
	}
	want := []string{"FirstChoice", "SecondChoice", "BlockedCorp"}
	for i, w := range want {
		if i >= len(names) || names[i] != w {
			t.Fatalf("group order = %v, want %v", names, want)
		}
	}

	// One row per group per profile: a duplicate name must be rejected.
	if _, err := st.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
		ProfileID: def.ID, Rank: 5, GroupName: "FirstChoice",
	}); err == nil {
		t.Error("duplicate group name in one profile should error")
	}
}

// The two batch reads behind the list endpoint: groups must stay partitioned by
// profile with each partition ranked as ListProfileGroups returns it, and a
// profile no series uses must be absent rather than zero.
func TestBatchProfileReadsPartitionByProfile(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	def, err := st.Q.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("get default profile: %v", err)
	}
	other, err := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "Strict", ResolutionOrder: `["1080p"]`, HardExcludes: `[]`,
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	unused, err := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "Unused", ResolutionOrder: `["1080p"]`, HardExcludes: `[]`,
	})
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	add := func(profileID, rank int64, name string, blocked int64) {
		t.Helper()
		if _, err := st.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
			ProfileID: profileID, Rank: rank, GroupName: name, Blocked: blocked,
		}); err != nil {
			t.Fatalf("add group %s: %v", name, err)
		}
	}
	add(other.ID, 1, "OtherBlocked", 1)
	add(def.ID, 2, "DefSecond", 0)
	add(other.ID, 2, "OtherOnly", 0)
	add(def.ID, 1, "DefFirst", 0)
	add(def.ID, 3, "DefBlocked", 1)

	rows, err := st.Q.ListAllProfileGroups(ctx)
	if err != nil {
		t.Fatalf("list all groups: %v", err)
	}
	got := map[int64][]string{}
	for _, g := range rows {
		got[g.ProfileID] = append(got[g.ProfileID], g.GroupName)
	}
	want := map[int64][]string{
		def.ID:   {"DefFirst", "DefSecond", "DefBlocked"},
		other.ID: {"OtherOnly", "OtherBlocked"},
	}
	for id, w := range want {
		if len(got[id]) != len(w) {
			t.Fatalf("profile %d groups = %v, want %v", id, got[id], w)
		}
		for i := range w {
			if got[id][i] != w[i] {
				t.Fatalf("profile %d groups = %v, want %v", id, got[id], w)
			}
		}
	}
	if _, ok := got[unused.ID]; ok {
		t.Errorf("profile with no groups should contribute no rows, got %v", got[unused.ID])
	}

	for _, title := range []string{"A", "B"} {
		s, err := st.Q.CreateSeries(ctx, db.CreateSeriesParams{Title: title, Format: "TV", Monitored: 1})
		if err != nil {
			t.Fatalf("create series %s: %v", title, err)
		}
		if _, err := st.Q.SetSeriesProfile(ctx, db.SetSeriesProfileParams{
			QualityProfileID: other.ID, ID: s.ID, ID_2: other.ID,
		}); err != nil {
			t.Fatalf("assign profile to %s: %v", title, err)
		}
	}

	countRows, err := st.Q.CountSeriesPerProfile(ctx)
	if err != nil {
		t.Fatalf("count series per profile: %v", err)
	}
	counts := map[int64]int64{}
	for _, c := range countRows {
		counts[c.QualityProfileID] = c.SeriesCount
	}
	if counts[other.ID] != 2 {
		t.Errorf("Strict series_count = %d, want 2", counts[other.ID])
	}
	if _, ok := counts[unused.ID]; ok {
		t.Errorf("an unused profile should have no count row, got %d", counts[unused.ID])
	}
}
