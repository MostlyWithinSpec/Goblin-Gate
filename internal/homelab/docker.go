package homelab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Docker reads the running containers of one Docker host over its TLS API
// (dockerd --tlsverify; the client certificate must be one dockerd's CA
// signed).
//
// A container on a macvlan or ipvlan network has its own address on the LAN,
// and that address is used. Everything else (bridge networks, host network)
// reaches the LAN from the Docker host's address, so that address is used.
type Docker struct {
	Settings DockerSettings
	HTTP     *http.Client
	// Resolve looks up the Docker host's addresses when Settings.Host is
	// not set (default net.DefaultResolver).
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
}

// NewDocker returns a client for s.
func NewDocker(s DockerSettings) *Docker {
	return &Docker{Settings: s, HTTP: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: s.TLS}}}
}

func (d *Docker) Name() string {
	u, _ := url.Parse(d.Settings.URL)
	return "docker " + u.Host
}

func (d *Docker) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", d.Settings.URL+path, nil)
	if err != nil {
		return err
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &e)
		return fmt.Errorf("%s: HTTP %d %s", path, resp.StatusCode, e.Message)
	}
	return json.Unmarshal(body, out)
}

type dockerContainer struct {
	ID              string            `json:"Id"`
	Names           []string          `json:"Names"`
	Labels          map[string]string `json:"Labels"`
	State           string            `json:"State"`
	NetworkSettings struct {
		Networks map[string]struct {
			NetworkID         string `json:"NetworkID"`
			IPAddress         string `json:"IPAddress"`
			GlobalIPv6Address string `json:"GlobalIPv6Address"`
			MacAddress        string `json:"MacAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
}

// ownAddressDrivers are the network drivers whose containers have their own
// address on the LAN.
var ownAddressDrivers = map[string]bool{"macvlan": true, "ipvlan": true}

// Guests lists the running containers with the addresses the gate sees them
// at.
func (d *Docker) Guests(ctx context.Context) ([]Guest, error) {
	var nets []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Driver string `json:"Driver"`
	}
	if err := d.get(ctx, "/networks", &nets); err != nil {
		return nil, fmt.Errorf("%s: networks: %w", d.Name(), err)
	}
	driver := map[string]string{}
	for _, n := range nets {
		driver[n.ID], driver[n.Name] = n.Driver, n.Driver
	}
	var cs []dockerContainer
	if err := d.get(ctx, "/containers/json", &cs); err != nil {
		return nil, fmt.Errorf("%s: containers: %w", d.Name(), err)
	}
	var host []netip.Addr
	hostDone := false
	hostAddrs := func() ([]netip.Addr, error) {
		if hostDone {
			return host, nil
		}
		hostDone = true
		if d.Settings.Host.IsValid() {
			host = []netip.Addr{d.Settings.Host}
			return host, nil
		}
		u, _ := url.Parse(d.Settings.URL)
		if a, err := netip.ParseAddr(u.Hostname()); err == nil {
			host = []netip.Addr{a.Unmap()}
			return host, nil
		}
		var err error
		host, err = d.resolve(ctx, u.Hostname())
		if err != nil {
			return nil, fmt.Errorf("%s: resolving the host for its NATed containers (set docker_host_address): %w", d.Name(), err)
		}
		return host, nil
	}
	var out []Guest
	for _, c := range cs {
		if c.State != "running" {
			continue
		}
		g := Guest{Source: d.Name(), Kind: "container", Name: c.ID}
		if len(c.Names) > 0 {
			g.Name = strings.TrimPrefix(c.Names[0], "/")
		}
		for k := range c.Labels {
			g.Tags = append(g.Tags, strings.ToLower(k))
		}
		sort.Strings(g.Tags)
		behindHost := c.HostConfig.NetworkMode == "host"
		for name, n := range c.NetworkSettings.Networks {
			drv := driver[n.NetworkID]
			if drv == "" {
				drv = driver[name]
			}
			switch {
			case ownAddressDrivers[drv]:
				for _, s := range []string{n.IPAddress, n.GlobalIPv6Address} {
					if a, err := netip.ParseAddr(s); err == nil {
						g.Addrs = append(g.Addrs, a)
					}
				}
			case drv == "null" || name == "none":
			default:
				behindHost = true
			}
		}
		if behindHost {
			h, err := hostAddrs()
			if err != nil {
				return nil, err
			}
			g.Addrs = append(g.Addrs, h...)
		}
		out = append(out, g)
	}
	return out, nil
}

func (d *Docker) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if d.Resolve != nil {
		return d.Resolve(ctx, host)
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	for i := range addrs {
		addrs[i] = addrs[i].Unmap()
	}
	return addrs, err
}
