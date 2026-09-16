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
	_ resource.ResourceWithConfigure   = (*FirewallRule)(nil)
	_ resource.ResourceWithImportState = (*FirewallRule)(nil)
	_ resource.ResourceWithModifyPlan  = (*FirewallRule)(nil)
)

// FirewallRule is the alta_firewall_rule resource: one rule in site.firewall.firewall.rules.
type FirewallRule struct {
	element[routerconfig.FirewallRule]
}

// NewFirewallRule returns the resource.
func NewFirewallRule() resource.Resource {
	return &FirewallRule{element: element[routerconfig.FirewallRule]{in: firewallRuleCollection}}
}

var firewallRuleCollection = collection[routerconfig.FirewallRule]{
	label: "firewall rule",
	list:  func(c routerconfig.Config) []routerconfig.FirewallRule { return deref(c.FirewallRules) },
	store: func(c *routerconfig.Config, v []routerconfig.FirewallRule) { c.FirewallRules = &v },
	id:    func(r routerconfig.FirewallRule) string { return r.ID },
}

// firewallRuleResourceModel is the flat form of firewallRuleModel: the framework's state
// reflection has no notion of embedded structs, so the fields are repeated.
type firewallRuleResourceModel struct {
	SiteID      types.String   `tfsdk:"site_id"`
	DeviceID    types.String   `tfsdk:"device_id"`
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

func (m firewallRuleResourceModel) rule() routerconfig.FirewallRule {
	return routerconfig.FirewallRule{
		ID: m.ID.ValueString(), Description: m.Description.ValueString(), Action: m.Action.ValueString(),
		Protocols: deref(m.Protocols), IPVersion: m.IPVersion.ValueString(), ICMPTypes: deref(m.ICMPTypes),
		ZoneIn: m.ZoneIn.ValueString(), ZoneOut: m.ZoneOut.ValueString(),
		Source: m.Source.toDomain(), Destination: m.Destination.toDomain(), Limit: m.Limit.ValueString(),
	}
}

// withRule copies what the cloud holds into the model, leaving the identity alone.
func (m firewallRuleResourceModel) withRule(r routerconfig.FirewallRule) firewallRuleResourceModel {
	m.Description, m.Action = optional(r.Description), optional(r.Action)
	m.Protocols, m.IPVersion, m.ICMPTypes = nonEmpty(r.Protocols), optional(r.IPVersion), nonEmpty(r.ICMPTypes)
	m.ZoneIn, m.ZoneOut, m.Limit = optional(r.ZoneIn), optional(r.ZoneOut), optional(r.Limit)
	m.Source, m.Destination = endpointFrom(r.Source), endpointFrom(r.Destination)
	return m
}

func (r *FirewallRule) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_rule"
}

func (r *FirewallRule) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "One firewall rule, written through the Alta cloud so the portal shows it.\n\n" +
			"Rules that exist in the portal and not in Terraform are left alone: this resource owns its own rule " +
			"and nothing else. The change is applied as one gated, commit-confirmed transaction — a single push to " +
			"the router, rolled back automatically unless the health probes pass.\n\n" +
			"## Order\n\n" +
			"The router evaluates filter rules in the order the site holds them, and the API has no position or " +
			"priority field: a rule's place is where it sits in the list. A new rule is appended after every rule " +
			"the site already holds, which is what the portal does, and a rule that already exists keeps its place " +
			"when Terraform changes it. Order therefore follows creation order.\n\n" +
			"Terraform creates resources in dependency order, not in the order they are written, so two rules " +
			"created in the same apply have no guaranteed order between them. Where one rule must be evaluated " +
			"before another, either chain them with `depends_on`, or manage the whole list with `alta_router_config`'s " +
			"`firewall_rules`, which is authoritative about order.",
		Attributes: with(siteAttributes(
			"Alta rule id. Generated the way the portal generates one when not set."), firewallRuleAttributes()),
	}
}

func (r *FirewallRule) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *FirewallRule) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	r.planGeneratedID(ctx, req, resp)
}

func (r *FirewallRule) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan firewallRuleResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	plan.ID = generatedID(plan.ID)
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *FirewallRule) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan firewallRuleResourceModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *FirewallRule) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallRuleResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	doc, err := r.document(ctx, state.SiteID.ValueString(), state.DeviceID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading firewall rule", err.Error())
		return
	}
	rule, ok := r.find(doc, state.ID.ValueString())
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state.withRule(rule))...)
}

func (r *FirewallRule) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state firewallRuleResourceModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	if err := r.drop(ctx, state.SiteID.ValueString(), state.DeviceID.ValueString(), state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Removing firewall rule", err.Error())
	}
}

// ImportState adopts a rule the portal already holds, in the place it already holds it.
func (r *FirewallRule) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	site, device, id, ok := importParts(req.ID, &resp.Diagnostics, "firewall rule")
	if !ok {
		return
	}
	doc, err := r.document(ctx, site, device)
	if err != nil {
		resp.Diagnostics.AddError("Reading firewall rule", err.Error())
		return
	}
	rule, found := r.find(doc, id)
	if !found {
		resp.Diagnostics.AddError("No such firewall rule", "The site has no firewall rule with id "+id+".")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, firewallRuleResourceModel{
		SiteID:   types.StringValue(site),
		DeviceID: types.StringValue(device),
		ID:       types.StringValue(id),
	}.withRule(rule))...)
}

func (r *FirewallRule) write(ctx context.Context, plan firewallRuleResourceModel, state stateSetter, diags *diag.Diagnostics) {
	if err := r.put(ctx, plan.SiteID.ValueString(), plan.DeviceID.ValueString(), plan.rule()); err != nil {
		diags.AddError("Writing firewall rule", err.Error())
		return
	}
	diags.Append(state.Set(ctx, plan)...)
}
