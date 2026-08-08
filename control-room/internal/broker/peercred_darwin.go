//go:build darwin

package broker

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// checkPeerUID verifies that the connected Unix-socket peer runs as the same OS
// user as the broker. On macOS this uses LOCAL_PEERCRED, which returns the
// peer's effective uid at connect time.
//
// This is defense in depth. The RFC's threat model already relies on the
// private 0700 state directory and the 0600 control secret to keep other users
// out; a same-uid peer check adds a second, transport-level barrier so a
// connection from a different uid is refused before the secret is even examined.
// It does NOT defend against a fully compromised same-user process (explicitly
// out of scope per the RFC).
func checkPeerUID(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		// Not a Unix socket (e.g. a test pipe); skip the check.
		return nil
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return fmt.Errorf("broker: obtaining raw conn for peer check: %w", err)
	}
	var (
		peerUID uint32
		sysErr  error
	)
	if ctrlErr := raw.Control(func(fd uintptr) {
		xucred, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if e != nil {
			sysErr = e
			return
		}
		peerUID = xucred.Uid
	}); ctrlErr != nil {
		return fmt.Errorf("broker: peer credential control: %w", ctrlErr)
	}
	if sysErr != nil {
		return fmt.Errorf("broker: reading peer credentials: %w", sysErr)
	}
	self := uint32(os.Getuid())
	if peerUID != self {
		return fmt.Errorf("broker: peer uid %d does not match broker uid %d", peerUID, self)
	}
	return nil
}
