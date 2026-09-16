resource "alta_port_forward" "game" {
  description = "Game server"
  protocols   = ["udp"]
  ip_version  = "ipv4"
  zone_in     = "wan"
  zone_out    = "lan"
  destination = { port = "8100" }
  translation = { address = "192.0.2.10", port = "8100" }
}

# Only reachable from the office, and on a range of ports.
resource "alta_port_forward" "office_rdp" {
  description = "Office desktops"
  protocols   = ["tcp"]
  zone_in     = "wan"
  zone_out    = "lan"
  source      = { address = "203.0.113.0/24" }
  destination = { port = "33890-33899" }
  translation = { address = "192.0.2.20", port = "3389" }
}

# The site is the provider's unless a forward names its own, for a configuration that spans
# more than one.
resource "alta_port_forward" "branch_vpn" {
  site_id = "aBcDeFgHiJkLmNoPqRsTu"

  description = "Branch WireGuard"
  protocols   = ["udp"]
  zone_in     = "wan"
  zone_out    = "lan"
  destination = { port = "51820" }
  translation = { address = "192.0.2.30", port = "51820" }
}
