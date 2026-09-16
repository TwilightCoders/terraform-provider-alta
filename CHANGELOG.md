# Changelog

## 0.2.0 — 2026-09-16

The first tagged release. Configuration is now written as individual resources — one
route, one VLAN, one rule — each owning its own object and leaving the rest of its
collection alone, rather than only as one whole-router resource.

### Breaking

- **`device_id` is gone from site-scoped resources.** A route, a VLAN, a NAT rule, a
  filter rule and a client's fixed address belong to the site, not to one of its devices.
  Only `alta_switch_port` and `alta_router_config` still name hardware. State written
  before this carries an attribute the schema no longer knows, so those resources must be
  removed from state and re-imported.
- **Import ids lost the parts that carried no information.** `<site>/<id>` where it was
  `<site>/<device>/<id>`, and the site may be omitted entirely when the provider names it.
  `alta_switch_port` keeps three parts because a port really does belong to a device.
- **An id nobody chooses is known after apply, not shown at plan.** Generating it during
  planning produced a different value when a dependency forced a re-plan, which Terraform
  rejects as an inconsistent plan. It only appeared once resources referred to each other.

### Build

- install an untagged checkout as 0.1.0
- what the registry needs to publish a release
- changelog configuration

### Changes

- empty provider skeleton
- 🌱
- authenticate to Alta's Cognito pool with SRP
- Alta management API client
- map router configuration onto cloud documents
- commit-confirmed gate for the router over SSH
- gated commit-confirmed transaction engine
- keep cloud order for unordered collections
- in-memory Alta API for tests
- alta_router_config resource
- read only the state word from the gate state file
- name the provider alta to match its resource prefix
- stub md5sum only where the host lacks it
- describe the Alta API from evidence, and keep it checked
- check the compile mapping against the running configuration
- manage the router's own hooks as a resource
- the router has no install(1)
- manage router files, and own the hook loader as one
- a missing hook loader is drift, not a warning
- dial the router one connection at a time
- write static routes the way the portal does
- one route as its own resource
- the cloud drops keys it has no field for
- record that a hook ran, where it can be read
- the rest of the site, one object at a time
- say the site once, and only name hardware that is hardware
- describe a network once and derive the rest

### Documentation

- design proposal for Route10 transport, persistence and risk model
- replace on-device reconcile loops with declarative and event-driven hooks
- cloud-first design; add May 11 compiler fixture
- gated commit-confirmed transaction as the write model
- record Phase 0 back-port results and add post-push fixture
- registry documentation, examples and usage
- how a release is signed and cut

### Tests

- the harness provider is the provider

