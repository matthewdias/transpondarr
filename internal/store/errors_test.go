package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/matthewdias/transpondarr/internal/store/db"
)

func TestIsUniqueViolationOnDuplicateProfileName(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	params := db.CreateQualityProfileParams{Name: "Dup", ResolutionOrder: `[]`, HardExcludes: `[]`}
	if _, err := st.Q.CreateQualityProfile(ctx, params); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := func() error { _, err := st.Q.CreateQualityProfile(ctx, params); return err }()
	if err == nil {
		t.Fatal("duplicate name should be rejected by the unique constraint")
	}
	if !IsUniqueViolation(err) {
		t.Errorf("IsUniqueViolation(%v) = false, want true", err)
	}
	if !IsUniqueViolation(fmt.Errorf("create profile: %w", err)) {
		t.Error("a wrapped unique violation should still be classified")
	}
}

func TestIsUniqueViolationIgnoresOtherErrors(t *testing.T) {
	st := tempStore(t)
	ctx := context.Background()

	_, checkErr := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
		Name: "BadRes", ResolutionOrder: `not json`, HardExcludes: `[]`,
	})
	if checkErr == nil {
		t.Fatal("invalid resolution_order JSON should be rejected")
	}

	cases := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"no rows", sql.ErrNoRows},
		{"lookalike message", errors.New("UNIQUE constraint failed: quality_profiles.name")},
		{"check constraint", checkErr},
	}
	for _, tc := range cases {
		if IsUniqueViolation(tc.err) {
			t.Errorf("IsUniqueViolation(%s) = true, want false", tc.name)
		}
	}
}
