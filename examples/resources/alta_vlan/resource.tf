# A network belongs to the site, so it takes the provider's site_id and names no device.
resource "alta_vlan" "lab" {
  vlan_id = 40

  name         = "Lab"
  router_ip    = "198.18.40.1/24"
  pool_size    = 243
  reserved_ips = 10
  domain_name  = "lan"
  dns_servers  = ["192.0.2.10", "192.0.2.1"]
}

# Describe the network once. Everything that follows from it — the subnet its hosts sit
# in, the router's own name for it, the number a port tags — is derived rather than
# written out a second time and kept in step by hand.
resource "alta_dhcp_reservation" "printer" {
  mac = "02:00:00:aa:bb:cc"
  ip  = cidrhost(alta_vlan.lab.subnet, 40)
}

resource "alta_static_route" "lab_return" {
  name      = "Lab return path"
  type      = "interface"
  network   = "198.18.41.0/24"
  interface = alta_vlan.lab.network # "lan_40"
}

resource "alta_switch_port" "lab_uplink" {
  port         = 4
  tagged_vlans = [alta_vlan.lab.vlan_id]
}

# Adopt a network the portal already has by its number:
#
#   terraform import alta_vlan.lab 40
#
# Name the site in the import id only for a site other than the provider's:
#
#   terraform import alta_vlan.lab "$SITE_ID/40"
