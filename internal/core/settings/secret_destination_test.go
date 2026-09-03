package settings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// stubQbit stands in for a qBittorrent WebUI, recording the credentials each login
// attempt carried so a test can assert a secret never left the process.
func stubQbit(t *testing.T, seen *url.Values) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			_ = r.ParseForm()
			*seen = r.PostForm
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test"})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stubTorznab records the apikey each search carried and answers with an empty feed.
func stubTorznab(t *testing.T, seen *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.URL.Query().Get("apikey")
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<rss version="2.0"><channel></channel></rss>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stubNtfy records the Authorization header each publish carried.
func stubNtfy(t *testing.T, seen *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The Test button fills a blank secret from storage, and the destination is the
// caller's to choose — so without a check the endpoint reads the stored secret back
// out to any host named in the request (#259). The assertion that matters is the
// second: the caller's server must have been sent nothing at all.
func TestTestDownloadRefusesToSendTheStoredPasswordElsewhere(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	var seen url.Values
	attacker := stubQbit(t, &seen)

	if err := svc.UpdateDownload(ctx, DownloadConfig{
		URL: "http://qb.saved:8080", User: "admin", Password: "hunter2",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := svc.TestDownload(ctx, DownloadConfig{URL: attacker.URL, User: "admin"})
	if !errors.Is(err, ErrSecretRequired) {
		t.Errorf("TestDownload at a caller-supplied host = %v, want ErrSecretRequired", err)
	}
	if seen != nil {
		t.Errorf("the stored password reached a caller-supplied host: %v", seen)
	}
}

func TestTestIndexerRefusesToSendTheStoredAPIKeyElsewhere(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	var seen string
	attacker := stubTorznab(t, &seen)

	if err := svc.UpdateIndexer(ctx, IndexerConfig{
		Name: "jackett", URL: "http://jackett.saved:9117/api", APIKey: "tracker-account-key",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := svc.TestIndexer(ctx, IndexerConfig{Name: "jackett", URL: attacker.URL})
	if !errors.Is(err, ErrSecretRequired) {
		t.Errorf("TestIndexer at a caller-supplied host = %v, want ErrSecretRequired", err)
	}
	if seen != "" {
		t.Errorf("the stored API key reached a caller-supplied host: %q", seen)
	}
}

func TestTestNotifyNtfyRefusesToSendTheStoredTokenElsewhere(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	var seen string
	attacker := stubNtfy(t, &seen)

	if err := svc.UpdateNotify(ctx, NotifyConfig{
		NtfyServer: "https://ntfy.saved", NtfyTopic: "transpondarr", NtfyToken: "tk_secret",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := svc.TestNotifyNtfy(ctx, NotifyConfig{NtfyServer: attacker.URL, NtfyTopic: "transpondarr"})
	if !errors.Is(err, ErrSecretRequired) {
		t.Errorf("TestNotifyNtfy at a caller-supplied server = %v, want ErrSecretRequired", err)
	}
	if seen != "" {
		t.Errorf("the stored token reached a caller-supplied host: %q", seen)
	}
}

// The save path inherits the same secrets and then builds a live client against the
// caller's URL, which authenticates to it on the next poll — the same exfiltration,
// one poll later. Nothing may be persisted or swapped on the refusal.
func TestUpdateDownloadRefusesToSendTheStoredPasswordElsewhere(t *testing.T) {
	svc, reg, st := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateDownload(ctx, DownloadConfig{
		URL: "http://qb.saved:8080", User: "admin", Password: "hunter2",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	client := reg.Download()

	err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://qb.attacker:8080", User: "admin"})
	if !errors.Is(err, ErrSecretRequired) {
		t.Fatalf("UpdateDownload to a new host with a blank password = %v, want ErrSecretRequired", err)
	}
	if got := svc.Snapshot().Download.URL; got != "http://qb.saved:8080" {
		t.Errorf("url = %q after a refused save, want the saved one", got)
	}
	if got, _ := st.Q.GetSetting(ctx, keyQbitURL); got != "http://qb.saved:8080" {
		t.Errorf("persisted url = %q after a refused save, want the saved one", got)
	}
	if reg.Download() != client {
		t.Error("registry client swapped despite a refused save")
	}
}

func TestUpdateIndexerRefusesToSendTheStoredAPIKeyElsewhere(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateIndexer(ctx, IndexerConfig{
		Name: "jackett", URL: "http://jackett.saved:9117/api", APIKey: "tracker-account-key",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := svc.UpdateIndexer(ctx, IndexerConfig{Name: "jackett", URL: "http://jackett.attacker:9117/api"})
	if !errors.Is(err, ErrSecretRequired) {
		t.Errorf("UpdateIndexer to a new host with a blank key = %v, want ErrSecretRequired", err)
	}
}

func TestUpdateNotifyRefusesToSendTheStoredTokenElsewhere(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateNotify(ctx, NotifyConfig{
		NtfyServer: "https://ntfy.saved", NtfyTopic: "transpondarr", NtfyToken: "tk_secret",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := svc.UpdateNotify(ctx, NotifyConfig{NtfyServer: "https://ntfy.attacker", NtfyTopic: "transpondarr"})
	if !errors.Is(err, ErrSecretRequired) {
		t.Errorf("UpdateNotify to a new server with a blank token = %v, want ErrSecretRequired", err)
	}
}

// The Test button's whole point: the saved host still authenticates with a secret the
// user never has to retype, and for the indexer that survives a path edit — switching
// which Jackett indexer is probed is not a new destination.
func TestInheritsTheStoredSecretForTheSavedDestination(t *testing.T) {
	ctx := context.Background()

	t.Run("download", func(t *testing.T) {
		svc, _, _ := newTestService(t)
		var seen url.Values
		qbit := stubQbit(t, &seen)

		if err := svc.UpdateDownload(ctx, DownloadConfig{URL: qbit.URL, User: "admin", Password: "hunter2"}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := svc.TestDownload(ctx, DownloadConfig{URL: qbit.URL, User: "admin"}); err != nil {
			t.Fatalf("TestDownload at the saved host: %v", err)
		}
		if got := seen.Get("password"); got != "hunter2" {
			t.Errorf("password sent = %q, want the stored one", got)
		}
	})

	t.Run("indexer through a path edit", func(t *testing.T) {
		svc, _, _ := newTestService(t)
		var seen string
		jackett := stubTorznab(t, &seen)

		if err := svc.UpdateIndexer(ctx, IndexerConfig{
			Name: "jackett", URL: jackett.URL + "/api/v2.0/indexers/one/results/torznab", APIKey: "k",
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		if err := svc.TestIndexer(ctx, IndexerConfig{
			Name: "jackett", URL: jackett.URL + "/api/v2.0/indexers/two/results/torznab",
		}); err != nil {
			t.Fatalf("TestIndexer at the saved host with a new path: %v", err)
		}
		if seen != "k" {
			t.Errorf("apikey sent = %q, want the stored one", seen)
		}
	})
}

// Nothing is stored, so nothing can leak: a qBittorrent with no password set stays
// testable at a host it was never saved for.
func TestAllowsABlankSecretWhenNoneIsStored(t *testing.T) {
	svc, _, _ := newTestService(t)
	var seen url.Values
	qbit := stubQbit(t, &seen)

	if err := svc.TestDownload(context.Background(), DownloadConfig{URL: qbit.URL, User: "admin"}); err != nil {
		t.Fatalf("TestDownload with no stored password: %v", err)
	}
	if got := seen.Get("password"); got != "" {
		t.Errorf("password sent = %q, want empty", got)
	}
}

// Clearing the URL disables the integration; no request is ever made, so there is
// nothing to leak and the stored secret must survive rather than be refused or wiped.
func TestClearingTheURLKeepsTheStoredSecret(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://qb.saved:8080", User: "admin", Password: "hunter2"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.UpdateDownload(ctx, DownloadConfig{URL: "", User: "admin"}); err != nil {
		t.Fatalf("clearing the url: %v", err)
	}
	if got := svc.Snapshot().Download.Password; got != "hunter2" {
		t.Errorf("password = %q after clearing the url, want it kept", got)
	}

	// The kept secret now belongs to no host, so re-enabling somewhere else still
	// has to ask for it rather than reviving it against a new destination.
	err := svc.UpdateDownload(ctx, DownloadConfig{URL: "http://qb.elsewhere:8080", User: "admin"})
	if !errors.Is(err, ErrSecretRequired) {
		t.Fatalf("re-pointing a disabled client with a blank password = %v, want ErrSecretRequired", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "http://qb.elsewhere:8080") {
		t.Errorf("refusal %q should name the host being asked for", msg)
	}
}

// A blank ntfy server means the public ntfy.sh, which is a destination in its own
// right — so a custom server's token must not ride along to it. This pins the order
// inside the two ntfy paths: defaulting after the inheritance instead of before
// makes the blank server read as "no destination", which hands the stored token to
// ntfy.sh.
func TestBlankNtfyServerDoesNotInheritACustomServersToken(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		call func(*Service, NotifyConfig) error
	}{
		{"test", func(s *Service, in NotifyConfig) error { return s.TestNotifyNtfy(ctx, in) }},
		{"save", func(s *Service, in NotifyConfig) error { return s.UpdateNotify(ctx, in) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newTestService(t)
			if err := svc.UpdateNotify(ctx, NotifyConfig{
				NtfyServer: "https://ntfy.custom", NtfyTopic: "transpondarr", NtfyToken: "tk_secret",
			}); err != nil {
				t.Fatalf("seed: %v", err)
			}
			err := tc.call(svc, NotifyConfig{NtfyTopic: "transpondarr"})
			if !errors.Is(err, ErrSecretRequired) {
				t.Errorf("a blank server against a stored custom one = %v, want ErrSecretRequired", err)
			}
		})
	}
}

// A blank topic builds no ntfy route, so there is no destination and nothing can
// leak. Turning ntfy off — and editing any other adapter afterwards, since the
// notifications body is the whole section's state (#227) — must not demand the
// token of a server that is no longer being written to.
func TestDisablingNtfyDoesNotDemandItsToken(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateNotify(ctx, NotifyConfig{
		NtfyServer: "https://ntfy.custom", NtfyTopic: "transpondarr", NtfyToken: "tk_secret",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.UpdateNotify(ctx, NotifyConfig{DiscordURL: "https://discord.example/api/webhooks/1/abc"}); err != nil {
		t.Fatalf("disabling ntfy while editing Discord: %v", err)
	}
	if got := svc.Snapshot().Notify.NtfyToken; got != "tk_secret" {
		t.Errorf("token = %q after disabling ntfy, want it kept", got)
	}
}

// The guard is per-request, so a request it lets through must not move the baseline
// the next request is compared against. A blank topic means "no destination", which
// is why the token is inherited — but persisting the caller's server beside it would
// rebind the token to that server, and the follow-up would then match (#259).
func TestABlankNtfyTopicCannotMoveTheServerTheTokenIsBoundTo(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	var seen string
	attacker := stubNtfy(t, &seen)

	if err := svc.UpdateNotify(ctx, NotifyConfig{
		NtfyServer: "https://ntfy.custom", NtfyTopic: "transpondarr", NtfyToken: "tk_secret",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Step 1: names no destination, so it is accepted — but it must not relocate
	// the stored server.
	if err := svc.UpdateNotify(ctx, NotifyConfig{NtfyServer: attacker.URL}); err != nil {
		t.Fatalf("blank-topic save: %v", err)
	}
	if got := svc.Snapshot().Notify.NtfyServer; got != "https://ntfy.custom" {
		t.Errorf("server = %q after a save naming no destination, want the stored one", got)
	}

	// Step 2: the same server, now with a topic. It is a different destination from
	// the one the token was saved for, so it must still be refused.
	err := svc.UpdateNotify(ctx, NotifyConfig{NtfyServer: attacker.URL, NtfyTopic: "pwn"})
	if !errors.Is(err, ErrSecretRequired) {
		t.Errorf("follow-up save at the caller's server = %v, want ErrSecretRequired", err)
	}
	if err := svc.TestNotifyNtfy(ctx, NotifyConfig{NtfyServer: attacker.URL, NtfyTopic: "pwn"}); !errors.Is(err, ErrSecretRequired) {
		t.Errorf("follow-up test at the caller's server = %v, want ErrSecretRequired", err)
	}
	if seen != "" {
		t.Errorf("the stored token reached a caller-supplied host: %q", seen)
	}
}

// The same seam without an attacker: turning ntfy off by clearing both fields must
// not leave the custom server's token attached to the defaulted ntfy.sh, or the next
// save that sets a topic ships it there.
func TestDisablingNtfyKeepsTheTokenBoundToItsOwnServer(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateNotify(ctx, NotifyConfig{
		NtfyServer: "https://ntfy.custom", NtfyTopic: "transpondarr", NtfyToken: "tk_secret",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.UpdateNotify(ctx, NotifyConfig{}); err != nil {
		t.Fatalf("disabling ntfy: %v", err)
	}

	snap := svc.Snapshot().Notify
	if snap.NtfyServer != "https://ntfy.custom" {
		t.Errorf("server = %q after disabling ntfy, want the token's own server", snap.NtfyServer)
	}
	if snap.NtfyToken != "tk_secret" {
		t.Errorf("token = %q after disabling ntfy, want it kept", snap.NtfyToken)
	}
	if snap.NtfyTopic != "" {
		t.Errorf("topic = %q, want it cleared", snap.NtfyTopic)
	}
}

// The freeze above is exactly as wide as the thing it protects: with no token
// stored there is nothing to rebind, so staging a server before choosing a topic
// must still save rather than be silently discarded.
func TestABlankNtfyTopicStillSavesTheServerWhenNoTokenIsStored(t *testing.T) {
	svc, _, _ := newTestService(t)

	if err := svc.UpdateNotify(context.Background(), NotifyConfig{NtfyServer: "https://ntfy.custom"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := svc.Snapshot().Notify.NtfyServer; got != "https://ntfy.custom" {
		t.Errorf("server = %q, want the one just saved", got)
	}
}

// A self-hosted ntfy still fills its own blank token on a save — the save path's
// inheritance was only ever covered with a blank server, so nothing caught a rule
// that refused every custom server.
func TestUpdateNotifyInheritsTokenForACustomServer(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.UpdateNotify(ctx, NotifyConfig{
		NtfyServer: "https://ntfy.custom", NtfyTopic: "transpondarr", NtfyToken: "tk_secret",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.UpdateNotify(ctx, NotifyConfig{
		NtfyServer: "https://ntfy.custom", NtfyTopic: "renamed",
	}); err != nil {
		t.Fatalf("save against the same custom server: %v", err)
	}
	snap := svc.Snapshot().Notify
	if snap.NtfyToken != "tk_secret" {
		t.Errorf("token = %q, want the stored one", snap.NtfyToken)
	}
	if snap.NtfyTopic != "renamed" {
		t.Errorf("topic = %q, want it updated", snap.NtfyTopic)
	}
}

// The destination is the host that receives the secret, so the path, the trailing
// slash, letter case and an explicitly written default port do not make a new one --
// each of those refusing would cost a retype and buy nothing.
func TestSameDestination(t *testing.T) {
	for _, tc := range []struct {
		name, a, b string
		want       bool
	}{
		{"identical", "http://qb:8080", "http://qb:8080", true},
		{"trailing slash", "http://qb:8080", "http://qb:8080/", true},
		{"path edit", "http://j:9117/api/one", "http://j:9117/api/two", true},
		{"host case", "http://QB:8080", "http://qb:8080", true},
		{"explicit default port", "https://idx.example", "https://idx.example:443", true},
		{"explicit http default port", "http://idx.example", "http://idx.example:80", true},
		{"query only", "http://j:9117/api?a=1", "http://j:9117/api?a=2", true},
		{"different host", "http://qb:8080", "http://evil:8080", false},
		{"different port", "http://qb:8080", "http://qb:9090", false},
		{"different scheme", "https://qb:8080", "http://qb:8080", false},
		{"non-default port against none", "https://idx.example", "https://idx.example:8443", false},
		// Two unrelated bare strings both parse to an empty host, so a hostless URL
		// must be only itself or the rule would call them the same destination.
		{"hostless pair", "qb.example/path", "evil.example/path", false},
		{"hostless identical", "not a url", "not a url", true},
		{"hostless against a url", "", "http://qb:8080", false},
	} {
		if got := sameDestination(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: sameDestination(%q, %q) = %v, want %v", tc.name, tc.a, tc.b, got, tc.want)
		}
	}
}
