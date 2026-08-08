//go:build !unix

package statedir

import "os"

// verifyOwnership is a no-op on non-Unix platforms, where the uid-based check
// does not apply. Windows support (a named pipe + an equivalent private
// application-data directory with an ACL check) is a documented follow-up.
func verifyOwnership(string, os.FileInfo) error { return nil }
