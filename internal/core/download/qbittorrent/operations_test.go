package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
				{"hash":"aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111","name":"Placeholder Saga S1E01","state":"downloading","progress":0.5,"save_path":"/downloads","content_path":"/downloads/Placeholder Saga S1E01.mkv"},
				{"hash":"bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222","name":"Placeholder Saga S1E02","state":"uploading","progress":1,"save_path":"/downloads","content_path":"/downloads/Placeholder Saga S1E02.mkv"}
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

	// The seeding torrent maps to the complete state the importer treats as ready.
	if got[1].State != download.StateComplete {
		t.Errorf("second state = %v, want complete", got[1].State)
	}

	if !strings.Contains(gotHashesFilter, "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111") {
		t.Errorf("hashes filter sent to qBittorrent = %q, want the lowercased hash", gotHashesFilter)
	}
}

func TestMapState(t *testing.T) {
	cases := map[string]download.State{
		"downloading": download.StateDownloading,
		"metaDL":      download.StateDownloading,
		"stalledDL":   download.StateStalled,
		"uploading":   download.StateComplete,
		"pausedUP":    download.StateComplete,
		"stoppedUP":   download.StateComplete,
		"pausedDL":    download.StatePaused,
		"stoppedDL":   download.StatePaused,
		"checkingDL":  download.StateChecking,
		"moving":      download.StateChecking,
		"error":       download.StateError,
		"missingFiles": download.StateError,
		"wat":         download.StateUnknown,
	}
	for in, want := range cases {
		if got := mapState(in); got != want {
			t.Errorf("mapState(%q) = %v, want %v", in, got, want)
		}
	}
}
