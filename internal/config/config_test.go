package config

import (
	"os"
	"path/filepath"
	"testing"
)

// restoreEnv puts keys back the way it found them, because loadDotEnv writes to
// the real process environment and would otherwise leak into the next test.
func restoreEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { _ = os.Setenv(k, v) })
		} else {
			t.Cleanup(func() { _ = os.Unsetenv(k) })
		}
		_ = os.Unsetenv(k)
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestEnvLocalOverridesEnv(t *testing.T) {
	restoreEnv(t, "TRANSPONDARR_TORZNAB_URL")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, ".env", "TRANSPONDARR_TORZNAB_URL=http://shared.example/api\n")
	writeFile(t, dir, ".env.local", "TRANSPONDARR_TORZNAB_URL=http://worktree.example/api\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.TorznabURL != "http://worktree.example/api" {
		t.Errorf("TorznabURL = %q, want the .env.local value to win over .env", c.TorznabURL)
	}
}

func TestRealEnvironmentBeatsBothDotEnvFiles(t *testing.T) {
	restoreEnv(t, "TRANSPONDARR_TORZNAB_URL")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, ".env", "TRANSPONDARR_TORZNAB_URL=http://shared.example/api\n")
	writeFile(t, dir, ".env.local", "TRANSPONDARR_TORZNAB_URL=http://worktree.example/api\n")
	t.Setenv("TRANSPONDARR_TORZNAB_URL", "http://real.example/api")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.TorznabURL != "http://real.example/api" {
		t.Errorf("TorznabURL = %q, want the real environment to win over both files", c.TorznabURL)
	}
}

func TestEnvIsStillReadWhenNoEnvLocalExists(t *testing.T) {
	restoreEnv(t, "TRANSPONDARR_TORZNAB_URL")
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, dir, ".env", "TRANSPONDARR_TORZNAB_URL=http://shared.example/api\n")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.TorznabURL != "http://shared.example/api" {
		t.Errorf("TorznabURL = %q, want the .env value", c.TorznabURL)
	}
}
func TestAnilistEndpointIsEmptyUnlessSet(t *testing.T) {
	restoreEnv(t, "TRANSPONDARR_ANILIST_ENDPOINT")
	t.Chdir(t.TempDir())

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AnilistEndpoint != "" {
		t.Errorf("AnilistEndpoint = %q, want empty so the client keeps its own default", c.AnilistEndpoint)
	}
}

func TestAnilistEndpointReadsItsEnvVar(t *testing.T) {
	restoreEnv(t, "TRANSPONDARR_ANILIST_ENDPOINT")
	t.Chdir(t.TempDir())
	t.Setenv("TRANSPONDARR_ANILIST_ENDPOINT", "http://127.0.0.1:9999/graphql")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.AnilistEndpoint != "http://127.0.0.1:9999/graphql" {
		t.Errorf("AnilistEndpoint = %q, want the env value", c.AnilistEndpoint)
	}
}
