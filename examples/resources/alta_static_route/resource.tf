# A return path for a subnet that lives behind another host on the LAN. Without it,
# traffic from that range reaches the router and has nowhere to go back to.
resource "alta_static_route" "container_return" {
  site_id   = var.alta_site_id
  device_id = var.alta_device_id

  name     = "Container return path"
  type     = "next-hop"
  network  = "198.18.20.0/28"
  next_hop = "192.0.2.10"
}

# The id is chosen for you, the way the portal chooses one. Set it explicitly to adopt
# a route that already exists:
#
#   terraform import alta_static_route.container_return "$SITE_ID/$DEVICE_ID/aB3dEf"
