package metadata

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeProvider struct {
	meta        TitleMeta
	items       []ItemMeta
	getErr      error
	getCalls    int
	searchCalls int
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Search(context.Context, string) ([]Candidate, error) {
	f.searchCalls++
	return []Candidate{{ProviderID: 1}}, nil
}

func (f *fakeProvider) GetTitle(context.Context, int64) (TitleMeta, []ItemMeta, error) {
	f.getCalls++
	if f.getErr != nil {
		return TitleMeta{}, nil, f.getErr
	}
	return f.meta, f.items, nil
}

type fakeCache struct {
	snap      CachedTitle
	fetchedAt time.Time
	ok        bool
	getErr    error
	putErr    error
	puts      int
	lastPut   CachedTitle
}

func (c *fakeCache) Get(context.Context, string, int64) (CachedTitle, time.Time, bool, error) {
	return c.snap, c.fetchedAt, c.ok, c.getErr
}

func (c *fakeCache) Put(_ context.Context, _ string, _ int64, snap CachedTitle) error {
	c.puts++
	c.lastPut = snap
	return c.putErr
}

func TestCachedGetTitleHitSkipsProvider(t *testing.T) {
	prov := &fakeProvider{meta: TitleMeta{ProviderID: 99}} // must NOT be returned
	cache := &fakeCache{
		snap:      CachedTitle{Title: TitleMeta{ProviderID: 5, Status: "FINISHED"}, Items: []ItemMeta{{Number: 1}}},
		fetchedAt: time.Now().Add(-time.Hour), // well within FINISHED's 30d TTL
		ok:        true,
	}
	c := Cached(prov, cache)

	meta, items, err := c.GetTitle(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if prov.getCalls != 0 {
		t.Errorf("provider called %d times on a fresh hit, want 0", prov.getCalls)
	}
	if meta.ProviderID != 5 || len(items) != 1 {
		t.Errorf("returned %+v / %d items, want the cached snapshot", meta, len(items))
	}
}

func TestCachedGetTitleMissFetchesAndStores(t *testing.T) {
	prov := &fakeProvider{
		meta:  TitleMeta{ProviderID: 5, Titles: Titles{Romaji: "X"}, Status: "FINISHED"},
		items: []ItemMeta{{Number: 1}, {Number: 2}},
	}
	cache := &fakeCache{ok: false}
	c := Cached(prov, cache)

	meta, items, err := c.GetTitle(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if prov.getCalls != 1 {
		t.Errorf("provider called %d times on a miss, want 1", prov.getCalls)
	}
	if cache.puts != 1 {
		t.Errorf("cache Put called %d times, want 1", cache.puts)
	}
	if cache.lastPut.Title.ProviderID != 5 || len(cache.lastPut.Items) != 2 {
		t.Errorf("stored snapshot = %+v, want the fetched title with 2 items", cache.lastPut)
	}
	if meta.ProviderID != 5 || len(items) != 2 {
		t.Errorf("returned %+v / %d items, want the fetched result", meta, len(items))
	}
}

func TestCachedGetTitleStaleRefetches(t *testing.T) {
	prov := &fakeProvider{meta: TitleMeta{ProviderID: 5, Status: "RELEASING"}}
	cache := &fakeCache{
		snap:      CachedTitle{Title: TitleMeta{ProviderID: 5, Status: "RELEASING"}},
		fetchedAt: time.Now().Add(-24 * time.Hour), // past RELEASING's 6h TTL
		ok:        true,
	}
	c := Cached(prov, cache)

	if _, _, err := c.GetTitle(context.Background(), 5); err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if prov.getCalls != 1 {
		t.Errorf("provider called %d times on a stale hit, want 1 (refetch)", prov.getCalls)
	}
}

// A cache read error must not fail the call — it degrades to a live fetch.
func TestCachedGetTitleCacheGetErrorFallsBack(t *testing.T) {
	prov := &fakeProvider{meta: TitleMeta{ProviderID: 5}}
	cache := &fakeCache{getErr: errors.New("db down")}
	c := Cached(prov, cache)

	if _, _, err := c.GetTitle(context.Background(), 5); err != nil {
		t.Fatalf("GetTitle: %v", err)
	}
	if prov.getCalls != 1 {
		t.Errorf("provider called %d times when cache Get errored, want 1", prov.getCalls)
	}
}

// A cache write failure is best-effort and must not fail the fetch.
func TestCachedGetTitlePutErrorIsSwallowed(t *testing.T) {
	prov := &fakeProvider{meta: TitleMeta{ProviderID: 5}}
	cache := &fakeCache{ok: false, putErr: errors.New("disk full")}
	c := Cached(prov, cache)

	meta, _, err := c.GetTitle(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetTitle should swallow a Put error, got: %v", err)
	}
	if meta.ProviderID != 5 {
		t.Errorf("returned %+v, want the freshly fetched title", meta)
	}
}

// A provider failure on a miss propagates and nothing is cached.
func TestCachedGetTitleProviderErrorPropagates(t *testing.T) {
	prov := &fakeProvider{getErr: errors.New("boom")}
	cache := &fakeCache{ok: false}
	c := Cached(prov, cache)

	if _, _, err := c.GetTitle(context.Background(), 5); err == nil {
		t.Fatal("expected the provider error to propagate")
	}
	if cache.puts != 0 {
		t.Errorf("cache Put called %d times after a provider error, want 0", cache.puts)
	}
}

// When the provider fails (e.g. AniList 429) but a stale snapshot exists, serve
// the stale snapshot rather than propagating the error.
func TestCachedGetTitleProviderErrorServesStale(t *testing.T) {
	prov := &fakeProvider{getErr: errors.New("rate limited")}
	cache := &fakeCache{
		snap:      CachedTitle{Title: TitleMeta{ProviderID: 5, Status: "RELEASING"}, Items: []ItemMeta{{Number: 1}}},
		fetchedAt: time.Now().Add(-24 * time.Hour), // stale (past RELEASING's 6h TTL)
		ok:        true,
	}
	c := Cached(prov, cache)

	meta, items, err := c.GetTitle(context.Background(), 5)
	if err != nil {
		t.Fatalf("expected stale snapshot to be served, got error: %v", err)
	}
	if prov.getCalls != 1 {
		t.Errorf("provider should be tried once, called %d times", prov.getCalls)
	}
	if meta.ProviderID != 5 || len(items) != 1 {
		t.Errorf("returned %+v / %d items, want the stale cached snapshot", meta, len(items))
	}
	if cache.puts != 0 {
		t.Errorf("nothing should be written after a provider error, got %d puts", cache.puts)
	}
}

// Search bypasses the cache entirely.
func TestCachedSearchBypassesCache(t *testing.T) {
	prov := &fakeProvider{}
	cache := &fakeCache{}
	c := Cached(prov, cache)

	if _, err := c.Search(context.Background(), "term"); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if prov.searchCalls != 1 {
		t.Errorf("provider Search called %d times, want 1", prov.searchCalls)
	}
	if cache.puts != 0 {
		t.Errorf("Search must not write the cache; got %d puts", cache.puts)
	}
}

func TestTTLFor(t *testing.T) {
	long := 30 * 24 * time.Hour
	short := 6 * time.Hour
	cases := map[string]time.Duration{
		"FINISHED":         long,
		"CANCELLED":        long,
		"RELEASING":        short,
		"NOT_YET_RELEASED": short,
		"HIATUS":           short,
		"":                 short,
		"anything else":    short,
	}
	for status, want := range cases {
		if got := TTLFor(status); got != want {
			t.Errorf("TTLFor(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestFresh(t *testing.T) {
	if !fresh("FINISHED", 12, time.Now().Add(-24*time.Hour)) {
		t.Error("a FINISHED title fetched 1 day ago should be fresh")
	}
	if fresh("RELEASING", 12, time.Now().Add(-7*time.Hour)) {
		t.Error("a RELEASING title fetched 7 hours ago should be stale (6h TTL)")
	}
	// An empty FINISHED snapshot uses the short TTL, not 30 days.
	if fresh("FINISHED", 0, time.Now().Add(-7*time.Hour)) {
		t.Error("an empty FINISHED snapshot fetched 7 hours ago should be stale")
	}
}

// fakeAiringProvider is a provider that also publishes a schedule.
type fakeAiringProvider struct {
	fakeProvider
	airings       []Airing
	lastNotYetAir bool
	scheduleCalls int
}

func (f *fakeAiringProvider) GetSchedule(_ context.Context, _ int64, notYetAired bool) ([]Airing, error) {
	f.scheduleCalls++
	f.lastNotYetAir = notYetAired
	return f.airings, nil
}

func TestCachedForwardsAiringCapability(t *testing.T) {
	prov := &fakeAiringProvider{airings: []Airing{{Number: 1, AirsAt: time.Unix(1700000000, 0).UTC()}}}

	airing, ok := Cached(prov, &fakeCache{}).(AiringProvider)
	if !ok {
		t.Fatal("Cached dropped the AiringProvider capability of its inner provider")
	}
	got, err := airing.GetSchedule(context.Background(), 5, true)
	if err != nil {
		t.Fatalf("GetSchedule: %v", err)
	}
	if len(got) != 1 || got[0].Number != 1 {
		t.Errorf("got %+v, want the inner provider's schedule", got)
	}
	if !prov.lastNotYetAir {
		t.Error("notYetAired was not passed through to the inner provider")
	}
}

// The capability must not be invented for a provider that has no schedule to give.
func TestCachedDoesNotInventAiringCapability(t *testing.T) {
	if _, ok := Cached(&fakeProvider{}, &fakeCache{}).(AiringProvider); ok {
		t.Error("Cached claims AiringProvider for an inner provider that lacks it")
	}
}
