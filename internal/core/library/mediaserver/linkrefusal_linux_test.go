//go:build linux

package mediaserver

import (
	"os"
	"testing"
)

// The real read has to reach a real capability set, or the split above is wired to
// nothing. An ordinary test process holds no capability that bypasses the check, and
// that is also the shape privdrop leaves the server in.
func TestBoundByFileOwnerReadsThisProcess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root in this container may hold CAP_FOWNER or CAP_DAC_OVERRIDE")
	}
	if !boundByFileOwner() {
		t.Error("an unprivileged process is checked against file ownership")
	}
}
