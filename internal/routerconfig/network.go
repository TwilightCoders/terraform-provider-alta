package routerconfig

import (
	"strconv"
	"strings"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
)

// VLAN is a site network (site.vlans). The cloud compiles it into a bridge, a firewall
// zone member and, unless DHCP is disabled, a DHCP scope.
type VLAN struct {
	ID          int64
	Name        string
	RouterIP    string // CIDR, e.g. 203.0.113.1/24
	PoolSize    *int64
	ReservedIPs *int64
	DNSServers  []string
	DomainName  string
	DHCP        bool
	Isolation   bool
	MDNS        *bool
}

var vlans = listCodec[VLAN]{
	path: []string{"vlans"},
	id:   func(v VLAN) string { return strconv.FormatInt(v.ID, 10) },
	decode: func(o cloud.Object) VLAN {
		id, _ := asInt(o["id"])
		dhcpDisabled, _ := o["disableDHCP"].(bool)
		isolation, _ := o["isolation"].(bool)
		v := VLAN{
			ID:          id,
			Name:        asString(o["notes"]),
			RouterIP:    asString(o["routerIP"]),
			PoolSize:    asOptionalInt(o["poolSize"]),
			ReservedIPs: asOptionalInt(o["reservedIPs"]),
			DNSServers:  splitCSV(o["dnsServers"]),
			DomainName:  asString(o["domainName"]),
			DHCP:        !dhcpDisabled,
			Isolation:   isolation,
		}
		if m, ok := o["mdns"].(bool); ok {
			v.MDNS = &m
		}
		return v
	},
	encode: func(v VLAN, o cloud.Object) {
		putInt(o, "id", &v.ID)
		putString(o, "notes", v.Name)
		putString(o, "routerIP", v.RouterIP)
		putInt(o, "poolSize", v.PoolSize)
		putInt(o, "reservedIPs", v.ReservedIPs)
		putString(o, "dnsServers", strings.Join(v.DNSServers, ","))
		putString(o, "domainName", v.DomainName)
		putFlag(o, "disableDHCP", !v.DHCP)
		putFlag(o, "isolation", v.Isolation)
		if v.MDNS == nil {
			delete(o, "mdns")
		} else {
			o["mdns"] = *v.MDNS
		}
	},
}

// StaticRoute is a site route (site.routes). Type is next-hop, interface or blackhole.
type StaticRoute struct {
	ID        string
	Name      string
	Type      string
	Network   string
	NextHop   string
	Interface string
	Metric    *int64
}

var staticRoutes = listCodec[StaticRoute]{
	path: []string{"routes"},
	id:   func(r StaticRoute) string { return r.ID },
	decode: func(o cloud.Object) StaticRoute {
		return StaticRoute{
			ID:        asString(o["id"]),
			Name:      asString(o["name"]),
			Type:      asString(o["type"]),
			Network:   asString(o["network"]),
			NextHop:   asString(o["nextHop"]),
			Interface: asString(o["interface"]),
			Metric:    asOptionalInt(o["metric"]),
		}
	},
	encode: func(r StaticRoute, o cloud.Object) {
		putString(o, "id", r.ID)
		putString(o, "name", r.Name)
		putString(o, "type", r.Type)
		putString(o, "network", r.Network)
		putString(o, "nextHop", r.NextHop)
		putString(o, "interface", r.Interface)
		putInt(o, "metric", r.Metric)
	},
}
