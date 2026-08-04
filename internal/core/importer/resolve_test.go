package importer

import (
	"fmt"
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
	writeTreeInto(t, root, paths...)
	return root
}

// writeTreeInto adds to a payload already on disk, the way extracting an archive
// into the download folder does.
func writeTreeInto(t *testing.T, root string, paths ...string) {
	t.Helper()
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
}

// collected returns the payload-relative paths the walk kept, sorted.
func collected(t *testing.T, root string) []string {
	t.Helper()
	p, err := collectPayloadFiles(root)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	out := make([]string, 0, len(p.files))
	for _, c := range p.files {
		out = append(out, c.rel)
	}
	sort.Strings(out)
	return out
}

// archivesOf returns the archive sets the walk reported, as "rel×parts" in the
// order the walk found them.
func archivesOf(t *testing.T, root string) []string {
	t.Helper()
	p, err := collectPayloadFiles(root)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(p.files) != 0 {
		t.Fatalf("collected %+v, want no candidate from an archive payload", p.files)
	}
	out := make([]string, 0, len(p.archives))
	for _, a := range p.archives {
		out = append(out, fmt.Sprintf("%s×%d", a.rel, a.parts))
	}
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

// No video at all: nothing to place, and the RAR set is reported rather than
// opened -- see the archive tests below.
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

// The standard scene layout: the episode is inside the RAR set, which nothing
// here opens, and the only video is a truncated sample the relaxation must not
// reach for.
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

// The whole set is one thing to extract, so a 3-volume release is one archive
// and not three -- the deferral names it, and the dialog lists it.
func TestReportsAMultipartRarSetAsOneArchive(t *testing.T) {
	root := writeTree(t,
		"placeholder.saga.s01e05.1080p.web.h264-example.rar",
		"placeholder.saga.s01e05.1080p.web.h264-example.r00",
		"placeholder.saga.s01e05.1080p.web.h264-example.r01",
		"placeholder.saga.s01e05.1080p.web.h264-example.sfv",
		"sample-placeholder.saga.s01e05.mkv",
	)

	wantCollected(t, archivesOf(t, root),
		[]string{"placeholder.saga.s01e05.1080p.web.h264-example.rar×3"})
}

// The other multipart scheme: the head is partNN with NN == 1, not the shortest name.
func TestReportsPartVolumesAsOneArchive(t *testing.T) {
	root := writeTree(t,
		"placeholder.saga.s01e05.1080p.web.h264-example.part01.rar",
		"placeholder.saga.s01e05.1080p.web.h264-example.part02.rar",
		"placeholder.saga.s01e05.1080p.web.h264-example.part03.rar",
	)

	wantCollected(t, archivesOf(t, root),
		[]string{"placeholder.saga.s01e05.1080p.web.h264-example.part01.rar×3"})
}

// Two single-volume archives are two things to extract; merging them would
// understate the work and name only one of them.
func TestReportsTwoArchiveSetsSeparately(t *testing.T) {
	root := writeTree(t,
		"[ExampleSubs] Placeholder Saga - 04 [1080p].rar",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].rar",
	)

	wantCollected(t, archivesOf(t, root), []string{
		"[ExampleSubs] Placeholder Saga - 04 [1080p].rar×1",
		"[ExampleSubs] Placeholder Saga - 05 [1080p].rar×1",
	})
}

// Sets are keyed on the folder as well as the stem, or two discs sharing a
// naming scheme merge into one set with twice the parts.
func TestReportsSameNamedArchivesInTwoFoldersSeparately(t *testing.T) {
	root := writeTree(t, "Disc1/payload.rar", "Disc2/payload.rar")

	wantCollected(t, archivesOf(t, root), []string{"Disc1/payload.rar×1", "Disc2/payload.rar×1"})
}

func TestReportsZipAndSevenZipArchives(t *testing.T) {
	split := writeTree(t, "payload.zip", "payload.z01")
	wantCollected(t, archivesOf(t, split), []string{"payload.zip×2"})

	seven := writeTree(t, "payload.7z")
	wantCollected(t, archivesOf(t, seven), []string{"payload.7z×1"})
}

// Sidecars are not something a human extracts, so "nothing at all" stays
// distinguishable from "an archive we decline to open".
func TestReportsNoArchiveForSidecarsAlone(t *testing.T) {
	root := writeTree(t,
		"placeholder.saga.s01e05.1080p.web.h264-example.nfo",
		"placeholder.saga.s01e05.1080p.web.h264-example.sfv",
		"Subs/placeholder.saga.s01e05.en.srt",
		"RARBG.txt",
	)

	if got := archivesOf(t, root); len(got) != 0 {
		t.Errorf("archives = %v, want none", got)
	}
}

// Identity by construction stops at an archive: hardlinking a .rar into the
// library as the episode is worse than deferring it.
func TestPlainArchiveFilePayloadIsNotACandidate(t *testing.T) {
	root := writeTree(t, "placeholder.saga.s01e05.1080p.web.h264-example.rar")

	wantCollected(t, archivesOf(t, filepath.Join(root, "placeholder.saga.s01e05.1080p.web.h264-example.rar")),
		[]string{"placeholder.saga.s01e05.1080p.web.h264-example.rar×1"})
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
