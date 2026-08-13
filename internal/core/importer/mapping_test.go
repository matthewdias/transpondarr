package importer

import (
	"path/filepath"
	"testing"

	"github.com/matthewdias/transpondarr/internal/core/parser"
)

// files builds candidates from bare filenames, the form the mapper reasons about.
func files(names ...string) []candidate {
	out := make([]candidate, 0, len(names))
	for _, n := range names {
		out = append(out, candidate{path: filepath.Join("/payload", n), rel: n, parsed: parser.Parse(n)})
	}
	return out
}

func coverage(numbers ...int) map[int]bool {
	set := make(map[int]bool, len(numbers))
	for _, n := range numbers {
		set[n] = true
	}
	return set
}

// assignedNames flattens a result to item -> filename for readable assertions.
func assignedNames(res mapResult) map[int]string {
	out := make(map[int]string, len(res.assigned))
	for n, c := range res.assigned {
		out[n] = c.rel
	}
	return out
}

func leftoverNames(res mapResult) []string {
	out := make([]string, 0, len(res.leftovers))
	for _, lo := range res.leftovers {
		out = append(out, lo.file.rel)
	}
	return out
}

// The folder-wrapped single: one file, one item, and no readable number. We
// chose this release, so the sole video is the episode however it is named.
func TestMapsLoneFileToLoneItem(t *testing.T) {
	res := mapFiles(files("b1946ac92492d2347c6235b4d2611184.mkv"), coverage(5), nil)

	if got := assignedNames(res); len(got) != 1 || got[5] != "b1946ac92492d2347c6235b4d2611184.mkv" {
		t.Errorf("assigned = %v, want the sole file taken as item 5", got)
	}
	if len(res.leftovers) != 0 {
		t.Errorf("leftovers = %v, want none", leftoverNames(res))
	}
}

// The other half of #135's relaxation: a video the walk kept only because it was
// the sole one still has to reach the library, not sit as a leftover.
func TestMapsSoleVideoCarryingAnExtrasToken(t *testing.T) {
	name := "[ExampleSubs] Preview Of A Placeholder - 05 [1080p].mkv"
	res := mapFiles(files(name), coverage(5), nil)

	if got := assignedNames(res); len(got) != 1 || got[5] != name {
		t.Errorf("assigned = %v, want the sole video placed as item 5", got)
	}
}

// Solo identity is exactly that: two covered items means the name has to answer.
func TestDoesNotGuessALoneFileAcrossTwoItems(t *testing.T) {
	res := mapFiles(files("b1946ac92492d2347c6235b4d2611184.mkv"), coverage(4, 5), nil)

	if len(res.assigned) != 0 {
		t.Errorf("assigned = %v, want nothing guessed", assignedNames(res))
	}
	if len(res.leftovers) != 1 || res.leftovers[0].number != 0 {
		t.Errorf("leftovers = %+v, want the unnumbered file left over", res.leftovers)
	}
}

// The headline case: a season pack maps file-by-file onto the items it covers.
func TestMapsEachFileOfAPackToItsItem(t *testing.T) {
	res := mapFiles(files(
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 02 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 03 [1080p].mkv",
	), coverage(1, 2, 3), nil)

	got := assignedNames(res)
	if len(got) != 3 || got[1] == "" || got[2] == "" || got[3] == "" {
		t.Fatalf("assigned = %v, want one file per episode", got)
	}
	if got[2] != "[SynthSubs] Placeholder Saga - 02 [1080p].mkv" {
		t.Errorf("item 2 = %q, want its own file", got[2])
	}
	if len(res.leftovers) != 0 || len(res.conflicts) != 0 {
		t.Errorf("leftovers = %v, conflicts = %v, want a clean mapping", leftoverNames(res), res.conflicts)
	}
}

// An absolute-numbered file still lands when the entry's own numbering does not
// reach it -- the degrade-to-absolute rule the parser exists for.
func TestMapsByAbsoluteNumberWhenSeasonRelativeMisses(t *testing.T) {
	res := mapFiles(files("[SynthSubs] Placeholder Saga S3 - 01 (51) [1080p].mkv"), coverage(50, 51), nil)

	if got := assignedNames(res); got[51] == "" {
		t.Errorf("assigned = %v, want the absolute number to place it at 51", got)
	}
}

// Season-relative wins when both numbers hit a covered item, matching decide.
func TestPrefersSeasonRelativeOverAbsolute(t *testing.T) {
	res := mapFiles(files("[SynthSubs] Placeholder Saga S3 - 01 (51) [1080p].mkv"), coverage(1, 51), nil)

	if got := assignedNames(res); got[1] == "" || got[51] != "" {
		t.Errorf("assigned = %v, want item 1 (season-relative), not 51", got)
	}
}

// A v2 supersedes the v1 it re-releases; the loser is consumed, not reported as
// an unmatched file, or every pack with a fix in it would read as incomplete.
func TestVersionTwoBeatsVersionOne(t *testing.T) {
	res := mapFiles(files(
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 01v2 [1080p].mkv",
	), coverage(1), nil)

	if got := assignedNames(res); got[1] != "[SynthSubs] Placeholder Saga - 01v2 [1080p].mkv" {
		t.Errorf("assigned = %v, want the v2", got)
	}
	if len(res.leftovers) != 0 {
		t.Errorf("leftovers = %v, want the superseded v1 consumed", leftoverNames(res))
	}
}

// REPACK breaks a version tie, which is the whole point of the marker.
func TestRepackBreaksAVersionTie(t *testing.T) {
	res := mapFiles(files(
		"Placeholder.Saga.S01E01.1080p.WEB.H264-SYNTH.mkv",
		"Placeholder.Saga.S01E01.REPACK.1080p.WEB.H264-SYNTH.mkv",
	), coverage(1), nil)

	if got := assignedNames(res); got[1] != "Placeholder.Saga.S01E01.REPACK.1080p.WEB.H264-SYNTH.mkv" {
		t.Errorf("assigned = %v, want the repack", got)
	}
}

// Two indistinguishable claims on one episode: guessing here silently drops the
// other file, so the row defers and a human picks.
func TestIndistinguishableClaimsConflict(t *testing.T) {
	res := mapFiles(files(
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
		"[OtherGroup] Placeholder Saga - 01 [720p].mkv",
	), coverage(1), nil)

	if len(res.assigned) != 0 {
		t.Errorf("assigned = %v, want nothing guessed between two equal claims", assignedNames(res))
	}
	if res.conflicts[1] != 2 {
		t.Errorf("conflicts = %v, want both claims on episode 1 reported as ambiguous", res.conflicts)
	}
}

// A file for an episode this release never claimed is a leftover carrying its
// number, which is what lets the importer place it against the item if one exists.
func TestUncoveredFileIsALeftoverKeepingItsNumber(t *testing.T) {
	res := mapFiles(files(
		"[SynthSubs] Placeholder Saga - 03 [1080p].mkv",
		"[SynthSubs] Placeholder Saga - 04 [1080p].mkv",
	), coverage(3), nil)

	if got := assignedNames(res); len(got) != 1 || got[3] == "" {
		t.Fatalf("assigned = %v, want only the covered episode", got)
	}
	if len(res.leftovers) != 1 || res.leftovers[0].number != 4 {
		t.Errorf("leftovers = %+v, want episode 4 left over with its number", res.leftovers)
	}
}

// A range inside a payload names no single episode, so it claims nothing rather
// than claiming its first.
func TestRangeFileClaimsNothing(t *testing.T) {
	res := mapFiles(files("[SynthSubs] Placeholder Saga - 01-03 [1080p].mkv"), coverage(1, 2, 3), nil)

	if len(res.assigned) != 0 {
		t.Errorf("assigned = %v, want a range to claim nothing", assignedNames(res))
	}
	if len(res.leftovers) != 1 || res.leftovers[0].number != 0 {
		t.Errorf("leftovers = %+v, want the range left over unnumbered", res.leftovers)
	}
}

// An override is a human answering "this file is episode 5"; it overrules the
// filename, and the rules never get a say.
func TestOverrideAssignsUnconditionally(t *testing.T) {
	res := mapFiles(files(
		"b1946ac92492d2347c6235b4d2611184.mkv",
		"[SynthSubs] Placeholder Saga - 01 [1080p].mkv",
	), coverage(1, 5), map[string]int{"b1946ac92492d2347c6235b4d2611184.mkv": 5})

	got := assignedNames(res)
	if got[5] != "b1946ac92492d2347c6235b4d2611184.mkv" {
		t.Errorf("assigned = %v, want the override honoured", got)
	}
	if got[1] != "[SynthSubs] Placeholder Saga - 01 [1080p].mkv" {
		t.Errorf("assigned = %v, want the remaining file still mapped by name", got)
	}
}

// An override that contradicts a filename still wins: the point of the escape
// hatch is that the name is wrong.
func TestOverrideBeatsTheFilename(t *testing.T) {
	name := "[SynthSubs] Placeholder Saga - 01 [1080p].mkv"
	res := mapFiles(files(name), coverage(1, 7), map[string]int{name: 7})

	got := assignedNames(res)
	if got[7] != name || got[1] != "" {
		t.Errorf("assigned = %v, want the file at 7, not at 1", got)
	}
}
