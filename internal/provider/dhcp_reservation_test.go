package provider

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud/cloudtest"
	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
)

const dhcpReservationName = "alta_dhcp_reservation.printer"

// fixtureReservations are the reservations the recorded site already holds. None of them
// belongs to Terraform, so all three must survive whatever a resource does to its own.
var fixtureReservations = map[string]string{
	"02:00:00:00:00:18": "192.0.2.20",
	"02:00:00:00:00:19": "198.51.100.20",
	"02:00:00:00:00:1b": "192.0.2.10",
}

// vlanClient is the fixture client carrying both a reservation and a client VLAN
// assignment, which the portal sets separately and a reservation must never disturb.
const vlanClient = "02:00:00:00:00:1b"

func dhcpReservationConfig(mac, ip string) string {
	return providerBlock(false) + fmt.Sprintf(`
resource "alta_dhcp_reservation" "printer" {
  site_id = %q

  mac = %q
  ip  = %q
}
`, cloudtest.SiteID, mac, ip)
}

// TestDHCPReservationLifecycle covers create, refresh clean, change, import and destroy
// for a client the site has never seen, which the cloud has to be given a record for.
func TestDHCPReservationLifecycle(t *testing.T) {
	h := newHarness(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: dhcpReservationConfig(reservedMAC, "192.0.2.40"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(dhcpReservationName, "id", cloudtest.SiteID+"/"+reservedMAC),
					h.expectReservations(alsoReserved("192.0.2.40")),
				),
			},
			{Config: dhcpReservationConfig(reservedMAC, "192.0.2.40"), PlanOnly: true},
			{
				Config: dhcpReservationConfig(reservedMAC, "192.0.2.41"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(dhcpReservationName, "ip", "192.0.2.41"),
					h.expectReservations(alsoReserved("192.0.2.41")),
				),
			},
			{
				ResourceName:      dhcpReservationName,
				ImportState:       true,
				ImportStateId:     cloudtest.SiteID + "/" + reservedMAC,
				ImportStateVerify: true,
			},
		},
		// Destroy takes back the address and nothing else: the site is left with exactly
		// the reservations it started with.
		CheckDestroy: h.expectReservations(fixtureReservations),
	})
}

// TestDHCPReservationLeavesOtherReservationsAlone is the difference between a single-item
// resource and the whole-router one: writing reservations is authoritative over every
// client at once, so the ones Terraform does not own must survive create and destroy.
func TestDHCPReservationLeavesOtherReservationsAlone(t *testing.T) {
	h := newHarness(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: dhcpReservationConfig(reservedMAC, "192.0.2.40"),
			Check:  h.expectReservations(alsoReserved("192.0.2.40")),
		}},
		CheckDestroy: h.expectReservations(fixtureReservations),
	})
}

// TestDHCPReservationKeepsTheClientVLAN pins the correctness note this resource exists
// under: a reservation is the address on a client record and nothing else. The record also
// carries the client's VLAN assignment, which the portal sets separately, and taking the
// reservation away must not take that with it.
func TestDHCPReservationKeepsTheClientVLAN(t *testing.T) {
	h := newHarness(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: dhcpReservationConfig(vlanClient, "192.0.2.11"),
			Check: resource.ComposeTestCheckFunc(
				h.expectClientConfig(vlanClient, map[string]string{"ip": "192.0.2.11", "vlan": "2"}),
				// A client with a VLAN but no reservation is not this resource's business.
				h.expectClientConfig("02:00:00:00:00:1c", map[string]string{"ip": "", "vlan": "2"}),
			),
		}},
		CheckDestroy: resource.ComposeTestCheckFunc(
			h.expectClientConfig(vlanClient, map[string]string{"ip": "", "vlan": "2"}),
			h.expectClientConfig("02:00:00:00:00:1c", map[string]string{"ip": "", "vlan": "2"}),
		),
	})
}

// TestDHCPReservationDefaultsToTheProviderSite is the short form the identities exist for:
// the site is named once on the provider, and a reservation that says nothing about where
// the client lives lands there anyway — including in the id, which is built from the site.
func TestDHCPReservationDefaultsToTheProviderSite(t *testing.T) {
	h := newHarness(t)
	config := providerBlockWithSite(false) + fmt.Sprintf(`
resource "alta_dhcp_reservation" "printer" {
  mac = %q
  ip  = "192.0.2.40"
}
`, reservedMAC)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(dhcpReservationName, "site_id", cloudtest.SiteID),
					resource.TestCheckResourceAttr(dhcpReservationName, "id", cloudtest.SiteID+"/"+reservedMAC),
					h.expectReservations(alsoReserved("192.0.2.40")),
				),
			},
			{Config: config, PlanOnly: true},
			{
				ResourceName:      dhcpReservationName,
				ImportState:       true,
				ImportStateId:     reservedMAC,
				ImportStateVerify: true,
			},
		},
		CheckDestroy: h.expectReservations(fixtureReservations),
	})
}

func TestDHCPReservationRefusedWhenReadOnly(t *testing.T) {
	h := newHarness(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: regexp.MustCompile(`read_only = false`).ReplaceAllString(
				dhcpReservationConfig("02:00:00:aa:bb:cc", "192.0.2.40"), "read_only = true"),
			ExpectError: regexp.MustCompile(`read_only`),
		}},
	})
}

// TestDHCPReservationNeedsACanonicalMAC keeps the resource's identity in one notation:
// the cloud renders client ids lowercase and colon-separated, and anything else would
// silently address a second reservation for the same client.
func TestDHCPReservationNeedsACanonicalMAC(t *testing.T) {
	h := newHarness(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config:      dhcpReservationConfig("02:00:00:AA:BB:CC", "192.0.2.40"),
			ExpectError: regexp.MustCompile(`lowercase, colon-separated`),
		}},
	})
}

// reservedMAC is the client these tests own; the fixture reserves others.
const reservedMAC = "02:00:00:aa:bb:cc"

// alsoReserved is the fixture's reservations plus the one under test, which is what the
// cloud should hold while this resource exists.
func alsoReserved(ip string) map[string]string {
	want := maps.Clone(fixtureReservations)
	want[reservedMAC] = ip
	return want
}

func (h *harness) expectReservations(want map[string]string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := h.reservations()
		if err != nil {
			return err
		}
		held := make(map[string]string, len(got))
		for _, r := range got {
			held[r.MAC] = r.IP
		}
		if !maps.Equal(held, want) {
			return fmt.Errorf("cloud reservations = %v, want %v", held, want)
		}
		return nil
	}
}

// expectClientConfig asserts fields of a raw client record, "" meaning the field is gone.
// Only the record shows what a write did to the fields this provider does not model.
func (h *harness) expectClientConfig(reservedMAC string, want map[string]string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		config, err := h.clientConfig(reservedMAC)
		if err != nil {
			return err
		}
		for key, value := range want {
			got := ""
			if raw, ok := config[key]; ok && raw != nil {
				got = fmt.Sprint(raw)
			}
			if got != value {
				return fmt.Errorf("client %s config.%s = %q, want %q", reservedMAC, key, got, value)
			}
		}
		return nil
	}
}

// reservations is the collection alta_dhcp_reservation writes into.
func (h *harness) reservations() ([]routerconfig.DHCPReservation, error) {
	c, err := h.config()
	if err != nil {
		return nil, err
	}
	return *c.DHCPReservations, nil
}

func (h *harness) clientConfig(reservedMAC string) (cloud.Object, error) {
	id, err := routerconfig.ClientID(reservedMAC)
	if err != nil {
		return nil, err
	}
	state, err := h.api.Client().State(context.Background(), cloudtest.SiteID)
	if err != nil {
		return nil, err
	}
	record, ok := state.Client(id)
	if !ok {
		return nil, fmt.Errorf("the cloud has no client %s", reservedMAC)
	}
	config, _ := record["config"].(cloud.Object)
	return config, nil
}
