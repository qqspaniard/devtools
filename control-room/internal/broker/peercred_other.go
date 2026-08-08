//go:build !darwin && !linux

package broker

import "net"

// checkPeerUID is a no-op on platforms where we have not implemented a
// peer-credential check. The private 0700 state directory and 0600 control
// secret remain the primary access controls. Peer-UID checks are implemented on
// darwin (LOCAL_PEERCRED) and linux (SO_PEERCRED); other platforms are a
// documented hardening follow-up (e.g. Windows named-pipe peer identity).
func checkPeerUID(net.Conn) error { return nil }
