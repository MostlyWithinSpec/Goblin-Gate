//go:build linux

package plugins

import (
	"fmt"
	"net"
	"os"
	"syscall"
)

// PeerUnit identifies the plugin on the other end of a unix socket
// connection: the peer's process (SO_PEERCRED) must sit in the cgroup of a
// goblin-plugin@<name>.service unit. Only systemd (root) places processes in
// a unit's cgroup, so a plugin cannot pass itself off as another one, and no
// secret has to be handed to it.
func PeerUnit(c net.Conn) (string, bool) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return "", false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return "", false
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || credErr != nil || cred == nil || cred.Pid <= 0 {
		return "", false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", cred.Pid))
	if err != nil {
		return "", false
	}
	return unitFromCgroup(string(data))
}
