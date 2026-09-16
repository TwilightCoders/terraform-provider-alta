# A device is identified by its MAC address. Find it rather than pasting it.
data "alta_devices" "routers" {
  kind = "router"
}

# Most configuration belongs to the site, so it needs no device at all. The ones that
# configure the hardware itself can take the id from here, or from the provider block.
resource "alta_switch_port" "uplink" {
  device_id = data.alta_devices.routers.devices[0].id

  port         = 5
  native_vlan  = 2
  tagged_vlans = [2, 30]
}
