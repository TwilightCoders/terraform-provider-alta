resource "alta_port_forward" "game" {
  site_id   = "aBcDeFgHiJkLmNoPqRsTu"
  device_id = "0a1b2c3d4e5f"

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
  site_id   = "aBcDeFgHiJkLmNoPqRsTu"
  device_id = "0a1b2c3d4e5f"

  description = "Office desktops"
  protocols   = ["tcp"]
  zone_in     = "wan"
  zone_out    = "lan"
  source      = { address = "203.0.113.0/24" }
  destination = { port = "33890-33899" }
  translation = { address = "192.0.2.20", port = "3389" }
}
