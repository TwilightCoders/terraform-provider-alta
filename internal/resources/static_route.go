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
	_ resource.ResourceWithConfigure   = (*StaticRoute)(nil)
	_ resource.ResourceWithImportState = (*StaticRoute)(nil)
	_ resource.ResourceWithModifyPlan  = (*StaticRoute)(nil)
)

// StaticRoute is the alta_static_route resource: one route in site.routes.
type StaticRoute struct {
	element[routerconfig.StaticRoute]
}

// NewStaticRoute returns the resource.
func NewStaticRoute() resource.Resource {
	return &StaticRoute{element: element[routerconfig.StaticRoute]{in: staticRouteCollection}}
}

var staticRouteCollection = collection[routerconfig.StaticRoute]{
	label: "static route",
	list:  func(c routerconfig.Config) []routerconfig.StaticRoute { return deref(c.StaticRoutes) },
	store: func(c *routerconfig.Config, v []routerconfig.StaticRoute) { c.StaticRoutes = &v },
	id:    func(r routerconfig.StaticRoute) string { return r.ID },
}

// staticRouteResourceModel is the flat form of staticRouteModel: the framework's state
// reflection has no notion of embedded structs, so the fields are repeated and the
// mapping to the domain type stays shared.
type staticRouteResourceModel struct {
	SiteID    types.String `tfsdk:"site_id"`
	DeviceID  types.String `tfsdk:"device_id"`
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Type      types.String `tfsdk:"type"`
	Network   types.String `tfsdk:"network"`
	NextHop   types.String `tfsdk:"next_hop"`
	Interface types.String `tfsdk:"interface"`
	Metric    types.Int64  `tfsdk:"metric"`
}

func (m staticRouteResourceModel) route() routerconfig.StaticRoute {
	return staticRouteModel{
		Name: m.Name, Type: m.Type, Network: m.Network,
		NextHop: m.NextHop, Interface: m.Interface, Metric: m.Metric,
	}.route(m.ID.ValueString())
}

// withRoute copies what the cloud holds into the model, leaving the identity alone.
func (m staticRouteResourceModel) withRoute(r routerconfig.StaticRoute) staticRouteResourceModel {
	f := staticRouteModelOf(r)
	m.Name, m.Type, m.Network = f.Name, f.Type, f.Network
	m.NextHop, m.Interface, m.Metric = f.NextHop, f.Interface, f.Metric
	return m
}

func (r *StaticRoute) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_static_route"
}

func (r *StaticRoute) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One static route, written through the Alta cloud so the portal shows it.\n\n" +
			"Routes that exist in the portal and not in Terraform are left alone: this resource owns its own route " +
			"and nothing else. The change is applied as one gated, commit-confirmed transaction — a single push to " +
			"the router, rolled back automatically unless the health probes pass.",
		Attributes: with(siteAttributes(
			"Alta route id. Generated the way the portal generates one when not set."), staticRouteAttributes()),
	}
}

func (r *StaticRoute) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *StaticRoute) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.refuseUnwritable(req, resp)
	if req.Plan.Raw.IsNull() || resp.Diagnostics.HasError() {
		return
	}
	// An id the user did not choose is known only at apply, which would leave every plan
	// unreadable; generating it here keeps the plan concrete.
	var id types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, idPath, &id)...)
	if id.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, idPath, types.StringValue(newAltaID()))...)
	}
}

func (r *StaticRoute) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan staticRouteResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	if plan.ID.IsUnknown() || plan.ID.ValueString() == "" {
		plan.ID = types.StringValue(newAltaID())
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *StaticRoute) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan staticRouteResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *StaticRoute) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state staticRouteResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	doc, err := r.document(ctx, state.SiteID.ValueString(), state.DeviceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading static route", err.Error())
		return
	}
	route, ok := r.find(doc, state.ID.ValueString())
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state.withRoute(route))...)
}

func (r *StaticRoute) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state staticRouteResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	if err := r.drop(ctx, state.SiteID.ValueString(), state.DeviceID.ValueString(), state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Removing static route", err.Error())
	}
}

// ImportState adopts a route the portal already holds.
func (r *StaticRoute) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	site, device, id, ok := importParts(req.ID, &resp.Diagnostics, "route")
	if !ok {
		return
	}
	doc, err := r.document(ctx, site, device)
	if err != nil {
		resp.Diagnostics.AddError("Reading static route", err.Error())
		return
	}
	route, found := r.find(doc, id)
	if !found {
		resp.Diagnostics.AddError("No such route", "The site has no route with id "+id+".")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, staticRouteResourceModel{
		SiteID:   types.StringValue(site),
		DeviceID: types.StringValue(device),
		ID:       types.StringValue(id),
	}.withRoute(route))...)
}

func (r *StaticRoute) write(ctx context.Context, plan staticRouteResourceModel, state stateSetter, diags *diag.Diagnostics) {
	route := plan.route()
	if err := route.Validate(); err != nil {
		diags.AddError("Invalid static route", err.Error())
		return
	}
	if err := r.put(ctx, plan.SiteID.ValueString(), plan.DeviceID.ValueString(), route); err != nil {
		diags.AddError("Writing static route", err.Error())
		return
	}
	diags.Append(state.Set(ctx, plan)...)
}
