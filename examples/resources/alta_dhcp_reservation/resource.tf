# Pin the media server, because the port forwards that point at it name an address rather
# than a client. The reservation is only that address: whichever VLAN the portal has the
# client on stays as it is, here and when this resource is destroyed.
resource "alta_dhcp_reservation" "media_server" {
  site_id   = "aBcDeFgHiJkLmNoPqRsTu"
  device_id = "0a1b2c3d4e5f"

  mac = "02:00:00:aa:bb:cc"
  ip  = "192.0.2.10"
}
