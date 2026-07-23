package importer

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeTree materialises a payload directory; a path ending in "/" is an empty
// directory, anything else a file.
func writeTree(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if p[len(p)-1] == '/' {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestResolvesSingleEpisodeAmongSidecars: the usual companions must not defeat resolution.
func TestResolvesSingleEpisodeAmongSidecars(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].nfo",
		"Subs/[ExampleSubs] Placeholder Saga - 05 [1080p].en.srt",
		"Sample/placeholder-saga-05-sample.mkv",
		"Screens/shot01.png",
		"RARBG.txt",
	)

	got, err := resolvePayloadFile(root, 5)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if filepath.Base(got) != "[ExampleSubs] Placeholder Saga - 05 [1080p].mkv" {
		t.Errorf("resolved %q, want the episode file", got)
	}
}

// TestResolvesSampleInPayloadRoot: a sample outside a Sample/ folder is caught by name.
func TestResolvesSampleInPayloadRoot(t *testing.T) {
	root := writeTree(t,
		"Placeholder.Saga.S01E05.1080p.WEB.H264-EXAMPLE.mkv",
		"sample-placeholder.saga.s01e05.mkv",
	)

	got, err := resolvePayloadFile(root, 5)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if filepath.Base(got) != "Placeholder.Saga.S01E05.1080p.WEB.H264-EXAMPLE.mkv" {
		t.Errorf("resolved %q, want the episode file", got)
	}
}

// TestResolvesEpisodeAlongsideUnnumberedExtra: an extra that escapes both filters
// carries no episode number, so it is not competing.
func TestResolvesEpisodeAlongsideUnnumberedExtra(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - Interview With The Director [1080p].mkv",
	)

	got, err := resolvePayloadFile(root, 5)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if filepath.Base(got) != "[ExampleSubs] Placeholder Saga - 05 [1080p].mkv" {
		t.Errorf("resolved %q, want the numbered episode", got)
	}
}

// TestResolvesUnrecognisableFilename is the identity-by-construction case.
func TestResolvesUnrecognisableFilename(t *testing.T) {
	root := writeTree(t, "b1946ac92492d2347c6235b4d2611184.mkv")

	got, err := resolvePayloadFile(root, 5)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if filepath.Base(got) != "b1946ac92492d2347c6235b4d2611184.mkv" {
		t.Errorf("resolved %q", got)
	}
}

// TestRefusesMultiEpisodePayload: a season pack contains the wanted episode too,
// so picking it out would "work" and drop every other episode.
func TestRefusesMultiEpisodePayload(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 04 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 06 [1080p].mkv",
	)

	if _, err := resolvePayloadFile(root, 5); !errors.Is(err, errAmbiguousPayload) {
		t.Errorf("err = %v, want errAmbiguousPayload", err)
	}
}

// TestRefusesDuplicateEpisodeFiles: with a v1/v2 pair either could be right, and
// guessing is not this resolver's job.
func TestRefusesDuplicateEpisodeFiles(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[OtherGroup] Placeholder Saga - 05 [720p].mkv",
	)

	if _, err := resolvePayloadFile(root, 5); !errors.Is(err, errAmbiguousPayload) {
		t.Errorf("err = %v, want errAmbiguousPayload", err)
	}
}

// TestRefusesPayloadWithoutVideo guards against importing a sidecar as the episode.
func TestRefusesPayloadWithoutVideo(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 05 [1080p].rar",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].sfv",
		"Subs/[ExampleSubs] Placeholder Saga - 05 [1080p].en.srt",
	)

	if _, err := resolvePayloadFile(root, 5); !errors.Is(err, errNoVideoFile) {
		t.Errorf("err = %v, want errNoVideoFile", err)
	}
}

// TestRefusesWhenOnlyCandidateIsAnExtra: filtering out extras must not leave the
// resolver reaching for whatever is left.
func TestRefusesWhenOnlyCandidateIsAnExtra(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - NCOP [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - NCED [1080p].mkv",
	)

	if _, err := resolvePayloadFile(root, 5); !errors.Is(err, errNoVideoFile) {
		t.Errorf("err = %v, want errNoVideoFile", err)
	}
}

// TestNonEpisodeTokensMatchWholeWords pins the token-not-substring rule.
func TestNonEpisodeTokensMatchWholeWords(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"[ExampleSubs] Extraordinary Placeholder - 05 [1080p].mkv", false},
		{"[ExampleSubs] Preview Of A Placeholder - 05 [1080p].mkv", true},
		{"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv", false},
		{"sample-placeholder.saga.s01e05.mkv", true},
		{"[ExampleSubs] Placeholder Saga - NCOP [1080p].mkv", true},
	}
	for _, tc := range tests {
		if got := hasNonEpisodeToken(tc.name); got != tc.want {
			t.Errorf("hasNonEpisodeToken(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
