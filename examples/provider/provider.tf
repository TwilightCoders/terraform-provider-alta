terraform {
  required_providers {
    alta = {
      source = "twilightcoders/alta-labs"
    }
  }
}

# Credentials come from ALTA_LABS_EMAIL and ALTA_LABS_PASSWORD.
provider "alta" {
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
