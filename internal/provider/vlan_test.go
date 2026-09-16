package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cloudtest"
	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
)

// fixtureVLANs are the networks the 2026-09-14 fixture already holds, in document order.
var fixtureVLANs = []int64{1, 2, 3, 20}

func vlanConfig(routerIP string) string {
	return providerBlock(false) + fmt.Sprintf(`
resource "alta_vlan" "lab" {
  site_id   = %q
  device_id = %q
  vlan_id   = 40

  name         = "Lab"
  router_ip    = %q
  pool_size    = 100
  reserved_ips = 10
  dns_servers  = ["192.0.2.10"]
}
`, cloudtest.SiteID, cloudtest.DeviceID, routerIP)
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
				ImportStateId:     cloudtest.SiteID + "/" + cloudtest.DeviceID + "/40",
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
