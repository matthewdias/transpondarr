package mediaserver

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

// protectedHardlinksFix is conditional because a mount with no hardlinks at all
// returns the same EPERM on the same file, and changing PUID would not help there.
const protectedHardlinksFix = "this user neither owns the download nor can both read and write it, which is what fs.protected_hardlinks refuses. " +
	"If the mount supports hardlinks: set PUID/PGID to qBittorrent's user, or share its group and set qBittorrent's UMASK to 002. " +
	"PGID sets the group unless PUID is 0, which skips the drop that applies it; set it on the container instead."

// fileOwner is the part of a file's identity that fs.protected_hardlinks checks.
type fileOwner struct {
	uid, gid int
	mode     fs.FileMode
}

// linker is the identity of the process that attempts the link.
type linker struct {
	euid, egid int
	groups     []int

	// boundByFileOwner is whether the kernel checks this process's ownership like
	// anyone else's: false with either capability below, and false off Linux.
	boundByFileOwner bool
}

// hardlinkRefusalAttrs are the log fields that explain a refused hardlink. Empty
// unless the errno is EPERM and the file's owner can be read.
func (t *Target) hardlinkRefusalAttrs(src string, linkErr error) []any {
	if !errors.Is(linkErr, syscall.EPERM) {
		return nil
	}
	f, l, ok := t.identify(src)
	if !ok {
		return nil
	}
	return protectedHardlinkAttrs(f, l)
}

// protectedHardlinkAttrs reports the conditions fs.protected_hardlinks refuses on,
// which are necessary for that refusal and not sufficient: a mount with no hardlinks
// returns the same EPERM on the same file, so neither result is proof (#303).
func protectedHardlinkAttrs(f fileOwner, l linker) []any {
	if !l.boundByFileOwner || !f.mode.IsRegular() || f.uid == l.euid || canReadWrite(f, l) {
		return nil
	}
	return []any{
		"source_owner", fmt.Sprintf("%d:%d", f.uid, f.gid),
		"source_mode", f.mode.String(),
		"process_owner", fmt.Sprintf("%d:%d", l.euid, l.egid),
		"likely", protectedHardlinksFix,
	}
}

// canReadWrite is the ordinary Unix check for a caller that doesn't own the file and
// has no capabilities: the group bits if it's in the group, else the other bits.
func canReadWrite(f fileOwner, l linker) bool {
	const rw = 0o6
	if inGroup(f.gid, l) {
		return f.mode.Perm()>>3&rw == rw
	}
	return f.mode.Perm()&rw == rw
}

func inGroup(gid int, l linker) bool {
	return gid == l.egid || slices.Contains(l.groups, gid)
}

// Either bit satisfies may_linkat(): CAP_FOWNER passes its ownership check, and
// CAP_DAC_OVERRIDE satisfies the read-and-write condition it accepts instead.
const (
	capDACOverride = 1
	capFowner      = 3
)

// boundByFileOwnerFrom reports whether a process whose /proc/self/status is what read
// returns is checked against file ownership like anyone else. Every unreadable case
// reports false, for canBypassFileOwner's reason.
func boundByFileOwnerFrom(read func() ([]byte, error)) bool {
	status, err := read()
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		if capEff, ok := strings.CutPrefix(line, "CapEff:"); ok {
			return !canBypassFileOwner(capEff)
		}
	}
	return false
}

// canBypassFileOwner reports whether a CapEff value lets the process past the
// ownership check. Unparseable reports true: silence beats an unfounded cause.
func canBypassFileOwner(capEff string) bool {
	set, err := strconv.ParseUint(strings.TrimSpace(capEff), 16, 64)
	if err != nil {
		return true
	}
	return set&(1<<capFowner|1<<capDACOverride) != 0
}
