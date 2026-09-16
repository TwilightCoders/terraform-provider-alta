package resources

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
)

var (
	_ resource.ResourceWithConfigure   = (*SwitchPort)(nil)
	_ resource.ResourceWithImportState = (*SwitchPort)(nil)
	_ resource.ResourceWithModifyPlan  = (*SwitchPort)(nil)
)

// SwitchPort is the alta_switch_port resource: the VLAN membership of one physical port.
type SwitchPort struct {
	element[routerconfig.SwitchPort]
}

// NewSwitchPort returns the resource.
func NewSwitchPort() resource.Resource {
	return &SwitchPort{element: element[routerconfig.SwitchPort]{in: switchPortCollection}}
}

var switchPortCollection = collection[routerconfig.SwitchPort]{
	label: "switch port",
	list:  func(c routerconfig.Config) []routerconfig.SwitchPort { return deref(c.SwitchPorts) },
	store: func(c *routerconfig.Config, v []routerconfig.SwitchPort) { c.SwitchPorts = &v },
	id:    func(p routerconfig.SwitchPort) string { return strconv.FormatInt(p.Port, 10) },
}

// switchPortID is the description of id and the format it is built from.
const switchPortID = "`<site_id>/<device_id>/<port>`."

// switchPortResourceModel is the Terraform state of one port. The framework's reflection
// has no notion of embedded structs, so the shared attributes are repeated here.
type switchPortResourceModel struct {
	SiteID      types.String `tfsdk:"site_id"`
	DeviceID    types.String `tfsdk:"device_id"`
	ID          types.String `tfsdk:"id"`
	Port        types.Int64  `tfsdk:"port"`
	NativeVLAN  types.Int64  `tfsdk:"native_vlan"`
	AllVLANs    types.Bool   `tfsdk:"all_vlans"`
	TaggedVLANs *[]int64     `tfsdk:"tagged_vlans"`
}

func (m switchPortResourceModel) switchPort() routerconfig.SwitchPort {
	return routerconfig.SwitchPort{
		Port:        m.Port.ValueInt64(),
		NativeVLAN:  m.NativeVLAN.ValueInt64Pointer(),
		AllVLANs:    m.AllVLANs.ValueBool(),
		TaggedVLANs: deref(m.TaggedVLANs),
	}
}

// withSwitchPort copies what the cloud holds into the model, leaving the identity alone.
func (m switchPortResourceModel) withSwitchPort(p routerconfig.SwitchPort) switchPortResourceModel {
	m.NativeVLAN = types.Int64PointerValue(p.NativeVLAN)
	m.AllVLANs = types.BoolValue(p.AllVLANs)
	m.TaggedVLANs = nonEmpty(p.TaggedVLANs)
	return m
}

// key is the port as the collection identifies it.
func (m switchPortResourceModel) key() string {
	return strconv.FormatInt(m.Port.ValueInt64(), 10)
}

func (m switchPortResourceModel) address() types.String {
	return types.StringValue(m.SiteID.ValueString() + "/" + m.DeviceID.ValueString() + "/" + m.key())
}

func (r *SwitchPort) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_switch_port"
}

func (r *SwitchPort) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The VLAN membership of one physical switch port, written through the Alta cloud so " +
			"the portal shows it.\n\n" +
			"Only the attributes below are managed. Other ports are left alone, and so are the port settings this " +
			"resource does not model — speed, EEE, PoE, name and WAN role all survive a change here. The change is " +
			"applied as one gated, commit-confirmed transaction — a single push to the router, rolled back " +
			"automatically unless the health probes pass.\n\n" +
			"Destroying this resource removes it from Terraform state only. A port is physical: it cannot be " +
			"deleted, and clearing its VLANs would cut off whatever is plugged into it, so the port keeps the " +
			"configuration it was last given.",
		Attributes: switchPortResourceAttributes(),
	}
}

func switchPortResourceAttributes() map[string]schema.Attribute {
	attributes := with(siteAttributes(switchPortID), switchPortAttributes())
	// siteAttributes offers an id for items whose id the user chooses or the provider
	// generates. A port already has a name the hardware gave it, so id only addresses it.
	attributes["id"] = schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: switchPortID,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attributes["port"] = schema.Int64Attribute{
		Required:            true,
		MarkdownDescription: "Physical port, numbered as the portal numbers it.",
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
	}
	return attributes
}

func (r *SwitchPort) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *SwitchPort) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.refuseUnwritable(req, resp)
	if req.Plan.Raw.IsNull() || resp.Diagnostics.HasError() {
		return
	}
	// The id is derived, so resolving it here keeps the plan concrete rather than leaving
	// every create showing "known after apply".
	var model switchPortResourceModel
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("site_id"), &model.SiteID)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("device_id"), &model.DeviceID)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("port"), &model.Port)...)
	if resp.Diagnostics.HasError() || model.SiteID.IsUnknown() || model.DeviceID.IsUnknown() || model.Port.IsUnknown() {
		return // Create resolves it instead, once the identity is known
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, idPath, model.address())...)
}

func (r *SwitchPort) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan switchPortResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *SwitchPort) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan switchPortResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *SwitchPort) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state switchPortResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	doc, err := r.document(ctx, state.SiteID.ValueString(), state.DeviceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading switch port", err.Error())
		return
	}
	port, ok := r.find(doc, state.key())
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state.withSwitchPort(port))...)
}

// Delete stops managing the port and changes nothing on the router. There is no way to
// remove a port, and the alternatives — clearing its VLANs, or restoring whatever it held
// before Terraform adopted it — both reconfigure a live link on the way out of the
// configuration, which is the last moment anyone is watching.
func (r *SwitchPort) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state switchPortResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.AddWarning("Switch port left as it is", fmt.Sprintf(
		"Port %d was removed from Terraform state only. A physical port cannot be deleted, so it keeps the VLAN "+
			"membership it was last given; change it in the portal if that is not what you want.", state.Port.ValueInt64()))
}

// ImportState adopts a port the router already has.
func (r *SwitchPort) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	site, device, key, ok := importParts(req.ID, &resp.Diagnostics, "port")
	if !ok {
		return
	}
	number, err := strconv.ParseInt(key, 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id",
			fmt.Sprintf("%q is not a port number. Use <site_id>/<device_id>/<port>, e.g. %s/%s/3.", key, site, device))
		return
	}
	doc, err := r.document(ctx, site, device)
	if err != nil {
		resp.Diagnostics.AddError("Reading switch port", err.Error())
		return
	}
	port, found := r.find(doc, key)
	if !found {
		resp.Diagnostics.AddError("No such port", fmt.Sprintf("The router has no port %d.", number))
		return
	}
	model := switchPortResourceModel{
		SiteID:   types.StringValue(site),
		DeviceID: types.StringValue(device),
		Port:     types.Int64Value(number),
	}
	model.ID = model.address()
	resp.Diagnostics.Append(resp.State.Set(ctx, model.withSwitchPort(port))...)
}

func (r *SwitchPort) write(ctx context.Context, plan switchPortResourceModel, state stateSetter, diags *diag.Diagnostics) {
	plan.ID = plan.address()
	if err := r.put(ctx, plan.SiteID.ValueString(), plan.DeviceID.ValueString(), plan.switchPort()); err != nil {
		diags.AddError("Writing switch port", err.Error())
		return
	}
	diags.Append(state.Set(ctx, plan)...)
}
