package importer

import (
	"os"
	"path/filepath"
	"sort"
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

// collected returns the payload-relative paths the walk kept, sorted.
func collected(t *testing.T, root string) []string {
	t.Helper()
	cands, err := collectPayloadFiles(root)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.rel)
	}
	sort.Strings(out)
	return out
}

func wantCollected(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("collected %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("collected %v, want %v", got, want)
		}
	}
}

// The usual companions must not become candidates: a sample descended into is
// how an obvious payload starts looking like a batch.
func TestCollectsEpisodeAmongSidecars(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].nfo",
		"Subs/[ExampleSubs] Placeholder Saga - 05 [1080p].en.srt",
		"Sample/placeholder-saga-05-sample.mkv",
		"Screens/shot01.png",
		"RARBG.txt",
	)

	wantCollected(t, collected(t, root), []string{"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv"})
}

// A sample outside a Sample/ folder is caught by name.
func TestSkipsSampleInPayloadRoot(t *testing.T) {
	root := writeTree(t,
		"Placeholder.Saga.S01E05.1080p.WEB.H264-EXAMPLE.mkv",
		"sample-placeholder.saga.s01e05.mkv",
	)

	wantCollected(t, collected(t, root), []string{"Placeholder.Saga.S01E05.1080p.WEB.H264-EXAMPLE.mkv"})
}

// An extra that escapes both filters is still collected: it carries no episode
// number, so the mapper leaves it over rather than placing it.
func TestCollectsUnnumberedExtraForTheMapper(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - Interview With The Director [1080p].mkv",
	)

	wantCollected(t, collected(t, root), []string{
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - Interview With The Director [1080p].mkv",
	})
}

// Every episode of a pack is collected -- the whole point of walking once.
func TestCollectsEveryEpisodeOfAPack(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 04 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 06 [1080p].mkv",
	)

	wantCollected(t, collected(t, root), []string{
		"[ExampleSubs] Placeholder Saga - 04 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - 06 [1080p].mkv",
	})
}

// A nested season folder is walked; the relative path is what a retry assignment
// names, so it must keep the folder.
func TestCollectsNestedFilesWithTheirRelativePath(t *testing.T) {
	root := writeTree(t, "Season 01/[ExampleSubs] Placeholder Saga - 05 [1080p].mkv")

	wantCollected(t, collected(t, root), []string{"Season 01/[ExampleSubs] Placeholder Saga - 05 [1080p].mkv"})
}

// No video at all: nothing to place, and #135 owns unpacking a RAR set.
func TestCollectsNothingFromAPayloadWithoutVideo(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 05 [1080p].rar",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].sfv",
		"Subs/[ExampleSubs] Placeholder Saga - 05 [1080p].en.srt",
	)

	if got := collected(t, root); len(got) != 0 {
		t.Errorf("collected %v, want nothing", got)
	}
}

// A token in the only video's name is a word in the title, not an extra --
// dropping it here would park the episode forever (#135).
func TestCollectsSoleVideoCarryingAnExtrasToken(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Preview Of A Placeholder - 05 [1080p].mkv",
		"[ExampleSubs] Preview Of A Placeholder - 05 [1080p].nfo",
		"Subs/[ExampleSubs] Preview Of A Placeholder - 05 [1080p].en.srt",
	)

	wantCollected(t, collected(t, root), []string{"[ExampleSubs] Preview Of A Placeholder - 05 [1080p].mkv"})
}

// The standard scene layout: the episode is inside the RAR set and the only
// video is a truncated sample, which the relaxation must not reach for.
func TestCollectsNothingFromAnArchivePayloadWithARootSample(t *testing.T) {
	root := writeTree(t,
		"placeholder.saga.s01e05.1080p.web.h264-example.rar",
		"placeholder.saga.s01e05.1080p.web.h264-example.r00",
		"placeholder.saga.s01e05.1080p.web.h264-example.sfv",
		"sample-placeholder.saga.s01e05.mkv",
	)

	if got := collected(t, root); len(got) != 0 {
		t.Errorf("collected %v, want nothing", got)
	}
}

// A sample is not a video at all, so the episode is still the sole one even when
// its own title carries an extras token.
func TestCollectsTokenedVideoBesideASample(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Preview Of A Placeholder - 05 [1080p].mkv",
		"sample-preview.of.a.placeholder.05.mkv",
	)

	wantCollected(t, collected(t, root), []string{"[ExampleSubs] Preview Of A Placeholder - 05 [1080p].mkv"})
}

// Filtering out extras must not leave the walk reaching for whatever is left.
func TestCollectsNothingWhenEveryFileIsAnExtra(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - NCOP [1080p].mkv",
		"[ExampleSubs] Placeholder Saga - NCED [1080p].mkv",
	)

	if got := collected(t, root); len(got) != 0 {
		t.Errorf("collected %v, want nothing", got)
	}
}

// A plain-file payload is taken as-is: identity by construction, whatever the
// extension filter would have said about it.
func TestCollectsAPlainFilePayload(t *testing.T) {
	root := writeTree(t, "raw.mkv")

	wantCollected(t, collected(t, filepath.Join(root, "raw.mkv")), []string{"raw.mkv"})
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
