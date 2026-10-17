//go:build !linux

package plugins

import "net"

// PeerUnit identifies nothing off Linux (dev builds have no plugins).
func PeerUnit(net.Conn) (string, bool) { return "", false }
