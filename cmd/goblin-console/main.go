// Command goblin-console is the menu on the box's console (tty1), like
// pfSense's: it shows where the web UI is and lets someone at the keyboard
// assign the WAN/LAN ports, reset the admin password, open a shell or
// reboot. It talks to goblind over the root-only API socket.
package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

const socket = "/run/goblind/api.sock"

// logo is shown on the console menu and, via /usr/share/goblin-gate/logo.txt
// and update-motd, when someone logs in to a shell.
//
//go:embed logo.txt
var logo string

type console struct {
	in   *bufio.Reader
	out  io.Writer
	http *http.Client
	// run executes a command attached to the terminal (shell, reboot).
	run func(name string, args ...string) error
}

func main() {
	c := &console{
		in:  bufio.NewReader(os.Stdin),
		out: os.Stdout,
		http: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
		}},
		run: func(name string, args ...string) error {
			cmd := exec.Command(name, args...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			return cmd.Run()
		},
	}
	for {
		if err := c.loop(); err != nil {
			fmt.Fprintf(c.out, "\nerror: %v\n", err)
			c.pause()
		}
	}
}

// api calls goblind; out may be nil.
func (c *console) api(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://goblind"+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("goblind is not answering (%v)", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Details any    `json:"details"`
			} `json:"error"`
		}
		json.Unmarshal(data, &e)
		if e.Error.Details != nil {
			d, _ := json.Marshal(e.Error.Details)
			return fmt.Errorf("%s: %s", e.Error.Message, d)
		}
		return errors.New(e.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *console) ask(prompt string) string {
	fmt.Fprint(c.out, prompt)
	line, _ := c.in.ReadString('\n')
	return strings.TrimSpace(line)
}

func (c *console) pause() { c.ask("\nPress Enter to continue...") }

type iface struct {
	Name    string `json:"name"`
	Device  string `json:"device"`
	Role    string `json:"role"`
	Mode    string `json:"mode"`
	Address string `json:"address"`
	Enabled bool   `json:"enabled"`
}

type runningConfig struct {
	System struct {
		Hostname  string `json:"hostname"`
		AdminPort int    `json:"admin_port"`
	} `json:"system"`
	Interfaces []iface `json:"interfaces"`
}

func (c *console) loop() error {
	fmt.Fprint(c.out, "\033[H\033[2J")
	fmt.Fprint(c.out, "\033[32m"+logo+"\033[0m")
	var cfg runningConfig
	if err := c.api("GET", "/api/v1/config/running", nil, &cfg); err != nil {
		fmt.Fprintf(c.out, "\n%v\n", err)
	} else {
		c.status(cfg)
	}
	var setup struct {
		Needed bool `json:"needed"`
	}
	if c.api("GET", "/api/v1/setup/state", nil, &setup) == nil && setup.Needed {
		fmt.Fprintln(c.out, "\nFirst start: open the web address above from a computer on the LAN port to run setup.")
	}
	fmt.Fprintln(c.out, "\n 1) Assign interfaces (WAN/LAN ports)")
	fmt.Fprintln(c.out, " 2) Set the LAN IP address")
	fmt.Fprintln(c.out, " 3) Reset the admin password")
	fmt.Fprintln(c.out, " 4) Shell")
	fmt.Fprintln(c.out, " 5) Reboot")
	fmt.Fprintln(c.out, " 6) Power off")
	fmt.Fprintln(c.out, " 0) Refresh")
	switch c.ask("\nChoose: ") {
	case "1":
		return c.assign(cfg)
	case "2":
		return c.setLAN(cfg, setup.Needed)
	case "3":
		return c.resetPassword()
	case "4":
		fmt.Fprintln(c.out, "Type exit to return to the menu.")
		return c.run("/bin/bash", "-l")
	case "5":
		if strings.EqualFold(c.ask("Reboot now? [y/N] "), "y") {
			return c.run("systemctl", "reboot")
		}
	case "6":
		if strings.EqualFold(c.ask("Power off now? [y/N] "), "y") {
			return c.run("systemctl", "poweroff")
		}
	}
	return nil
}

func (c *console) status(cfg runningConfig) {
	port := cfg.System.AdminPort
	if port == 0 {
		port = 443
	}
	fmt.Fprintf(c.out, "Hostname: %s\n\n", cfg.System.Hostname)
	devs := deviceAddrs()
	for _, i := range cfg.Interfaces {
		if !i.Enabled {
			continue
		}
		addr := devs[i.Device]
		if addr == "" {
			addr = "(no address)"
		}
		state := "no link"
		if up(i.Device) {
			state = "up"
		}
		if _, err := os.Stat("/sys/class/net/" + i.Device); err != nil {
			state, addr = "MISSING - choose 1 to assign the ports", "-"
		}
		fmt.Fprintf(c.out, "  %-6s %-4s -> %-10s %-18s %s\n", i.Name, strings.ToUpper(i.Role), i.Device, addr, state)
		if i.Role != "wan" && i.Address != "" {
			ip := strings.Split(i.Address, "/")[0]
			if port == 443 {
				fmt.Fprintf(c.out, "         web UI: https://%s/\n", ip)
			} else {
				fmt.Fprintf(c.out, "         web UI: https://%s:%d/\n", ip, port)
			}
		}
	}
}

// deviceAddrs maps device names to their first IPv4 address.
func deviceAddrs() map[string]string {
	out := map[string]string{}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil {
				out[i.Name] = n.String()
				break
			}
		}
	}
	return out
}

func up(dev string) bool {
	b, err := os.ReadFile("/sys/class/net/" + dev + "/operstate")
	return err == nil && strings.TrimSpace(string(b)) == "up"
}

// physicalDevices lists real network ports (not loopback, VLANs, tunnels).
func physicalDevices() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 || len(i.HardwareAddr) == 0 || strings.Contains(i.Name, ".") {
			continue
		}
		if _, err := os.Stat("/sys/class/net/" + i.Name + "/device"); err != nil {
			continue // virtual (bridge, wireguard, tailscale)
		}
		out = append(out, i.Name)
	}
	sort.Strings(out)
	return out
}

func (c *console) pick(what string, devs []string, exclude string) (string, error) {
	fmt.Fprintf(c.out, "\n%s port:\n", what)
	var choices []string
	for _, d := range devs {
		if d == exclude {
			continue
		}
		choices = append(choices, d)
		hw := ""
		if i, err := net.InterfaceByName(d); err == nil {
			hw = i.HardwareAddr.String()
		}
		link := "no link"
		if up(d) {
			link = "link up"
		}
		fmt.Fprintf(c.out, "  %d) %-10s %s  %s\n", len(choices), d, hw, link)
	}
	n, err := strconv.Atoi(c.ask("Number: "))
	if err != nil || n < 1 || n > len(choices) {
		return "", errors.New("no such port")
	}
	return choices[n-1], nil
}

func (c *console) assign(cfg runningConfig) error {
	devs := physicalDevices()
	if len(devs) < 2 {
		return fmt.Errorf("found %d network ports; Goblin Gate needs two (WAN and LAN)", len(devs))
	}
	fmt.Fprintln(c.out, "\nTip: unplug and replug a cable and choose 0 on the menu to see which port shows link up.")
	wan, err := c.pick("WAN (internet / modem)", devs, "")
	if err != nil {
		return err
	}
	lan, err := c.pick("LAN (your network)", devs, wan)
	if err != nil {
		return err
	}
	if !strings.EqualFold(c.ask(fmt.Sprintf("\nWAN = %s, LAN = %s. Apply now? [y/N] ", wan, lan)), "y") {
		return nil
	}
	for _, p := range []struct{ name, dev string }{{"wan1", wan}, {"lan", lan}} {
		found := false
		for _, i := range cfg.Interfaces {
			found = found || i.Name == p.name
		}
		if !found {
			return fmt.Errorf("the config has no interface %q; use the web UI", p.name)
		}
		if err := c.api("PATCH", "/api/v1/config/candidate", map[string]any{"path": "interfaces[name=" + p.name + "].device", "value": p.dev}, nil); err != nil {
			return err
		}
	}
	if err := c.api("POST", "/api/v1/commit", map[string]any{"comment": "console: assign interfaces"}, nil); err != nil {
		c.api("POST", "/api/v1/commit/discard", nil, nil)
		return err
	}
	fmt.Fprintln(c.out, "Applied.")
	c.pause()
	return nil
}

func (c *console) resetPassword() error {
	user := c.ask("User [admin]: ")
	if user == "" {
		user = "admin"
	}
	fmt.Fprint(c.out, "New password (at least 10 characters): ")
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(c.out)
	if err != nil {
		return err
	}
	fmt.Fprint(c.out, "Repeat: ")
	again, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(c.out)
	if err != nil {
		return err
	}
	if string(pw) != string(again) {
		return errors.New("the passwords do not match")
	}
	if err := c.api("POST", "/api/v1/auth/password", map[string]any{"user": user, "new_password": string(pw)}, nil); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "Password for %s changed.\n", user)
	c.pause()
	return nil
}

// suggestRange picks a DHCP range in the subnet that leaves the box's own
// address out: .100-.199 in a /24 or larger, else the upper half.
func suggestRange(pfx netip.Prefix) (netip.Addr, netip.Addr) {
	toU := func(a netip.Addr) uint32 {
		b := a.As4()
		return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	}
	toA := func(x uint32) netip.Addr {
		return netip.AddrFrom4([4]byte{byte(x >> 24), byte(x >> 16), byte(x >> 8), byte(x)})
	}
	base, host := toU(pfx.Masked().Addr()), toU(pfx.Addr())
	n := uint32(1)<<(32-pfx.Bits()) - 2 // usable host addresses
	lo, hi := base+100, base+199
	if n < 254 {
		lo, hi = base+n/2+1, base+n
	}
	if host >= lo && host <= hi { // the box sits in the range: use the lower half
		lo, hi = base+1, base+n/2
		if host >= lo && host <= hi {
			lo = host + 1
		}
	}
	return toA(lo), toA(hi)
}

// parseLAN checks a console answer like 10.10.10.1/24.
func parseLAN(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	switch {
	case err != nil || !p.Addr().Is4():
		return netip.Prefix{}, errors.New("enter an IPv4 address with prefix, e.g. 10.10.10.1/24")
	case p.Bits() < 8 || p.Bits() > 30:
		return netip.Prefix{}, errors.New("the prefix must be /8 to /30")
	case p.Addr() == p.Masked().Addr():
		return netip.Prefix{}, errors.New("that is the network address; use a host address such as .1")
	}
	return p, nil
}

// setLAN changes the LAN interface address and moves its DHCP range into
// the new subnet (pfSense's "Set interface IP address").
func (c *console) setLAN(cfg runningConfig, setupNeeded bool) error {
	current := ""
	for _, i := range cfg.Interfaces {
		if i.Name == "lan" {
			current = i.Address
		}
	}
	ans := c.ask(fmt.Sprintf("New LAN address with prefix [%s]: ", current))
	if ans == "" {
		return nil
	}
	pfx, err := parseLAN(ans)
	if err != nil {
		return err
	}
	lo, hi := suggestRange(pfx)
	dhcp := c.ask(fmt.Sprintf("DHCP range [%s-%s] (Enter = this range, none = no DHCP server): ", lo, hi))
	noDHCP := strings.EqualFold(dhcp, "none")
	if dhcp != "" && !noDHCP {
		a, b, ok := strings.Cut(dhcp, "-")
		la, err1 := netip.ParseAddr(strings.TrimSpace(a))
		hb, err2 := netip.ParseAddr(strings.TrimSpace(b))
		if !ok || err1 != nil || err2 != nil || !pfx.Contains(la) || !pfx.Contains(hb) || hb.Less(la) {
			return fmt.Errorf("the range must be two addresses in %s, e.g. %s-%s", pfx.Masked(), lo, hi)
		}
		lo, hi = la, hb
	}

	// keep the other DHCP servers and this one's other settings
	var cand struct {
		DHCP []map[string]any `json:"dhcp"`
	}
	if err := c.api("GET", "/api/v1/config/candidate", nil, &cand); err != nil {
		return err
	}
	servers := []map[string]any{}
	found := false
	for _, s := range cand.DHCP {
		if s["interface"] != "lan" {
			servers = append(servers, s)
			continue
		}
		found = true
		if noDHCP {
			continue
		}
		if leases, ok := s["static_leases"].([]any); ok && len(leases) > 0 {
			fmt.Fprintf(c.out, "Note: %d static DHCP lease(s) in the old subnet are removed; add them again in the web UI.\n", len(leases))
			delete(s, "static_leases")
		}
		s["range_start"], s["range_end"] = lo.String(), hi.String()
		s["gateway"], s["dns"] = pfx.Addr().String(), []string{pfx.Addr().String()}
		servers = append(servers, s)
	}
	if !found && !noDHCP {
		servers = append(servers, map[string]any{"interface": "lan", "range_start": lo.String(), "range_end": hi.String(),
			"lease_seconds": 86400, "gateway": pfx.Addr().String(), "dns": []string{pfx.Addr().String()}})
	}
	summary := "no DHCP server"
	if !noDHCP {
		summary = fmt.Sprintf("DHCP %s-%s", lo, hi)
	}
	if !strings.EqualFold(c.ask(fmt.Sprintf("\nLAN = %s, %s. Apply now? [y/N] ", pfx, summary)), "y") {
		return nil
	}
	for _, p := range []map[string]any{
		{"path": "interfaces[name=lan].mode", "value": "static"},
		{"path": "interfaces[name=lan].address", "value": pfx.String()},
		{"path": "dhcp", "value": servers},
	} {
		if err := c.api("PATCH", "/api/v1/config/candidate", p, nil); err != nil {
			c.api("POST", "/api/v1/commit/discard", nil, nil)
			return err
		}
	}
	if err := c.api("POST", "/api/v1/commit", map[string]any{"comment": "console: set LAN address"}, nil); err != nil {
		c.api("POST", "/api/v1/commit/discard", nil, nil)
		return err
	}
	fmt.Fprintf(c.out, "Applied. The web UI is now at https://%s/ - computers on the LAN need a new DHCP lease (reconnect or reboot them).\n", pfx.Addr())
	if setupNeeded {
		fmt.Fprintln(c.out, "The setup wizard will suggest this address for the LAN; keep it.")
	}
	c.pause()
	return nil
}
