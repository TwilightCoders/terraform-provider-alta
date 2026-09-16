package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cloudtest"
	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
)

// forwards is the collection alta_port_forward writes into.
func (h *harness) forwards() ([]routerconfig.PortForward, error) {
	c, err := h.config()
	if err != nil {
		return nil, err
	}
	return *c.PortForwards, nil
}

func (h *harness) forwardIDs() ([]string, error) {
	forwards, err := h.forwards()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(forwards))
	for _, f := range forwards {
		ids = append(ids, f.ID)
	}
	return ids, nil
}

// portForwardID is the id the tests give the forward they own, so an assertion about the
// cloud can name it.
const portForwardID = "SipFwd"

func portForwardConfig(port string) string {
	return providerBlock(false) + fmt.Sprintf(`
resource "alta_port_forward" "sip" {
  site_id   = %q
  device_id = %q
  id        = %q

  description = "SIP"
  protocols   = ["udp"]
  ip_version  = "ipv4"
  zone_in     = "wan"
  zone_out    = "lan"
  destination = { port = %q }
  translation = { address = "192.0.2.10", port = "5060" }
}
`, cloudtest.SiteID, cloudtest.DeviceID, portForwardID, port)
}

// TestPortForwardLifecycle covers the loop the whole provider is judged on: create,
// refresh clean, change, refresh clean, destroy.
func TestPortForwardLifecycle(t *testing.T) {
	h := newHarness(t)
	const name = "alta_port_forward.sip"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: portForwardConfig("5060"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "destination.port", "5060"),
					func(*terraform.State) error {
						forwards, err := h.forwards()
						if err != nil {
							return err
						}
						last := forwards[len(forwards)-1]
						if last.ID != portForwardID || last.Translation.Address != "192.0.2.10" {
							return fmt.Errorf("cloud forwards = %+v", forwards)
						}
						return nil
					},
				),
			},
			{Config: portForwardConfig("5060"), PlanOnly: true},
			{
				Config: portForwardConfig("5061"),
				Check:  resource.TestCheckResourceAttr(name, "destination.port", "5061"),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     cloudtest.SiteID + "/" + cloudtest.DeviceID + "/" + portForwardID,
				ImportStateVerify: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			ids, err := h.forwardIDs()
			if err != nil {
				return err
			}
			for _, id := range ids {
				if id == portForwardID {
					return fmt.Errorf("destroy left the forward in the cloud: %v", ids)
				}
			}
			return nil
		},
	})
}

// TestPortForwardLeavesOtherForwardsAlone is the difference between a single-item resource
// and the whole-router one: writing a key replaces the whole array, so the forwards the
// portal already holds must survive this resource's create and its destroy.
func TestPortForwardLeavesOtherForwardsAlone(t *testing.T) {
	h := newHarness(t)
	before, err := h.forwardIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) == 0 {
		t.Fatal("the fixture has no port forwards to leave alone")
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: portForwardConfig("5060"),
			Check: func(*terraform.State) error {
				return sameIDs(h.forwardIDs, append(append([]string{}, before...), portForwardID))
			},
		}},
		CheckDestroy: func(*terraform.State) error {
			return sameIDs(h.forwardIDs, before)
		},
	})
}

func TestPortForwardRefusedWhenReadOnly(t *testing.T) {
	h := newHarness(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: regexp.MustCompile(`read_only = false`).ReplaceAllString(
				portForwardConfig("5060"), "read_only = true"),
			ExpectError: regexp.MustCompile(`read_only`),
		}},
	})
}
