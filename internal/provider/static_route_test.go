package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cloudtest"
)

// routeID is the route these tests own; the fixture holds no routes of its own.
const routeID = "vpnret"

func staticRouteConfig(network string) string {
	return providerBlock(false) + fmt.Sprintf(`
resource "alta_static_route" "vpnret" {
  site_id   = %q
  device_id = %q
  id        = %q

  name     = "Return path"
  type     = "next-hop"
  network  = %q
  next_hop = "192.0.2.10"
}
`, cloudtest.SiteID, cloudtest.DeviceID, routeID, network)
}

// TestStaticRouteLifecycle covers the loop the whole provider is judged on: create,
// refresh clean, change, refresh clean, destroy.
func TestStaticRouteLifecycle(t *testing.T) {
	h := newHarness(t)
	const name = "alta_static_route.vpnret"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: staticRouteConfig("198.18.20.0/28"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "network", "198.18.20.0/28"),
					func(*terraform.State) error {
						routes, err := h.routes()
						if err != nil {
							return err
						}
						if len(routes) != 1 || routes[0].ID != "vpnret" {
							return fmt.Errorf("cloud routes = %+v", routes)
						}
						return nil
					},
				),
			},
			{Config: staticRouteConfig("198.18.20.0/28"), PlanOnly: true},
			{
				Config: staticRouteConfig("198.18.21.0/28"),
				Check:  resource.TestCheckResourceAttr(name, "network", "198.18.21.0/28"),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     cloudtest.SiteID + "/" + cloudtest.DeviceID + "/vpnret",
				ImportStateVerify: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			routes, err := h.routes()
			if err != nil {
				return err
			}
			if len(routes) != 0 {
				return fmt.Errorf("destroy left %d route(s) in the cloud", len(routes))
			}
			return nil
		},
	})
}

// TestStaticRouteLeavesOtherRoutesAlone is the difference between a single-item resource
// and the whole-router one: writing a key replaces the whole array, so a route this
// resource does not own must survive its create and its destroy.
func TestStaticRouteLeavesOtherRoutesAlone(t *testing.T) {
	h := newHarness(t)
	h.seedRoute("keepme", "198.18.99.0/24")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: staticRouteConfig("198.18.20.0/28"),
			Check: func(*terraform.State) error {
				routes, err := h.routes()
				if err != nil {
					return err
				}
				if len(routes) != 2 {
					return fmt.Errorf("cloud routes = %+v, want the seeded one kept", routes)
				}
				return nil
			},
		}},
		CheckDestroy: func(*terraform.State) error {
			routes, err := h.routes()
			if err != nil {
				return err
			}
			if len(routes) != 1 || routes[0].ID != "keepme" {
				return fmt.Errorf("destroy did not leave the unmanaged route alone: %+v", routes)
			}
			return nil
		},
	})
}

func TestStaticRouteRefusedWhenReadOnly(t *testing.T) {
	h := newHarness(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: regexp.MustCompile(`read_only = false`).ReplaceAllString(
				staticRouteConfig("198.18.20.0/28"), "read_only = true"),
			ExpectError: regexp.MustCompile(`read_only`),
		}},
	})
}
