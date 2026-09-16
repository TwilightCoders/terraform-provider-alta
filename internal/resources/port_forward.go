package resources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
)

var (
	_ resource.ResourceWithConfigure   = (*PortForward)(nil)
	_ resource.ResourceWithImportState = (*PortForward)(nil)
	_ resource.ResourceWithModifyPlan  = (*PortForward)(nil)
)

// PortForward is the alta_port_forward resource: one rule in site.firewall.nat.rules.
type PortForward struct {
	element[routerconfig.PortForward]
}

// NewPortForward returns the resource.
func NewPortForward() resource.Resource {
	return &PortForward{element: element[routerconfig.PortForward]{in: portForwardCollection}}
}

var portForwardCollection = collection[routerconfig.PortForward]{
	label: "port forward",
	list:  func(c routerconfig.Config) []routerconfig.PortForward { return deref(c.PortForwards) },
	store: func(c *routerconfig.Config, v []routerconfig.PortForward) { c.PortForwards = &v },
	id:    func(f routerconfig.PortForward) string { return f.ID },
}

// portForwardResourceModel is the flat form of portForwardModel: the framework's state
// reflection has no notion of embedded structs, so the fields are repeated.
type portForwardResourceModel struct {
	SiteID      types.String   `tfsdk:"site_id"`
	ID          types.String   `tfsdk:"id"`
	Description types.String   `tfsdk:"description"`
	Protocols   []string       `tfsdk:"protocols"`
	IPVersion   types.String   `tfsdk:"ip_version"`
	ZoneIn      types.String   `tfsdk:"zone_in"`
	ZoneOut     types.String   `tfsdk:"zone_out"`
	Source      *endpointModel `tfsdk:"source"`
	Destination *endpointModel `tfsdk:"destination"`
	Translation *endpointModel `tfsdk:"translation"`
}

func (m portForwardResourceModel) siteRef() types.String { return m.SiteID }

// deviceRef is null: a NAT rule belongs to the site, not to one of its devices.
func (m portForwardResourceModel) deviceRef() types.String { return types.StringNull() }

func (m portForwardResourceModel) forward() routerconfig.PortForward {
	return routerconfig.PortForward{
		ID: m.ID.ValueString(), Description: m.Description.ValueString(), Protocols: m.Protocols,
		IPVersion: m.IPVersion.ValueString(), ZoneIn: m.ZoneIn.ValueString(), ZoneOut: m.ZoneOut.ValueString(),
		Source: m.Source.toDomain(), Destination: m.Destination.toDomain(), Translation: m.Translation.toDomain(),
	}
}

// withForward copies what the cloud holds into the model, leaving the identity alone.
func (m portForwardResourceModel) withForward(f routerconfig.PortForward) portForwardResourceModel {
	m.Description, m.Protocols, m.IPVersion = optional(f.Description), f.Protocols, optional(f.IPVersion)
	m.ZoneIn, m.ZoneOut = optional(f.ZoneIn), optional(f.ZoneOut)
	m.Source, m.Destination = endpointFrom(f.Source), endpointFrom(f.Destination)
	m.Translation = endpointFrom(f.Translation)
	return m
}

func (r *PortForward) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_port_forward"
}

func (r *PortForward) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One port forward — a destination NAT rule — written through the Alta cloud so the portal shows it.\n\n" +
			"Forwards that exist in the portal and not in Terraform are left alone: this resource owns its own rule " +
			"and nothing else. The change is applied as one gated, commit-confirmed transaction — a single push to " +
			"the router, rolled back automatically unless the health probes pass.",
		Attributes: with(siteAttributes(
			"Alta rule id. Generated the way the portal generates one when not set."), portForwardAttributes()),
	}
}

func (r *PortForward) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *PortForward) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.planGeneratedID(ctx, req, resp)
}

func (r *PortForward) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan portForwardResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	plan.ID = generatedID(plan.ID)
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *PortForward) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan portForwardResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *PortForward) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state portForwardResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	doc, err := r.document(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Reading port forward", err.Error())
		return
	}
	forward, ok := r.find(doc, state.ID.ValueString())
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state.withForward(forward))...)
}

func (r *PortForward) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state portForwardResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	if err := r.drop(ctx, state, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Removing port forward", err.Error())
	}
}

// ImportState adopts a forward the portal already holds.
func (r *PortForward) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	site, id, ok := r.siteScopedImport(req.ID, &resp.Diagnostics)
	if !ok {
		return
	}
	state := portForwardResourceModel{SiteID: types.StringValue(site), ID: types.StringValue(id)}
	doc, err := r.document(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Reading port forward", err.Error())
		return
	}
	forward, found := r.find(doc, id)
	if !found {
		resp.Diagnostics.AddError("No such port forward", "The site has no NAT rule with id "+id+".")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state.withForward(forward))...)
}

func (r *PortForward) write(ctx context.Context, plan portForwardResourceModel, state stateSetter, diags *diag.Diagnostics) {
	if err := r.put(ctx, plan, plan.forward()); err != nil {
		diags.AddError("Writing port forward", err.Error())
		return
	}
	diags.Append(state.Set(ctx, plan)...)
}
