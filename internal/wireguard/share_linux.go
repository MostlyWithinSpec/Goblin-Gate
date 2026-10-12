//go:build linux

package wireguard

import (
	"os"
	"os/user"
	"strconv"
)

// shareWithNetworkd gives group systemd-network read access (networkd
// reads PrivateKeyFile as that user); a missing group is ignored.
func shareWithNetworkd(path string) {
	g, err := user.LookupGroup("systemd-network")
	if err != nil {
		return
	}
	if gid, err := strconv.Atoi(g.Gid); err == nil {
		os.Chown(path, 0, gid)
	}
}
