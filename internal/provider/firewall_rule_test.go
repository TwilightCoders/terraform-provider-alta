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

// firewallRules is the collection alta_firewall_rule writes into. Its order is the
// router's evaluation order, so tests compare it as a sequence and not as a set.
func (h *harness) firewallRules() ([]routerconfig.FirewallRule, error) {
	c, err := h.config()
	if err != nil {
		return nil, err
	}
	return *c.FirewallRules, nil
}

func (h *harness) firewallRuleIDs() ([]string, error) {
	rules, err := h.firewallRules()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// firewallRuleID is the id the tests give the rule they own, so an assertion about the
// cloud can name it.
const firewallRuleID = "VpnAcc"

func firewallRuleConfig(port string) string {
	return providerBlock(false) + fmt.Sprintf(`
resource "alta_firewall_rule" "vpn" {
  site_id = %q
  id      = %q

  description = "Allow WireGuard"
  action      = "ACCEPT"
  protocols   = ["udp"]
  ip_version  = "ipv4"
  zone_in     = "wan"
  destination = { port = %q }
}
`, cloudtest.SiteID, firewallRuleID, port)
}

// TestFirewallRuleLifecycle covers the loop the whole provider is judged on: create,
// refresh clean, change, refresh clean, destroy.
func TestFirewallRuleLifecycle(t *testing.T) {
	h := newHarness(t)
	const name = "alta_firewall_rule.vpn"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: firewallRuleConfig("51820"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "destination.port", "51820"),
					func(*terraform.State) error {
						rules, err := h.firewallRules()
						if err != nil {
							return err
						}
						last := rules[len(rules)-1]
						if last.ID != firewallRuleID || last.Action != "ACCEPT" {
							return fmt.Errorf("cloud rules = %+v", rules)
						}
						return nil
					},
				),
			},
			{Config: firewallRuleConfig("51820"), PlanOnly: true},
			{
				Config: firewallRuleConfig("51821"),
				Check:  resource.TestCheckResourceAttr(name, "destination.port", "51821"),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     cloudtest.SiteID + "/" + firewallRuleID,
				ImportStateVerify: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			ids, err := h.firewallRuleIDs()
			if err != nil {
				return err
			}
			for _, id := range ids {
				if id == firewallRuleID {
					return fmt.Errorf("destroy left the rule in the cloud: %v", ids)
				}
			}
			return nil
		},
	})
}

// TestFirewallRuleLeavesOtherRulesAlone is the difference between a single-item resource
// and the whole-router one: writing a key replaces the whole array, so the rules the
// portal already holds must survive this resource's create and its destroy.
func TestFirewallRuleLeavesOtherRulesAlone(t *testing.T) {
	h := newHarness(t)
	before, err := h.firewallRuleIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) == 0 {
		t.Fatal("the fixture has no firewall rules to leave alone")
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: firewallRuleConfig("51820"),
			Check: func(*terraform.State) error {
				return sameIDs(h.firewallRuleIDs, append(append([]string{}, before...), firewallRuleID))
			},
		}},
		CheckDestroy: func(*terraform.State) error {
			return sameIDs(h.firewallRuleIDs, before)
		},
	})
}

func firewallRulePairConfig(guestAction string) string {
	return providerBlock(false) + fmt.Sprintf(`
resource "alta_firewall_rule" "vpn" {
  site_id = %[1]q
  id      = %[3]q

  description = "Allow WireGuard"
  action      = "ACCEPT"
  protocols   = ["udp"]
  ip_version  = "ipv4"
  zone_in     = "wan"
  destination = { port = "51820" }
}

resource "alta_firewall_rule" "guest" {
  site_id = %[1]q
  id      = "GstDrp"

  description = "Guest to LAN"
  action      = %[2]q
  zone_in     = "v1zone"
  zone_out    = "lan"
  destination = { address = "192.0.2.0/24" }
}
`, cloudtest.SiteID, guestAction, firewallRuleID)
}

// TestFirewallRuleKeepsEvaluationOrder is the risk a per-item resource carries on an
// ordered array: the position of a rule is the only thing that says when it is evaluated.
// Creating rules must append after the rules already there without disturbing them, and
// changing a rule must leave it where it is rather than moving it to the end.
func TestFirewallRuleKeepsEvaluationOrder(t *testing.T) {
	h := newHarness(t)
	before, err := h.firewallRuleIDs()
	if err != nil {
		t.Fatal(err)
	}
	// The two rules are created in one apply, which fixes no order between them; what the
	// cloud settled on is the baseline the update must not change.
	var afterCreate []string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{
			{
				Config: firewallRulePairConfig("DROP"),
				Check: func(*terraform.State) error {
					ids, err := h.firewallRuleIDs()
					if err != nil {
						return err
					}
					if len(ids) != len(before)+2 {
						return fmt.Errorf("rule ids = %v, want the fixture's plus two", ids)
					}
					if err := sameIDs(func() ([]string, error) { return ids[:len(before)], nil }, before); err != nil {
						return fmt.Errorf("creating rules reordered the rules already there: %w", err)
					}
					afterCreate = ids
					return nil
				},
			},
			{
				Config: firewallRulePairConfig("REJECT"),
				Check: func(*terraform.State) error {
					return sameIDs(h.firewallRuleIDs, afterCreate)
				},
			},
		},
	})
}

// TestFirewallRuleDefaultsToTheProviderSite is the short form the identities exist for: the
// site is named once on the provider, and a rule that says nothing about where it belongs
// lands there anyway, with the site in its state rather than "known after apply".
func TestFirewallRuleDefaultsToTheProviderSite(t *testing.T) {
	h := newHarness(t)
	const name = "alta_firewall_rule.vpn"
	config := providerBlockWithSite(false) + fmt.Sprintf(`
resource "alta_firewall_rule" "vpn" {
  id = %q

  description = "Allow WireGuard"
  action      = "ACCEPT"
  protocols   = ["udp"]
  ip_version  = "ipv4"
  zone_in     = "wan"
  destination = { port = "51820" }
}
`, firewallRuleID)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.defaultingFactories(),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(name, "site_id", cloudtest.SiteID),
					func(*terraform.State) error {
						rules, err := h.firewallRules()
						if err != nil {
							return err
						}
						last := rules[len(rules)-1]
						if last.ID != firewallRuleID {
							return fmt.Errorf("cloud rules = %+v", rules)
						}
						return nil
					},
				),
			},
			{Config: config, PlanOnly: true},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateId:     firewallRuleID,
				ImportStateVerify: true,
			},
		},
	})
}

func TestFirewallRuleRefusedWhenReadOnly(t *testing.T) {
	h := newHarness(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: h.factories(),
		Steps: []resource.TestStep{{
			Config: regexp.MustCompile(`read_only = false`).ReplaceAllString(
				firewallRuleConfig("51820"), "read_only = true"),
			ExpectError: regexp.MustCompile(`read_only`),
		}},
	})
}
