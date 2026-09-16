package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestResourcesComposeFromOneNetwork is the test for whether these resources are usable
// together rather than merely present. One network is described once; the address of a
// host on it, the VLAN a port carries, and the interface a route leaves by are all derived
// from it. Nothing here repeats a subnet, an interface name or a VLAN number.
func TestResourcesComposeFromOneNetwork(t *testing.T) {
	h := newHarness(t)

	const config = `
resource "alta_vlan" "lab" {
  vlan_id   = 40
  name      = "Lab"
  router_ip = "198.18.40.1/24"
}

resource "alta_dhcp_reservation" "printer" {
  mac = "02:00:00:aa:bb:cc"
  ip  = cidrhost(alta_vlan.lab.subnet, 40)
}

resource "alta_static_route" "lab_return" {
  name      = "Lab return path"
  type      = "interface"
  network   = "198.18.41.0/24"
  interface = alta_vlan.lab.network
}

resource "alta_switch_port" "lab_uplink" {
  port         = 4
  tagged_vlans = [alta_vlan.lab.vlan_id]
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: providerBlockWithSite() + config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("alta_vlan.lab", "network", "lan_40"),
					resource.TestCheckResourceAttr("alta_vlan.lab", "interface", "br-lan_40"),
					resource.TestCheckResourceAttr("alta_vlan.lab", "gateway", "198.18.40.1"),
					resource.TestCheckResourceAttr("alta_vlan.lab", "subnet", "198.18.40.0/24"),
					// The reservation's address came from the network, not from a second
					// copy of the subnet.
					resource.TestCheckResourceAttr("alta_dhcp_reservation.printer", "ip", "198.18.40.40"),
					resource.TestCheckResourceAttr("alta_static_route.lab_return", "interface", "lan_40"),
					resource.TestCheckResourceAttr("alta_switch_port.lab_uplink", "tagged_vlans.0", "40"),
					func(*terraform.State) error {
						c, err := h.config()
						if err != nil {
							return err
						}
						for _, v := range *c.VLANs {
							if v.ID == 40 && v.RouterIP == "198.18.40.1/24" {
								return nil
							}
						}
						return fmt.Errorf("the cloud does not hold the composed network: %+v", *c.VLANs)
					},
				),
			},
			{Config: providerBlockWithSite() + config, PlanOnly: true},
		},
	})
}
