// Package wireguard manages WireGuard keys and client configs (task 036).
// Server private keys live only in config.WireGuardKeyDir; client private
// keys are generated for a new peer, shown once and never stored.
package wireguard

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/curve25519"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// Key is a WireGuard (X25519) key.
type Key [32]byte

// GenerateKey returns a new clamped private key.
func GenerateKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return k, err
	}
	k[0] &= 248
	k[31] = k[31]&127 | 64
	return k, nil
}

// Public derives the public key.
func (k Key) Public() Key {
	var p Key
	out, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		panic(err) // only for low-order points, impossible for a clamped key
	}
	copy(p[:], out)
	return p
}

// String is the base64 form wg and config files use.
func (k Key) String() string { return base64.StdEncoding.EncodeToString(k[:]) }

// ParseKey reads a base64 key.
func ParseKey(s string) (Key, error) {
	var k Key
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != 32 {
		return k, errors.New("wireguard: not a base64 32-byte key")
	}
	copy(k[:], b)
	return k, nil
}

// KeyPath is an interface's private key file.
func KeyPath(dir, name string) string { return filepath.Join(dir, name+".key") }

// EnsureKey returns the interface's public key, creating its private key
// file (mode 0640, group systemd-network where it exists, so networkd can
// read it via PrivateKeyFile) on first use.
func EnsureKey(dir, name string) (Key, error) {
	path := KeyPath(dir, name)
	if b, err := os.ReadFile(path); err == nil {
		k, err := ParseKey(string(b))
		if err != nil {
			return Key{}, fmt.Errorf("wireguard: %s: %w", path, err)
		}
		return k.Public(), nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Key{}, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Key{}, err
	}
	os.Chmod(dir, 0o750)
	shareWithNetworkd(dir)
	k, err := GenerateKey()
	if err != nil {
		return Key{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(k.String()+"\n"), 0o640); err != nil {
		return Key{}, err
	}
	shareWithNetworkd(tmp)
	if err := os.Rename(tmp, path); err != nil {
		return Key{}, err
	}
	return k.Public(), nil
}

// EnsureKeys creates the keys of every WireGuard interface in cfg (the
// commit engine's BeforeApply: networkd needs them when it loads the
// .netdev files).
func EnsureKeys(dir string, cfg *config.Config) error {
	for _, w := range cfg.VPN.WireGuard {
		if _, err := EnsureKey(dir, w.Name); err != nil {
			return fmt.Errorf("wireguard %s: %w", w.Name, err)
		}
	}
	return nil
}

// NextAddress returns the first free host address in the interface's
// subnet (skipping the box's own and the peers').
func NextAddress(w config.WireGuard) (netip.Addr, error) {
	used := map[netip.Addr]bool{w.Address.Addr(): true}
	for _, p := range w.Peers {
		used[p.Address] = true
	}
	pfx := w.Address.Masked()
	a := pfx.Addr().Next() // skip the network address
	for pfx.Contains(a) {
		next := a.Next()
		if !pfx.Contains(next) { // a is the broadcast address
			break
		}
		if !used[a] {
			return a, nil
		}
		a = next
	}
	return netip.Addr{}, fmt.Errorf("wireguard %s: no free address in %s", w.Name, pfx)
}

// ClientOptions shapes a client config.
type ClientOptions struct {
	FullTunnel bool // route everything through the box (needs a policy to wan with nat)
}

// ClientConfig is the wg-quick config for a new peer. Split tunnel routes
// the tunnel subnet and the box's internal subnets; DNS is the box.
func ClientConfig(cfg *config.Config, w config.WireGuard, peer config.WireGuardPeer, clientKey, serverPub Key, opt ClientOptions) string {
	port := w.ListenPort
	if port == 0 {
		port = config.DefaultWireGuardPort
	}
	endpoint := w.Endpoint
	if endpoint == "" {
		endpoint = "YOUR-PUBLIC-ADDRESS"
	}
	allowed := []string{"0.0.0.0/0"}
	if !opt.FullTunnel {
		allowed = []string{w.Address.Masked().String()}
		for _, p := range render.InternalPrefixes(cfg) {
			allowed = append(allowed, p.String())
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Goblin Gate WireGuard: %s on %s\n", peer.Name, w.Name)
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nAddress = %s/32\nDNS = %s\n\n", clientKey, peer.Address, w.Address.Addr())
	fmt.Fprintf(&b, "[Peer]\nPublicKey = %s\nEndpoint = %s:%d\nAllowedIPs = %s\nPersistentKeepalive = 25\n",
		serverPub, endpoint, port, strings.Join(allowed, ", "))
	return b.String()
}

// PeerStat is a peer's live state from `wg show <dev> dump`.
type PeerStat struct {
	Endpoint        string `json:"endpoint,omitempty"`
	LatestHandshake int64  `json:"latest_handshake,omitempty"` // unix seconds, 0 = never
	RxBytes         int64  `json:"rx_bytes"`
	TxBytes         int64  `json:"tx_bytes"`
}

// ParseDump reads `wg show <dev> dump` output into stats by public key.
func ParseDump(out string) map[string]PeerStat {
	stats := map[string]PeerStat{}
	for i, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if i == 0 || len(f) < 8 { // the first line is the interface itself
			continue
		}
		var st PeerStat
		if f[2] != "(none)" {
			st.Endpoint = f[2]
		}
		fmt.Sscan(f[4], &st.LatestHandshake)
		fmt.Sscan(f[5], &st.RxBytes)
		fmt.Sscan(f[6], &st.TxBytes)
		stats[f[0]] = st
	}
	return stats
}
