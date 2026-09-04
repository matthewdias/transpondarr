package devdata

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/decide"
	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/metadata"
	"github.com/matthewdias/transpondarr/internal/core/metadata/dbcache"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// Options configures a seed run. Now exists for reproducibility: a bug found
// against a seeded library has to be reachable again from the same clock.
type Options struct {
	Now time.Time
}

const providerName = "anilist"

// Seed writes the development world into st. Entity creation goes through the
// store layer; only backdated timestamps, which no query sets, are direct SQL.
func Seed(ctx context.Context, st *store.Store, opts Options) error {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	profileIDs, err := seedProfiles(ctx, st)
	if err != nil {
		return err
	}
	return seedTitles(ctx, st, dbcache.New(st.Q), fixtures(), profileIDs, now)
}

// seedTitles takes its titles as an argument so a test can seed a fixture the
// set does not contain.
func seedTitles(ctx context.Context, st *store.Store, cache *dbcache.Cache, titles []title, profileIDs map[string]int64, now time.Time) error {
	for _, t := range titles {
		if err := seedTitle(ctx, st, cache, t, profileIDs, now); err != nil {
			return fmt.Errorf("seed %s: %w", t.name, err)
		}
	}
	return nil
}

func seedProfiles(ctx context.Context, st *store.Store) (map[string]int64, error) {
	ids := map[string]int64{}
	for _, p := range profiles() {
		row, err := st.Q.CreateQualityProfile(ctx, db.CreateQualityProfileParams{
			Name:                 p.name,
			ResolutionOrder:      p.resolutionOrder,
			PreferredSource:      p.preferredSource,
			SubPref:              p.subPref,
			PreferDualAudio:      boolInt(p.preferDualAudio),
			CodecPref:            p.codecPref,
			HardExcludes:         defaultString(p.hardExcludes, "[]"),
			MinScore:             p.minScore,
			UpgradesEnabled:      boolInt(p.upgrades),
			CutoffScore:          p.cutoffScore,
			UpgradeV2AboveCutoff: 1,
		})
		if err != nil {
			return nil, fmt.Errorf("create profile %s: %w", p.name, err)
		}
		ids[p.name] = row.ID
		for _, g := range p.groups {
			if _, err := st.Q.AddProfileGroup(ctx, db.AddProfileGroupParams{
				ProfileID: row.ID,
				Rank:      g.rank,
				GroupName: g.name,
				Blocked:   boolInt(g.blocked),
			}); err != nil {
				return nil, fmt.Errorf("add group %s: %w", g.name, err)
			}
		}
	}
	return ids, nil
}

func seedTitle(ctx context.Context, st *store.Store, cache *dbcache.Cache, t title, profileIDs map[string]int64, now time.Time) error {
	row, err := st.Q.CreateTitle(ctx, db.CreateTitleParams{
		Provider:   sql.NullString{String: providerName, Valid: true},
		ProviderID: sql.NullInt64{Int64: t.providerID, Valid: true},
		Title:      t.name,
		Format:     string(t.format),
		Monitored:  boolInt(t.monitored),
		Year:       int64(t.year),
	})
	if err != nil {
		return fmt.Errorf("create title: %w", err)
	}
	if t.profile != "" {
		// Skipping an unknown name would leave the title on the built-in Default
		// profile, and no screen would show that the fixture's axes went unapplied.
		id, ok := profileIDs[t.profile]
		if !ok {
			return fmt.Errorf("unknown quality profile %q", t.profile)
		}
		if _, err := st.Q.SetTitleProfile(ctx, db.SetTitleProfileParams{QualityProfileID: id, ID: row.ID, ID_2: id}); err != nil {
			return fmt.Errorf("set profile: %w", err)
		}
	}
	if t.pinnedGroup != "" {
		if _, err := st.Q.SetTitlePinnedGroup(ctx, db.SetTitlePinnedGroupParams{
			PinnedGroup: sql.NullString{String: t.pinnedGroup, Valid: true},
			ID:          row.ID,
		}); err != nil {
			return fmt.Errorf("set pinned group: %w", err)
		}
	}
	if t.monitorNewFrom > 0 {
		if err := st.Q.SetTitleMonitorNewFrom(ctx, db.SetTitleMonitorNewFromParams{
			MonitorNewFrom: sql.NullInt64{Int64: int64(t.monitorNewFrom), Valid: true},
			ID:             row.ID,
		}); err != nil {
			return fmt.Errorf("set monitor cut: %w", err)
		}
	}
	if t.scheduleChecked {
		// The parameter is the stamp's expected current value, not the one being
		// written: the query is a compare-and-set that always writes datetime('now').
		if err := st.Q.SetTitleAiringSyncedAt(ctx, db.SetTitleAiringSyncedAtParams{
			ID:             row.ID,
			AiringSyncedAt: sql.NullString{},
		}); err != nil {
			return fmt.Errorf("set airing stamp: %w", err)
		}
	}
	if err := seedItems(ctx, st, t, row.ID, now); err != nil {
		return err
	}
	if err := seedBlocklist(ctx, st, t, row.ID, now); err != nil {
		return err
	}
	return cache.Put(ctx, providerName, t.providerID, metadata.CachedTitle{
		Title: metadata.TitleMeta{
			ProviderID: t.providerID,
			Titles:     metadata.Titles{Romaji: t.name, English: firstAlt(t)},
			Format:     t.format,
			Episodes:   t.episodes,
			Status:     t.status,
			CoverURL:   t.cover,
			Year:       t.year,
		},
	})
}

func seedItems(ctx context.Context, st *store.Store, t title, titleID int64, now time.Time) error {
	kind := string(domain.KindFor(t.format))
	for _, it := range t.items {
		row, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
			SeriesID:  titleID,
			Kind:      kind,
			Number:    sql.NullInt64{Int64: int64(it.number), Valid: true},
			InLibrary: boolInt(it.inLibrary),
			Monitored: boolInt(!it.unmonitored),
		})
		if err != nil {
			return fmt.Errorf("create item %d: %w", it.number, err)
		}
		if it.dated {
			if err := st.Q.UpsertWantedItemAiring(ctx, db.UpsertWantedItemAiringParams{
				SeriesID:  titleID,
				Kind:      kind,
				Number:    sql.NullInt64{Int64: int64(it.number), Valid: true},
				AirsAt:    sql.NullString{String: store.FormatTimestamp(now.Add(it.airsIn)), Valid: true},
				Monitored: boolInt(!it.unmonitored),
			}); err != nil {
				return fmt.Errorf("set air date %d: %w", it.number, err)
			}
		}
		if it.held != "" {
			if err := st.Q.SetWantedItemHeld(ctx, db.SetWantedItemHeldParams{
				InLibrary: boolInt(it.inLibrary), HeldReleaseTitle: it.held, ID: row.ID,
			}); err != nil {
				return fmt.Errorf("set held %d: %w", it.number, err)
			}
		}
		if it.grab != nil {
			if err := seedGrab(ctx, st, *it.grab, titleID, row.ID, int64(it.number), kind, now); err != nil {
				return fmt.Errorf("seed grab %d: %w", it.number, err)
			}
		}
		if it.pass != nil {
			if err := seedPassOutcome(ctx, st, *it.pass, row.ID, now); err != nil {
				return fmt.Errorf("seed pass outcome %d: %w", it.number, err)
			}
		}
	}
	return nil
}

// seedPassOutcome records what the last pass decided, which is the one reason
// the Missing screen reads from storage rather than deriving per request (#181).
func seedPassOutcome(ctx context.Context, st *store.Store, p passOutcome, itemID int64, now time.Time) error {
	held := sql.NullString{}
	if p.heldFor > 0 {
		held = sql.NullString{String: store.FormatTimestamp(now.Add(p.heldFor)), Valid: true}
	}
	if err := st.Q.UpsertPassOutcome(ctx, db.UpsertPassOutcomeParams{
		WantedItemID: itemID,
		Outcome:      p.outcome,
		Source:       p.source,
		ReleaseTitle: p.release,
		Detail:       p.detail,
		HeldUntil:    held,
		RecordedAt:   store.FormatTimestamp(now.Add(-p.agedBy)),
	}); err != nil {
		return fmt.Errorf("upsert pass outcome: %w", err)
	}
	return nil
}

func seedGrab(ctx context.Context, st *store.Store, g grab, titleID, itemID, number int64, kind string, now time.Time) error {
	row, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: itemID, InfoHash: g.hash, ReleaseTitle: g.release, Status: g.status,
	})
	if err != nil {
		return fmt.Errorf("upsert grab: %w", err)
	}
	if err := st.Q.SetGrabStatus(ctx, db.SetGrabStatusParams{Status: g.status, ID: row.ID}); err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	if g.missingFor > 0 {
		if err := st.Q.SetGrabMissingSince(ctx, db.SetGrabMissingSinceParams{
			MissingSince: sql.NullString{String: store.FormatTimestamp(now.Add(-g.missingFor)), Valid: true}, ID: row.ID,
		}); err != nil {
			return fmt.Errorf("set missing since: %w", err)
		}
	}
	if g.stalledFor > 0 {
		if err := st.Q.SetGrabStalledSince(ctx, db.SetGrabStalledSinceParams{
			StalledSince: sql.NullString{String: store.FormatTimestamp(now.Add(-g.stalledFor)), Valid: true}, ID: row.ID,
		}); err != nil {
			return fmt.Errorf("set stalled since: %w", err)
		}
	}
	if g.lastError != "" {
		if err := st.Q.SetGrabLastError(ctx, db.SetGrabLastErrorParams{
			LastError: sql.NullString{String: g.lastError, Valid: true}, ID: row.ID,
		}); err != nil {
			return fmt.Errorf("set last error: %w", err)
		}
	}
	if err := backdateGrab(ctx, st, row.ID, now.Add(-g.agedBy)); err != nil {
		return err
	}
	for _, e := range g.events {
		if err := st.Q.AppendGrabEvent(ctx, db.AppendGrabEventParams{
			SeriesID: titleID, WantedItemID: itemID, ItemNumber: number, ItemKind: kind,
			InfoHash: g.hash, ReleaseTitle: g.release, Event: e.kind, Detail: e.detail,
		}); err != nil {
			return fmt.Errorf("append event %s: %w", e.kind, err)
		}
		if err := backdateEvent(ctx, st, itemID, e.kind, now.Add(-e.agedBy)); err != nil {
			return err
		}
	}
	return nil
}

func seedBlocklist(ctx context.Context, st *store.Store, t title, titleID int64, now time.Time) error {
	for _, b := range t.blocklist {
		var row db.ReleaseBlocklist
		// The upsert is what increments failures, so a rung is reached by
		// repeating the entry rather than by writing the count.
		for i := 0; i < max(b.failures, 1); i++ {
			var err error
			row, err = st.Q.UpsertBlocklistEntry(ctx, db.UpsertBlocklistEntryParams{
				SeriesID:        titleID,
				InfoHash:        b.hash,
				ReleaseTitle:    b.release,
				NormalizedTitle: decide.NormalizeReleaseTitle(b.release),
				Reason:          b.reason,
			})
			if err != nil {
				return fmt.Errorf("blocklist %s: %w", b.release, err)
			}
		}
		expiry := sql.NullString{}
		if !b.permanent {
			expiry = sql.NullString{String: store.FormatTimestamp(now.Add(b.expiresIn)), Valid: true}
		}
		if err := st.Q.SetBlocklistExpiry(ctx, db.SetBlocklistExpiryParams{BlockedUntil: expiry, ID: row.ID}); err != nil {
			return fmt.Errorf("blocklist expiry %s: %w", b.release, err)
		}
	}
	return nil
}

// backdateGrab writes a past created_at; a sqlc query for it would put dev-only
// SQL in the shipped store layer permanently.
func backdateGrab(ctx context.Context, st *store.Store, id int64, at time.Time) error {
	if _, err := st.DB.ExecContext(ctx, `UPDATE grabs SET created_at = ? WHERE id = ?`, store.FormatTimestamp(at), id); err != nil {
		return fmt.Errorf("backdate grab: %w", err)
	}
	return nil
}

// backdateEvent keys on the newest matching row because AppendGrabEvent returns
// no id, and last_insert_rowid() is per-connection while database/sql pools them.
func backdateEvent(ctx context.Context, st *store.Store, itemID int64, kind string, at time.Time) error {
	const stmt = `UPDATE grab_events SET created_at = ?
	              WHERE id = (SELECT id FROM grab_events WHERE wanted_item_id = ? AND event = ? ORDER BY id DESC LIMIT 1)`
	if _, err := st.DB.ExecContext(ctx, stmt, store.FormatTimestamp(at), itemID, kind); err != nil {
		return fmt.Errorf("backdate grab event: %w", err)
	}
	return nil
}

// firstAlt is the title's second accepted name, which the metadata snapshot
// stores as the English one so catalog.TitleVariants has more than one to return.
func firstAlt(t title) string {
	if len(t.altNames) == 0 {
		return ""
	}
	return t.altNames[0]
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func defaultString(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
