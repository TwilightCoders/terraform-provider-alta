package resources

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
)

// routerConfigModel is the Terraform state of alta_router_config. Each section is a
// pointer so that null (unmanaged) and empty (managed, nothing configured) stay distinct,
// matching routerconfig.Config.
type routerConfigModel struct {
	ID               types.String                 `tfsdk:"id"`
	SiteID           types.String                 `tfsdk:"site_id"`
	DeviceID         types.String                 `tfsdk:"device_id"`
	PortForwards     *map[string]portForwardModel `tfsdk:"port_forwards"`
	FirewallRules    *[]firewallRuleModel         `tfsdk:"firewall_rules"`
	VLANs            *map[string]vlanModel        `tfsdk:"vlans"`
	StaticRoutes     *map[string]staticRouteModel `tfsdk:"static_routes"`
	SwitchPorts      *map[string]switchPortModel  `tfsdk:"switch_ports"`
	DHCPReservations *map[string]string           `tfsdk:"dhcp_reservations"`
}

type endpointModel struct {
	Address types.String `tfsdk:"address"`
	Port    types.String `tfsdk:"port"`
}

type portForwardModel struct {
	Description types.String   `tfsdk:"description"`
	Protocols   []string       `tfsdk:"protocols"`
	IPVersion   types.String   `tfsdk:"ip_version"`
	ZoneIn      types.String   `tfsdk:"zone_in"`
	ZoneOut     types.String   `tfsdk:"zone_out"`
	Source      *endpointModel `tfsdk:"source"`
	Destination *endpointModel `tfsdk:"destination"`
	Translation *endpointModel `tfsdk:"translation"`
}

type firewallRuleModel struct {
	ID          types.String   `tfsdk:"id"`
	Description types.String   `tfsdk:"description"`
	Action      types.String   `tfsdk:"action"`
	Protocols   *[]string      `tfsdk:"protocols"`
	IPVersion   types.String   `tfsdk:"ip_version"`
	ICMPTypes   *[]string      `tfsdk:"icmp_types"`
	ZoneIn      types.String   `tfsdk:"zone_in"`
	ZoneOut     types.String   `tfsdk:"zone_out"`
	Source      *endpointModel `tfsdk:"source"`
	Destination *endpointModel `tfsdk:"destination"`
	Limit       types.String   `tfsdk:"limit"`
}

type vlanModel struct {
	Name        types.String `tfsdk:"name"`
	RouterIP    types.String `tfsdk:"router_ip"`
	PoolSize    types.Int64  `tfsdk:"pool_size"`
	ReservedIPs types.Int64  `tfsdk:"reserved_ips"`
	DNSServers  *[]string    `tfsdk:"dns_servers"`
	DomainName  types.String `tfsdk:"domain_name"`
	DHCP        types.Bool   `tfsdk:"dhcp"`
	Isolation   types.Bool   `tfsdk:"isolation"`
	MDNS        types.Bool   `tfsdk:"mdns"`
}

type staticRouteModel struct {
	Name      types.String `tfsdk:"name"`
	Type      types.String `tfsdk:"type"`
	Network   types.String `tfsdk:"network"`
	NextHop   types.String `tfsdk:"next_hop"`
	Interface types.String `tfsdk:"interface"`
	Metric    types.Int64  `tfsdk:"metric"`
}

type switchPortModel struct {
	NativeVLAN  types.Int64 `tfsdk:"native_vlan"`
	AllVLANs    types.Bool  `tfsdk:"all_vlans"`
	TaggedVLANs *[]int64    `tfsdk:"tagged_vlans"`
}

// allSections returns an import-shaped model: every section present, so a read fills them all.
func (m routerConfigModel) allSections() routerConfigModel {
	m.PortForwards = &map[string]portForwardModel{}
	m.FirewallRules = &[]firewallRuleModel{}
	m.VLANs = &map[string]vlanModel{}
	m.StaticRoutes = &map[string]staticRouteModel{}
	m.SwitchPorts = &map[string]switchPortModel{}
	m.DHCPReservations = &map[string]string{}
	return m
}

// toConfig converts the model to domain configuration.
func (m routerConfigModel) toConfig() (routerconfig.Config, error) {
	var c routerconfig.Config
	var err error
	if c.PortForwards, err = convertMap(m.PortForwards, func(id string, f portForwardModel) (routerconfig.PortForward, error) {
		return routerconfig.PortForward{
			ID: id, Description: f.Description.ValueString(), Protocols: f.Protocols,
			IPVersion: f.IPVersion.ValueString(), ZoneIn: f.ZoneIn.ValueString(), ZoneOut: f.ZoneOut.ValueString(),
			Source: f.Source.toDomain(), Destination: f.Destination.toDomain(), Translation: f.Translation.toDomain(),
		}, nil
	}); err != nil {
		return c, err
	}
	if m.FirewallRules != nil {
		rules := make([]routerconfig.FirewallRule, 0, len(*m.FirewallRules))
		for _, r := range *m.FirewallRules {
			rules = append(rules, routerconfig.FirewallRule{
				ID: r.ID.ValueString(), Description: r.Description.ValueString(), Action: r.Action.ValueString(),
				Protocols: deref(r.Protocols), IPVersion: r.IPVersion.ValueString(), ICMPTypes: deref(r.ICMPTypes),
				ZoneIn: r.ZoneIn.ValueString(), ZoneOut: r.ZoneOut.ValueString(),
				Source: r.Source.toDomain(), Destination: r.Destination.toDomain(), Limit: r.Limit.ValueString(),
			})
		}
		c.FirewallRules = &rules
	}
	if c.VLANs, err = convertMap(m.VLANs, func(key string, v vlanModel) (routerconfig.VLAN, error) {
		id, err := parseKey("vlans", key)
		return routerconfig.VLAN{
			ID: id, Name: v.Name.ValueString(), RouterIP: v.RouterIP.ValueString(),
			PoolSize: v.PoolSize.ValueInt64Pointer(), ReservedIPs: v.ReservedIPs.ValueInt64Pointer(),
			DNSServers: deref(v.DNSServers), DomainName: v.DomainName.ValueString(),
			DHCP: v.DHCP.ValueBool(), Isolation: v.Isolation.ValueBool(), MDNS: v.MDNS.ValueBoolPointer(),
		}, err
	}); err != nil {
		return c, err
	}
	if c.StaticRoutes, err = convertMap(m.StaticRoutes, func(id string, r staticRouteModel) (routerconfig.StaticRoute, error) {
		route := routerconfig.StaticRoute{
			ID: id, Name: r.Name.ValueString(), Type: r.Type.ValueString(), Network: r.Network.ValueString(),
			NextHop: r.NextHop.ValueString(), Interface: r.Interface.ValueString(), Metric: r.Metric.ValueInt64Pointer(),
		}
		return route, route.Validate()
	}); err != nil {
		return c, err
	}
	if c.SwitchPorts, err = convertMap(m.SwitchPorts, func(key string, p switchPortModel) (routerconfig.SwitchPort, error) {
		port, err := parseKey("switch_ports", key)
		return routerconfig.SwitchPort{
			Port: port, NativeVLAN: p.NativeVLAN.ValueInt64Pointer(), AllVLANs: p.AllVLANs.ValueBool(), TaggedVLANs: deref(p.TaggedVLANs),
		}, err
	}); err != nil {
		return c, err
	}
	c.DHCPReservations, err = convertMap(m.DHCPReservations, func(mac, ip string) (routerconfig.DHCPReservation, error) {
		return routerconfig.DHCPReservation{MAC: mac, IP: ip}, nil
	})
	return c, err
}

func parseKey(section, key string) (int64, error) {
	n, err := strconv.ParseInt(key, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: key %q must be a number", section, key)
	}
	return n, nil
}

// withConfig returns m with each section m manages replaced by c's value.
func (m routerConfigModel) withConfig(c routerconfig.Config) routerConfigModel {
	if m.PortForwards != nil {
		m.PortForwards = toMap(*c.PortForwards, func(f routerconfig.PortForward) (string, portForwardModel) {
			return f.ID, portForwardModel{
				Description: optional(f.Description), Protocols: f.Protocols, IPVersion: optional(f.IPVersion),
				ZoneIn: optional(f.ZoneIn), ZoneOut: optional(f.ZoneOut),
				Source: endpointFrom(f.Source), Destination: endpointFrom(f.Destination), Translation: endpointFrom(f.Translation),
			}
		})
	}
	if m.FirewallRules != nil {
		rules := make([]firewallRuleModel, 0, len(*c.FirewallRules))
		for _, r := range *c.FirewallRules {
			rules = append(rules, firewallRuleModel{
				ID: types.StringValue(r.ID), Description: optional(r.Description), Action: optional(r.Action),
				Protocols: nonEmpty(r.Protocols), IPVersion: optional(r.IPVersion), ICMPTypes: nonEmpty(r.ICMPTypes),
				ZoneIn: optional(r.ZoneIn), ZoneOut: optional(r.ZoneOut),
				Source: endpointFrom(r.Source), Destination: endpointFrom(r.Destination), Limit: optional(r.Limit),
			})
		}
		m.FirewallRules = &rules
	}
	if m.VLANs != nil {
		m.VLANs = toMap(*c.VLANs, func(v routerconfig.VLAN) (string, vlanModel) {
			return strconv.FormatInt(v.ID, 10), vlanModel{
				Name: optional(v.Name), RouterIP: optional(v.RouterIP),
				PoolSize: types.Int64PointerValue(v.PoolSize), ReservedIPs: types.Int64PointerValue(v.ReservedIPs),
				DNSServers: nonEmpty(v.DNSServers), DomainName: optional(v.DomainName),
				DHCP: types.BoolValue(v.DHCP), Isolation: types.BoolValue(v.Isolation), MDNS: types.BoolPointerValue(v.MDNS),
			}
		})
	}
	if m.StaticRoutes != nil {
		m.StaticRoutes = toMap(*c.StaticRoutes, func(r routerconfig.StaticRoute) (string, staticRouteModel) {
			return r.ID, staticRouteModel{
				Name: optional(r.Name), Type: optional(r.Type), Network: optional(r.Network),
				NextHop: optional(r.NextHop), Interface: optional(r.Interface), Metric: types.Int64PointerValue(r.Metric),
			}
		})
	}
	if m.SwitchPorts != nil {
		m.SwitchPorts = toMap(*c.SwitchPorts, func(p routerconfig.SwitchPort) (string, switchPortModel) {
			return strconv.FormatInt(p.Port, 10), switchPortModel{
				NativeVLAN: types.Int64PointerValue(p.NativeVLAN), AllVLANs: types.BoolValue(p.AllVLANs), TaggedVLANs: nonEmpty(p.TaggedVLANs),
			}
		})
	}
	if m.DHCPReservations != nil {
		m.DHCPReservations = toMap(*c.DHCPReservations, func(r routerconfig.DHCPReservation) (string, string) {
			return r.MAC, r.IP
		})
	}
	return m
}

func (e *endpointModel) toDomain() routerconfig.Endpoint {
	if e == nil {
		return routerconfig.Endpoint{}
	}
	return routerconfig.Endpoint{Address: e.Address.ValueString(), Port: e.Port.ValueString()}
}

func endpointFrom(e routerconfig.Endpoint) *endpointModel {
	if e == (routerconfig.Endpoint{}) {
		return nil
	}
	return &endpointModel{Address: optional(e.Address), Port: optional(e.Port)}
}

// convertMap converts a keyed section to domain items in key order; nil stays nil.
func convertMap[M, D any](section *map[string]M, convert func(string, M) (D, error)) (*[]D, error) {
	if section == nil {
		return nil, nil //nolint:nilnil // a nil section is unmanaged, which is not an error
	}
	keys := make([]string, 0, len(*section))
	for k := range *section {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	items := make([]D, 0, len(keys))
	for _, k := range keys {
		item, err := convert(k, (*section)[k])
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return &items, nil
}

func toMap[D, M any](items []D, convert func(D) (string, M)) *map[string]M {
	out := make(map[string]M, len(items))
	for _, item := range items {
		k, v := convert(item)
		out[k] = v
	}
	return &out
}

func optional(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func deref[T any](p *[]T) []T {
	if p == nil {
		return nil
	}
	return *p
}

func nonEmpty[T any](s []T) *[]T {
	if len(s) == 0 {
		return nil
	}
	return &s
}
