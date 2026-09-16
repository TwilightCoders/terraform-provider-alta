package resources

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
)

var (
	_ resource.ResourceWithConfigure   = (*DHCPReservation)(nil)
	_ resource.ResourceWithImportState = (*DHCPReservation)(nil)
	_ resource.ResourceWithModifyPlan  = (*DHCPReservation)(nil)
)

// macAddress is the notation a client is named by everywhere in this provider: lowercase
// and colon-separated, which is what the cloud's client ids render back to.
var macAddress = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)

// DHCPReservation is the alta_dhcp_reservation resource: one client's fixed address.
type DHCPReservation struct {
	element[routerconfig.DHCPReservation]
}

// NewDHCPReservation returns the resource.
func NewDHCPReservation() resource.Resource {
	return &DHCPReservation{element: element[routerconfig.DHCPReservation]{in: dhcpReservationCollection}}
}

var dhcpReservationCollection = collection[routerconfig.DHCPReservation]{
	label: "DHCP reservation",
	list:  func(c routerconfig.Config) []routerconfig.DHCPReservation { return deref(c.DHCPReservations) },
	store: func(c *routerconfig.Config, v []routerconfig.DHCPReservation) { c.DHCPReservations = &v },
	id:    func(r routerconfig.DHCPReservation) string { return r.MAC },
}

// dhcpReservationID is the description of id and the format it is built from.
const dhcpReservationID = "`<site_id>/<device_id>/<mac>`."

// dhcpReservationResourceModel is the Terraform state of one reservation. The framework's
// reflection has no notion of embedded structs, so the shared attributes are repeated here.
type dhcpReservationResourceModel struct {
	SiteID   types.String `tfsdk:"site_id"`
	DeviceID types.String `tfsdk:"device_id"`
	ID       types.String `tfsdk:"id"`
	MAC      types.String `tfsdk:"mac"`
	IP       types.String `tfsdk:"ip"`
}

func (m dhcpReservationResourceModel) reservation() routerconfig.DHCPReservation {
	return routerconfig.DHCPReservation{MAC: m.MAC.ValueString(), IP: m.IP.ValueString()}
}

// withReservation copies what the cloud holds into the model, leaving the identity alone.
func (m dhcpReservationResourceModel) withReservation(r routerconfig.DHCPReservation) dhcpReservationResourceModel {
	m.IP = types.StringValue(r.IP)
	return m
}

func (m dhcpReservationResourceModel) address() types.String {
	return types.StringValue(m.SiteID.ValueString() + "/" + m.DeviceID.ValueString() + "/" + m.MAC.ValueString())
}

func (r *DHCPReservation) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dhcp_reservation"
}

func (r *DHCPReservation) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One client's fixed address, written through the Alta cloud so the portal shows it.\n\n" +
			"A reservation is a MAC-to-IP mapping and nothing else. The cloud keeps it on the client's record and " +
			"compiles it into the DHCP static mappings of whichever network the client is on, so the network is not " +
			"named here. Everything else on that record — most importantly the client's VLAN assignment, which the " +
			"portal sets separately — is left untouched by creating, changing and destroying this resource, and so " +
			"are the reservations of every other client. The change is applied as one gated, commit-confirmed " +
			"transaction — a single push to the router, rolled back automatically unless the health probes pass.",
		Attributes: dhcpReservationResourceAttributes(),
	}
}

func dhcpReservationResourceAttributes() map[string]schema.Attribute {
	attributes := siteAttributes(dhcpReservationID)
	// siteAttributes offers an id for items whose id the user chooses or the provider
	// generates. A client is already named by its MAC, so id only addresses the reservation.
	attributes["id"] = schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: dhcpReservationID,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attributes["mac"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "The client's MAC address, lowercase and colon-separated, e.g. `02:00:00:aa:bb:cc`.",
		Validators: []validator.String{
			stringvalidator.RegexMatches(macAddress, "must be a lowercase, colon-separated MAC address"),
		},
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
	attributes["ip"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "Address to hand the client. It must sit in the network the client is on, and outside its DHCP pool.",
	}
	return attributes
}

func (r *DHCPReservation) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *DHCPReservation) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.refuseUnwritable(req, resp)
	if req.Plan.Raw.IsNull() || resp.Diagnostics.HasError() {
		return
	}
	// The id is derived, so resolving it here keeps the plan concrete rather than leaving
	// every create showing "known after apply".
	var model dhcpReservationResourceModel
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("site_id"), &model.SiteID)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("device_id"), &model.DeviceID)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("mac"), &model.MAC)...)
	if resp.Diagnostics.HasError() || model.SiteID.IsUnknown() || model.DeviceID.IsUnknown() || model.MAC.IsUnknown() {
		return // Create resolves it instead, once the identity is known
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, idPath, model.address())...)
}

func (r *DHCPReservation) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dhcpReservationResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *DHCPReservation) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dhcpReservationResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *DHCPReservation) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dhcpReservationResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	doc, err := r.document(ctx, state.SiteID.ValueString(), state.DeviceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading DHCP reservation", err.Error())
		return
	}
	reservation, ok := r.find(doc, state.MAC.ValueString())
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state.withReservation(reservation))...)
}

// Delete takes the fixed address off the client and leaves the rest of its record — its
// VLAN assignment above all — exactly as it was. The client keeps its lease until it
// expires, then takes an address from the pool like any other.
func (r *DHCPReservation) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dhcpReservationResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	if err := r.drop(ctx, state.SiteID.ValueString(), state.DeviceID.ValueString(), state.MAC.ValueString()); err != nil {
		resp.Diagnostics.AddError("Removing DHCP reservation", err.Error())
	}
}

// ImportState adopts a reservation the portal already holds.
func (r *DHCPReservation) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	site, device, mac, ok := importParts(req.ID, &resp.Diagnostics, "reservation")
	if !ok {
		return
	}
	if !macAddress.MatchString(mac) {
		resp.Diagnostics.AddError("Invalid import id",
			"Use <site_id>/<device_id>/<mac>, with the MAC address lowercase and colon-separated, e.g. "+
				site+"/"+device+"/02:00:00:aa:bb:cc.")
		return
	}
	doc, err := r.document(ctx, site, device)
	if err != nil {
		resp.Diagnostics.AddError("Reading DHCP reservation", err.Error())
		return
	}
	reservation, found := r.find(doc, mac)
	if !found {
		resp.Diagnostics.AddError("No such reservation", "The site has no client "+mac+" with a fixed address.")
		return
	}
	model := dhcpReservationResourceModel{
		SiteID:   types.StringValue(site),
		DeviceID: types.StringValue(device),
		MAC:      types.StringValue(mac),
	}
	model.ID = model.address()
	resp.Diagnostics.Append(resp.State.Set(ctx, model.withReservation(reservation))...)
}

func (r *DHCPReservation) write(ctx context.Context, plan dhcpReservationResourceModel, state stateSetter, diags *diag.Diagnostics) {
	plan.ID = plan.address()
	if err := r.put(ctx, plan.SiteID.ValueString(), plan.DeviceID.ValueString(), plan.reservation()); err != nil {
		diags.AddError("Writing DHCP reservation", err.Error())
		return
	}
	diags.Append(state.Set(ctx, plan)...)
}
