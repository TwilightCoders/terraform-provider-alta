package routerconfig

import "github.com/TwilightCoders/terraform-provider-alta-labs/internal/cloud"

// Endpoint is an address/port pair, used for rule sources, destinations and NAT
// translations. Ports may be single ("443") or ranges ("50000-50100").
type Endpoint struct {
	Address string
	Port    string
}

func decodeEndpoint(o cloud.Object, key string) Endpoint {
	e, _ := o[key].(cloud.Object)
	return Endpoint{Address: asString(e["address"]), Port: asString(e["port"])}
}

func encodeEndpoint(o cloud.Object, key string, e Endpoint) {
	c := child(o, key)
	putString(c, "address", e.Address)
	putPort(c, "port", e.Port)
	pruneEmpty(o, key)
}

// PortForward is a destination NAT rule (site.firewall.nat.rules, type "destination").
type PortForward struct {
	ID          string
	Description string
	Protocols   []string
	IPVersion   string
	ZoneIn      string
	ZoneOut     string
	Source      Endpoint
	Destination Endpoint
	Translation Endpoint
}

var portForwards = listCodec[PortForward]{
	path: []string{"firewall", "nat", "rules"},
	id:   func(f PortForward) string { return f.ID },
	decode: func(o cloud.Object) PortForward {
		return PortForward{
			ID:          asString(o["id"]),
			Description: asString(o["description"]),
			Protocols:   asStrings(o["protocol"]),
			IPVersion:   asString(o["ipVersion"]),
			ZoneIn:      asString(o["zoneIn"]),
			ZoneOut:     asString(o["zoneOut"]),
			Source:      decodeEndpoint(o, "source"),
			Destination: decodeEndpoint(o, "destination"),
			Translation: decodeEndpoint(o, "translation"),
		}
	},
	encode: func(f PortForward, o cloud.Object) {
		putString(o, "id", f.ID)
		putString(o, "type", "destination")
		putString(o, "description", f.Description)
		putStringSet(o, "protocol", f.Protocols)
		putString(o, "ipVersion", f.IPVersion)
		putString(o, "zoneIn", f.ZoneIn)
		putString(o, "zoneOut", f.ZoneOut)
		encodeEndpoint(o, "source", f.Source)
		encodeEndpoint(o, "destination", f.Destination)
		encodeEndpoint(o, "translation", f.Translation)
	},
}

// FirewallRule is a filter rule (site.firewall.firewall.rules). Order is significant.
type FirewallRule struct {
	ID          string
	Description string
	Action      string
	Protocols   []string
	IPVersion   string
	ICMPTypes   []string
	ZoneIn      string
	ZoneOut     string
	Source      Endpoint
	Destination Endpoint
	Limit       string
}

var firewallRules = listCodec[FirewallRule]{
	path:    []string{"firewall", "firewall", "rules"},
	ordered: true,
	id:      func(r FirewallRule) string { return r.ID },
	decode: func(o cloud.Object) FirewallRule {
		return FirewallRule{
			ID:          asString(o["id"]),
			Description: asString(o["description"]),
			Action:      asString(o["action"]),
			Protocols:   asStrings(o["protocol"]),
			IPVersion:   asString(o["ipVersion"]),
			ICMPTypes:   asStrings(o["icmpType"]),
			ZoneIn:      asString(o["zoneIn"]),
			ZoneOut:     asString(o["zoneOut"]),
			Source:      decodeEndpoint(o, "source"),
			Destination: decodeEndpoint(o, "destination"),
			Limit:       asString(o["limit"]),
		}
	},
	encode: func(r FirewallRule, o cloud.Object) {
		putString(o, "id", r.ID)
		putString(o, "description", r.Description)
		putString(o, "action", r.Action)
		putStringSet(o, "protocol", r.Protocols)
		putString(o, "ipVersion", r.IPVersion)
		putStringSet(o, "icmpType", r.ICMPTypes)
		putString(o, "zoneIn", r.ZoneIn)
		putString(o, "zoneOut", r.ZoneOut)
		encodeEndpoint(o, "source", r.Source)
		encodeEndpoint(o, "destination", r.Destination)
		putString(o, "limit", r.Limit)
	},
}
