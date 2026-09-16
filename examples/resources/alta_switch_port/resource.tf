# The uplink to a second switch: untagged on the LAN, with the isolated network tagged so
# it reaches the far side of the link.
#
# A port is physical, so this resource belongs to a device. It takes the provider's
# device_id; set device_id here only for a second router in the same site.
#
# Only the VLANs are managed. Speed, EEE and PoE stay whatever the portal has them set to,
# and destroying this resource leaves the port exactly as it is — a port is physical, so
# there is nothing to remove.
resource "alta_switch_port" "uplink" {
  port = 3

  native_vlan  = 2
  tagged_vlans = [20]
}

# Adopt a port the router already has by its number:
#
#   terraform import alta_switch_port.uplink 3
#
# Name a device or a site other than the provider's by putting them in the import id:
#
#   terraform import alta_switch_port.uplink "$DEVICE_ID/3"
#   terraform import alta_switch_port.uplink "$SITE_ID/$DEVICE_ID/3"
