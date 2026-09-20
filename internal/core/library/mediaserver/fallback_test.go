package mediaserver

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"testing"
)

// refuseLink makes every hardlink attempt fail with errno. The errno is the only
// simulated part — a real EXDEV needs a second filesystem and a real EPERM a second
// user — and isUnsupportedLink, copyFallback, copyFile and Place all run for real.
func refuseLink(target *Target, errno syscall.Errno) {
	target.link = func(oldname, newname string) error {
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: errno}
	}
}

// ownedByAnother makes every source file look like a download qBittorrent saved
// under its own user, which is what fs.protected_hardlinks refuses a link to. A
// test process can't create one, since only root can give a file away.
func ownedByAnother(target *Target) {
	target.identify = func(string) (fileOwner, linker, bool) {
		return fileOwner{uid: 1000, gid: 1000, mode: 0o644}, linker{euid: 1001, egid: 1001, boundByFileOwner: true}, true
	}
}

func logTo(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// lines splits a text-handler buffer into its records.
func lines(buf *bytes.Buffer) []string {
	var out []string
	for l := range strings.SplitSeq(buf.String(), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestAutoModeWarnsWhenAHardlinkFallsBackToACopy(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	var buf bytes.Buffer
	target := New(Roots{Series: root}, LayoutSeasonFolders, "auto", logTo(&buf))
	refuseLink(target, syscall.EPERM)

	dest, err := target.Place(t.Context(), req(src, "Placeholder Saga", 5))
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	got := lines(&buf)
	if len(got) != 1 {
		t.Fatalf("want exactly one log record, got %d: %q", len(got), buf.String())
	}
	if !strings.Contains(got[0], "level=WARN") {
		t.Errorf("fallback should be a warning the first time; got %q", got[0])
	}
	if !strings.Contains(got[0], "operation not permitted") {
		t.Errorf("fallback line should name the link error; got %q", got[0])
	}
	if !strings.Contains(got[0], dest) {
		t.Errorf("fallback line should name the destination; got %q", got[0])
	}
	// This target keeps the real linkIdentities, and the test user owns the source,
	// so the diagnosis must not fire.
	if strings.Contains(got[0], "likely=") {
		t.Errorf("a source we own is not an fs.protected_hardlinks refusal; got %q", got[0])
	}

	// The copy itself still has to happen, or the line is reporting a fiction.
	si, _ := os.Stat(src)
	di, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if os.SameFile(si, di) {
		t.Error("the fallback should have copied, not linked")
	}
	body, err := os.ReadFile(dest)
	if err != nil || string(body) != "video-bytes" {
		t.Errorf("dest contents = %q, %v; want the source's bytes", body, err)
	}
}

func TestUpgradeWarnsWhenAHardlinkFallsBackToACopy(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	target := New(Roots{Series: root}, LayoutSeasonFolders, "auto", logTo(&buf))

	// Occupy the destination first, so the upgrade takes replace's staged path.
	first := writeSource(t, "first.mkv")
	dest, err := target.Place(t.Context(), req(first, "Placeholder Saga", 5))
	if err != nil {
		t.Fatalf("seed Place: %v", err)
	}
	buf.Reset()

	better := writeSource(t, "better.mkv")
	r := req(better, "Placeholder Saga", 5)
	r.Replace = true
	refuseLink(target, syscall.EPERM)
	if _, err := target.Place(t.Context(), r); err != nil {
		t.Fatalf("upgrade Place: %v", err)
	}

	got := lines(&buf)
	if len(got) != 1 {
		t.Fatalf("want exactly one log record, got %d: %q", len(got), buf.String())
	}
	if !strings.Contains(got[0], "level=WARN") || !strings.Contains(got[0], "operation not permitted") {
		t.Errorf("an upgrade's fallback should warn and name the link error; got %q", got[0])
	}
	si, _ := os.Stat(better)
	di, _ := os.Stat(dest)
	if os.SameFile(si, di) {
		t.Error("the upgrade's fallback should have copied, not linked")
	}
}

func TestRepeatedFallbacksDropBelowWarn(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	target := New(Roots{Series: root}, LayoutSeasonFolders, "auto", logTo(&buf))
	refuseLink(target, syscall.EPERM)

	for _, n := range []int{5, 6} {
		if _, err := target.Place(t.Context(), req(writeSource(t, "raw.mkv"), "Placeholder Saga", n)); err != nil {
			t.Fatalf("Place %d: %v", n, err)
		}
	}

	got := lines(&buf)
	if len(got) != 2 {
		t.Fatalf("want one record per import, got %d: %q", len(got), buf.String())
	}
	if !strings.Contains(got[0], "level=WARN") {
		t.Errorf("first fallback should warn; got %q", got[0])
	}
	if !strings.Contains(got[1], "level=INFO") {
		t.Errorf("a repeat fallback should drop below a warning; got %q", got[1])
	}
	if !strings.Contains(got[1], "operation not permitted") {
		t.Errorf("a repeat fallback should still name the link error; got %q", got[1])
	}
}

// A failed copy takes no disk space, so reporting one would be false — and would
// spend the single warning on an attempt the importer is about to retry.
func TestAFailedCopyFallbackReportsNothing(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	var buf bytes.Buffer
	target := New(Roots{Series: root}, LayoutSeasonFolders, "auto", logTo(&buf))
	refuseLink(target, syscall.EPERM)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := target.Place(ctx, req(src, "Placeholder Saga", 5)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Place error = %v, want context.Canceled", err)
	}
	if got := lines(&buf); len(got) != 0 {
		t.Fatalf("a failed copy should report nothing, got %q", buf.String())
	}

	// The warning is still unspent, so the retry that succeeds gets it.
	if _, err := target.Place(t.Context(), req(src, "Placeholder Saga", 5)); err != nil {
		t.Fatalf("retry Place: %v", err)
	}
	got := lines(&buf)
	if len(got) != 1 || !strings.Contains(got[0], "level=WARN") {
		t.Errorf("the retry should warn; got %q", buf.String())
	}
}

func TestCrossDeviceFallbackDoesNotBlameHardlinkProtection(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	var buf bytes.Buffer
	target := New(Roots{Series: root}, LayoutSeasonFolders, "auto", logTo(&buf))
	refuseLink(target, syscall.EXDEV)
	// Owned by another user, so only the errno can suppress the diagnosis.
	ownedByAnother(target)

	if _, err := target.Place(t.Context(), req(src, "Placeholder Saga", 5)); err != nil {
		t.Fatalf("Place: %v", err)
	}

	got := lines(&buf)
	if len(got) != 1 {
		t.Fatalf("want exactly one log record, got %d: %q", len(got), buf.String())
	}
	if !strings.Contains(got[0], "cross-device link") {
		t.Errorf("a cross-device fallback should name its own error; got %q", got[0])
	}
	if strings.Contains(got[0], "likely=") {
		t.Errorf("only EPERM can be fs.protected_hardlinks; got %q", got[0])
	}
}

func TestEpermFallbackNamesHardlinkProtection(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	root := t.TempDir()
	var buf bytes.Buffer
	target := New(Roots{Series: root}, LayoutSeasonFolders, "auto", logTo(&buf))
	refuseLink(target, syscall.EPERM)
	ownedByAnother(target)

	if _, err := target.Place(t.Context(), req(src, "Placeholder Saga", 5)); err != nil {
		t.Fatalf("Place: %v", err)
	}

	got := lines(&buf)
	if len(got) != 1 {
		t.Fatalf("want exactly one log record, got %d: %q", len(got), buf.String())
	}
	for _, want := range []string{"fs.protected_hardlinks", "PUID", "source_owner=1000:1000", "process_owner=1001:1001"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("fallback line should contain %q; got %q", want, got[0])
		}
	}
}

func TestProtectedHardlinkAttrs(t *testing.T) {
	const (
		us    = 1001
		them  = 1000
		ourGp = 3001
	)
	self := linker{euid: us, egid: ourGp, groups: []int{ourGp, 44}, boundByFileOwner: true}

	for _, tc := range []struct {
		name string
		file fileOwner
		want bool
	}{
		{"another user's private download", fileOwner{uid: them, gid: them, mode: 0o644}, true},
		{"a file we own", fileOwner{uid: us, gid: them, mode: 0o644}, false},
		{"group-writable and we are in the group", fileOwner{uid: them, gid: ourGp, mode: 0o664}, false},
		{"group-writable but a group we are not in", fileOwner{uid: them, gid: them, mode: 0o664}, true},
		{"group-writable through a supplementary group", fileOwner{uid: them, gid: 44, mode: 0o664}, false},
		{"world-writable", fileOwner{uid: them, gid: them, mode: 0o666}, false},
		{"readable to us but not writable", fileOwner{uid: them, gid: ourGp, mode: 0o654}, true},
		{"not a regular file", fileOwner{uid: them, gid: them, mode: 0o644 | fs.ModeDevice}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := protectedHardlinkAttrs(tc.file, self) != nil
			if got != tc.want {
				t.Errorf("diagnosed as fs.protected_hardlinks = %v, want %v", got, tc.want)
			}
		})
	}

	attrs := protectedHardlinkAttrs(fileOwner{uid: them, gid: them, mode: 0o644}, self)
	rendered := renderAttrs(t, attrs)
	for key, want := range map[string]string{
		"source_owner":  "1000:1000",
		"source_mode":   "-rw-r--r--",
		"process_owner": "1001:3001",
	} {
		if rendered[key] != want {
			t.Errorf("%s = %q, want %q", key, rendered[key], want)
		}
	}
	if !strings.Contains(rendered["likely"], "fs.protected_hardlinks") {
		t.Errorf("likely should name the kernel setting; got %q", rendered["likely"])
	}
	if !strings.Contains(rendered["likely"], "PUID") {
		t.Errorf("likely should name the fix; got %q", rendered["likely"])
	}
}

// renderAttrs turns a slog key/value slice into a map, and fails the test if the
// slice is malformed.
func renderAttrs(t *testing.T, attrs []any) map[string]string {
	t.Helper()
	if len(attrs)%2 != 0 {
		t.Fatalf("attrs must pair up, got %d: %v", len(attrs), attrs)
	}
	out := make(map[string]string, len(attrs)/2)
	for i := 0; i < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		if !ok {
			t.Fatalf("attr key %d is not a string: %v", i, attrs[i])
		}
		out[key] = attrs[i+1].(string)
	}
	return out
}

// One target covers both library roots, so a Movies root on a second disk falls
// back on EXDEV forever. Keying the one-shot on the target would let that
// permanent, benign case spend the only warning the fixable EPERM had.
func TestEachKindOfRefusalWarnsOnce(t *testing.T) {
	var buf bytes.Buffer
	target := New(Roots{Series: t.TempDir(), Movies: t.TempDir()}, LayoutSeasonFolders, "auto", logTo(&buf))

	refuseLink(target, syscall.EXDEV)
	if _, err := target.Place(t.Context(), movieReq(writeSource(t, "film.mkv"), "Placeholder Film", 2021)); err != nil {
		t.Fatalf("movie Place: %v", err)
	}
	refuseLink(target, syscall.EPERM)
	if _, err := target.Place(t.Context(), req(writeSource(t, "raw.mkv"), "Placeholder Saga", 5)); err != nil {
		t.Fatalf("episode Place: %v", err)
	}

	got := lines(&buf)
	if len(got) != 2 {
		t.Fatalf("want one record per import, got %d: %q", len(got), buf.String())
	}
	for i, l := range got {
		if !strings.Contains(l, "level=WARN") {
			t.Errorf("record %d is the first of its kind and should warn; got %q", i, l)
		}
	}
}

func TestASecondRefusalOfTheSameKindDoesNotWarn(t *testing.T) {
	var buf bytes.Buffer
	target := New(Roots{Series: t.TempDir(), Movies: t.TempDir()}, LayoutSeasonFolders, "auto", logTo(&buf))
	refuseLink(target, syscall.EXDEV)

	for _, n := range []int{5, 6} {
		if _, err := target.Place(t.Context(), req(writeSource(t, "raw.mkv"), "Placeholder Saga", n)); err != nil {
			t.Fatalf("Place %d: %v", n, err)
		}
	}

	got := lines(&buf)
	if len(got) != 2 {
		t.Fatalf("want one record per import, got %d: %q", len(got), buf.String())
	}
	if !strings.Contains(got[1], "level=INFO") {
		t.Errorf("a repeat of the same refusal should not warn again; got %q", got[1])
	}
}

// Every CapEff value here was observed in Docker against a real hardlink, so the
// bits this reads are checked against traced behaviour rather than an assumed rule.
func TestCanBypassFileOwner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		capEff string
		want   bool
	}{
		{"the example compose's cap_add, which cannot hardlink", "\t00000000000000c5", false},
		{"cap_drop: ALL, which cannot hardlink", "\t0000000000000000", false},
		{"docker's default set for root, which can", "\t00000000a80425fb", true},
		// DAC_OVERRIDE alone restores the hardlink: it makes the source readable
		// and writable, which is the other arm of what may_linkat() accepts.
		{"DAC_OVERRIDE alone, which can", "\t0000000000000002", true},
		{"FOWNER alone, which satisfies the ownership check", "\t0000000000000008", true},
		{"unparseable, so we can't say", "\tnot-a-number", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := canBypassFileOwner(tc.capEff); got != tc.want {
				t.Errorf("canBypassFileOwner(%q) = %v, want %v", tc.capEff, got, tc.want)
			}
		})
	}
}

// Two EPERMs are not the same refusal when only one of them names a cause: the
// undiagnosed one must not spend the warning the actionable one needs.
func TestAnUndiagnosedRefusalDoesNotSpendTheDiagnosedOnesWarning(t *testing.T) {
	var buf bytes.Buffer
	target := New(Roots{Series: t.TempDir()}, LayoutSeasonFolders, "auto", logTo(&buf))
	refuseLink(target, syscall.EPERM)

	if _, err := target.Place(t.Context(), req(writeSource(t, "raw.mkv"), "Placeholder Saga", 5)); err != nil {
		t.Fatalf("undiagnosed Place: %v", err)
	}
	ownedByAnother(target)
	if _, err := target.Place(t.Context(), req(writeSource(t, "raw.mkv"), "Placeholder Saga", 6)); err != nil {
		t.Fatalf("diagnosed Place: %v", err)
	}

	got := lines(&buf)
	if len(got) != 2 {
		t.Fatalf("want one record per import, got %d: %q", len(got), buf.String())
	}
	if strings.Contains(got[0], "likely=") || !strings.Contains(got[1], "likely=") {
		t.Fatalf("want an undiagnosed record then a diagnosed one; got %q", buf.String())
	}
	if !strings.Contains(got[1], "level=WARN") {
		t.Errorf("the first diagnosed refusal should warn; got %q", got[1])
	}
}

// A process holding CAP_FOWNER is never refused by fs.protected_hardlinks, so an
// EPERM it sees is the mount and the diagnosis would name a cause that cannot apply.
func TestAPrivilegedProcessIsNotDiagnosed(t *testing.T) {
	root := fileOwner{uid: 1000, gid: 1000, mode: 0o644}
	capable := linker{euid: 0, egid: 0, groups: []int{0}}
	if attrs := protectedHardlinkAttrs(root, capable); attrs != nil {
		t.Errorf("a process that can override the owner check should not be diagnosed; got %v", attrs)
	}
	bound := linker{euid: 0, egid: 0, groups: []int{0}, boundByFileOwner: true}
	if attrs := protectedHardlinkAttrs(root, bound); attrs == nil {
		t.Error("root under cap_drop: ALL is bound by file ownership, and is the case #303 documents")
	}
}
