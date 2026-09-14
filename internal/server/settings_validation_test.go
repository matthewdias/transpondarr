package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The settings service's validation errors are lowercase Go text, so the 422 a
// user reads is worded here, and keeps the value the user entered.
func TestSettingsValidationErrorsAreWordedForTheUI(t *testing.T) {
	h := newHarness(t, nil, nil)
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")

	library := func(dir string) map[string]any {
		return map[string]any{"dir": dir, "movies_dir": "", "series_layout": "season_folders", "mode": "auto"}
	}
	cases := []struct {
		name, method, path string
		body               map[string]any
		want               string
	}{
		{
			"category on save", http.MethodPut, "/api/v1/settings/indexer",
			map[string]any{"name": "idx", "url": "http://idx.example/api", "categories": "5070,anime"},
			`"anime" isn't a Newznab category ID. Enter positive numbers separated by commas, such as 5070.`,
		},
		{
			"category on test", http.MethodPost, "/api/v1/settings/indexer/test",
			map[string]any{"name": "idx", "url": "http://idx.example/api", "categories": "-1"},
			`"-1" isn't a Newznab category ID. Enter positive numbers separated by commas, such as 5070.`,
		},
		{
			"no library directory", http.MethodPost, "/api/v1/settings/library/test",
			library(""),
			"Enter a library directory, a movies directory, or both.",
		},
		{
			"library path is a file", http.MethodPost, "/api/v1/settings/library/test",
			library(file),
			`The library path "` + file + `" is a file. Enter a directory.`,
		},
		{
			"library directory missing", http.MethodPost, "/api/v1/settings/library/test",
			library(missing),
			`Transpondarr can't open the library directory "` + missing + `": no such file or directory. Check that the path exists where Transpondarr runs.`,
		},
		{
			"no qBittorrent URL", http.MethodPost, "/api/v1/settings/download/test",
			map[string]any{"url": "", "user": "", "category": "anime", "stall_hours": 6},
			"Enter the qBittorrent WebUI URL to test the connection.",
		},
		{
			"no Torznab URL", http.MethodPost, "/api/v1/settings/indexer/test",
			map[string]any{"name": "idx", "url": "", "categories": ""},
			"Enter the Torznab URL to test the indexer.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p problemBody
			if code := do(t, h, tc.method, tc.path, tc.body, &p); code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (detail %q)", code, p.Detail)
			}
			if p.Detail != tc.want {
				t.Errorf("detail = %q\n want %q", p.Detail, tc.want)
			}
		})
	}
}

func TestLibraryNotWritableIsWordedForTheUI(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions don't restrict this user")
	}
	h := newHarness(t, nil, nil)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	var p problemBody
	body := map[string]any{"dir": dir, "movies_dir": "", "series_layout": "season_folders", "mode": "auto"}
	if code := do(t, h, http.MethodPost, "/api/v1/settings/library/test", body, &p); code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
	want := `Transpondarr can't write to the library directory "` + dir + `": permission denied. Give the user Transpondarr runs as write access.`
	if p.Detail != want {
		t.Errorf("detail = %q\n want %q", p.Detail, want)
	}
}

func TestSecretRefusalIsWordedForTheUI(t *testing.T) {
	h := newHarness(t, nil, nil)
	seed := map[string]any{
		"url": "http://qb.saved:8080", "user": "admin", "password": "hunter2",
		"category": "anime", "stall_hours": 6,
	}
	if code := do(t, h, http.MethodPut, "/api/v1/settings/download", seed, nil); code != http.StatusOK {
		t.Fatalf("seed = %d, want 200", code)
	}
	elsewhere := map[string]any{"url": "http://qb.other:8080", "user": "admin", "category": "anime", "stall_hours": 6}

	var p problemBody
	if code := do(t, h, http.MethodPost, "/api/v1/settings/download/test", elsewhere, &p); code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", code)
	}
	want := "The saved qBittorrent password is only sent to http://qb.saved:8080. Enter the qBittorrent password for http://qb.other:8080."
	if p.Detail != want {
		t.Errorf("detail = %q\n want %q", p.Detail, want)
	}
}
