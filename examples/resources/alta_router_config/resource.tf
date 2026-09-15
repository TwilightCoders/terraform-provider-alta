resource "alta_router_config" "route10" {
  site_id   = "aBcDeFgHiJkLmNoPqRsTu"
  device_id = "0a1b2c3d4e5f"

  port_forwards = {
    game = {
      description = "Game server"
      protocols   = ["udp"]
      ip_version  = "ipv4"
      zone_in     = "wan"
      zone_out    = "lan"
      destination = { port = "8100" }
      translation = { address = "192.0.2.10", port = "8100" }
    }
  }

  vlans = {
    "3" = {
      name         = "IoT"
      router_ip    = "203.0.113.1/24"
      pool_size    = 243
      reserved_ips = 10
      domain_name  = "lan"
      dns_servers  = ["192.0.2.10", "192.0.2.1"]
    }
  }

  static_routes = {
    vpnret = {
      name     = "Return path"
      type     = "next-hop"
      network  = "198.18.20.0/28"
      next_hop = "192.0.2.10"
    }
  }

  switch_ports = {
    "2" = { tagged_vlans = [1, 2, 3] }
  }

  dhcp_reservations = {
    "02:00:00:aa:bb:cc" = "192.0.2.10"
  }
}
