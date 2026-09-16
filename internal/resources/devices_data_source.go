package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
)

var _ datasource.DataSourceWithConfigure = (*Devices)(nil)

// Devices is the alta_devices data source: the hardware a site has adopted.
type Devices struct {
	data *ProviderData
}

// NewDevices returns the data source.
func NewDevices() datasource.DataSource { return &Devices{} }

// deviceKinds are the type numbers the cloud stores, named the way the portal names them.
var deviceKinds = map[int64]string{1: "filter", 2: "ap", 3: "switch", 4: "controller", 5: "router"}

type devicesModel struct {
	SiteID  types.String       `tfsdk:"site_id"`
	Kind    types.String       `tfsdk:"kind"`
	Devices []deviceEntryModel `tfsdk:"devices"`
}

type deviceEntryModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Kind    types.String `tfsdk:"kind"`
	Model   types.String `tfsdk:"model"`
	Version types.String `tfsdk:"version"`
}

func (d *Devices) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_devices"
}

func (d *Devices) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The hardware a site has adopted.\n\n" +
			"A device is identified by its MAC address, which is not something anyone should have to look up in " +
			"the portal and paste into a configuration. Use this to find it: `data.alta_devices.routers.devices[0].id`.",
		Attributes: map[string]schema.Attribute{
			"site_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Alta site. Defaults to the provider's `site_id`.",
			},
			"kind": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Return only devices of this kind: `router`, `switch`, `ap`, `filter` or `controller`.",
				Validators: []validator.String{stringvalidator.OneOf(
					"router", "switch", "ap", "filter", "controller")},
			},
			"devices": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Matching devices, ordered by id so the list is stable.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":      schema.StringAttribute{Computed: true, MarkdownDescription: "MAC address, lowercase without separators."},
					"name":    schema.StringAttribute{Computed: true, MarkdownDescription: "Name shown in the portal."},
					"kind":    schema.StringAttribute{Computed: true, MarkdownDescription: "`router`, `switch`, `ap`, `filter` or `controller`."},
					"model":   schema.StringAttribute{Computed: true, MarkdownDescription: "Model, as the cloud reports it."},
					"version": schema.StringAttribute{Computed: true, MarkdownDescription: "Firmware version."},
				}},
			},
		},
	}
}

func (d *Devices) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *Devices) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model devicesModel
	if resp.Diagnostics.Append(req.Config.Get(ctx, &model)...); resp.Diagnostics.HasError() {
		return
	}
	if d.data == nil {
		resp.Diagnostics.AddError("Provider is not configured", "The Alta provider has no credentials.")
		return
	}
	siteID := model.SiteID.ValueString()
	if siteID == "" {
		siteID = d.data.SiteID
	}
	if siteID == "" {
		resp.Diagnostics.AddError("No site", "Set site_id here or on the provider.")
		return
	}

	state, err := d.data.Cloud.State(ctx, siteID)
	if err != nil {
		resp.Diagnostics.AddError("Reading devices", err.Error())
		return
	}

	wanted := model.Kind.ValueString()
	model.Devices = []deviceEntryModel{}
	for _, device := range state.Devices {
		entry := deviceEntry(device)
		if wanted != "" && entry.Kind.ValueString() != wanted {
			continue
		}
		model.Devices = append(model.Devices, entry)
	}
	sort.Slice(model.Devices, func(i, j int) bool {
		return model.Devices[i].ID.ValueString() < model.Devices[j].ID.ValueString()
	})
	model.SiteID = types.StringValue(siteID)
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func deviceEntry(device cloud.Object) deviceEntryModel {
	return deviceEntryModel{
		ID:      optional(asText(device["id"])),
		Name:    optional(asText(device["name"])),
		Kind:    optional(kindOf(device["type"])),
		Model:   optional(asText(device["model"])),
		Version: optional(asText(device["version"])),
	}
}

// asText reads a cloud string field, which may be absent or null.
func asText(v any) string {
	text, _ := v.(string)
	return text
}

// asNumber reads a cloud number, which arrives as json.Number through the decoder and as
// a float64 through anything that has been round-tripped.
func asNumber(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		parsed, err := n.Int64()
		return parsed, err == nil
	case float64:
		return int64(n), true
	case int64:
		return n, true
	}
	return 0, false
}

// kindOf names a device type, and reports an unknown one as its number rather than
// guessing: the portal's table is evidence, not contract.
func kindOf(raw any) string {
	number, ok := asNumber(raw)
	if !ok {
		return ""
	}
	if name, known := deviceKinds[number]; known {
		return name
	}
	return fmt.Sprintf("%d", number)
}
