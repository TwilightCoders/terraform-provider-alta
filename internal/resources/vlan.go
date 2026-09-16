package resources

import (
	"context"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
)

var (
	_ resource.ResourceWithConfigure   = (*VLAN)(nil)
	_ resource.ResourceWithImportState = (*VLAN)(nil)
	_ resource.ResourceWithModifyPlan  = (*VLAN)(nil)
)

// VLAN is the alta_vlan resource: one network in site.vlans.
type VLAN struct {
	element[routerconfig.VLAN]
}

// NewVLAN returns the resource.
func NewVLAN() resource.Resource {
	return &VLAN{element: element[routerconfig.VLAN]{in: vlanCollection}}
}

var vlanCollection = collection[routerconfig.VLAN]{
	label: "VLAN",
	list:  func(c routerconfig.Config) []routerconfig.VLAN { return deref(c.VLANs) },
	store: func(c *routerconfig.Config, v []routerconfig.VLAN) { c.VLANs = &v },
	id:    func(v routerconfig.VLAN) string { return strconv.FormatInt(v.ID, 10) },
}

// vlanIDPath is the network's identity: the VLAN number the router tags with.
var vlanIDPath = path.Root("vlan_id")

// vlanIDDescription documents the framework's id, which is only the number's string form.
const vlanIDDescription = "The VLAN number, as a string."

// vlanResourceModel is the flat form of vlanModel, plus the identity. The framework's
// state reflection has no notion of embedded structs, so the fields are repeated.
type vlanResourceModel struct {
	SiteID      types.String `tfsdk:"site_id"`
	ID          types.String `tfsdk:"id"`
	VLANID      types.Int64  `tfsdk:"vlan_id"`
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

func (m vlanResourceModel) siteRef() types.String { return m.SiteID }

// deviceRef is null: a VLAN belongs to the site, not to one of its devices.
func (m vlanResourceModel) deviceRef() types.String { return types.StringNull() }

func (m vlanResourceModel) vlan() routerconfig.VLAN {
	return routerconfig.VLAN{
		ID: m.VLANID.ValueInt64(), Name: m.Name.ValueString(), RouterIP: m.RouterIP.ValueString(),
		PoolSize: m.PoolSize.ValueInt64Pointer(), ReservedIPs: m.ReservedIPs.ValueInt64Pointer(),
		DNSServers: deref(m.DNSServers), DomainName: m.DomainName.ValueString(),
		DHCP: m.DHCP.ValueBool(), Isolation: m.Isolation.ValueBool(), MDNS: m.MDNS.ValueBoolPointer(),
	}
}

// withVLAN copies what the cloud holds into the model, leaving the identity alone.
func (m vlanResourceModel) withVLAN(v routerconfig.VLAN) vlanResourceModel {
	m.Name, m.RouterIP = optional(v.Name), optional(v.RouterIP)
	m.PoolSize, m.ReservedIPs = types.Int64PointerValue(v.PoolSize), types.Int64PointerValue(v.ReservedIPs)
	m.DNSServers, m.DomainName = nonEmpty(v.DNSServers), optional(v.DomainName)
	m.DHCP, m.Isolation = types.BoolValue(v.DHCP), types.BoolValue(v.Isolation)
	m.MDNS = types.BoolPointerValue(v.MDNS)
	return m
}

func (r *VLAN) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlan"
}

func (r *VLAN) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One network, written through the Alta cloud so the portal shows it.\n\n" +
			"Networks that exist in the portal and not in Terraform are left alone: this resource owns its own " +
			"VLAN and nothing else. The change is applied as one gated, commit-confirmed transaction — a single " +
			"push to the router, rolled back automatically unless the health probes pass.",
		Attributes: with(vlanIdentityAttributes(), vlanAttributes()),
	}
}

// vlanIdentityAttributes are siteAttributes with the VLAN number in place of a generated
// id: the number is what the router tags with, so the user chooses it and the framework's
// id is only its string form.
func vlanIdentityAttributes() map[string]schema.Attribute {
	attributes := siteAttributes(vlanIDDescription)
	// siteAttributes offers an id the user chooses or the provider generates. A VLAN is
	// identified by its number instead, so id only follows from vlan_id.
	attributes["id"] = schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: vlanIDDescription,
	}
	attributes["vlan_id"] = schema.Int64Attribute{
		Required:            true,
		MarkdownDescription: "VLAN number. Changing it is a different network, so the old one is removed and the new one created.",
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
	}
	return attributes
}

func (r *VLAN) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *VLAN) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.refuseUnwritable(req, resp)
	if req.Plan.Raw.IsNull() || resp.Diagnostics.HasError() {
		return
	}
	// The id follows from vlan_id, which the user gives, so it is known at plan rather
	// than left unknown until apply.
	var number types.Int64
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, vlanIDPath, &number)...)
	if number.IsNull() || number.IsUnknown() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, idPath, types.StringValue(vlanCollection.id(routerconfig.VLAN{ID: number.ValueInt64()})))...)
	r.planDefaults(ctx, resp)
}

func (r *VLAN) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vlanResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *VLAN) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan vlanResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *VLAN) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vlanResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	doc, err := r.document(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Reading VLAN", err.Error())
		return
	}
	vlan, ok := r.find(doc, state.ID.ValueString())
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state.withVLAN(vlan))...)
}

func (r *VLAN) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vlanResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	if err := r.drop(ctx, state, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Removing VLAN", err.Error())
	}
}

// ImportState adopts a network the portal already holds.
func (r *VLAN) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	site, number, ok := r.importVLAN(req.ID, &resp.Diagnostics)
	if !ok {
		return
	}
	id := vlanCollection.id(routerconfig.VLAN{ID: number})
	state := vlanResourceModel{
		SiteID: types.StringValue(site),
		ID:     types.StringValue(id),
		VLANID: types.Int64Value(number),
	}
	doc, err := r.document(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Reading VLAN", err.Error())
		return
	}
	vlan, found := r.find(doc, id)
	if !found {
		resp.Diagnostics.AddError("No such VLAN", "The site has no VLAN "+id+".")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state.withVLAN(vlan))...)
}

// importVLAN reads "<vlan_id>" or "<site_id>/<vlan_id>". A network is identified by the
// number the router tags with, so the id half is a number rather than a generated id.
func (r *VLAN) importVLAN(raw string, diags *diag.Diagnostics) (site string, number int64, ok bool) {
	site, id, ok := r.siteScopedImport(raw, diags)
	if !ok {
		return "", 0, false
	}
	number, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		diags.AddError("Invalid import id", "A VLAN is identified by its number, and "+id+" is not one.")
		return "", 0, false
	}
	return site, number, true
}

func (r *VLAN) write(ctx context.Context, plan vlanResourceModel, state stateSetter, diags *diag.Diagnostics) {
	if err := r.put(ctx, plan, plan.vlan()); err != nil {
		diags.AddError("Writing VLAN", err.Error())
		return
	}
	diags.Append(state.Set(ctx, plan)...)
}
