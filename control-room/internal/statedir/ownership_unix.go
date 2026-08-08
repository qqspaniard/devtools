//go:build unix

package statedir

import (
	"fmt"
	"os"
	"syscall"
)

// verifyOwnership fails closed if an existing state directory is not owned by
// the current user. On Unix the directory's owning uid must equal the process
// euid; otherwise we refuse to adopt a directory that belongs to someone else.
//
// This is defense in depth, not a hard boundary: it prevents adopting a
// directory owned by another user, but does not defend against a malicious
// process already running as the current user (RFC threat model).
func verifyOwnership(dir string, info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// No stat data available; do not block on a platform we cannot inspect.
		return nil
	}
	self := uint32(os.Geteuid())
	if st.Uid != self {
		return fmt.Errorf("statedir: %q is owned by uid %d, not the current user (uid %d); refusing to adopt it", dir, st.Uid, self)
	}
	return nil
}
