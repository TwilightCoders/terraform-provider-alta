package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cloudtest"
	"github.com/TwilightCoders/terraform-provider-alta/internal/resources"
	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
	"github.com/TwilightCoders/terraform-provider-alta/internal/txn"
)

// fakeTransactor applies changes straight to the cloud, standing in for the router gate.
type fakeTransactor struct {
	cloud *cloud.Client
	ready error
	runs  []txn.Change
}

func (f *fakeTransactor) Ready(context.Context) error           { return f.ready }
func (f *fakeTransactor) Recover(context.Context) (bool, error) { return false, nil }
func (f *fakeTransactor) Run(ctx context.Context, c txn.Change) (txn.Result, error) {
	f.runs = append(f.runs, c)
	for _, w := range c.Writes {
		if err := f.cloud.Apply(ctx, w); err != nil {
			return txn.Result{}, err
		}
	}
	return txn.Result{Pushed: true}, nil
}

type harness struct {
	api   *cloudtest.Server
	tx    *fakeTransactor
	hooks resources.DeviceHooks
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	site, state := cloudtest.Current(t)
	api := cloudtest.NewServer(t, site, state)
	return &harness{api: api, tx: &fakeTransactor{cloud: api.Client()}}
}

func (h *harness) factories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"alta": providerserver.NewProtocol6WithError(&Provider{version: "test", build: func(s Settings) (*resources.ProviderData, error) {
			data := &resources.ProviderData{Cloud: h.api.Client(), ReadOnly: s.ReadOnly}
			if s.SSH != nil {
				data.Transactions = h.tx
				data.Hooks = h.hooks
			}
			return data, nil
		}}),
	}
}

// config reads the cloud back as configuration, so a test can assert what a single-item
// resource did to the collection it shares with everything else.
func (h *harness) config() (routerconfig.Config, error) {
	state, err := h.api.Client().State(context.Background(), cloudtest.SiteID)
	if err != nil {
		return routerconfig.Config{}, err
	}
	doc, err := routerconfig.NewDocument(cloudtest.SiteID, cloudtest.DeviceID, h.api.Site(), state)
	if err != nil {
		return routerconfig.Config{}, err
	}
	return routerconfig.Read(doc), nil
}

// routes is the collection alta_static_route writes into.
func (h *harness) routes() ([]routerconfig.StaticRoute, error) {
	c, err := h.config()
	if err != nil {
		return nil, err
	}
	return *c.StaticRoutes, nil
}

// seedRoute puts a route in the cloud that Terraform does not manage.
func (h *harness) seedRoute(id, network string) {
	site := h.api.Site()
	routes, _ := site["routes"].([]any)
	site["routes"] = append(routes, cloud.Object{
		"id": id, "name": id, "type": "blackhole", "network": network,
	})
}

// sameIDs compares a collection's ids against what it should hold, in order, so a test
// that cares about untouched neighbours also catches a reshuffle.
func sameIDs(read func() ([]string, error), want []string) error {
	got, err := read()
	if err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("ids = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			return fmt.Errorf("ids = %v, want %v", got, want)
		}
	}
	return nil
}

func providerBlock(readOnly bool) string {
	return fmt.Sprintf(`
provider "alta" {
  email     = "test"
  password  = "test"
  read_only = %t
  ssh = {
    host                 = "192.0.2.1"
    host_key_fingerprint = "SHA256:test"
  }
}
`, readOnly)
}

// routerConfig is HCL matching the 2026-09-14 fixture, with the app forward's port substitutable.
func routerConfig(appPort string) string {
	return fmt.Sprintf(`
resource "alta_router_config" "route10" {
  site_id   = %q
  device_id = %q

  port_forwards = {
    cYzVVn = {
      description = "Game server"
      protocols   = ["tcp", "udp"]
      zone_in     = "wan"
      zone_out    = "lan"
      destination = { port = "7000" }
      translation = { address = "192.0.2.10", port = "7000" }
    }
    W9PlTq = {
      description = "Media server"
      protocols   = ["udp", "tcp"]
      ip_version  = "any"
      zone_in     = "wan"
      zone_out    = "lan"
      destination = { port = "9000" }
      translation = { address = "192.0.2.10", port = "9000" }
    }
    LR0Hi6 = {
      description = "HTTP proxy"
      protocols   = ["udp", "tcp"]
      zone_in     = "wan"
      destination = { port = "80" }
      translation = { address = "192.0.2.10", port = "80" }
    }
    lhX53l = {
      description = "HTTPS proxy"
      protocols   = ["udp", "tcp"]
      zone_in     = "wan"
      destination = { port = "443" }
      translation = { address = "192.0.2.10", port = "443" }
    }
    AppFwd = {
      description = "App server"
      protocols   = ["udp"]
      ip_version  = "ipv4"
      zone_in     = "wan"
      zone_out    = "lan"
      destination = { port = %q }
      translation = { address = "192.0.2.10", port = "8100" }
    }
  }

  vlans = {
    "1"  = { name = "Guest", isolation = true, dns_servers = ["192.0.2.10", "192.0.2.1"] }
    "2"  = { name = "Home", router_ip = "192.0.2.1/24", pool_size = 243, reserved_ips = 10, domain_name = "lan", dns_servers = ["192.0.2.10", "192.0.2.1"] }
    "3"  = { name = "Wireless", router_ip = "203.0.113.1/24", pool_size = 243, reserved_ips = 10, domain_name = "lan", dns_servers = ["192.0.2.10", "192.0.2.1"] }
    "20" = { name = "Isolated", router_ip = "198.18.20.1/24", pool_size = 243, dhcp = false, mdns = false }
  }
}
`, cloudtest.SiteID, cloudtest.DeviceID, appPort)
}

const resourceName = "alta_router_config.route10"

func TestRouterConfigLifecycle(t *testing.T) {
	h := newHarness(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				// Adopting configuration that matches the cloud writes nothing, even read-only.
				Config: providerBlock(true) + routerConfig("8100"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "id", cloudtest.SiteID+"/"+cloudtest.DeviceID),
					resource.TestCheckResourceAttr(resourceName, "port_forwards.%", "5"),
					resource.TestCheckResourceAttr(resourceName, "vlans.20.dhcp", "false"),
					resource.TestCheckNoResourceAttr(resourceName, "static_routes"),
					h.expectWrites(0),
				),
			},
			{
				Config:   providerBlock(true) + routerConfig("8100"),
				PlanOnly: true,
			},
			{
				Config:      providerBlock(true) + routerConfig("8101"),
				ExpectError: regexp.MustCompile(`read_only and this change writes site\.firewall`),
			},
			{
				Config: providerBlock(false) + routerConfig("8101"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "port_forwards.AppFwd.destination.port", "8101"),
					h.expectTransactions(1, "site.firewall"),
				),
			},
			{
				ResourceName:  resourceName,
				ImportState:   true,
				ImportStateId: cloudtest.SiteID + "/" + cloudtest.DeviceID,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					attrs := states[0].Attributes
					want := map[string]string{"port_forwards.%": "5", "vlans.%": "4", "dhcp_reservations.%": "3", "switch_ports.%": "6", "firewall_rules.#": "11"}
					for k, v := range want {
						if attrs[k] != v {
							return fmt.Errorf("imported %s = %q, want %q", k, attrs[k], v)
						}
					}
					return nil
				},
			},
		},
	})
}

func TestRouterConfigRefusesToPushOverLocalEdits(t *testing.T) {
	h := newHarness(t)
	h.tx.ready = fmt.Errorf("%w (config md5 a, last push b)", txn.ErrLocalEdits)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{Config: providerBlock(false) + routerConfig("8100")},
			{
				Config:      providerBlock(false) + routerConfig("8101"),
				ExpectError: regexp.MustCompile(`local configuration edits`),
			},
		},
	})
}

func TestProviderConfigurationErrors(t *testing.T) {
	h := newHarness(t)
	t.Setenv(EnvEmail, "")
	t.Setenv(EnvPassword, "")
	resourceBlock := `resource "alta_router_config" "x" {
  site_id   = "s"
  device_id = "d"
}`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config:      `provider "alta" {}` + "\n" + resourceBlock,
				ExpectError: regexp.MustCompile(`Set email and password`),
			},
			{
				Config: `provider "alta" {
  email    = "e"
  password = "p"
  probes = {
    dns_server = "192.0.2.10"
    dns_name   = "static.example.net"
  }
  transaction = { settle = "soon" }
}` + "\n" + resourceBlock,
				ExpectError: regexp.MustCompile(`(?s)Must contain \{nonce\}.*positive duration`),
			},
		},
	})
}

func (h *harness) expectWrites(n int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := len(h.api.Writes()); got != n {
			return fmt.Errorf("cloud writes = %d, want %d", got, n)
		}
		return nil
	}
}

func (h *harness) expectTransactions(n int, keys ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if len(h.tx.runs) != n {
			return fmt.Errorf("transactions = %d, want %d", len(h.tx.runs), n)
		}
		last := h.tx.runs[n-1]
		got := make([]string, 0, len(last.Writes))
		for _, w := range last.Writes {
			got = append(got, string(w.Kind)+"."+w.Key)
		}
		if strings.Join(got, ",") != strings.Join(keys, ",") {
			return fmt.Errorf("transaction writes %v, want %v", got, keys)
		}
		return nil
	}
}

// TestStaticRouteIsCheckedAtPlan keeps a route the portal would refuse from reaching a
// push: a next-hop route without a gateway compiles to nothing on the router, so the
// failure belongs in the plan rather than in a transaction that has already stopped the
// cloud agent.
func TestStaticRouteIsCheckedAtPlan(t *testing.T) {
	h := newHarness(t)
	site, device := cloudtest.SiteID, cloudtest.DeviceID
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: providerBlock(false) + fmt.Sprintf(`
resource "alta_router_config" "route10" {
  site_id   = %q
  device_id = %q

  static_routes = {
    vpnret = {
      name    = "Return path"
      type    = "next-hop"
      network = "198.18.20.0/28"
    }
  }
}
`, site, device),
			ExpectError: regexp.MustCompile(`needs next_hop`),
		}},
	})
}
