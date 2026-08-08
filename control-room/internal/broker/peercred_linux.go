//go:build linux

package broker

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// checkPeerUID verifies that the connected Unix-socket peer runs as the same OS
// user as the broker. On Linux this uses SO_PEERCRED, which returns the peer's
// credentials (pid/uid/gid) as they were at connect time.
//
// This is defense in depth, matching the darwin implementation: a connection
// from a different uid is refused before the control secret is examined. The
// private 0700 state directory and 0600 secret remain the primary controls; per
// the RFC threat model this does not defend against a compromised same-user
// process.
func checkPeerUID(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
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
		ucred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if e != nil {
			sysErr = e
			return
		}
		peerUID = ucred.Uid
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
