//go:build unix && !linux

package mediaserver

import "testing"

// Off Linux there is no fs.protected_hardlinks, so the diagnosis must never fire —
// reporting true here would name a Linux sysctl on macOS and the BSDs.
func TestBoundByFileOwnerIsFalseOffLinux(t *testing.T) {
	if boundByFileOwner() {
		t.Error("boundByFileOwner should be false where fs.protected_hardlinks does not exist")
	}
}
