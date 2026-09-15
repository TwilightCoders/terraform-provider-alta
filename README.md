# Terraform Provider for Alta Labs

Manage Alta Labs routers (Route10 and family) with Terraform, **through the Alta cloud**,
so the portal always shows what Terraform applied.

> **Status: pre-alpha.** Under active construction. No resources are available yet.

Design: [docs/DESIGN.md](docs/DESIGN.md). In short:

- Port forwards, firewall rules, VLANs, routes, switch ports and DHCP reservations are
  written to the Alta cloud, which compiles and pushes the router's configuration.
- Every apply is one **gated, commit-confirmed transaction**: the router's cloud agent
  is paused while changes are staged, a single push is released, and the router rolls
  itself back unless health probes pass and the provider confirms.
- SSH to the router is used to gate, verify and roll back, and for the few device
  extensions the portal has no concept of.

## Requirements

| Component | Version |
|---|---|
| Alta Labs router | Route10, firmware 1.5g or later |
| Terraform | 1.14 or later |
| Go (development) | pinned in `go.mod`; fetched automatically by the `go` command |

## Development

Everything runs through `make`. Run `make help` for the full list.

```bash
make build        # bin/terraform-provider-alta-labs
make test         # unit tests
make lint         # golangci-lint (pinned in tools/go.mod)
make docs         # regenerate docs/ with tfplugindocs
make install      # install into the local Terraform plugin mirror
make dev-override # print a ~/.terraformrc dev_overrides block
```

## License

[MIT](LICENSE)
