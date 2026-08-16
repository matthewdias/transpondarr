package catalog

import (
	"context"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/coretest"
)

// movieProvider is a film as the adapter hands one over: one item, whatever the
// upstream episode count said.
func movieProvider(year, next int) *fakeProvider {
	return &fakeProvider{
		meta: metadata.TitleMeta{
			ProviderID: 4321, Titles: metadata.Titles{Romaji: "Sample Film"},
			Format: domain.FormatMovie, Status: "FINISHED", Year: year, NextItem: next,
		},
		items: []metadata.ItemMeta{{Number: 1}},
	}
}

func TestAddMovieCreatesOneItemOfKindMovie(t *testing.T) {
	st := coretest.NewStore(t)
	svc := NewService(st, movieProvider(2020, 0))
	ctx := context.Background()

	title, err := svc.AddTitle(ctx, "fake", 4321, true, MonitorAll, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if len(title.Items) != 1 || title.Items[0].Kind != domain.KindMovie || title.Items[0].Number != 1 {
		t.Errorf("returned items = %+v, want one movie-kind item numbered 1", title.Items)
	}
	if title.Year != 2020 {
		t.Errorf("returned Year = %d, want 2020", title.Year)
	}

	var (
		kind   string
		number int
		items  int
	)
	if err := st.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM wanted_items WHERE series_id = ?`, title.ID).Scan(&items); err != nil {
		t.Fatalf("count items: %v", err)
	}
	if items != 1 {
		t.Fatalf("stored items = %d, want 1", items)
	}
	if err := st.DB.QueryRowContext(ctx,
		`SELECT kind, number FROM wanted_items WHERE series_id = ?`, title.ID).Scan(&kind, &number); err != nil {
		t.Fatalf("read item: %v", err)
	}
	if kind != string(domain.KindMovie) || number != 1 {
		t.Errorf("stored item = (%q, %d), want (movie, 1)", kind, number)
	}

	var year int
	if err := st.DB.QueryRowContext(ctx, `SELECT year FROM series WHERE id = ?`, title.ID).Scan(&year); err != nil {
		t.Fatalf("read year: %v", err)
	}
	if year != 2020 {
		t.Errorf("stored year = %d, want 2020", year)
	}
}

// A released film has nothing left to come, so "future only" monitors nothing —
// the same answer a finished series gives, and deliberate rather than the #188
// edge it used to be (#217). Format is not the discriminator here; having aired
// is.
func TestAddMovieWithMonitorFutureLeavesItsItemUnmonitored(t *testing.T) {
	st := coretest.NewStore(t)
	svc := NewService(st, movieProvider(0, 0))
	ctx := context.Background()

	title, err := svc.AddTitle(ctx, "fake", 4321, true, MonitorFuture, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	var monitored int
	if err := st.DB.QueryRowContext(ctx,
		`SELECT monitored FROM wanted_items WHERE series_id = ?`, title.ID).Scan(&monitored); err != nil {
		t.Fatalf("read monitored: %v", err)
	}
	if monitored != 0 {
		t.Errorf("monitored = %d, want 0 (cut lands past the only item)", monitored)
	}
}

// The #217 population: AniList schedules no broadcast for most unreleased films,
// so "future" fell past the only item and the film was never searched for.
func TestAddUnreleasedMovieWithMonitorFutureMonitorsIt(t *testing.T) {
	st := coretest.NewStore(t)
	prov := movieProvider(0, 0)
	prov.meta.Status = "NOT_YET_RELEASED"
	svc := NewService(st, prov)
	ctx := context.Background()

	title, err := svc.AddTitle(ctx, "fake", 4321, true, MonitorFuture, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	var monitored int
	if err := st.DB.QueryRowContext(ctx,
		`SELECT monitored FROM wanted_items WHERE series_id = ?`, title.ID).Scan(&monitored); err != nil {
		t.Fatalf("read monitored: %v", err)
	}
	if monitored != 1 {
		t.Errorf("monitored = %d, want 1 (nothing has aired, so there is no back catalogue to skip)", monitored)
	}
}

// A dated film's premiere is its "next item", so "future" monitors it.
func TestAddMovieWithMonitorFutureAndAPremiereMonitorsIt(t *testing.T) {
	st := coretest.NewStore(t)
	svc := NewService(st, movieProvider(2027, 1))
	ctx := context.Background()

	title, err := svc.AddTitle(ctx, "fake", 4321, true, MonitorFuture, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	var monitored int
	if err := st.DB.QueryRowContext(ctx,
		`SELECT monitored FROM wanted_items WHERE series_id = ?`, title.ID).Scan(&monitored); err != nil {
		t.Fatalf("read monitored: %v", err)
	}
	if monitored != 1 {
		t.Errorf("monitored = %d, want 1", monitored)
	}
}

// A single-episode OVA is not a movie: the kind keys on format alone.
func TestAddOneEpisodeOVAKeepsEpisodeKind(t *testing.T) {
	st := coretest.NewStore(t)
	prov := &fakeProvider{
		meta: metadata.TitleMeta{
			ProviderID: 77, Titles: metadata.Titles{Romaji: "Sample OVA"},
			Format: domain.FormatOVA, Status: "FINISHED", Year: 2014,
		},
		items: []metadata.ItemMeta{{Number: 1}},
	}
	svc := NewService(st, prov)
	ctx := context.Background()

	title, err := svc.AddTitle(ctx, "fake", 77, true, MonitorAll, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	var kind string
	if err := st.DB.QueryRowContext(ctx,
		`SELECT kind FROM wanted_items WHERE series_id = ?`, title.ID).Scan(&kind); err != nil {
		t.Fatalf("read kind: %v", err)
	}
	if kind != string(domain.KindEpisode) {
		t.Errorf("kind = %q, want episode", kind)
	}
	if title.Items[0].Kind != domain.KindEpisode {
		t.Errorf("returned kind = %q, want episode", title.Items[0].Kind)
	}
}
