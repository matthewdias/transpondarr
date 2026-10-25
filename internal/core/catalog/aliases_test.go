package catalog

import (
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

func storedAliases(t *testing.T, st *store.Store, titleID int64) []string {
	t.Helper()
	title, err := st.Q.GetTitle(t.Context(), titleID)
	if err != nil {
		t.Fatalf("load title: %v", err)
	}
	return store.TitleAliases(title.Aliases)
}

// PUT replaces: each call stores exactly the list it was given, tidied, and an
// empty list clears them.
func TestSetAliases_ReplacesTheWholeList(t *testing.T) {
	st := coretest.NewStore(t)
	id := seedItemlessTitle(t, st, 1)
	svc := NewService(st, &fakeProvider{})

	got, err := svc.SetAliases(t.Context(), id, []string{"  Hoshi   no Kishi 2026 ", "Starlit Knight"})
	if err != nil {
		t.Fatalf("SetAliases: %v", err)
	}
	want := []string{"Hoshi no Kishi 2026", "Starlit Knight"}
	if !slices.Equal(got, want) {
		t.Errorf("returned %q, want %q: trimmed, inner spaces collapsed, order kept", got, want)
	}
	if stored := storedAliases(t, st, id); !slices.Equal(stored, want) {
		t.Errorf("stored %q, want %q", stored, want)
	}

	if _, err := svc.SetAliases(t.Context(), id, []string{"Other Name"}); err != nil {
		t.Fatalf("SetAliases: %v", err)
	}
	if stored := storedAliases(t, st, id); !slices.Equal(stored, []string{"Other Name"}) {
		t.Errorf("stored %q after a second PUT, want only [Other Name]", stored)
	}

	if _, err := svc.SetAliases(t.Context(), id, nil); err != nil {
		t.Fatalf("SetAliases(nil): %v", err)
	}
	if stored := storedAliases(t, st, id); len(stored) != 0 {
		t.Errorf("stored %q after clearing, want none", stored)
	}
}

// Every refusal names its cause and leaves the stored list as it was.
func TestSetAliases_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		aliases []string
		want    string
	}{
		{"blank", []string{"Starlit Knight", "   "}, "blank"},
		{"duplicate ignoring case and spacing", []string{"Starlit Knight", "starlit  knight"}, "listed twice"},
		{"too many", []string{"Alias One", "Alias Two", "Alias Three", "Alias Four", "Alias Five", "Alias Six"}, "at most 5"},
		{"too long", []string{strings.Repeat("a", MaxAliasLength+1)}, "longer than 200"},
		{"nothing a release name can match", []string{"魔法騎士"}, "Latin letters or digits"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := coretest.NewStore(t)
			id := seedItemlessTitle(t, st, 1)
			svc := NewService(st, &fakeProvider{})
			if _, err := svc.SetAliases(t.Context(), id, []string{"Kept Alias"}); err != nil {
				t.Fatalf("seed alias: %v", err)
			}

			_, err := svc.SetAliases(t.Context(), id, tc.aliases)
			var aliasErr *AliasError
			if !errors.As(err, &aliasErr) {
				t.Fatalf("err = %v, want an *AliasError", err)
			}
			if !strings.Contains(aliasErr.Error(), tc.want) {
				t.Errorf("message %q doesn't say %q", aliasErr.Error(), tc.want)
			}
			if stored := storedAliases(t, st, id); !slices.Equal(stored, []string{"Kept Alias"}) {
				t.Errorf("stored %q after a refusal, want it unchanged", stored)
			}
		})
	}
}

// A changed alias changes what the search sweep asks for, so the title is due at
// once instead of waiting out a backoff its old names accrued.
func TestSetAliases_ResetsSearchCadence(t *testing.T) {
	st := coretest.NewStore(t)
	id := seedItemlessTitle(t, st, 1)
	settledDeadEnd(t, st, id)

	if _, err := NewService(st, &fakeProvider{}).SetAliases(t.Context(), id, []string{"Hoshi no Kishi 2026"}); err != nil {
		t.Fatalf("SetAliases: %v", err)
	}
	if _, backoff, next, _ := searchState(t, st, id); backoff != 0 || next != nil {
		t.Errorf("backoff = %d, next_search_at = %v; want the cadence reset", backoff, next)
	}
}

func TestSetAliases_UnknownTitle(t *testing.T) {
	st := coretest.NewStore(t)

	_, err := NewService(st, &fakeProvider{}).SetAliases(t.Context(), 999, []string{"Hoshi no Kishi 2026"})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows so the route can 404", err)
	}
}
