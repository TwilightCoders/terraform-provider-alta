package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cloudtest"
	"github.com/TwilightCoders/terraform-provider-alta/internal/resources"
	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
)

// fixtureVLANs are the networks the 2026-09-14 fixture already holds, in document order.
var fixtureVLANs = []int64{1, 2, 3, 20}

func vlanConfig(routerIP string) string {
	return providerBlock(false) + fmt.Sprintf(`
resource "alta_vlan" "lab" {
  site_id = %q
  vlan_id = 40

  name         = "Lab"
  router_ip    = %q
  pool_size    = 100
  reserved_ips = 10
  dns_servers  = ["192.0.2.10"]
}
`, cloudtest.SiteID, routerIP)
}

// vlanProviderBlock names the site on the provider, which is where a configuration that
// manages one site puts it.
func vlanProviderBlock() string {
	return fmt.Sprintf(`
provider "alta" {
  email     = "test"
  password  = "test"
  read_only = false
  site_id   = %q
  ssh = {
    host                 = "192.0.2.1"
    host_key_fingerprint = "SHA256:test"
  }
}
`, cloudtest.SiteID)
}

// vlanDefaultedConfig is vlanConfig with no identity at all: the site comes from the
// provider, and a network needs no device.
func vlanDefaultedConfig() string {
	return vlanProviderBlock() + `
resource "alta_vlan" "lab" {
  vlan_id = 40

  name         = "Lab"
  router_ip    = "198.18.40.1/24"
  pool_size    = 100
  reserved_ips = 10
  dns_servers  = ["192.0.2.10"]
}
`
}

// vlanScopedFactories carries the provider's own identities into ProviderData, which the
// shared harness leaves out.
func (h *harness) vlanScopedFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"alta": providerserver.NewProtocol6WithError(&Provider{version: "test", build: func(s Settings) (*resources.ProviderData, error) {
			data := &resources.ProviderData{Cloud: h.api.Client(), ReadOnly: s.ReadOnly, SiteID: s.SiteID, DeviceID: s.DeviceID}
			if s.SSH != nil {
				data.Transactions = h.tx
				data.Hooks = h.hooks
			}
			return data, nil
		}}),
	}
}

// TestVLANLifecycle covers the loop the whole provider is judged on: create, refresh
// clean, change, destroy.
func TestVLANLifecycle(t *testing.T) {
	h := newHarness(t)
	const name = "alta_vlan.lab"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: vlanConfig("198.18.40.1/24"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "id", "40"),
					resource.TestCheckResourceAttr(name, "router_ip", "198.18.40.1/24"),
					resource.TestCheckResourceAttr(name, "dhcp", "true"),
					h.expectVLANs(append(slices.Clone(fixtureVLANs), 40)...),
				),
			},
			{Config: vlanConfig("198.18.40.1/24"), PlanOnly: true},
			{
				Config: vlanConfig("198.18.41.1/24"),
				Check:  resource.TestCheckResourceAttr(name, "router_ip", "198.18.41.1/24"),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     cloudtest.SiteID + "/40",
				ImportStateVerify: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			vlans, err := h.vlans()
			if err != nil {
				return err
			}
			if slices.ContainsFunc(vlans, func(v routerconfig.VLAN) bool { return v.ID == 40 }) {
				return fmt.Errorf("destroy left VLAN 40 in the cloud: %+v", vlans)
			}
			return nil
		},
	})
}

// TestVLANLeavesOtherNetworksAlone is the difference between a single-item resource and
// the whole-router one: writing a key replaces the whole array, so networks this resource
// does not own must survive its create and its destroy, in their existing order.
func TestVLANLeavesOtherNetworksAlone(t *testing.T) {
	h := newHarness(t)
	h.seedVLAN(50, "Cameras")
	kept := append(slices.Clone(fixtureVLANs), 50)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: vlanConfig("198.18.40.1/24"),
			Check:  h.expectVLANs(append(slices.Clone(kept), 40)...),
		}},
		CheckDestroy: h.expectVLANs(kept...),
	})
}

// TestVLANDefaultsToTheProvidersSite is the point of an optional site_id: a configuration
// that manages one site names it once, on the provider, and the resource carries no
// identity of its own. The import id is the VLAN number alone for the same reason.
func TestVLANDefaultsToTheProvidersSite(t *testing.T) {
	h := newHarness(t)
	const name = "alta_vlan.lab"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.vlanScopedFactories(),
		Steps: []resource.TestStep{
			{
				Config: vlanDefaultedConfig(),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "site_id", cloudtest.SiteID),
					resource.TestCheckResourceAttr(name, "id", "40"),
					h.expectVLANs(append(slices.Clone(fixtureVLANs), 40)...),
				),
			},
			{Config: vlanDefaultedConfig(), PlanOnly: true},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     "40",
				ImportStateVerify: true,
			},
		},
	})
}

func TestVLANRefusedWhenReadOnly(t *testing.T) {
	h := newHarness(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: regexp.MustCompile(`read_only = false`).ReplaceAllString(
				vlanConfig("198.18.40.1/24"), "read_only = true"),
			ExpectError: regexp.MustCompile(`read_only`),
		}},
	})
}

// vlans is the collection alta_vlan writes into.
func (h *harness) vlans() ([]routerconfig.VLAN, error) {
	c, err := h.config()
	if err != nil {
		return nil, err
	}
	return *c.VLANs, nil
}

// seedVLAN puts a network in the cloud that Terraform does not manage. The id is a
// json.Number because that is how the decoder receives the fixture's own numbers.
func (h *harness) seedVLAN(id int64, name string) {
	site := h.api.Site()
	vlans, _ := site["vlans"].([]any)
	site["vlans"] = append(vlans, cloud.Object{"id": json.Number(strconv.FormatInt(id, 10)), "notes": name})
}

// expectVLANs asserts the cloud's networks, by number and in document order.
func (h *harness) expectVLANs(want ...int64) resource.TestCheckFunc {
	return func(*terraform.State) error {
		vlans, err := h.vlans()
		if err != nil {
			return err
		}
		got := make([]int64, 0, len(vlans))
		for _, v := range vlans {
			got = append(got, v.ID)
		}
		if !slices.Equal(got, want) {
			return fmt.Errorf("cloud VLANs = %v, want %v", got, want)
		}
		return nil
	}
}
