# A network belongs to the site, so it takes the provider's site_id and names no device.
resource "alta_vlan" "iot" {
  vlan_id = 30

  name         = "IoT"
  router_ip    = "203.0.113.1/24"
  pool_size    = 243
  reserved_ips = 10
  domain_name  = "lan"
  dns_servers  = ["192.0.2.10", "192.0.2.1"]
  isolation    = true
  mdns         = true
}

# Adopt a network the portal already has by its number:
#
#   terraform import alta_vlan.iot 30
#
# Set site_id on the resource, and name it in the import id, only for a site other than
# the provider's:
#
#   terraform import alta_vlan.iot "$SITE_ID/30"
