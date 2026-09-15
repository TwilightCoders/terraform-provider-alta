package resources

import (
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	numericKey = mapvalidator.KeysAre(stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9]+$`), "must be a number"))
	macKey     = mapvalidator.KeysAre(stringvalidator.RegexMatches(regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`),
		"must be a lowercase, colon-separated MAC address"))
)

func optionalString(description string) schema.StringAttribute {
	return schema.StringAttribute{Optional: true, MarkdownDescription: description}
}

func requiredString(description string, validators ...validator.String) schema.StringAttribute {
	return schema.StringAttribute{Required: true, MarkdownDescription: description, Validators: validators}
}

func endpointAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"address": optionalString("Address or CIDR."),
			"port":    optionalString("Port or range, e.g. `443` or `50000-50100`."),
		},
	}
}

func routerConfigSchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "The configuration of one Alta router, written through the Alta cloud so the portal shows it.\n\n" +
			"Each section is optional. An omitted section is not managed; a present section is authoritative, " +
			"so entries that exist in the portal but not in Terraform are removed. Every change is applied as one " +
			"gated, commit-confirmed transaction: one push to the router, rolled back automatically unless health " +
			"probes pass. Destroying this resource only removes it from state; it never erases the router's configuration.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<site_id>/<device_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"site_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Alta site id.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"device_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Router device id: its MAC address, lowercase without separators.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"port_forwards": schema.MapNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Destination NAT rules, keyed by Alta rule id.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"description": optionalString("Name shown in the portal."),
					"protocols": schema.SetAttribute{
						Required: true, ElementType: types.StringType,
						MarkdownDescription: "Protocols to forward.",
					},
					"ip_version":  optionalString("`ipv4`, `ipv6` or `any`."),
					"zone_in":     requiredString("Zone the traffic arrives from, usually `wan`."),
					"zone_out":    optionalString("Zone of the destination host."),
					"source":      endpointAttribute("Restrict the forward to these sources."),
					"destination": endpointAttribute("Address and port the traffic is sent to."),
					"translation": endpointAttribute("Internal address and port it is forwarded to."),
				}},
			},
			"firewall_rules": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Filter rules, in evaluation order.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":          requiredString("Alta rule id."),
					"description": optionalString("Name shown in the portal."),
					"action":      requiredString("`ACCEPT`, `DROP` or `REJECT`.", stringvalidator.OneOf("ACCEPT", "DROP", "REJECT")),
					"protocols": schema.SetAttribute{
						Optional: true, ElementType: types.StringType,
						MarkdownDescription: "Protocols the rule matches; all when omitted.",
					},
					"ip_version": optionalString("`ipv4`, `ipv6` or `any`."),
					"icmp_types": schema.SetAttribute{
						Optional: true, ElementType: types.StringType,
						MarkdownDescription: "ICMP types, for ICMP rules.",
					},
					"zone_in":     optionalString("Zone the traffic arrives from."),
					"zone_out":    optionalString("Zone the traffic is going to."),
					"source":      endpointAttribute("Source match."),
					"destination": endpointAttribute("Destination match."),
					"limit":       optionalString("Rate limit, e.g. `1000/sec`."),
				}},
			},
			"vlans": schema.MapNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Networks, keyed by VLAN id.",
				Validators:          []validator.Map{numericKey},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":      optionalString("Name shown in the portal."),
					"router_ip": optionalString("Router address and prefix, e.g. `203.0.113.1/24`."),
					"pool_size": schema.Int64Attribute{Optional: true, MarkdownDescription: "DHCP pool size."},
					"reserved_ips": schema.Int64Attribute{
						Optional: true, MarkdownDescription: "Addresses at the start of the subnet kept out of the pool.",
					},
					"dns_servers": schema.ListAttribute{
						Optional: true, ElementType: types.StringType,
						MarkdownDescription: "DNS servers handed out by DHCP, in order.",
					},
					"domain_name": optionalString("DHCP domain name."),
					"dhcp": schema.BoolAttribute{
						Optional: true, Computed: true, Default: booldefault.StaticBool(true),
						MarkdownDescription: "Serve DHCP on this network.",
					},
					"isolation": schema.BoolAttribute{
						Optional: true, Computed: true, Default: booldefault.StaticBool(false),
						MarkdownDescription: "Isolate clients from each other.",
					},
					"mdns": schema.BoolAttribute{Optional: true, MarkdownDescription: "Repeat mDNS into this network."},
				}},
			},
			"static_routes": schema.MapNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Static routes, keyed by Alta route id.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":      optionalString("Name shown in the portal."),
					"type":      requiredString("`next-hop`, `interface` or `blackhole`.", stringvalidator.OneOf("next-hop", "interface", "blackhole")),
					"network":   requiredString("Destination CIDR."),
					"next_hop":  optionalString("Gateway, for `next-hop` routes."),
					"interface": optionalString("Outgoing interface."),
					"metric":    schema.Int64Attribute{Optional: true, MarkdownDescription: "Route metric."},
				}},
			},
			"switch_ports": schema.MapNestedAttribute{
				Optional: true,
				MarkdownDescription: "VLAN membership of physical ports, keyed by the portal's port index. " +
					"Listed ports are managed; other ports and port settings such as speed or PoE are left alone.",
				Validators: []validator.Map{numericKey},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"native_vlan": schema.Int64Attribute{Optional: true, MarkdownDescription: "Untagged VLAN; the default VLAN when omitted."},
					"all_vlans": schema.BoolAttribute{
						Optional: true, Computed: true, Default: booldefault.StaticBool(false),
						MarkdownDescription: "Tag every VLAN on this port.",
					},
					"tagged_vlans": schema.SetAttribute{
						Optional: true, ElementType: types.Int64Type,
						MarkdownDescription: "VLANs tagged on this port, when `all_vlans` is false.",
					},
				}},
			},
			"dhcp_reservations": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Fixed addresses: lowercase, colon-separated MAC address to IP address.",
				Validators:          []validator.Map{macKey},
			},
		},
	}
}
