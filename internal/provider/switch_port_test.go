package provider

import (
	"context"
	"fmt"
	"regexp"
	"slices"
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

const switchPortName = "alta_switch_port.uplink"

// switchPortConfig manages fixture port 3, which arrives tagged for VLANs 2 and 3 with an
// eee setting this provider does not model.
func switchPortConfig(tagged string) string {
	return providerBlock(false) + fmt.Sprintf(`
resource "alta_switch_port" "uplink" {
  site_id   = %q
  device_id = %q
  port      = 3

  native_vlan  = 2
  tagged_vlans = %s
}
`, cloudtest.SiteID, cloudtest.DeviceID, tagged)
}

// switchPortProviderBlock names the identities on the provider, which is where a
// configuration that manages one router puts them. An empty device leaves device_id out,
// so a port has nowhere to resolve one from.
func switchPortProviderBlock(deviceID string) string {
	device := ""
	if deviceID != "" {
		device = fmt.Sprintf("  device_id = %q\n", deviceID)
	}
	return fmt.Sprintf(`
provider "alta" {
  email     = "test"
  password  = "test"
  read_only = false
  site_id   = %q
%s  ssh = {
    host                 = "192.0.2.1"
    host_key_fingerprint = "SHA256:test"
  }
}
`, cloudtest.SiteID, device)
}

// switchPortDefaultedConfig is switchPortConfig with no identity on the resource: both the
// site and the device come from the provider.
func switchPortDefaultedConfig(deviceID string) string {
	return switchPortProviderBlock(deviceID) + `
resource "alta_switch_port" "uplink" {
  port = 3

  native_vlan  = 2
  tagged_vlans = [20]
}
`
}

// switchPortScopedFactories carries the provider's own identities into ProviderData, which
// the shared harness leaves out.
func (h *harness) switchPortScopedFactories() map[string]func() (tfprotov6.ProviderServer, error) {
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

// TestSwitchPortLifecycle covers create, refresh clean, change, import and destroy. The
// destroy check is the one that differs from every other single-item resource: a port is
// physical, so removing the resource must leave the port configured as it was.
func TestSwitchPortLifecycle(t *testing.T) {
	h := newHarness(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: switchPortConfig("[20]"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(switchPortName, "id", cloudtest.SiteID+"/"+cloudtest.DeviceID+"/3"),
					resource.TestCheckResourceAttr(switchPortName, "all_vlans", "false"),
					h.expectPort(routerconfig.SwitchPort{Port: 3, NativeVLAN: vlan(2), TaggedVLANs: []int64{20}}),
				),
			},
			{Config: switchPortConfig("[20]"), PlanOnly: true},
			{
				Config: switchPortConfig("[2, 20]"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(switchPortName, "tagged_vlans.#", "2"),
					h.expectPort(routerconfig.SwitchPort{Port: 3, NativeVLAN: vlan(2), TaggedVLANs: []int64{2, 20}}),
				),
			},
			{
				ResourceName:      switchPortName,
				ImportState:       true,
				ImportStateId:     cloudtest.SiteID + "/" + cloudtest.DeviceID + "/3",
				ImportStateVerify: true,
			},
		},
		CheckDestroy: h.expectPort(routerconfig.SwitchPort{Port: 3, NativeVLAN: vlan(2), TaggedVLANs: []int64{2, 20}}),
	})
}

// TestSwitchPortLeavesTheRestOfTheSwitchAlone is what separates a single-port resource
// from the whole-router one. Writing ports merges rather than replaces, so both the other
// ports and the settings this resource does not model must survive its create and its
// destroy.
func TestSwitchPortLeavesTheRestOfTheSwitchAlone(t *testing.T) {
	h := newHarness(t)
	untouched := resource.ComposeTestCheckFunc(
		h.expectPortFields("5", map[string]string{"name": "NAS", "speed": "auto", "wan": "default1", "eee": "off"}),
		h.expectPort(routerconfig.SwitchPort{Port: 5, NativeVLAN: vlan(2), TaggedVLANs: []int64{2, 20}}),
		h.expectPortFields("1", map[string]string{"mode": "wan", "name": "ONT", "poe": "on"}),
		h.expectPort(routerconfig.SwitchPort{Port: 0, NativeVLAN: vlan(2), AllVLANs: true}),
		h.expectPortCount(6),
	)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: switchPortConfig("[20]"),
			Check: resource.ComposeTestCheckFunc(
				untouched,
				// Port 3's own unmodelled settings survive being managed.
				h.expectPortFields("3", map[string]string{"eee": "off"}),
			),
		}},
		CheckDestroy: untouched,
	})
}

// TestSwitchPortDefaultsToTheProvidersDevice is the point of an optional device_id: a
// configuration that manages one router names the site and the device once, on the
// provider. The import id is the port number alone for the same reason.
func TestSwitchPortDefaultsToTheProvidersDevice(t *testing.T) {
	h := newHarness(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.switchPortScopedFactories(),
		Steps: []resource.TestStep{
			{
				Config: switchPortDefaultedConfig(cloudtest.DeviceID),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(switchPortName, "site_id", cloudtest.SiteID),
					resource.TestCheckResourceAttr(switchPortName, "device_id", cloudtest.DeviceID),
					resource.TestCheckResourceAttr(switchPortName, "id", cloudtest.SiteID+"/"+cloudtest.DeviceID+"/3"),
					h.expectPort(routerconfig.SwitchPort{Port: 3, NativeVLAN: vlan(2), TaggedVLANs: []int64{20}}),
				),
			},
			{Config: switchPortDefaultedConfig(cloudtest.DeviceID), PlanOnly: true},
			{
				ResourceName:      switchPortName,
				ImportState:       true,
				ImportStateId:     "3",
				ImportStateVerify: true,
			},
		},
		CheckDestroy: h.expectPort(routerconfig.SwitchPort{Port: 3, NativeVLAN: vlan(2), TaggedVLANs: []int64{20}}),
	})
}

// TestSwitchPortWithoutADeviceSaysSo is the failure a site-scoped resource cannot have: a
// port is a piece of hardware, so with no device on either the resource or the provider
// there is nothing to configure, and the refusal has to say where to put one.
func TestSwitchPortWithoutADeviceSaysSo(t *testing.T) {
	h := newHarness(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.switchPortScopedFactories(),
		Steps: []resource.TestStep{{
			Config:      switchPortDefaultedConfig(""),
			ExpectError: regexp.MustCompile(`No device for this switch port`),
		}},
	})
}

func TestSwitchPortRefusedWhenReadOnly(t *testing.T) {
	h := newHarness(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: regexp.MustCompile(`read_only = false`).ReplaceAllString(
				switchPortConfig("[20]"), "read_only = true"),
			ExpectError: regexp.MustCompile(`read_only`),
		}},
	})
}

func TestSwitchPortImportNeedsAPortNumber(t *testing.T) {
	h := newHarness(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{Config: switchPortConfig("[20]")},
			{
				ResourceName:  switchPortName,
				ImportState:   true,
				ImportStateId: cloudtest.SiteID + "/" + cloudtest.DeviceID + "/eth3",
				ExpectError:   regexp.MustCompile(`not a port number`),
			},
		},
	})
}

// expectPort asserts one port's VLAN membership as the cloud currently holds it.
func (h *harness) expectPort(want routerconfig.SwitchPort) resource.TestCheckFunc {
	return func(*terraform.State) error {
		ports, err := h.switchPorts()
		if err != nil {
			return err
		}
		for _, p := range ports {
			if p.Port != want.Port {
				continue
			}
			if describePort(p) != describePort(want) {
				return fmt.Errorf("cloud port %d is %s, want %s", want.Port, describePort(p), describePort(want))
			}
			return nil
		}
		return fmt.Errorf("the cloud has no port %d", want.Port)
	}
}

// describePort renders a port's membership so two can be compared and either printed.
// Tagged VLANs are sorted: they reach the cloud from a Terraform set, in no fixed order.
func describePort(p routerconfig.SwitchPort) string {
	native := "the default VLAN"
	if p.NativeVLAN != nil {
		native = fmt.Sprintf("VLAN %d", *p.NativeVLAN)
	}
	if p.AllVLANs {
		return "untagged on " + native + ", tagged for every VLAN"
	}
	tagged := slices.Clone(p.TaggedVLANs)
	slices.Sort(tagged)
	return fmt.Sprintf("untagged on %s, tagged for %v", native, tagged)
}

// vlan names a VLAN as an optional field.
func vlan(id int64) *int64 { return &id }

func (h *harness) expectPortCount(n int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		ports, err := h.switchPorts()
		if err != nil {
			return err
		}
		if len(ports) != n {
			return fmt.Errorf("cloud ports = %+v, want %d of them", ports, n)
		}
		return nil
	}
}

// expectPortFields asserts fields of the raw port object, which is the only way to see
// the settings routerconfig does not model.
func (h *harness) expectPortFields(port string, want map[string]string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		fields, err := h.portFields(port)
		if err != nil {
			return err
		}
		for key, value := range want {
			if got, _ := fields[key].(string); got != value {
				return fmt.Errorf("port %s %s = %v, want %q", port, key, fields[key], value)
			}
		}
		return nil
	}
}

// switchPorts is the collection alta_switch_port writes into.
func (h *harness) switchPorts() ([]routerconfig.SwitchPort, error) {
	c, err := h.config()
	if err != nil {
		return nil, err
	}
	return *c.SwitchPorts, nil
}

func (h *harness) portFields(port string) (cloud.Object, error) {
	state, err := h.api.Client().State(context.Background(), cloudtest.SiteID)
	if err != nil {
		return nil, err
	}
	device, ok := state.Device(cloudtest.DeviceID)
	if !ok {
		return nil, fmt.Errorf("the cloud has no device %s", cloudtest.DeviceID)
	}
	cfg, _ := device["portsCfg"].(cloud.Object)
	ports, _ := cfg["ports"].(cloud.Object)
	fields, ok := ports[port].(cloud.Object)
	if !ok {
		return nil, fmt.Errorf("the cloud has no port %s", port)
	}
	return fields, nil
}
