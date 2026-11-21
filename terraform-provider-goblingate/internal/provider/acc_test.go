package provider

// Acceptance tests: real Terraform against a real goblind.
//
//   TF_ACC=1 go test ./internal/provider -run TestAcc -v
//
// Without GOBLINGATE_URL the tests build goblind from this repository and
// run it in --dev mode (fake applier, no authentication) in a temporary
// directory. Terraform is the one on PATH, or TF_ACC_TERRAFORM_PATH, or
// downloaded by terraform-plugin-testing.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

var factories = map[string]func() (tfprotov6.ProviderServer, error){
	"goblingate": providerserver.NewProtocol6WithError(New("test")()),
}

func TestMain(m *testing.M) {
	if os.Getenv("TF_ACC") == "" || os.Getenv("GOBLINGATE_URL") != "" {
		os.Exit(m.Run())
	}
	stop, err := startDevGate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "acceptance tests: start goblind:", err)
		os.Exit(1)
	}
	code := m.Run()
	stop()
	os.Exit(code)
}

// startDevGate builds goblind, runs it in --dev mode and runs the setup
// wizard (a LAN with a DHCP server), then points GOBLINGATE_URL at it.
func startDevGate() (func(), error) {
	dir, err := os.MkdirTemp("", "goblingate-acc-")
	if err != nil {
		return nil, err
	}
	bin := filepath.Join(dir, "goblind")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/goblind")
	build.Dir = filepath.Join("..", "..", "..") // the repository root
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("go build: %v\n%s", err, out)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	addr := l.Addr().String()
	l.Close()
	logf, err := os.Create(filepath.Join(dir, "goblind.log"))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(bin, "--dev", "--config-dir", filepath.Join(dir, "config"), "--listen", addr)
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logf.Close()
		if os.Getenv("GOBLINGATE_ACC_KEEP") == "" {
			os.RemoveAll(dir)
		} else {
			fmt.Fprintln(os.Stderr, "goblind dev gate kept in", dir)
		}
	}
	base := "http://" + addr
	for i := 0; ; i++ {
		resp, err := http.Get(base + "/api/v1/setup/state")
		if err == nil {
			resp.Body.Close()
			break
		}
		if i == 100 {
			stop()
			return nil, fmt.Errorf("goblind did not come up on %s", addr)
		}
		time.Sleep(100 * time.Millisecond)
	}
	setup := `{"admin_password":"Goblin-Test-Passw0rd!","hostname":"tfgate","security_level":"basic",
		"wan":{"device":"eth0","mode":"dhcp"},
		"lan":{"device":"eth1","address":"192.168.1.1/24","dhcp_start":"192.168.1.100","dhcp_end":"192.168.1.199"}}`
	resp, err := http.Post(base+"/api/v1/setup", "application/json", strings.NewReader(setup))
	if err != nil {
		stop()
		return nil, err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		stop()
		return nil, fmt.Errorf("setup: HTTP %d", resp.StatusCode)
	}
	os.Setenv("GOBLINGATE_URL", base)
	return stop, nil
}

func accPreCheck(t *testing.T) {
	if os.Getenv("GOBLINGATE_URL") == "" {
		t.Fatal("GOBLINGATE_URL must be set for acceptance tests")
	}
}

// api calls the gate the tests run against, outside Terraform.
func api(t *testing.T, method, path string, body any, out any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, os.Getenv("GOBLINGATE_URL")+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if tok := os.Getenv("GOBLINGATE_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
}

type revision struct {
	Revision int    `json:"revision"`
	Comment  string `json:"comment"`
}

func revisions(t *testing.T) []revision {
	var r struct {
		Revisions []revision `json:"revisions"`
	}
	api(t, http.MethodGet, "/api/v1/revisions", nil, &r)
	return r.Revisions // newest first
}

const homelabStep1 = `
resource "goblingate_vlan" "iot" {
  name         = "acc-iot"
  parent       = "lan"
  vlan_id      = 120
  address      = "192.168.120.1/24"
  allow_access = ["ping"]
}

resource "goblingate_address" "nas" {
  name    = "acc-nas"
  type    = "host"
  value   = "192.168.1.10"
  comment = "acceptance"
}

resource "goblingate_address" "cams" {
  name  = "acc-cams"
  type  = "range"
  value = "192.168.120.50-192.168.120.60"
}

resource "goblingate_address_group" "servers" {
  name    = "acc-servers"
  members = [goblingate_address.nas.name]
}

resource "goblingate_service" "plex" {
  name     = "acc-plex"
  protocol = "tcp"
  ports    = "32400"
}

resource "goblingate_policy" "iot_plex" {
  name     = "acc-iot-to-plex"
  src_intf = [goblingate_vlan.iot.name]
  dst_intf = ["lan"]
  dst_addr = [goblingate_address_group.servers.name]
  service  = [goblingate_service.plex.name]
  action   = "accept"
}

resource "goblingate_port_forward" "plex" {
  name     = "acc-plex"
  in_intf  = "wan1"
  ext_port = "32400"
  protocol = "tcp"
  to_addr  = goblingate_address.nas.value
  to_port  = "32400"
}

resource "goblingate_dhcp_static_lease" "nas" {
  interface = "lan"
  mac       = "52:54:00:aa:bb:01"
  ip        = "192.168.1.10"
  hostname  = "acc-nas"
}

resource "goblingate_dns_record" "nas" {
  name = "acc-nas.home.lan"
  ip   = goblingate_dhcp_static_lease.nas.ip
}
`

func TestAccHomelab(t *testing.T) {
	step2 := strings.NewReplacer(
		`value   = "192.168.1.10"`, `value   = "192.168.1.11"`,
		`ip        = "192.168.1.10"`, `ip        = "192.168.1.11"`,
		`action   = "accept"`, "action   = \"deny\"\n  log      = \"all\"",
		`allow_access = ["ping"]`, `allow_access = ["ping", "https"]`,
	).Replace(homelabStep1)
	var before int
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t); before = revisions(t)[0].Revision },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: homelabStep1,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("goblingate_vlan.iot", "id", "acc-iot"),
					resource.TestCheckResourceAttr("goblingate_vlan.iot", "mode", "static"),
					resource.TestCheckResourceAttr("goblingate_vlan.iot", "enabled", "true"),
					resource.TestCheckResourceAttr("goblingate_policy.iot_plex", "src_addr.#", "1"),
					resource.TestCheckTypeSetElemAttr("goblingate_policy.iot_plex", "src_addr.*", "all"),
					resource.TestCheckResourceAttrSet("goblingate_policy.iot_plex", "policy_id"),
					resource.TestCheckResourceAttr("goblingate_dhcp_static_lease.nas", "id", "lan/52:54:00:aa:bb:01"),
					resource.TestCheckResourceAttr("goblingate_dns_record.nas", "ip", "192.168.1.10"),
					checkRunning(t, func(cfg map[string]any) error {
						recs := object(cfg, "dns", "local_records")
						if recs["acc-nas.home.lan"] != "192.168.1.10" {
							return fmt.Errorf("dns.local_records = %v", recs)
						}
						srv, _ := findBy(list(cfg, "dhcp"), "interface", "lan", false)
						if _, ok := findBy(list(srv, "static_leases"), "mac", "52:54:00:aa:bb:01", false); !ok {
							return fmt.Errorf("no static lease in %v", srv)
						}
						return nil
					}),
					func(*terraform.State) error {
						// one commit per dependency level, each with a summary,
						// not one per resource
						revs := revisions(t)
						n := revs[0].Revision - before
						if n < 1 || n > 5 {
							return fmt.Errorf("%d commits for 9 resources", n)
						}
						all := ""
						for _, r := range revs[:n] {
							if !strings.HasPrefix(r.Comment, "terraform: ") {
								return fmt.Errorf("revision %d comment %q", r.Revision, r.Comment)
							}
							all += r.Comment + "\n"
						}
						for _, want := range []string{"create VLAN acc-iot", "create address acc-nas", "create policy", "create DHCP static lease 52:54:00:aa:bb:01 on lan", "create DNS record acc-nas.home.lan"} {
							if !strings.Contains(all, want) {
								return fmt.Errorf("no %q in the commit comments:\n%s", want, all)
							}
						}
						return nil
					},
				),
			},
			{
				Config: step2,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("goblingate_address.nas", "value", "192.168.1.11"),
					resource.TestCheckResourceAttr("goblingate_policy.iot_plex", "action", "deny"),
					resource.TestCheckResourceAttr("goblingate_dns_record.nas", "ip", "192.168.1.11"),
					resource.TestCheckResourceAttr("goblingate_vlan.iot", "allow_access.#", "2"),
				),
			},
			{ResourceName: "goblingate_vlan.iot", ImportState: true, ImportStateVerify: true},
			{ResourceName: "goblingate_address.nas", ImportState: true, ImportStateVerify: true},
			{ResourceName: "goblingate_address.cams", ImportState: true, ImportStateVerify: true},
			{ResourceName: "goblingate_address_group.servers", ImportState: true, ImportStateVerify: true},
			{ResourceName: "goblingate_service.plex", ImportState: true, ImportStateVerify: true},
			{ResourceName: "goblingate_policy.iot_plex", ImportState: true, ImportStateVerify: true},
			{ResourceName: "goblingate_port_forward.plex", ImportState: true, ImportStateVerify: true},
			{ResourceName: "goblingate_dhcp_static_lease.nas", ImportState: true, ImportStateVerify: true},
			{ResourceName: "goblingate_dns_record.nas", ImportState: true, ImportStateVerify: true},
		},
		CheckDestroy: checkRunning(t, func(cfg map[string]any) error {
			for _, key := range []string{"vlans", "addresses", "address_groups", "services"} {
				for _, it := range list(cfg, key) {
					if n, _ := it["name"].(string); strings.HasPrefix(n, "acc-") {
						return fmt.Errorf("%s %s is still there", key, n)
					}
				}
			}
			if _, ok := object(cfg, "dns", "local_records")["acc-nas.home.lan"]; ok {
				return fmt.Errorf("the DNS record is still there")
			}
			return nil
		}),
	})
}

// TestAccRefusesForeignChanges: someone else's uncommitted change stops
// Terraform before it touches anything.
func TestAccRefusesForeignChanges(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			PreConfig: func() {
				api(t, http.MethodPatch, "/api/v1/config/candidate", map[string]any{"path": "system.hostname", "value": "someone-else"}, nil)
			},
			Config: `resource "goblingate_address" "x" {
  name  = "acc-x"
  type  = "host"
  value = "10.9.9.9"
}`,
			ExpectError: regexp.MustCompile(`(?s)uncommitted change.*system\.hostname`),
		}},
	})
	var cfg map[string]any
	api(t, http.MethodGet, "/api/v1/config/candidate", nil, &cfg)
	if h := object(cfg, "system")["hostname"]; h != "someone-else" {
		t.Errorf("the other change was lost: hostname %v", h)
	}
	if _, ok := findBy(list(cfg, "addresses"), "name", "acc-x", false); ok {
		t.Error("the address was added to the candidate")
	}
	api(t, http.MethodPost, "/api/v1/commit/discard", nil, nil)
}

// TestAccInvalidChangeIsNotCommitted: the gate's validation stops the
// commit and the candidate is left clean.
func TestAccInvalidChangeIsNotCommitted(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `resource "goblingate_policy" "bad" {
  name     = "acc-bad"
  src_intf = ["lan"]
  dst_intf = ["wan1"]
  dst_addr = ["no-such-address"]
  action   = "accept"
}`,
			ExpectError: regexp.MustCompile(`(?s)no-such-address`),
		}},
	})
	var d struct{ Count int }
	api(t, http.MethodGet, "/api/v1/config/diff", nil, &d)
	if d.Count != 0 {
		t.Errorf("%d changes left in the candidate", d.Count)
	}
}

func TestAccDataSources(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: `
data "goblingate_interfaces" "all" {}
output "wan" { value = [for i in data.goblingate_interfaces.all.interfaces : i.name if i.role == "wan"][0] }
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckOutput("wan", "wan1"),
				resource.TestCheckResourceAttr("data.goblingate_interfaces.all", "interfaces.#", "2"),
				resource.TestCheckResourceAttr("data.goblingate_interfaces.all", "interfaces.1.address", "192.168.1.1/24"),
			),
		}},
	})
}

// TestAccDevicesDataSource reads a fake inventory: a --dev gate has no
// device inventory (it answers 503).
func TestAccDevicesDataSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/devices" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"devices":[
			{"mac":"aa:bb:cc:00:00:02","ips":["192.168.20.7"],"interface":"iot","first_seen":"2026-11-01T10:00:00Z","last_seen":"2026-11-20T10:00:00Z","vlan":"iot"},
			{"mac":"aa:bb:cc:00:00:01","ips":["192.168.1.10","fd00::10"],"hostname":"nas","vendor":"Synology","interface":"lan","name":"NAS","first_seen":"2026-11-01T10:00:00Z","last_seen":"2026-11-20T10:00:00Z"}]}`))
	}))
	defer srv.Close()
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
provider "goblingate" { url = %q }
data "goblingate_devices" "all" {}
data "goblingate_devices" "lan" { interface = "lan" }
`, srv.URL),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.goblingate_devices.all", "devices.#", "2"),
				resource.TestCheckResourceAttr("data.goblingate_devices.all", "devices.0.mac", "aa:bb:cc:00:00:01"),
				resource.TestCheckResourceAttr("data.goblingate_devices.all", "devices.1.vlan", "iot"),
				resource.TestCheckResourceAttr("data.goblingate_devices.lan", "devices.#", "1"),
				resource.TestCheckResourceAttr("data.goblingate_devices.lan", "devices.0.name", "NAS"),
				resource.TestCheckResourceAttr("data.goblingate_devices.lan", "devices.0.ips.1", "fd00::10"),
			),
		}},
	})
}

// TestAccExampleHomelabPlans plans the published homelab example against
// the gate (no apply).
func TestAccExampleHomelabPlans(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "terraform", "homelab", "main.tf"))
	if err != nil {
		t.Fatal(err)
	}
	// the test framework supplies the provider: drop the registry source
	cfg := regexp.MustCompile(`(?s)terraform \{.*?\n\}\n`).ReplaceAllString(string(src), "")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config:             cfg,
			ConfigVariables:    config.Variables{"gate_url": config.StringVariable(os.Getenv("GOBLINGATE_URL"))},
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
		}},
	})
}

func checkRunning(t *testing.T, f func(map[string]any) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		var cfg map[string]any
		api(t, http.MethodGet, "/api/v1/config/running", nil, &cfg)
		return f(cfg)
	}
}
