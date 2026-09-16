package provider

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cloudtest"
)

// TestDevicesDataSource covers the question every configuration starts with: what is the
// device id? Nobody should have to read a MAC address out of the portal by hand.
func TestDevicesDataSource(t *testing.T) {
	site, state := cloudtest.Current(t)
	state.Devices = []cloud.Object{
		{"id": "0a1b2c3d4e5f", "name": "Router", "type": json.Number("5"), "model": "10", "version": "1.5g"},
		{"id": "aa00bb11cc22", "name": "Switch", "type": json.Number("3"), "model": "S16", "version": "2.0a"},
		{"id": "0000aaaa1111", "name": "Ceiling AP", "type": json.Number("2"), "model": "AP6", "version": "3.1b"},
	}
	h := &harness{api: cloudtest.NewServer(t, site, state)}
	h.tx = &fakeTransactor{cloud: h.api.Client()}

	const name = "data.alta_devices.routers"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: providerBlock(false) + fmt.Sprintf(`
data "alta_devices" "routers" {
  site_id = %q
  kind    = "router"
}
`, cloudtest.SiteID),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "devices.#", "1"),
					resource.TestCheckResourceAttr(name, "devices.0.id", "0a1b2c3d4e5f"),
					resource.TestCheckResourceAttr(name, "devices.0.kind", "router"),
					resource.TestCheckResourceAttr(name, "devices.0.model", "10"),
				),
			},
			{
				// Without a filter every adopted device is listed, ordered by id so the
				// list does not move under a configuration that indexes into it.
				Config: providerBlock(false) + fmt.Sprintf(`
data "alta_devices" "routers" {
  site_id = %q
}
`, cloudtest.SiteID),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "devices.#", "3"),
					resource.TestCheckResourceAttr(name, "devices.0.id", "0000aaaa1111"),
					resource.TestCheckResourceAttr(name, "devices.0.kind", "ap"),
					resource.TestCheckResourceAttr(name, "devices.1.kind", "router"),
					resource.TestCheckResourceAttr(name, "devices.2.kind", "switch"),
				),
			},
		},
	})
}
