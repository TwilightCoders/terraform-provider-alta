resource "alta_vlan" "iot" {
  site_id   = "aBcDeFgHiJkLmNoPqRsTu"
  device_id = "0a1b2c3d4e5f"
  vlan_id   = 30

  name         = "IoT"
  router_ip    = "203.0.113.1/24"
  pool_size    = 243
  reserved_ips = 10
  domain_name  = "lan"
  dns_servers  = ["192.0.2.10", "192.0.2.1"]
  isolation    = true
  mdns         = true
}
