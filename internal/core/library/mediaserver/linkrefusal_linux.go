//go:build linux

package mediaserver

import (
	"os"
	"strings"
)

// boundByFileOwner reports whether the kernel checks this process's ownership like
// anyone else's. An unreadable capability set reports false, for canBypassFileOwner's reason.
func boundByFileOwner() bool {
	status, err := os.ReadFile("/proc/self/status")
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
