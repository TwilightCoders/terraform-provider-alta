package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
	"github.com/TwilightCoders/terraform-provider-alta/internal/txn"
)

var (
	_ resource.ResourceWithConfigure   = (*RouterConfig)(nil)
	_ resource.ResourceWithImportState = (*RouterConfig)(nil)
	_ resource.ResourceWithModifyPlan  = (*RouterConfig)(nil)
)

// RouterConfig is the alta_router_config resource.
type RouterConfig struct {
	data *ProviderData
}

// NewRouterConfig returns the resource.
func NewRouterConfig() resource.Resource { return &RouterConfig{} }

func (r *RouterConfig) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_router_config"
}

func (r *RouterConfig) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = routerConfigSchema()
}

func (r *RouterConfig) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *RouterConfig) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	siteID, deviceID, ok := strings.Cut(req.ID, "/")
	if !ok || siteID == "" || deviceID == "" {
		resp.Diagnostics.AddError("Invalid import id", "Use <site_id>/<device_id>, e.g. aBcDeFgHiJkLmNoPqRsTu/0a1b2c3d4e5f.")
		return
	}
	model := routerConfigModel{
		ID:       types.StringValue(req.ID),
		SiteID:   types.StringValue(siteID),
		DeviceID: types.StringValue(deviceID),
	}.allSections()
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *RouterConfig) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state routerConfigModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	doc, err := r.document(ctx, state)
	if err != nil {
		resp.Diagnostics.AddError("Reading router configuration", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state.withConfig(routerconfig.Read(doc)))...)
}

func (r *RouterConfig) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan routerConfigModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	plan.ID = resourceID(plan.SiteID, plan.DeviceID)
	r.apply(ctx, nil, plan, &resp.State, &resp.Diagnostics)
}

func (r *RouterConfig) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state routerConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &state, plan, &resp.State, &resp.Diagnostics)
}

func (r *RouterConfig) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddWarning("Router configuration left in place",
		"alta_router_config was removed from Terraform state only. The router and the Alta portal are unchanged.")
}

// ModifyPlan previews the cloud writes and refuses changes that cannot be applied safely.
func (r *RouterConfig) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.data == nil || req.State.Raw.Equal(req.Plan.Raw) {
		return
	}
	var siteID, deviceID types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("site_id"), &siteID)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("device_id"), &deviceID)...)
	if !siteID.IsUnknown() && !deviceID.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), resourceID(siteID, deviceID))...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if !resp.Plan.Raw.IsFullyKnown() {
		if r.data.ReadOnly {
			resp.Diagnostics.AddError("Provider is read-only", "The plan depends on values not known yet, so it cannot be proven to write nothing.")
		}
		return // otherwise the checks run again at apply, once values are known
	}

	var plan routerConfigModel
	if resp.Diagnostics.Append(resp.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	change, _, err := r.change(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Planning router configuration", err.Error())
		return
	}
	if len(change.Writes) == 0 {
		return // only which sections Terraform manages changed
	}
	if err := r.writable(ctx, change); err != nil {
		resp.Diagnostics.AddError("Router change refused", err.Error())
		return
	}
	resp.Diagnostics.AddWarning("Router change will be pushed", fmt.Sprintf(
		"Apply writes %s to the Alta cloud in one transaction and releases a single push to the router.", describeWrites(change.Writes)))
}

// writable returns why change cannot be applied, or nil.
func (r *RouterConfig) writable(ctx context.Context, change txn.Change) error {
	switch {
	case r.data.ReadOnly:
		return fmt.Errorf("the provider is read_only and this change writes %s", describeWrites(change.Writes))
	case r.data.Transactions == nil:
		return errors.New("changing router configuration needs the provider's ssh block, to gate the push and roll it back if it fails")
	}
	if err := r.data.Transactions.Ready(ctx); err != nil && !errors.Is(err, txn.ErrRecoveryNeeded) {
		// An unfinished transaction is recovered at apply; local edits would be destroyed.
		return fmt.Errorf("%w; any push would discard them, so bring the cloud level with the router first", err)
	}
	return nil
}

// apply moves the router from prior (nil on create) to plan.
func (r *RouterConfig) apply(ctx context.Context, prior *routerConfigModel, plan routerConfigModel, state stateSetter, diags *diag.Diagnostics) {
	change, doc, err := r.change(ctx, plan)
	if err != nil {
		diags.AddError("Planning router configuration", err.Error())
		return
	}
	if prior != nil {
		if err := unchangedSince(*prior, doc); err != nil {
			diags.AddError("Router configuration changed outside Terraform", err.Error())
			return
		}
	}

	if len(change.Writes) > 0 {
		if err := r.writable(ctx, change); err != nil {
			diags.AddError("Router change refused", err.Error())
			return
		}
		if recovered, err := r.data.Transactions.Recover(ctx); err != nil {
			diags.AddError("Recovering an unfinished transaction", err.Error())
			return
		} else if recovered {
			diags.AddError("Recovered an unfinished transaction",
				"An earlier transaction was rolled back and the cloud repaired. Nothing else was applied; run terraform plan again.")
			return
		}
		if _, err := r.data.Transactions.Run(ctx, change); err != nil {
			diags.AddError("Applying router configuration", err.Error())
			return
		}
		if doc, err = r.document(ctx, plan); err != nil {
			diags.AddError("Reading router configuration after apply", err.Error())
			return
		}
	}
	diags.Append(state.Set(ctx, plan.withConfig(routerconfig.Read(doc)))...)
}

type stateSetter interface {
	Set(ctx context.Context, val any) diag.Diagnostics
}

// change reads the cloud and computes the writes that realise model.
func (r *RouterConfig) change(ctx context.Context, model routerConfigModel) (txn.Change, *routerconfig.Document, error) {
	desired, err := model.toConfig()
	if err != nil {
		return txn.Change{}, nil, err
	}
	doc, err := r.document(ctx, model)
	if err != nil {
		return txn.Change{}, nil, err
	}
	writes, pre, err := desired.Plan(doc)
	if err != nil {
		return txn.Change{}, nil, err
	}
	return txn.Change{Writes: writes, PreImages: pre}, doc, nil
}

// unchangedSince reports an error when the managed sections of the cloud no longer match
// the state Terraform last recorded, meaning someone changed them in the portal since.
func unchangedSince(prior routerConfigModel, doc *routerconfig.Document) error {
	recorded, err := prior.toConfig()
	if err != nil {
		return err
	}
	writes, _, err := recorded.Plan(doc)
	if err != nil {
		return err
	}
	if len(writes) > 0 {
		return fmt.Errorf("%s differ from the last refresh; run terraform plan again to review the current state",
			describeWrites(writes))
	}
	return nil
}

func (r *RouterConfig) document(ctx context.Context, model routerConfigModel) (*routerconfig.Document, error) {
	if r.data == nil {
		return nil, errors.New("provider is not configured")
	}
	siteID, deviceID := model.SiteID.ValueString(), model.DeviceID.ValueString()
	site, err := r.data.Cloud.Site(ctx, siteID)
	if err != nil {
		return nil, err
	}
	state, err := r.data.Cloud.State(ctx, siteID)
	if err != nil {
		return nil, err
	}
	return routerconfig.NewDocument(siteID, deviceID, site, state)
}

func resourceID(siteID, deviceID types.String) types.String {
	return types.StringValue(siteID.ValueString() + "/" + deviceID.ValueString())
}

func describeWrites(writes []cloud.Write) string {
	parts := make([]string, 0, len(writes))
	for _, w := range writes {
		switch w.Kind {
		case cloud.KindClient:
			parts = append(parts, "client "+w.ID)
		default:
			parts = append(parts, string(w.Kind)+"."+w.Key)
		}
	}
	return strings.Join(parts, ", ")
}
