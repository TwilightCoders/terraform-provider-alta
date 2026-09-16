package resources

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/device"
)

var (
	_ resource.ResourceWithConfigure   = (*DeviceFile)(nil)
	_ resource.ResourceWithImportState = (*DeviceFile)(nil)
	_ resource.ResourceWithModifyPlan  = (*DeviceFile)(nil)
)

// DeviceFile is the alta_device_file resource.
type DeviceFile struct {
	data *ProviderData
}

// NewDeviceFile returns the resource.
func NewDeviceFile() resource.Resource { return &DeviceFile{} }

type deviceFileModel struct {
	Path           types.String `tfsdk:"path"`
	Content        types.String `tfsdk:"content"`
	Mode           types.String `tfsdk:"mode"`
	DriftDetection types.String `tfsdk:"drift_detection"`
	OnDestroy      types.String `tfsdk:"on_destroy"`
	Backup         types.Bool   `tfsdk:"backup"`
	SHA256         types.String `tfsdk:"sha256"`
}

func (m deviceFileModel) file() device.File {
	return device.File{
		Path:    m.Path.ValueString(),
		Content: m.Content.ValueString(),
		Mode:    m.Mode.ValueString(),
		Backup:  m.Backup.ValueBool(),
	}
}

func (r *DeviceFile) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_device_file"
}

func (r *DeviceFile) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A file the provider keeps on the router, so a script or configuration fragment that lives " +
			"in version control is the one the router is actually running.\n\n" +
			"The router regenerates `/etc` on every boot and every configuration push, so files that must survive " +
			"belong on its persistent filesystem. Writes are atomic, and one copy of whatever was there first is kept.",
		Attributes: map[string]schema.Attribute{
			"path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Absolute path on the router. Use its persistent filesystem; anything under `/etc` is lost at boot.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^/[^\s]+[^/\s]$`), "must be an absolute path to a file")},
			},
			"content": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The file's contents, usually `file()` or a template. It is stored in Terraform " +
					"state, so keep secrets out of it.",
			},
			"mode": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Permissions in octal, e.g. `0755`. Required: omitting it must never be able to loosen a file.",
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^0?[0-7]{3}$`), "must be octal, e.g. 0755")},
			},
			"drift_detection": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("content"),
				MarkdownDescription: "`content` compares the router's copy on every refresh, so an edit made on the box " +
					"shows up in the next plan. `none` only checks that the file exists.",
				Validators: []validator.String{stringvalidator.OneOf("content", "none")},
			},
			"on_destroy": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString("keep"),
				MarkdownDescription: "`keep` leaves the file on the router, which is usually right for something it " +
					"depends on; `delete` removes it.",
				Validators: []validator.String{stringvalidator.OneOf("keep", "delete")},
			},
			"backup": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Keep one copy, alongside the file, of whatever was there before this resource first wrote it.",
			},
			"sha256": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Checksum of the router's copy.",
			},
		},
	}
}

func (r *DeviceFile) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *DeviceFile) ModifyPlan(_ context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.data == nil || req.Plan.Raw.IsNull() || req.State.Raw.Equal(req.Plan.Raw) {
		return
	}
	switch {
	case r.data.ReadOnly:
		resp.Diagnostics.AddError("Provider is read-only",
			"This plan would write a file to the router, and the provider is configured with read_only = true.")
	case r.data.Hooks == nil:
		resp.Diagnostics.AddError("Router connection required", "Managing router files needs the provider's ssh block.")
	}
}

func (r *DeviceFile) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deviceFileModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *DeviceFile) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan deviceFileModel
	if resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...); resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, &resp.Diagnostics)
}

func (r *DeviceFile) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state deviceFileModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	if r.data == nil || r.data.Hooks == nil {
		return
	}
	got, err := r.data.Hooks.GetFile(ctx, state.file())
	if err != nil {
		resp.Diagnostics.AddError("Reading router file", err.Error())
		return
	}
	if !got.Present {
		resp.State.RemoveResource(ctx)
		return
	}
	state.SHA256 = types.StringValue(got.SHA256)
	state.Mode = types.StringValue(got.Mode)
	if state.DriftDetection.ValueString() == "content" {
		// Report what the router holds, so an edit made on the box appears in the next plan.
		state.Content = types.StringValue(got.Content)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *DeviceFile) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state deviceFileModel
	if resp.Diagnostics.Append(req.State.Get(ctx, &state)...); resp.Diagnostics.HasError() {
		return
	}
	if state.OnDestroy.ValueString() != "delete" {
		resp.Diagnostics.AddWarning("File left on the router",
			state.Path.ValueString()+" is no longer managed by Terraform, and was left in place. Set on_destroy = \"delete\" to remove it.")
		return
	}
	if r.data == nil || r.data.ReadOnly || r.data.Hooks == nil {
		resp.Diagnostics.AddError("Router changes are not enabled", "The provider is read-only or has no ssh block.")
		return
	}
	if err := r.data.Hooks.DeleteFile(ctx, state.file()); err != nil {
		resp.Diagnostics.AddError("Removing router file", err.Error())
	}
}

// ImportState adopts a file already on the router, so its contents do not have to be
// retyped into configuration to bring it under management.
func (r *DeviceFile) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if r.data == nil || r.data.Hooks == nil {
		resp.Diagnostics.AddError("Router connection required", "Importing a router file needs the provider's ssh block.")
		return
	}
	got, err := r.data.Hooks.GetFile(ctx, device.File{Path: req.ID})
	if err != nil {
		resp.Diagnostics.AddError("Reading router file", err.Error())
		return
	}
	if !got.Present {
		resp.Diagnostics.AddError("No such file on the router", req.ID+" does not exist.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, deviceFileModel{
		Path:           types.StringValue(req.ID),
		Content:        types.StringValue(got.Content),
		Mode:           types.StringValue(got.Mode),
		DriftDetection: types.StringValue("content"),
		OnDestroy:      types.StringValue("keep"),
		Backup:         types.BoolValue(true),
		SHA256:         types.StringValue(got.SHA256),
	})...)
}

func (r *DeviceFile) write(ctx context.Context, plan deviceFileModel, state stateSetter, diags *diag.Diagnostics) {
	if r.data == nil || r.data.ReadOnly || r.data.Hooks == nil {
		diags.AddError("Router changes are not enabled", "The provider is read-only or has no ssh block.")
		return
	}
	got, err := r.data.Hooks.PutFile(ctx, plan.file())
	if err != nil {
		diags.AddError("Writing router file", err.Error())
		return
	}
	plan.SHA256 = types.StringValue(got.SHA256)
	plan.Mode = types.StringValue(got.Mode)
	diags.Append(state.Set(ctx, plan)...)
}
