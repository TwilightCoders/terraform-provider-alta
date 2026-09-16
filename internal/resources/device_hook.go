package resources

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/device"
)

var (
	_ resource.ResourceWithConfigure   = (*DeviceHook)(nil)
	_ resource.ResourceWithImportState = (*DeviceHook)(nil)
	_ resource.ResourceWithModifyPlan  = (*DeviceHook)(nil)
)

// DeviceHook is the alta_device_hook resource.
type DeviceHook struct {
	data *ProviderData
}

// NewDeviceHook returns the resource.
func NewDeviceHook() resource.Resource { return &DeviceHook{} }

type deviceHookModel struct {
	Name          types.String `tfsdk:"name"`
	Interface     types.String `tfsdk:"interface"`
	Action        types.String `tfsdk:"action"`
	Priority      types.String `tfsdk:"priority"`
	Script        types.String `tfsdk:"script"`
	DestroyScript types.String `tfsdk:"destroy_script"`
	Requires      *[]string    `tfsdk:"requires"`
	RunOnApply    types.Bool   `tfsdk:"run_on_apply"`
	Path          types.String `tfsdk:"path"`
	SHA256        types.String `tfsdk:"sha256"`
	Loader        types.Bool   `tfsdk:"loader_installed"`
}

func (m deviceHookModel) hook() device.Hook {
	return device.Hook{
		Name:      m.Name.ValueString(),
		Interface: m.Interface.ValueString(),
		Action:    m.Action.ValueString(),
		Priority:  m.Priority.ValueString(),
		Script:    m.Script.ValueString(),
		Run:       m.RunOnApply.ValueBool(),
		Requires:  deref(m.Requires),
	}
}

func (r *DeviceHook) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device_hook"
}

func (r *DeviceHook) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A script the router runs for itself, at boot or when an interface comes up.\n\n" +
			"This is the escape hatch for behaviour the Alta cloud has no concept of. The script is stored on the " +
			"router's persistent filesystem and reinstalled by a managed block in `post-cfg.sh`, because the router " +
			"rebuilds `/etc` on every boot and every configuration push. Write it to be idempotent: it runs again on " +
			"every boot, every push, and every matching interface event.\n\n" +
			"Hooks touch the router only. They make no cloud changes and trigger no push.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Identifies the hook on the router.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`), "must be lowercase letters, digits and dashes")},
			},
			"interface": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Network interface to hook, e.g. `wg0`. With no interface the script runs at boot " +
					"and after every configuration push.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"action": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Interface event to act on. Defaults to `ifup`.",
				Validators:          []validator.String{stringvalidator.OneOf("ifup", "ifdown", "ifupdate")},
			},
			"priority": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Orders this hook among the router's other interface scripts. Defaults to `" +
					device.DefaultPriority + "`, which runs after the router's own.",
			},
			"script": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Shell script. Event guards are added for you. Make the body idempotent, and detach " +
					"anything slow (see the example) rather than blocking the router's hotplug queue.",
			},
			"destroy_script": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Shell run when the hook is destroyed, to undo what it asserted. Without one, " +
					"the hook's effect lasts until the router next reboots.",
			},
			"requires": schema.ListAttribute{
				Optional: true, ElementType: types.StringType,
				MarkdownDescription: "Binaries the script needs, e.g. `iptables`. They are checked on the router before " +
					"the hook is written, so a missing one fails here instead of silently at the next boot. The router " +
					"runs busybox: it has no `install`, for example.",
			},
			"run_on_apply": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Run the hook once when it is created or changed, rather than waiting for its event.",
			},
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Where the hook is stored on the router.",
			},
			"sha256": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Checksum of the installed script, so drift on the router is visible.",
			},
			"loader_installed": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "Whether the loader that reinstalls hooks after a boot or push is on the router. " +
					"The provider owns that file, so a missing one is drift: the next plan proposes restoring it.",
			},
		},
	}
}

func (r *DeviceHook) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerData(req.ProviderData, &resp.Diagnostics)
}

// ModifyPlan puts a missing loader in the diff, and refuses hook changes the provider is
// not allowed or able to make.
func (r *DeviceHook) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.data == nil || req.Plan.Raw.IsNull() {
		return
	}
	changed := !req.State.Raw.Equal(req.Plan.Raw)
	if !req.State.Raw.IsNull() && r.loaderMissing(ctx, req.State, &resp.Diagnostics) {
		// The loader is the provider's own file, so restoring it is a change to propose,
		// not a warning to print over an empty plan.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("loader_installed"), types.BoolUnknown())...)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("sha256"), types.StringUnknown())...)
		changed = true
	}
	if !changed {
		return
	}
	switch {
	case r.data.ReadOnly:
		resp.Diagnostics.AddError("Provider is read-only",
			"This plan would change a script on the router, and the provider is configured with read_only = true.")
	case r.data.Hooks == nil:
		resp.Diagnostics.AddError("Router connection required",
			"Managing router hooks needs the provider's ssh block.")
	}
}

// loaderMissing reports whether the last refresh found the loader gone.
func (r *DeviceHook) loaderMissing(ctx context.Context, state tfsdk.State, diags *diag.Diagnostics) bool {
	var installed types.Bool
	diags.Append(state.GetAttribute(ctx, path.Root("loader_installed"), &installed)...)
	return !installed.IsNull() && !installed.ValueBool()
}

func (r *DeviceHook) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deviceHookModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.put(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *DeviceHook) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan deviceHookModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.put(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *DeviceHook) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state deviceHookModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	if r.data == nil || r.data.Hooks == nil {
		return // no router connection: leave state as it is rather than claiming it is gone
	}
	got, err := r.data.Hooks.Get(ctx, state.hook())
	if err != nil {
		resp.Diagnostics.AddError("Reading router hook", err.Error())
		return
	}
	if !got.Present {
		resp.State.RemoveResource(ctx)
		return
	}
	if !got.Sourced {
		// post-cfg.sh is not the provider's to write, so this is as far as it can go.
		resp.Diagnostics.AddWarning("Hook loader not sourced",
			"post-cfg.sh no longer runs the hook loader, so hooks will not survive the next boot or "+
				"configuration push. Add this line to post-cfg.sh:\n\n    "+r.data.Hooks.SourceLine())
	}
	state.Loader = types.BoolValue(got.LoaderPresent)
	state.SHA256 = types.StringValue(got.SHA256)
	state.Path = types.StringValue(state.hook().FileName())
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *DeviceHook) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state deviceHookModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	if r.data == nil || r.data.ReadOnly || r.data.Hooks == nil {
		resp.Diagnostics.AddError("Router changes are not enabled", "The provider is read-only or has no ssh block.")
		return
	}
	if err := r.data.Hooks.Delete(ctx, state.hook(), state.DestroyScript.ValueString()); err != nil {
		resp.Diagnostics.AddError("Removing router hook", err.Error())
		return
	}
	if state.DestroyScript.IsNull() {
		resp.Diagnostics.AddWarning("Hook removed, effect not undone",
			"The script is gone, but whatever it asserted stays until the router next reboots. Set destroy_script to undo it.")
	}
}

func (r *DeviceHook) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.AddError("Hooks cannot be imported",
		"A hook's script lives in configuration, not on the router. Declare "+req.ID+" and apply it.")
}

func (r *DeviceHook) put(ctx context.Context, plan deviceHookModel, state stateSetter, diags *diag.Diagnostics) {
	if r.data == nil || r.data.ReadOnly || r.data.Hooks == nil {
		diags.AddError("Router changes are not enabled", "The provider is read-only or has no ssh block.")
		return
	}
	got, err := r.data.Hooks.Put(ctx, plan.hook())
	if err != nil {
		diags.AddError("Installing router hook", err.Error())
		return
	}
	plan.SHA256 = types.StringValue(got.SHA256)
	plan.Path = types.StringValue(plan.hook().FileName())
	plan.Loader = types.BoolValue(got.LoaderPresent)
	diags.Append(state.Set(ctx, plan)...)
}
