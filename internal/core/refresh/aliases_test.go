package refresh_test

import (
	"slices"
	"testing"

	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// Aliases are what a person entered, so a refresh rewriting provider-derived state
// leaves them. Nothing but SetTitleAliases writes the column, so no mutation of
// refresh can make this fail; it records the guarantee for the next writer.
func TestRefresh_LeavesAliases(t *testing.T) {
	st := coretest.NewStore(t)
	prov := newFakeProvider()
	prov.episodes[100] = 13
	id := seedTitle(t, st, 100, 12)
	want := []string{"Hoshi no Kishi 2026"}
	if _, err := st.Q.SetTitleAliases(t.Context(), db.SetTitleAliasesParams{
		Aliases: store.EncodeAliases(want), ID: id,
	}); err != nil {
		t.Fatalf("set aliases: %v", err)
	}

	if err := newService(t, st, prov).RefreshOnce(t.Context()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if got := items(t, st, id); len(got) != 13 {
		t.Fatalf("got %d items, want 13: the refresh must have reached this title", len(got))
	}
	title, err := st.Q.GetTitle(t.Context(), id)
	if err != nil {
		t.Fatalf("load title: %v", err)
	}
	if got := store.TitleAliases(title.Aliases); !slices.Equal(got, want) {
		t.Errorf("aliases after refresh = %q, want %q", got, want)
	}
}
