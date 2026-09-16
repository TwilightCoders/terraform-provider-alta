resource "alta_firewall_rule" "wireguard" {
  description = "Allow WireGuard"
  action      = "ACCEPT"
  protocols   = ["udp"]
  ip_version  = "ipv4"
  zone_in     = "wan"
  destination = { port = "51820" }
}

# Rules are evaluated in the order the site holds them, and a new rule is appended after
# the rules already there. depends_on is what puts this one after the rule above.
resource "alta_firewall_rule" "guest_to_lan" {
  description = "Keep the guest network off the LAN"
  action      = "DROP"
  zone_in     = "v1zone"
  zone_out    = "lan"
  destination = { address = "192.0.2.0/24" }

  depends_on = [alta_firewall_rule.wireguard]
}

# The site is the provider's unless a rule names its own, for a configuration that spans
# more than one.
resource "alta_firewall_rule" "branch_guest_to_lan" {
  site_id = "aBcDeFgHiJkLmNoPqRsTu"

  description = "Keep the branch guest network off its LAN"
  action      = "DROP"
  zone_in     = "v1zone"
  zone_out    = "lan"
  destination = { address = "198.51.100.0/24" }
}
