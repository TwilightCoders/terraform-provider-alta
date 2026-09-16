# terraform-provider-alta — Design

How the provider manages Alta Labs routers (Route10 and family), and why it is built
the way it is.

## 1. How an Alta router gets its configuration

```
L0  Alta cloud objects (site, devices, clients)
      │  server-side compile, pushed to the router by its cloud agent (rc)
L1  /cfg/config.json + /cfg/hash.txt       persistent, replaced wholesale by every push
      │  `cfg` regenerates UCI from scratch
L2  /etc/config/*                          tmpfs: rebuilt on every boot and every push
      │
L3  /cfg/post-cfg.sh                       the router's only local extension point
```

Consequences:

- **The cloud is the source of truth.** Editing `/etc` does not survive a reboot. Editing
  `/cfg/config.json` survives a reboot, but the next portal save or cloud push discards it.
  Only changes made to the cloud objects are durable, and only they show in the portal.
- `hash.txt` is the md5 of the configuration as the cloud last pushed it. When
  `md5(config.json) != hash.txt`, the router carries local edits that a push will destroy.
- Every cloud write triggers a push, and every push restarts networking on the router.

## 2. Transport

**Alta cloud API** (primary). `manage.alta.inc/api/*`, authenticated with the Cognito
user pool behind the portal (SRP, no AWS SDK). The API is unpublished; it was mapped from
the portal's own requests.

| Read | |
|---|---|
| `GET /api/site?id=` | site document: `firewall`, `vlans`, `routes`, … |
| `GET /api/site/state?id=` | devices (`portsCfg`, `services`, …) and clients (`config.ip`, …) |
| `GET /api/site/audit?id=` | audit trail with author |

| Write | |
|---|---|
| `POST /api/site {id, <key>}` | replaces one top-level site key |
| `POST /api/device/edit {id, <key>}` | replaces one device field |
| `POST /api/client/edit {siteid, id, …}` | client record fields, including fixed IP |

There is no version field: the last writer wins on a whole key.

**SSH to the router** (secondary). Used only to gate, verify and roll back pushes, and for
device extensions the portal cannot express. The host key is pinned. No local HTTP
configuration API exists on the router.

## 3. Mapping configuration to cloud objects

`internal/routerconfig` is pure: it overlays desired configuration onto working copies of
the cloud documents and diffs them into the minimal set of writes, with a pre-image for each.

- Fields the provider does not model are preserved.
- Equivalent values are left byte-for-byte alone (`443` vs `"443"`), so unchanged
  configuration never writes.
- Sections are authoritative. Collections keyed by id keep their cloud order; firewall
  rules keep the given order.

| Section | Cloud object |
|---|---|
| `port_forwards` | `site.firewall.nat.rules[]` |
| `firewall_rules` | `site.firewall.firewall.rules[]` |
| `vlans` | `site.vlans[]` (network, DHCP pool, DNS servers) |
| `static_routes` | `site.routes[]` |
| `switch_ports` | `device.portsCfg.ports{}` |
| `dhcp_reservations` | `clients[].config.ip` |

## 4. Every change is one gated, commit-confirmed transaction

Writing through the cloud means a partial change reaches the router as soon as it is
saved. If it breaks connectivity, whatever is driving Terraform may lose the network needed
to finish or undo it. So changes are staged while the router is not listening:

```
PREPARE  guard in-sync · baseline probes · snapshot · journal · pause the cloud agent
STAGE    apply every cloud write; the router receives nothing
COMMIT   arm the router's rollback timer · resume the agent · one push arrives
CONFIRM  probes show no regression against the baseline → disarm
ROLLBACK otherwise the router restores its snapshot on its own, agent left paused
REPAIR   revert the cloud from the journal's pre-images · resume the agent
```

- No partial configuration ever reaches the router.
- Rollback needs nothing off the router: the timer disarms only when told to.
- The journal lives on the router, so a later run from any machine can finish an
  interrupted transaction before doing anything else.
- The DNS probe resolves a random name under a wildcard record, so a cached answer can
  never pass.

Terraform has no end-of-apply hook, so the transaction boundary is the resource: one
`alta_router_config` per router, one transaction per apply.

## 5. Safety rails

- **Local edits guard:** no write while `md5(config.json) != hash.txt`.
- **Concurrency:** apply re-reads the cloud and refuses if managed sections changed since
  the last refresh.
- **`read_only`:** refuses any plan that writes to the cloud; import and plan stay possible.
- **Delete** removes the resource from state only.
- **Cleanup** runs even if the apply is interrupted; an unreachable router is never
  released before it has rolled back.

## 6. Device extensions

Some router behaviour has no cloud representation (for example firewall details of
third-party VPN scripts). These belong in small, idempotent hooks that ride extension
points the router already has — `post-cfg.sh`, OpenWrt hotplug — rather than periodic
reconcile loops. Most reversions on these routers trace to state kept in `/etc`, which is
lost at every reboot; deterministic replay at boot and push removes the need for loops.

## 7. The observed API description

`api/schema.json` is the single record of what the API looks like, kept honest by tests
rather than by discipline:

- **Provenance** names the portal bundle (by hash) and router firmware it was observed
  from, so "is this still true?" is answerable.
- **Object shapes are fitted** from captured responses, never hand-asserted. Objects whose
  captures are reduced for privacy are augmented rather than narrowed; objects never yet
  seen are marked, and fitting fails if instances appear.
- **Collections** record identity, ordering and whether the provider rewrites them wholesale.
- **The compile mapping** records how each cloud field reaches the device, with dated
  observations and an explicit status, including `defect` where the cloud stores a value
  and the compiler ignores it. That mapping is the part that repeatedly costs days when it
  lives only in someone's memory.

Conformance runs against the fixtures in unit tests and, optionally, against a live site
read-only, so an API change fails a test instead of an apply.

One observation point is not enough. A field the cloud stores, echoes back and never
delivers looks correct from the API alone; only the configuration the router is running
disproves it. The compile checks therefore read that configuration and verify each rule
still behaves as recorded — including reporting a defect that has since been fixed, so a
workaround is dropped rather than carried indefinitely.

## 8. Testing

- Cognito SRP is pinned by a test vector shared with an independent implementation.
- `routerconfig` is tested against anonymised fixtures captured before and after a real
  change, including a replay that must reproduce exactly the writes that were made.
- The router scripts run end to end under `sh` against a stub layout, including the
  rollback timer firing.
- The resource is tested with Terraform against an in-memory fake of the API.
- Live tests (`TF_ACC=1`) read a real site and router and never write.
