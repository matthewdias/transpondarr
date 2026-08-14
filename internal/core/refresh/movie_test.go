package refresh_test

import (
	"context"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
)

// staticProvider answers GetTitle with one fixed snapshot, so a test can vary
// the format and year the refresh sees.
type staticProvider struct {
	meta  metadata.TitleMeta
	items []metadata.ItemMeta
}

func (p *staticProvider) Name() string { return "anilist" }

func (p *staticProvider) Search(context.Context, string) ([]metadata.Candidate, error) {
	return nil, nil
}

func (p *staticProvider) GetTitle(context.Context, int64) (metadata.TitleMeta, []metadata.ItemMeta, error) {
	return p.meta, p.items, nil
}

// seedMovie inserts a movie title whose single item is seeded with the given
// kind, so the pre-#208 legacy row is expressible.
func seedMovie(t *testing.T, st *store.Store, providerID int64, kind string, year int) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := st.DB.QueryRowContext(ctx,
		`INSERT INTO series (provider, provider_id, title, format, monitored, year)
		 VALUES ('anilist', ?, 'Sample Film', 'MOVIE', 1, ?) RETURNING id`,
		providerID, year).Scan(&id); err != nil {
		t.Fatalf("insert movie series: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO wanted_items (series_id, kind, number) VALUES (?, ?, 1)`, id, kind); err != nil {
		t.Fatalf("insert item: %v", err)
	}
	return id
}

func storedYear(t *testing.T, st *store.Store, titleID int64) int {
	t.Helper()
	var year int
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT year FROM series WHERE id = ?`, titleID).Scan(&year); err != nil {
		t.Fatalf("read year: %v", err)
	}
	return year
}

func movieProvider(year int) *staticProvider {
	return &staticProvider{
		meta: metadata.TitleMeta{
			ProviderID: 4321, Format: domain.FormatMovie, Status: "FINISHED", Year: year,
		},
		items: []metadata.ItemMeta{{Number: 1}},
	}
}

// The #208 migration re-keys a legacy ('episode', 1) row precisely so this
// refresh upserts onto it: idx_wanted_items_identity is (title_id, kind,
// number), so a ('movie', 1) insert would not conflict and the film would read
// 1/2 forever.
func TestRefreshDoesNotDuplicateAMoviesItem(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovie(t, st, 4321, string(domain.KindMovie), 2020)
	svc := newService(t, st, movieProvider(2020))

	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}

	var items int
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM wanted_items WHERE series_id = ?`, titleID).Scan(&items); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if items != 1 {
		t.Errorf("items after refresh = %d, want 1", items)
	}
	var kind string
	if err := st.DB.QueryRowContext(context.Background(),
		`SELECT kind FROM wanted_items WHERE series_id = ?`, titleID).Scan(&kind); err != nil {
		t.Fatalf("read kind: %v", err)
	}
	if kind != string(domain.KindMovie) {
		t.Errorf("kind = %q, want movie", kind)
	}
}

// A film added before AniList published a date gains one on cadence.
func TestRefreshFillsAYearThatArrivesLater(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovie(t, st, 4321, string(domain.KindMovie), 0)
	svc := newService(t, st, movieProvider(2027))

	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if got := storedYear(t, st, titleID); got != 2027 {
		t.Errorf("year after refresh = %d, want 2027", got)
	}
}

// A transient upstream null must never erase a stored year: the naming layer
// reads it, and zero means "not on record".
func TestRefreshNeverClearsAStoredYear(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovie(t, st, 4321, string(domain.KindMovie), 2020)
	svc := newService(t, st, movieProvider(0))

	if err := svc.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if got := storedYear(t, st, titleID); got != 2020 {
		t.Errorf("year after refresh = %d, want the stored 2020 kept", got)
	}
}
