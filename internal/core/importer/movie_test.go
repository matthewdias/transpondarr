package importer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/domain"
	"github.com/matthewdias/transpondarr/internal/core/download"
	"github.com/matthewdias/transpondarr/internal/core/library/mediaserver"
	"github.com/matthewdias/transpondarr/internal/core/notify"
	"github.com/matthewdias/transpondarr/internal/coretest"
	"github.com/matthewdias/transpondarr/internal/store"
	"github.com/matthewdias/transpondarr/internal/store/db"
)

// seedMovieGrab creates a movie title with its single grabbed item.
func seedMovieGrab(t *testing.T, st *store.Store, title, hash string, year int64) int64 {
	t.Helper()
	return seedOneItemGrab(t, st, title, hash, domain.FormatMovie, year)
}

// seedOneItemGrab creates a one-item title of any format with its item grabbed.
// The kind is derived, never chosen, so a test cannot seed a shape the catalog
// could not produce.
func seedOneItemGrab(t *testing.T, st *store.Store, title, hash string, format domain.Format, year int64) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := st.Q.CreateTitle(ctx, db.CreateTitleParams{
		Title: title, Format: string(format), Year: year, Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create title: %v", err)
	}
	item, err := st.Q.CreateWantedItem(ctx, db.CreateWantedItemParams{
		SeriesID: s.ID, Kind: string(domain.KindFor(format)),
		Number: sql.NullInt64{Int64: 1, Valid: true}, Monitored: 1,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if _, err := st.Q.UpsertGrab(ctx, db.UpsertGrabParams{
		WantedItemID: item.ID, InfoHash: hash, ReleaseTitle: title + " release", Status: statusGrabbed,
	}); err != nil {
		t.Fatalf("upsert grab: %v", err)
	}
	return s.ID
}

// movieLibrary is a real media-server target over both roots, so these tests see
// the layout a movie lands in rather than a fake's canned destination.
func movieLibrary(t *testing.T) (target *mediaserver.Target, series, movies string) {
	t.Helper()
	series, movies = t.TempDir(), t.TempDir()
	return mediaserver.New(mediaserver.Roots{Series: series, Movies: movies}, mediaserver.LayoutSeasonFolders, "copy", nil), series, movies
}

// completedPayload is a finished download whose content is the given path — a
// directory for the walk, or a bare file for the plain-file branch.
func completedPayload(hash, path string) *coretest.FakeDownload {
	return &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: hash, State: download.StateComplete, ContentPath: path},
	}}
}

// heldByTitle reports whether the library flag was set on a title's only item.
func heldByTitle(t *testing.T, st *store.Store, titleID int64) bool {
	t.Helper()
	items, err := st.Q.ListWantedItems(context.Background(), titleID)
	if err != nil {
		t.Fatalf("list wanted items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("title holds %d items, want the single one a movie has", len(items))
	}
	return items[0].InLibrary == 1
}

// grow pads a payload file, so a test states which video is the feature rather
// than leaving every candidate the same size and the choice to a tie. The
// content is stamped with the file's own name, so two equally sized videos are
// still tellable apart once one of them is in the library.
func grow(t *testing.T, dir, rel string, size int) {
	t.Helper()
	body := make([]byte, size)
	copy(body, rel)
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), body, 0o644); err != nil {
		t.Fatalf("grow %q: %v", rel, err)
	}
}

// wantPlacedFrom asserts which payload file the library ended up holding.
func wantPlacedFrom(t *testing.T, dest, rel string) {
	t.Helper()
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read placed file: %v", err)
	}
	if !bytes.HasPrefix(body, []byte(rel)) {
		t.Errorf("the library holds %q, want the bytes of %q", firstLine(body), rel)
	}
}

// firstLine is the name grow stamped, for a legible failure message.
func firstLine(body []byte) string {
	if i := bytes.IndexByte(body, 0); i >= 0 {
		return string(body[:i])
	}
	return string(body)
}

// deferralDetail is the reason the latest deferral settled by. A settled row's
// last_error is cleared, so history is where the Activity queue reads it from.
func deferralDetail(t *testing.T, st *store.Store, titleID int64) string {
	t.Helper()
	events, err := st.Q.ListTitleGrabEvents(context.Background(), titleID)
	if err != nil {
		t.Fatalf("list grab events: %v", err)
	}
	for _, e := range events {
		if e.Event == statusDeferred {
			return e.Detail
		}
	}
	t.Fatal("no deferral was recorded in history")
	return ""
}

// filesUnder lists every regular file below root, payload-relative and sorted,
// so a layout assertion can say what landed as well as what did not.
func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %q: %v", root, err)
	}
	sort.Strings(out)
	return out
}

func wantFiles(t *testing.T, root string, want ...string) {
	t.Helper()
	got := filesUnder(t, root)
	if len(got) != len(want) {
		t.Fatalf("%q holds %v, want %v", root, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%q holds %v, want %v", root, got, want)
		}
	}
}

// The target routes on format and names on year, so both have to reach Place —
// neither is derivable from the wanted item.
func TestImportPassesFormatAndYearToTheLibrary(t *testing.T) {
	st := coretest.NewStore(t)
	seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)

	dl := completedSource(t, "abc")
	target := &coretest.FakeLibrary{}
	im := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), noRecorder{}, nil)

	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(target.Placed) != 1 {
		t.Fatalf("Place called %d times, want 1", len(target.Placed))
	}
	got := target.Placed[0].Title
	if got.Format != domain.FormatMovie || got.Year != 2019 {
		t.Errorf("placed title = %+v, want format MOVIE and year 2019", got)
	}
}

// The missing-root decision, end to end: a movie grabbed with no movies root
// configured holds with a legible error instead of landing in the title root,
// and imports on the next scan once the root is set.
func TestMovieWithoutAMoviesRootHoldsAndThenSelfHeals(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	ctx := context.Background()

	series, movies := t.TempDir(), t.TempDir()
	dl := completedSource(t, "abc")
	unconfigured := fakeSource{dl: dl, lib: mediaserver.New(mediaserver.Roots{Series: series}, mediaserver.LayoutSeasonFolders, "copy", nil)}

	if err := New(st, unconfigured, discardLogger(), noRecorder{}, nil).ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != statusGrabbed {
		t.Errorf("status = %q, want it still grabbed: a missing root is a config error, not a settled import", g.Status)
	}
	if !strings.Contains(g.LastError.String, "movies library directory") {
		t.Errorf("last_error = %q, want it to name the unconfigured movies directory", g.LastError.String)
	}
	if items, _ := st.Q.ListWantedItems(ctx, titleID); items[0].InLibrary != 0 {
		t.Error("the item must not read as held when nothing was placed")
	}
	if entries, _ := os.ReadDir(series); len(entries) != 0 {
		t.Errorf("the series root holds %d entries; a movie must never fall back into it", len(entries))
	}

	// The cause is a path-mapping gap, not a bad release: nothing may be
	// remembered against the release or spent from the failure ladder.
	if blocked, _ := st.Q.ListBlocklistByTitle(ctx, titleID); len(blocked) != 0 {
		t.Errorf("blocklisted %d release(s); an unconfigured root says nothing about the release", len(blocked))
	}
	if events, _ := st.Q.ListTitleGrabEvents(ctx, titleID); len(events) != 0 {
		t.Errorf("wrote %d history event(s); the grab has not settled, so it has no step to record", len(events))
	}

	// A second pass with the root still missing must cost nothing: no state
	// churn, and no second stuck notification for one incident.
	if err := New(st, unconfigured, discardLogger(), noRecorder{}, nil).ScanOnce(ctx); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if again := grabByHash(t, st, "abc"); again.Status != statusGrabbed || again.LastError != g.LastError {
		t.Errorf("second pass changed the grab (%q/%q); a held movie must not churn", again.Status, again.LastError.String)
	}
	if blocked, _ := st.Q.ListBlocklistByTitle(ctx, titleID); len(blocked) != 0 {
		t.Errorf("second pass blocklisted %d release(s)", len(blocked))
	}

	configured := fakeSource{dl: dl, lib: mediaserver.New(mediaserver.Roots{Series: series, Movies: movies}, mediaserver.LayoutSeasonFolders, "copy", nil)}
	if err := New(st, configured, discardLogger(), noRecorder{}, nil).ScanOnce(ctx); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if g := grabByHash(t, st, "abc"); g.Status != statusImported {
		t.Errorf("status = %q, want imported once the root is configured", g.Status)
	}
	want := filepath.Join(movies, "Placeholder Film (2019)", "Placeholder Film (2019).mkv")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("stat %q: %v", want, err)
	}
}

// --- the payload shapes a movie arrives in ----------------------------------

// The lone-file rule is what carries a movie: nothing in its filename names an
// item, so the only thing that can identify it is that we chose this release for
// this one item. It lands in the movie layout, with no season folder anywhere.
func TestMovieSingleVideoPlacesByTheLoneFileRule(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	target, series, movies := movieLibrary(t)

	dir := writeTree(t,
		"Placeholder.Film.2019.1080p.BluRay.x264-SynthGroup.mkv",
		"Placeholder.Film.2019.1080p.BluRay.x264-SynthGroup.nfo",
		"Subs/eng.ass",
	)
	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: target}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	wantFiles(t, movies, "Placeholder Film (2019)/Placeholder Film (2019).mkv")
	wantFiles(t, series)
	if g := grabByHash(t, st, "abc"); g.Status != statusImported {
		t.Errorf("status = %q, want imported", g.Status)
	}
	if !heldByTitle(t, st, titleID) {
		t.Error("the item must read as held once its file is in the library")
	}
}

// The extras filter's sole-video yield, from the other side: a real movie
// payload ships a sample and a trailer beside the feature, and picking either
// would file a two-minute clip as the film.
func TestMoviePayloadPicksTheFeatureOverSamplesAndExtras(t *testing.T) {
	st := coretest.NewStore(t)
	seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	target := &coretest.FakeLibrary{}

	const feature = "Placeholder.Film.2019.1080p.BluRay.x264-SynthGroup.mkv"
	dir := writeTree(t,
		feature,
		"Placeholder.Film.2019.1080p.BluRay.x264-SynthGroup.sample.mkv",
		"Sample/Placeholder.Film.2019.sample.mkv",
		"Extras/Placeholder.Film.2019.Bonus.Interview.mkv",
		"Placeholder.Film.2019.Trailer.mkv",
	)
	grow(t, dir, feature, 4096)
	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: target}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 1 {
		t.Fatalf("Place called %d times with %+v, want only the feature", len(target.Placed), target.Placed)
	}
	if got := filepath.Base(target.Placed[0].SourcePath); got != feature {
		t.Errorf("placed %q, want the feature %q", got, feature)
	}
}

// A sample is a truncated copy of the film, never the film, so it is held out of
// the sole-video relaxation before the video is counted at all. The payload
// yields nothing and the grab settles as a deferral a human can look at.
func TestMovieSampleIsNeverTheFeature(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	target := &coretest.FakeLibrary{}
	rec := &fakeRecorder{}

	dir := writeTree(t,
		"Placeholder.Film.2019.1080p.sample.mkv",
		"Placeholder.Film.2019.1080p.nfo",
	)
	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: target}, discardLogger(), rec, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 0 {
		t.Fatalf("placed %+v; a sample must never be filed as the film", target.Placed)
	}
	g := grabByHash(t, st, "abc")
	if g.Status != statusDeferred {
		t.Errorf("status = %q, want it deferred for a human", g.Status)
	}
	if heldByTitle(t, st, titleID) {
		t.Error("nothing was placed, so the item must not read as held")
	}
	if len(rec.calls) != 0 {
		t.Errorf("recorded %d blocklist entries; a deferral says nothing about the release", len(rec.calls))
	}
}

// The yield itself: one video and nothing to confuse it with means an extras
// token in its name is a word in the title, which is how a "Bonus Edition"
// release imports instead of parking with the file sitting right there.
func TestMovieSoleVideoWithAnExtrasTokenIsStillTheFeature(t *testing.T) {
	st := coretest.NewStore(t)
	seedMovieGrab(t, st, "Placeholder Film Bonus Edition", "abc", 2019)
	target := &coretest.FakeLibrary{}

	dir := writeTree(t, "Placeholder.Film.Bonus.Edition.2019.1080p-SynthGroup.mkv")
	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: target}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if len(target.Placed) != 1 {
		t.Fatalf("Place called %d times, want the sole video taken despite its token", len(target.Placed))
	}
}

// Nothing unpacks an archive, so a movie shipped as one defers naming what to
// extract — the same settled deferral an episode gets, and never a failure,
// because the film is sitting inside it.
func TestMovieArchivePayloadDefersWithTheExtractionAdvice(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	target := &coretest.FakeLibrary{}
	rec := &fakeRecorder{}

	const archive = "Placeholder.Film.2019.1080p.BluRay.x264-SynthGroup.rar"
	dir := writeTree(t, archive)
	src := fakeSource{dl: completedPayload("abc", filepath.Join(dir, archive)), lib: target}

	im := New(st, src, discardLogger(), rec, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != statusDeferred {
		t.Fatalf("status = %q, want import_deferred", g.Status)
	}
	detail := deferralDetail(t, st, titleID)
	if !strings.Contains(detail, archive) || !strings.Contains(detail, "extract it into the download folder") {
		t.Errorf("deferral detail = %q, want it to name the archive and what to do with it", detail)
	}
	if len(target.Placed) != 0 {
		t.Errorf("placed %+v; an archive is unassignable by construction", target.Placed)
	}
	if heldByTitle(t, st, titleID) {
		t.Error("nothing was placed, so the item must not read as held")
	}
	if len(rec.calls) != 0 {
		t.Errorf("recorded %d blocklist entries; the release is fine, it is just packed", len(rec.calls))
	}
}

// An archive keeps the item deferred on every path, a retry clicked before
// extracting included — and once it is extracted in place, the same retry
// imports with no new code.
func TestMovieArchiveRetryStaysDeferredUntilItIsExtracted(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	target, _, movies := movieLibrary(t)

	dir := writeTree(t, "Placeholder.Film.2019.1080p-SynthGroup.rar")
	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: target}, discardLogger(), noRecorder{}, nil)
	ctx := context.Background()
	if err := im.ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	grabID := grabByHash(t, st, "abc").ID

	results, err := im.RetryImport(ctx, grabID, nil)
	if err != nil {
		t.Fatalf("retry before extracting: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != statusDeferred {
		t.Fatalf("retry results = %+v, want it still deferred", results)
	}
	if !strings.Contains(results[0].Detail, "extract it into the download folder") {
		t.Errorf("retry detail = %q, want it to say what is still to be extracted", results[0].Detail)
	}
	if info, err := im.ListPayload(ctx, grabID); err != nil {
		t.Fatalf("ListPayload: %v", err)
	} else if len(info.Files) != 0 || len(info.Archives) != 1 {
		t.Errorf("payload = %+v, want the archive listed beside no files", info)
	}
	wantFiles(t, movies)

	// The human extracts it into the download folder. The scan still leaves it
	// alone — settled bytes are never re-walked — so the retry is the only way back.
	writeTreeInto(t, dir, "Placeholder.Film.2019.1080p-SynthGroup.mkv")
	if err := im.ScanOnce(ctx); err != nil {
		t.Fatalf("scan after extracting: %v", err)
	}
	if g := grabByHash(t, st, "abc"); g.Status != statusDeferred {
		t.Errorf("status = %q, want the scan to leave a settled deferral alone", g.Status)
	}
	wantFiles(t, movies)

	results, err = im.RetryImport(ctx, grabID, nil)
	if err != nil {
		t.Fatalf("retry after extracting: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != statusImported {
		t.Fatalf("retry results = %+v, want it imported", results)
	}
	wantFiles(t, movies, "Placeholder Film (2019)/Placeholder Film (2019).mkv")
	if !heldByTitle(t, st, titleID) {
		t.Error("the item must read as held once the extracted film is in the library")
	}
}

// The bug this rule exists to stop: a numbered non-feature ("Deleted Scene 1")
// claimed the film's only item, hardlinked a clip as the movie and dropped the
// feature as a leftover — silently, with the grab settled and the item held. A
// film is the biggest thing in its payload, so size decides and numbering does
// not get a say.
func TestMovieTakesTheLargestVideoAsTheFeature(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	target, _, movies := movieLibrary(t)

	const feature = "Placeholder.Film.2019.1080p.BluRay.x264-SynthGroup.mkv"
	dir := writeTree(t, feature, "Placeholder Film - Deleted Scene 1.mkv", "Placeholder Film - Interview 2.mkv")
	grow(t, dir, feature, 4096)

	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: target}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	wantFiles(t, movies, "Placeholder Film (2019)/Placeholder Film (2019).mkv")
	placed := filepath.Join(movies, "Placeholder Film (2019)", "Placeholder Film (2019).mkv")
	info, err := os.Stat(placed)
	if err != nil {
		t.Fatalf("stat placed film: %v", err)
	}
	if info.Size() != 4096 {
		t.Errorf("placed a %d-byte file as the film; the feature is the 4096-byte one", info.Size())
	}
	wantPlacedFrom(t, placed, feature)
	if g := grabByHash(t, st, "abc"); g.Status != statusImported {
		t.Errorf("status = %q, want imported", g.Status)
	}
	if !heldByTitle(t, st, titleID) {
		t.Error("the item must read as held once the feature is in the library")
	}
}

// An exact size tie is a conflict rather than a coin flip, exactly as for
// same-number claimants: taking either would silently drop the other. It is the
// one deferral a movie payload holding videos can reach, and a human resolves it
// from Activity by naming the file — an override still overrules every rule.
func TestMovieSizeTieDefersAndIsFixableByNamingTheFile(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	target, _, movies := movieLibrary(t)

	const feature = "Placeholder.Film.2019.1080p.BluRay.x264-SynthGroup.mkv"
	const other = "Placeholder Film - Deleted Scene 1.mkv"
	dir := writeTree(t, feature, other)
	grow(t, dir, feature, 2048)
	grow(t, dir, other, 2048)

	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: target}, discardLogger(), noRecorder{}, nil)
	ctx := context.Background()
	if err := im.ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	g := grabByHash(t, st, "abc")
	if g.Status != statusDeferred {
		t.Fatalf("status = %q, want it deferred rather than guessed at", g.Status)
	}
	wantFiles(t, movies)
	if heldByTitle(t, st, titleID) {
		t.Error("nothing was placed, so the item must not read as held")
	}

	info, err := im.ListPayload(ctx, g.ID)
	if err != nil {
		t.Fatalf("ListPayload: %v", err)
	}
	if len(info.Files) != 2 {
		t.Fatalf("payload files = %+v, want both videos offered", info.Files)
	}

	results, err := im.RetryImport(ctx, g.ID, map[string]int{feature: 1})
	if err != nil {
		t.Fatalf("RetryImport: %v", err)
	}
	if len(results) != 1 || results[0].Outcome != statusImported {
		t.Fatalf("retry results = %+v, want the named file imported", results)
	}
	wantFiles(t, movies, "Placeholder Film (2019)/Placeholder Film (2019).mkv")
	wantPlacedFrom(t, filepath.Join(movies, "Placeholder Film (2019)", "Placeholder Film (2019).mkv"), feature)
	if !heldByTitle(t, st, titleID) {
		t.Error("the item must read as held after a successful fix")
	}
}

// A failed movie grab settles the same way an episode's does: the item reverts
// to wanted and the release is remembered, so the sweep does not re-derive it.
func TestMovieGrabFailureRevertsTheItemAndRemembersTheRelease(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	target := &coretest.FakeLibrary{}
	rec := &fakeRecorder{}

	dl := &coretest.FakeDownload{Statuses: []download.Status{
		{Hash: "abc", State: download.StateError},
	}}
	im := New(st, fakeSource{dl: dl, lib: target}, discardLogger(), rec, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if g := grabByHash(t, st, "abc"); g.Status != statusFailed {
		t.Errorf("status = %q, want failed", g.Status)
	}
	if heldByTitle(t, st, titleID) {
		t.Error("a failed grab must leave the item wanted, not held")
	}
	if len(rec.calls) != 1 {
		t.Fatalf("recorded %d blocklist entries, want one for the release", len(rec.calls))
	}
	if rec.calls[0].titleID != titleID || rec.calls[0].infoHash != "abc" {
		t.Errorf("recorded %+v, want it keyed on this movie's release", rec.calls[0])
	}
}

// Format is the discriminator and item count never is: a one-item OVA takes the
// episodic path through the importer, season folder and SxxEyy stem included.
func TestSingleItemOVAKeepsTheEpisodicImportPath(t *testing.T) {
	st := coretest.NewStore(t)
	seedOneItemGrab(t, st, "Placeholder OVA", "abc", domain.FormatOVA, 2019)
	target, series, movies := movieLibrary(t)

	dir := writeTree(t, "[SynthSubs] Placeholder OVA [1080p].mkv")
	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: target}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	wantFiles(t, series, "Placeholder OVA/Season 01/Placeholder OVA - S01E01.mkv")
	wantFiles(t, movies)
}

// --- what a movie's settled reasons say -------------------------------------

// These reasons are read in the Activity queue by someone deciding what to do
// next, and a movie reaches them: calling its one item "episode 1" names
// something the user never asked for. A movie holding videos can only ever
// defer on a size tie — the largest-video rule answers every other shape — so
// that and the archive advice below are the two reasons to get right.
func TestMovieConflictReasonNamesTheMovieRatherThanAnEpisode(t *testing.T) {
	st := coretest.NewStore(t)
	titleID := seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	dir := writeTree(t,
		"[SynthSubs] Placeholder Film [1080p].mkv",
		"[OtherGroup] Placeholder Film [720p].mkv",
	)
	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	const want = "2 files claim this movie and nothing tells them apart"
	if got := deferralDetail(t, st, titleID); got != want {
		t.Errorf("deferral detail = %q, want %q", got, want)
	}
}

// The archive advice reaches a movie through the retry, which is where a human
// clicking Fix import before extracting lands.
func TestMovieArchiveRetryReasonNamesTheMovie(t *testing.T) {
	st := coretest.NewStore(t)
	seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	dir := writeTree(t, "Placeholder.Film.2019.rar")
	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil)
	ctx := context.Background()
	if err := im.ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	results, err := im.RetryImport(ctx, grabByHash(t, st, "abc").ID, nil)
	if err != nil {
		t.Fatalf("RetryImport: %v", err)
	}
	const want = `no file matched this movie; the archive "Placeholder.Film.2019.rar" is still packed, ` +
		"so Transpondarr does not unpack archives, so extract it into the download folder and use Fix import"
	if len(results) != 1 || results[0].Detail != want {
		t.Errorf("retry detail = %q, want %q", results[0].Detail, want)
	}
}

// The importer is where a notification learns what its item is, so a movie's
// import carries the kind that keeps "Episode 1" out of the push.
func TestMovieImportDispatchesTheMovieItemKind(t *testing.T) {
	st := coretest.NewStore(t)
	seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)
	fn := coretest.NewFakeNotifier()
	im := New(st, notifyingSource(completedSource(t, "abc"), &coretest.FakeLibrary{}, fn), discardLogger(), noRecorder{}, nil)
	if err := im.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	ev := waitEvent(t, fn)
	if ev.Kind != notify.KindImported || ev.ItemKind != domain.KindMovie {
		t.Errorf("event = %+v, want an imported movie", ev)
	}
	if ev.ItemNumber != 1 {
		t.Errorf("item number = %d, want 1 kept for machine consumers", ev.ItemNumber)
	}
}

// The retry endpoint's refusals are reachable by a direct API call rather than
// from the Fix import dialog, and they name a number the caller chose — which a
// movie's one item makes nonsense of if it is called an episode.
func TestMovieRetryRefusalsNameItemsRatherThanEpisodes(t *testing.T) {
	st := coretest.NewStore(t)
	seedMovieGrab(t, st, "Placeholder Film", "abc", 2019)

	const feature = "Placeholder.Film.2019.1080p.mkv"
	const other = "Placeholder Film - Deleted Scene 1.mkv"
	dir := writeTree(t, feature, other)
	grow(t, dir, feature, 2048)
	grow(t, dir, other, 2048)

	im := New(st, fakeSource{dl: completedPayload("abc", dir), lib: &coretest.FakeLibrary{}}, discardLogger(), noRecorder{}, nil)
	ctx := context.Background()
	if err := im.ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
	grabID := grabByHash(t, st, "abc").ID

	cases := []struct {
		name       string
		assignment map[string]int
		want       string
	}{
		{"a number the movie does not have", map[string]int{feature: 2}, "a movie has no item 2"},
		{"not an item number at all", map[string]int{feature: 0}, `"` + feature + `" was assigned item 0`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := im.RetryImport(ctx, grabID, tc.assignment)
			if err == nil {
				t.Fatal("the retry was accepted; want it refused")
			}
			if !errors.Is(err, ErrBadAssignment) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}
