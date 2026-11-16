package homelab

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
)

func TestSettings(t *testing.T) {
	pki := newPKI(t)
	// a one-line form field turns the PEM's line breaks into spaces
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	s, err := SettingsFrom(map[string]any{
		"proxmox_url": "https://pve.lan", "proxmox_token_id": "goblin@pve!gate", "proxmox_token_secret": "x",
		"proxmox_fingerprint": strings.Repeat("ab:", 31) + "ab",
		"docker_url":          "tcp://docker.lan", "docker_ca": flat(pki.CA), "docker_cert": flat(pki.ClientCert), "docker_key": flat(pki.ClientKey),
		"name_patterns": []any{"web = web-*", "db=db?"}, "networks": []any{"192.168.20.7/24"}, "interval_seconds": float64(30),
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Proxmox.URL != "https://pve.lan:8006" || s.Docker[0].URL != "https://docker.lan:2376" || s.Interval != 30*time.Second {
		t.Errorf("settings %+v", s)
	}
	if len(s.Docker[0].TLS.Certificates) != 1 || s.Docker[0].TLS.RootCAs == nil {
		t.Error("flattened PEM not read")
	}
	if s.Networks[0].String() != "192.168.20.0/24" || !s.Patterns[0].Matches("WEB-3") || s.Patterns[0].Object != "web" {
		t.Errorf("patterns %+v networks %v", s.Patterns, s.Networks)
	}

	for _, bad := range []map[string]any{
		{},
		{"proxmox_url": "http://pve.lan:8006", "proxmox_token_id": "goblin@pve!gate", "proxmox_token_secret": "x"},
		{"proxmox_url": "https://pve.lan", "proxmox_token_id": "goblin@pve", "proxmox_token_secret": "x"},
		{"proxmox_url": "https://pve.lan", "proxmox_token_id": "goblin@pve!gate"},
		{"proxmox_url": "https://pve.lan", "proxmox_token_id": "goblin@pve!gate", "proxmox_token_secret": "x", "proxmox_fingerprint": "abcd"},
		{"docker_url": "tcp://docker.lan:2375"},
		{"docker_url": "unix:///var/run/docker.sock", "docker_ca": pki.CA, "docker_cert": pki.ClientCert, "docker_key": pki.ClientKey},
		{"docker_url": "tcp://docker.lan", "docker_ca": pki.CA, "docker_cert": pki.ClientCert, "docker_key": pki.ServerKey},
		{"docker_url": "tcp://docker.lan", "docker_ca": pki.CA, "docker_cert": pki.ClientCert, "docker_key": pki.ClientKey, "name_patterns": []any{"web"}},
		{"docker_url": "tcp://docker.lan", "docker_ca": pki.CA, "docker_cert": pki.ClientCert, "docker_key": pki.ClientKey, "networks": []any{"lan"}},
	} {
		if _, err := SettingsFrom(bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestManifest(t *testing.T) {
	data, err := os.ReadFile("../../plugins/homelab/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	m, err := plugins.ParseManifest(data, "homelab")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "homelab" {
		t.Errorf("name %q", m.Name)
	}
	// every setting the plugin reads is in the manifest
	var raw struct {
		Settings struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"settings_schema"`
	}
	json.Unmarshal(data, &raw)
	for _, k := range []string{"proxmox_url", "proxmox_token_id", "proxmox_token_secret", "proxmox_fingerprint", "proxmox_ca",
		"docker_url", "docker_ca", "docker_cert", "docker_key", "docker_host_address", "name_patterns", "networks", "interval_seconds"} {
		if _, ok := raw.Settings.Properties[k]; !ok {
			t.Errorf("manifest lacks %s", k)
		}
	}
}
