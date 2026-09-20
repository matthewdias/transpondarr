//go:build unix

package mediaserver

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLinkIdentitiesReadsThisProcessAndTheFile(t *testing.T) {
	src := writeSource(t, "raw.mkv")

	wantGID := regroup(t, src)

	owner, self, ok := linkIdentities(src)
	if !ok {
		t.Fatal("linkIdentities should read a file this process just wrote")
	}
	if owner.uid != os.Geteuid() || owner.gid != wantGID {
		t.Errorf("source owner = %d:%d, want %d:%d", owner.uid, owner.gid, os.Geteuid(), wantGID)
	}
	// Swapping this pair is an equivalent mutant on an account whose uid and gid
	// match, which a CI runner's often does and a developer's rarely does.
	if self.euid != os.Geteuid() || self.egid != os.Getegid() {
		t.Errorf("process identity = %d:%d, want %d:%d", self.euid, self.egid, os.Geteuid(), os.Getegid())
	}
	if !owner.mode.IsRegular() {
		t.Errorf("source mode = %v, want a regular file", owner.mode)
	}
}

// regroup gives path a group this process belongs to but does not run as, so that
// reading the file's gid where its uid belongs is a difference a test can see even
// on an account whose uid and gid match. Returns the gid the file ends up with.
func regroup(t *testing.T, path string) int {
	t.Helper()
	groups, err := os.Getgroups()
	if err != nil {
		return os.Getegid()
	}
	for _, g := range groups {
		if g != os.Getegid() && os.Chown(path, -1, g) == nil {
			return g
		}
	}
	return os.Getegid()
}

func TestLinkIdentitiesReportsAMissingFile(t *testing.T) {
	if _, _, ok := linkIdentities(filepath.Join(t.TempDir(), "gone.mkv")); ok {
		t.Error("linkIdentities should report a path it cannot stat")
	}
}

// New has to wire identify to the real reader, or every diagnosis the log carries
// is decided by a stub no test would notice.
func TestNewWiresTheRealIdentity(t *testing.T) {
	src := writeSource(t, "raw.mkv")
	target := New(Roots{Series: t.TempDir()}, LayoutSeasonFolders, "auto", logTo(&bytes.Buffer{}))

	gotOwner, gotSelf, gotOK := target.identify(src)
	wantOwner, wantSelf, wantOK := linkIdentities(src)
	if gotOK != wantOK || gotOwner != wantOwner {
		t.Errorf("target.identify = %+v, %v; linkIdentities = %+v, %v", gotOwner, gotOK, wantOwner, wantOK)
	}
	// Only fields a stub would get wrong are compared: boundByFileOwner would be
	// itself on both sides, so a mutation moves both and the assertion says nothing.
	if gotSelf.euid != wantSelf.euid || gotSelf.egid != wantSelf.egid {
		t.Errorf("target.identify process identity = %+v, want %+v", gotSelf, wantSelf)
	}
}
