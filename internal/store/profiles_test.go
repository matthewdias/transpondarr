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
	if err := st.Q.DeleteQualityProfile(ctx, def.ID); err != nil {
		t.Fatalf("delete default profile: %v", err)
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
	if err := st.Q.SetSeriesProfile(ctx, db.SetSeriesProfileParams{QualityProfileID: prof.ID, ID: series.ID}); err != nil {
		t.Fatalf("set series profile: %v", err)
	}
	n, err := st.Q.CountSeriesByProfile(ctx, prof.ID)
	if err != nil || n != 1 {
		t.Fatalf("count by profile = %d (%v), want 1", n, err)
	}

	// The prompt-to-migrate delete flow: move the series, then delete.
	if err := st.Q.ReassignSeriesProfile(ctx, db.ReassignSeriesProfileParams{
		QualityProfileID: 1, QualityProfileID_2: prof.ID,
	}); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	if err := st.Q.DeleteQualityProfile(ctx, prof.ID); err != nil {
		t.Fatalf("delete profile: %v", err)
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
	if got.QualityProfileID != 1 {
		t.Errorf("series profile after reassign = %d, want 1", got.QualityProfileID)
	}
}

func TestProfileGroupsOrderedUnblockedFirstThenByRank(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	add := func(rank int64, name string, blocked int64) {
		t.Helper()
		if _, err := st.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
			ProfileID: 1, Rank: rank, GroupName: name, Blocked: blocked,
		}); err != nil {
			t.Fatalf("add group %s: %v", name, err)
		}
	}
	add(1, "BlockedCorp", 1)
	add(2, "SecondChoice", 0)
	add(1, "FirstChoice", 0)

	groups, err := st.Q.ListProfileGroups(ctx, 1)
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
		ProfileID: 1, Rank: 5, GroupName: "FirstChoice",
	}); err == nil {
		t.Error("duplicate group name in one profile should error")
	}
}
