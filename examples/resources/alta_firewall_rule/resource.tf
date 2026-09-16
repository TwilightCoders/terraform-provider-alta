resource "alta_firewall_rule" "wireguard" {
  site_id   = "aBcDeFgHiJkLmNoPqRsTu"
  device_id = "0a1b2c3d4e5f"

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
  site_id   = "aBcDeFgHiJkLmNoPqRsTu"
  device_id = "0a1b2c3d4e5f"

  description = "Keep the guest network off the LAN"
  action      = "DROP"
  zone_in     = "v1zone"
  zone_out    = "lan"
  destination = { address = "192.0.2.0/24" }

  depends_on = [alta_firewall_rule.wireguard]
}
