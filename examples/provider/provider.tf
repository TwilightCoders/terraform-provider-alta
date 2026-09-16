terraform {
  required_providers {
    alta = {
      source = "twilightcoders/alta"
    }
  }
}

# Credentials come from ALTA_LABS_EMAIL and ALTA_LABS_PASSWORD.
provider "alta" {
  # The site every resource belongs to unless it names another, so it is written once
  # rather than on every resource. Defaults to ALTA_LABS_SITE_ID.
  site_id = "aBcDeFgHiJkLmNoPqRsTu"

  # Only resources that configure the hardware itself need a device, such as
  # alta_switch_port. Defaults to ALTA_LABS_DEVICE_ID.
  device_id = "0a1b2c3d4e5f"

  ssh = {
    host                 = "192.0.2.1"
    host_key_fingerprint = "SHA256:AbCdEfGhIjKlMnOpQrStUvWxYz0123456789abcdefg" # ssh-keyscan 192.0.2.1 | ssh-keygen -lf -
  }

  probes = {
    dns_server  = "192.0.2.10"
    dns_name    = "{nonce}.example.net" # any name under a wildcard record
    lan_targets = ["192.0.2.10"]
  }
}
