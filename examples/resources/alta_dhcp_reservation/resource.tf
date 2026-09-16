# Pin the media server, because the port forwards that point at it name an address rather
# than a client. The reservation is only that address: whichever VLAN the portal has the
# client on stays as it is, here and when this resource is destroyed.
resource "alta_dhcp_reservation" "media_server" {
  mac = "02:00:00:aa:bb:cc"
  ip  = "192.0.2.10"
}

# The site is the provider's unless a reservation names its own, for a configuration that
# spans more than one.
resource "alta_dhcp_reservation" "branch_printer" {
  site_id = "aBcDeFgHiJkLmNoPqRsTu"

  mac = "02:00:00:aa:bb:dd"
  ip  = "198.51.100.10"
}

# Adopt a reservation the portal already holds. The site is the provider's unless the
# import id names one:
#
#   terraform import alta_dhcp_reservation.media_server "02:00:00:aa:bb:cc"
#   terraform import alta_dhcp_reservation.branch_printer "$SITE_ID/02:00:00:aa:bb:dd"
