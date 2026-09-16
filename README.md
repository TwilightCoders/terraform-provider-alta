# Terraform Provider for Alta Labs

Manage Alta Labs routers (Route10 and family) with Terraform, **through the Alta cloud**,
so the portal always shows what Terraform applied.

> **Status: pre-alpha.** `alta_router_config` is implemented and tested against recorded
> fixtures and read-only against a live Route10. It has not yet applied a change to a
> real router.

Design: [docs/DESIGN.md](docs/DESIGN.md). In short:

- Port forwards, firewall rules, VLANs, routes, switch ports and DHCP reservations are
  written to the Alta cloud, which compiles and pushes the router's configuration.
- Every apply is one **gated, commit-confirmed transaction**: the router's cloud agent
  is paused while changes are staged, a single push is released, and the router rolls
  itself back unless health probes pass and the provider confirms.
- SSH to the router is used to gate, verify and roll back, and for the few device
  extensions the portal has no concept of.

## Usage

```terraform
provider "alta" {
  read_only = true # import and plan with a guarantee of no writes

  ssh = {
    host                 = "192.0.2.1"
    host_key_fingerprint = "SHA256:…" # ssh-keyscan <host> | ssh-keygen -lf -
  }
}

resource "alta_router_config" "router" {
  site_id   = "…"
  device_id = "…"
  # port_forwards, firewall_rules, vlans, static_routes, switch_ports, dhcp_reservations
}

# For the few behaviours the cloud has no concept of. Both touch the router only,
# never the cloud, so neither triggers a configuration push.
resource "alta_device_file" "post_cfg" {
  path    = "/cfg/post-cfg.sh"
  content = file("post-cfg.sh")
  mode    = "0755"
}

resource "alta_device_hook" "example" {
  name      = "example"
  interface = "wg0"
  script    = "…"
}
```

Credentials come from `ALTA_LABS_EMAIL` and `ALTA_LABS_PASSWORD`. Import an existing router
with `terraform import alta_router_config.router <site_id>/<device_id>`; a plan straight after
import should show no changes. Full reference: [docs/index.md](docs/index.md) and
[docs/resources/router_config.md](docs/resources/router_config.md).

## The API description

Alta publishes no API schema, so [`api/schema.json`](api/schema.json) records what was
observed: endpoints extracted from the portal bundle named in its provenance, object
shapes **fitted from captured responses** rather than asserted, how each collection
behaves when written, and a compile mapping from cloud fields to what the router ends up
running — including the places where Alta's own compiler silently drops a field.

It is evidence, not contract, so it is re-checked rather than trusted:

```bash
make schema            # refit object shapes from the captured fixtures
make portal-endpoints  # re-extract endpoints from the live portal bundle (read-only)
go test ./internal/apischema                       # fixtures still conform
TF_ACC=1 go test ./internal/apischema -run Live    # a real site and router still agree (read-only)
```

Drift in Alta's API then shows up as a failing test instead of a surprise at apply time.

The live checks use two observation points, because one is not enough: the cloud's own
read-back cannot show whether a value it stores ever reaches a router. So the compile
mapping is checked against the configuration the router is actually running, which also
reports a recorded defect that has been **fixed** — a prompt to drop the workaround rather
than carry it forever.

## Requirements

| Component | Version |
|---|---|
| Alta Labs router | Route10, firmware 1.5g or later |
| Terraform | 1.14 or later |
| Go (development) | pinned in `go.mod`; fetched automatically by the `go` command |

## Development

Everything runs through `make`. Run `make help` for the full list.

```bash
make build        # bin/terraform-provider-alta
make test         # unit tests
make lint         # golangci-lint (pinned in tools/go.mod)
make docs         # regenerate docs/ with tfplugindocs
make install      # install into the local Terraform plugin mirror
make dev-override # print a ~/.terraformrc dev_overrides block
```

## License

[MIT](LICENSE)
