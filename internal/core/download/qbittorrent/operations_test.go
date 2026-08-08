package qbittorrent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/matthewdias/transpondarr/internal/core/download"
)

// Add sends the category and (when Paused) the stopped field on the add request,
// exercised end-to-end through the autobrr client against a stub qBittorrent.
func TestAddSendsCategoryAndStopped(t *testing.T) {
	var addCategory string
	var addStopped bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info": // idempotency check: no existing torrent
			_, _ = w.Write([]byte("[]"))
		case "/api/v2/torrents/add":
			addCategory = r.FormValue("category")
			addStopped = r.FormValue("stopped") == "true"
			_, _ = w.Write([]byte("Ok."))
		default:
			// Be lenient about any extra endpoints the client probes.
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	const magnet = "magnet:?xt=urn:btih:c9e15763f722f23e98a29decdfae341b98d53056&dn=x"
	res, err := c.Add(context.Background(), download.AddOptions{
		URL: magnet, Category: "transpondarr", Paused: true,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if res.Outcome != download.AddSuccess {
		t.Errorf("outcome = %v, want success", res.Outcome)
	}
	if res.Hash != "c9e15763f722f23e98a29decdfae341b98d53056" {
		t.Errorf("hash = %q, want the magnet's btih", res.Hash)
	}
	if addCategory != "transpondarr" {
		t.Errorf("add category field = %q, want transpondarr", addCategory)
	}
	if !addStopped {
		t.Error("expected stopped=true on the add (Paused)")
	}
}

// Add reports AddAlreadyExists (without re-adding) when the hash is already known.
func TestAddAlreadyExists(t *testing.T) {
	var addCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info": // torrent already present
			_, _ = w.Write([]byte(`[{"hash":"c9e15763f722f23e98a29decdfae341b98d53056","state":"downloading"}]`))
		case "/api/v2/torrents/add":
			addCalled = true
			_, _ = w.Write([]byte("Ok."))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	const magnet = "magnet:?xt=urn:btih:c9e15763f722f23e98a29decdfae341b98d53056"
	res, err := c.Add(context.Background(), download.AddOptions{URL: magnet})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if res.Outcome != download.AddAlreadyExists {
		t.Errorf("outcome = %v, want already-exists", res.Outcome)
	}
	if addCalled {
		t.Error("add endpoint should not be called when the torrent already exists")
	}
}

// Duplicate detection is check-then-act, so two concurrent adds of one hash both
// pass the empty check and one loses at the client. Re-checking after a failed
// add turns that loser back into convergence rather than an error the caller
// would answer by grabbing a different release for the same item.
func TestAddConvergesWhenAFailedAddWasADuplicate(t *testing.T) {
	const hash = "c9e15763f722f23e98a29decdfae341b98d53056"
	var added bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			// Empty until the racing add lands, present on the post-failure recheck.
			if added {
				_, _ = w.Write([]byte(`[{"hash":"` + hash + `","state":"downloading"}]`))
				return
			}
			_, _ = w.Write([]byte("[]"))
		case "/api/v2/torrents/add":
			added = true
			w.WriteHeader(http.StatusConflict)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	res, err := New(srv.URL, "u", "p").Add(context.Background(),
		download.AddOptions{URL: "magnet:?xt=urn:btih:" + hash})
	if err != nil {
		t.Fatalf("Add: %v, want convergence on the duplicate", err)
	}
	if res.Outcome != download.AddAlreadyExists {
		t.Errorf("outcome = %v, want already-exists", res.Outcome)
	}
	if res.Hash != hash {
		t.Errorf("hash = %q, want %q", res.Hash, hash)
	}
}

// A genuine add failure must still surface: the recheck only converges when the
// hash actually turned up, never by swallowing the error.
func TestAddSurfacesAFailureThatWasNotADuplicate(t *testing.T) {
	const hash = "c9e15763f722f23e98a29decdfae341b98d53056"
	srv := qbitStub(t, http.StatusInternalServerError)

	_, err := New(srv.URL, "u", "p").Add(context.Background(),
		download.AddOptions{URL: "magnet:?xt=urn:btih:" + hash})
	if err == nil {
		t.Fatal("expected the original add error when the hash is still absent")
	}
	if !strings.Contains(err.Error(), "add") {
		t.Errorf("error = %v, want the original add error", err)
	}
}

// qbitStub answers the endpoints Add probes, so a test only has to say how the
// add itself behaves.
func qbitStub(t *testing.T, addStatus int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			_, _ = w.Write([]byte("[]"))
		case "/api/v2/torrents/add":
			w.WriteHeader(addStatus)
			_, _ = w.Write([]byte("Ok."))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Only what the release itself is responsible for is reported as ErrBadRelease:
// the caller remembers those and must not remember a sick client's refusals,
// which say nothing about which release was asked for (#120).
func TestAddClassifiesReleaseFaultsSeparatelyFromClientFaults(t *testing.T) {
	torrentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gone.torrent":
			http.Error(w, "no such torrent", http.StatusNotFound)
		case "/junk.torrent":
			_, _ = w.Write([]byte("this is not bencoded metainfo"))
		default:
			http.Error(w, "unexpected", http.StatusTeapot)
		}
	}))
	defer torrentSrv.Close()

	cases := []struct {
		name       string
		url        string
		addStatus  int
		badRelease bool
	}{
		{"unparseable magnet", "magnet:?xt=urn:btih:not-a-hash", http.StatusOK, true},
		{"torrent url 404s", torrentSrv.URL + "/gone.torrent", http.StatusOK, true},
		{"torrent url serves junk", torrentSrv.URL + "/junk.torrent", http.StatusOK, true},
		{
			"client refuses the add",
			"magnet:?xt=urn:btih:c9e15763f722f23e98a29decdfae341b98d53056",
			http.StatusInternalServerError, false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			qb := New(qbitStub(t, c.addStatus).URL, "u", "p")
			_, err := qb.Add(context.Background(), download.AddOptions{URL: c.url})
			if err == nil {
				t.Fatal("Add succeeded, want an error")
			}
			if got := errors.Is(err, download.ErrBadRelease); got != c.badRelease {
				t.Errorf("errors.Is(%v, ErrBadRelease) = %v, want %v", err, got, c.badRelease)
			}
		})
	}
}

// The filter has to reach qBittorrent, not just the result: listing unmatched
// downloads otherwise pulls the user's entire client every poll (#131).
func TestStatusByCategoryFiltersAtTheClient(t *testing.T) {
	var gotCategory, gotFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			gotCategory = r.FormValue("category")
			gotFilter = r.FormValue("filter")
			_, _ = w.Write([]byte(`[{"hash":"aaaa1111","name":"ours","state":"downloading","category":"transpondarr"}]`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	var lister download.CategoryLister = c
	got, err := lister.StatusByCategory(context.Background(), "transpondarr")
	if err != nil {
		t.Fatalf("StatusByCategory: %v", err)
	}
	if gotCategory != "transpondarr" {
		t.Errorf("category sent to qBittorrent = %q, want transpondarr", gotCategory)
	}
	// A hash filter would contradict the category one; nothing must narrow it further.
	if gotFilter != "" && gotFilter != "all" {
		t.Errorf("filter sent to qBittorrent = %q, want none", gotFilter)
	}
	if len(got) != 1 || got[0].Category != "transpondarr" {
		t.Errorf("statuses = %+v, want the one categorized torrent", got)
	}
}

// Status deserializes qBittorrent's /torrents/info JSON into download.Status,
// mapping every field the import pipeline relies on — most importantly
// content_path and the normalized state — and forwards the requested hashes
// (lowercased) as the filter. TestMapState covers the state vocabulary in
// isolation; this covers the full response parse the importer actually consumes.
func TestStatusParsesTorrentsInfo(t *testing.T) {
	var gotHashesFilter string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			gotHashesFilter = r.FormValue("hashes")
			_, _ = w.Write([]byte(`[
				{"hash":"aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111","name":"Placeholder Saga S1E01","state":"downloading","progress":0.5,"save_path":"/downloads","content_path":"/downloads/Placeholder Saga S1E01.mkv","category":"transpondarr","size":734003200,"added_on":1754524800},
				{"hash":"bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222","name":"Placeholder Saga S1E02","state":"uploading","progress":1,"save_path":"/downloads","content_path":"/downloads/Placeholder Saga S1E02.mkv","category":"someone-elses"}
			]`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	// A mixed-case hash must reach qBittorrent lowercased (identity is keyed on the
	// lowercase info hash throughout the pipeline).
	got, err := c.Status(context.Background(), "AAAA1111AAAA1111AAAA1111AAAA1111AAAA1111")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d statuses, want 2", len(got))
	}

	first := got[0]
	if first.Hash != "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111" {
		t.Errorf("hash = %q", first.Hash)
	}
	if first.Name != "Placeholder Saga S1E01" {
		t.Errorf("name = %q", first.Name)
	}
	if first.State != download.StateDownloading {
		t.Errorf("state = %v, want downloading", first.State)
	}
	if first.Progress != 0.5 {
		t.Errorf("progress = %v, want 0.5", first.Progress)
	}
	if first.SavePath != "/downloads" {
		t.Errorf("save_path = %q", first.SavePath)
	}
	if first.ContentPath != "/downloads/Placeholder Saga S1E01.mkv" {
		t.Errorf("content_path = %q", first.ContentPath)
	}

	// The category is the whole safety boundary for the unmatched-downloads view,
	// so it has to survive the parse verbatim, other people's torrents included.
	if first.Category != "transpondarr" {
		t.Errorf("category = %q, want transpondarr", first.Category)
	}

	// Size and added time are what identify an unmatched torrent to a human, who
	// has no grab row to read it off (#131).
	if first.Size != 734003200 {
		t.Errorf("size = %d, want 734003200", first.Size)
	}
	if got := first.AddedAt.UTC().Format(time.RFC3339); got != "2025-08-07T00:00:00Z" {
		t.Errorf("added_at = %q, want 2025-08-07T00:00:00Z", got)
	}
	// A client that reports no add time leaves the zero value, never the epoch:
	// the DTO omits it rather than claiming the torrent arrived in 1970.
	if !got[1].AddedAt.IsZero() {
		t.Errorf("second added_at = %v, want the zero time when unreported", got[1].AddedAt)
	}

	// The seeding torrent maps to the complete state the importer treats as ready.
	if got[1].State != download.StateComplete {
		t.Errorf("second state = %v, want complete", got[1].State)
	}
	if got[1].Category != "someone-elses" {
		t.Errorf("second category = %q, want someone-elses", got[1].Category)
	}

	if !strings.Contains(gotHashesFilter, "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111") {
		t.Errorf("hashes filter sent to qBittorrent = %q, want the lowercased hash", gotHashesFilter)
	}
}

// Remove forwards the hashes lowercased (identity is the lowercase info hash
// throughout the pipeline) and asks qBittorrent to delete the payload data too.
func TestRemoveDeletesTorrentsWithData(t *testing.T) {
	var gotHashes, gotDeleteFiles string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/delete":
			gotHashes = r.FormValue("hashes")
			gotDeleteFiles = r.FormValue("deleteFiles")
			_, _ = w.Write([]byte("Ok."))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "u", "p")
	err := c.Remove(context.Background(),
		[]string{"AAAA1111AAAA1111AAAA1111AAAA1111AAAA1111", "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222"}, true)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if gotHashes != "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111|bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222" {
		t.Errorf("hashes sent to qBittorrent = %q, want both, lowercased, pipe-joined", gotHashes)
	}
	if gotDeleteFiles != "true" {
		t.Errorf("deleteFiles = %q, want true", gotDeleteFiles)
	}
}

func TestMapState(t *testing.T) {
	cases := map[string]download.State{
		"downloading":  download.StateDownloading,
		"metaDL":       download.StateDownloading,
		"stalledDL":    download.StateStalled,
		"uploading":    download.StateComplete,
		"pausedUP":     download.StateComplete,
		"stoppedUP":    download.StateComplete,
		"pausedDL":     download.StatePaused,
		"stoppedDL":    download.StatePaused,
		"checkingDL":   download.StateChecking,
		"moving":       download.StateChecking,
		"error":        download.StateError,
		"missingFiles": download.StateError,
		"wat":          download.StateUnknown,
	}
	for in, want := range cases {
		if got := mapState(in); got != want {
			t.Errorf("mapState(%q) = %v, want %v", in, got, want)
		}
	}
}

// A download URL's query string carries the indexer's API key, and this error is
// logged, stored on the item's pass outcome and rendered in a tooltip (#181), so
// no path may put the raw URL in it.
func TestAddNeverLeaksTheDownloadURLQueryString(t *testing.T) {
	const key = "s3cr3t-indexer-key"
	torrentSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such torrent", http.StatusNotFound)
	}))
	defer torrentSrv.Close()

	cases := []struct{ name, url string }{
		{"host answered and refused", torrentSrv.URL + "/gone.torrent?apikey=" + key},
		{"transport error", "http://127.0.0.1:1/gone.torrent?apikey=" + key},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			qb := New(qbitStub(t, http.StatusOK).URL, "u", "p")
			_, err := qb.Add(context.Background(), download.AddOptions{URL: c.url})
			if err == nil {
				t.Fatal("Add succeeded, want an error")
			}
			if strings.Contains(err.Error(), key) {
				t.Errorf("error leaks the API key: %v", err)
			}
			if strings.Contains(err.Error(), "apikey=") {
				t.Errorf("error leaks the query string: %v", err)
			}
		})
	}
}
