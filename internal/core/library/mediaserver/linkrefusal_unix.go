//go:build unix

package mediaserver

import (
	"os"
	"syscall"
)

// linkIdentities reads the owner of src and the identity of this process. Unix only,
// since syscall.Stat_t has no Uid field on Windows — a target .goreleaser.yaml builds.
func linkIdentities(src string) (fileOwner, linker, bool) {
	info, err := os.Stat(src)
	if err != nil {
		return fileOwner{}, linker{}, false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileOwner{}, linker{}, false
	}
	groups, err := os.Getgroups()
	if err != nil {
		groups = nil
	}
	owner := fileOwner{uid: int(st.Uid), gid: int(st.Gid), mode: info.Mode()}
	self := linker{euid: os.Geteuid(), egid: os.Getegid(), groups: groups, boundByFileOwner: boundByFileOwner()}
	return owner, self, true
}
