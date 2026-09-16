# The uplink to a second switch: untagged on the LAN, with the isolated network tagged so
# it reaches the far side of the link.
#
# Only the VLANs are managed. Speed, EEE and PoE stay whatever the portal has them set to,
# and destroying this resource leaves the port exactly as it is — a port is physical, so
# there is nothing to remove.
resource "alta_switch_port" "uplink" {
  site_id   = "aBcDeFgHiJkLmNoPqRsTu"
  device_id = "0a1b2c3d4e5f"
  port      = 3

  native_vlan  = 2
  tagged_vlans = [20]
}
