package resources

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/routerconfig"
	"github.com/TwilightCoders/terraform-provider-alta/internal/txn"
)

// idPath is where every single-item resource keeps its Alta id.
var idPath = path.Root("id")

// collection is one identified section of the site document: routes, VLANs, port
// forwards, reservations. The Alta API replaces a whole key at a time, so a resource that
// owns a single item reads the whole collection, changes its own item, and writes the
// collection back.
type collection[T any] struct {
	// label names one item in diagnostics, e.g. "static route".
	label string
	list  func(routerconfig.Config) []T
	store func(*routerconfig.Config, []T)
	id    func(T) string
}

// element applies one item of a collection as a gated transaction.
//
// Read-modify-write of a shared array is only safe one at a time, so every apply holds
// the provider's lock across reading the document, computing the writes and running the
// transaction. Terraform's parallelism would otherwise let two items read the same array
// and each write back a version missing the other.
type element[T any] struct {
	data *ProviderData
	in   collection[T]
}

func (e element[T]) document(ctx context.Context, siteID, deviceID string) (*routerconfig.Document, error) {
	if e.data == nil {
		return nil, errors.New("provider is not configured")
	}
	site, err := e.data.Cloud.Site(ctx, siteID)
	if err != nil {
		return nil, err
	}
	state, err := e.data.Cloud.State(ctx, siteID)
	if err != nil {
		return nil, err
	}
	return routerconfig.NewDocument(siteID, deviceID, site, state)
}

// find returns the item with this id as the cloud currently holds it.
func (e element[T]) find(doc *routerconfig.Document, id string) (T, bool) {
	for _, item := range e.in.list(routerconfig.Read(doc)) {
		if e.in.id(item) == id {
			return item, true
		}
	}
	var zero T
	return zero, false
}

// put writes item into the collection, replacing any item with the same id.
func (e element[T]) put(ctx context.Context, siteID, deviceID string, item T) error {
	id := e.in.id(item)
	return e.apply(ctx, siteID, deviceID, func(items []T) []T {
		for i, existing := range items {
			if e.in.id(existing) == id {
				items[i] = item
				return items
			}
		}
		return append(items, item)
	})
}

// drop removes the item with this id, leaving every other item alone.
func (e element[T]) drop(ctx context.Context, siteID, deviceID, id string) error {
	return e.apply(ctx, siteID, deviceID, func(items []T) []T {
		kept := make([]T, 0, len(items))
		for _, item := range items {
			if e.in.id(item) != id {
				kept = append(kept, item)
			}
		}
		return kept
	})
}

// apply runs change against the collection as one gated transaction.
func (e element[T]) apply(ctx context.Context, siteID, deviceID string, change func([]T) []T) error {
	e.data.applying.Lock()
	defer e.data.applying.Unlock()

	doc, err := e.document(ctx, siteID, deviceID)
	if err != nil {
		return err
	}
	var desired routerconfig.Config
	e.in.store(&desired, change(e.in.list(routerconfig.Read(doc))))
	writes, pre, err := desired.Plan(doc)
	if err != nil {
		return err
	}
	if len(writes) == 0 {
		return nil // the cloud already holds it
	}
	if err := e.writable(ctx); err != nil {
		return err
	}
	if recovered, err := e.data.Transactions.Recover(ctx); err != nil {
		return fmt.Errorf("recovering an unfinished transaction: %w", err)
	} else if recovered {
		return errors.New("an earlier transaction was rolled back and the cloud repaired; nothing else was applied, so run terraform plan again")
	}
	_, err = e.data.Transactions.Run(ctx, txn.Change{Writes: writes, PreImages: pre})
	return err
}

// writable returns why a change cannot be applied, or nil.
func (e element[T]) writable(ctx context.Context) error {
	switch {
	case e.data.ReadOnly:
		return fmt.Errorf("the provider is read_only, and changing a %s writes to the Alta cloud", e.in.label)
	case e.data.Transactions == nil:
		return errors.New("changing router configuration needs the provider's ssh block, to gate the push and roll it back if it fails")
	}
	if err := e.data.Transactions.Ready(ctx); err != nil && !errors.Is(err, txn.ErrRecoveryNeeded) {
		return fmt.Errorf("%w; any push would discard them, so bring the cloud level with the router first", err)
	}
	return nil
}

// refuseUnwritable puts the reason a change cannot be applied into the plan, so it is
// refused before anything is pushed rather than half way through an apply.
func (e element[T]) refuseUnwritable(req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if e.data == nil || req.Plan.Raw.IsNull() || req.State.Raw.Equal(req.Plan.Raw) {
		return
	}
	switch {
	case e.data.ReadOnly:
		resp.Diagnostics.AddError("Provider is read-only",
			fmt.Sprintf("This plan would change a %s, and the provider is configured with read_only = true.", e.in.label))
	case e.data.Transactions == nil:
		resp.Diagnostics.AddError("Router connection required",
			fmt.Sprintf("Changing a %s needs the provider's ssh block, so the push can be gated and rolled back.", e.in.label))
	}
}

// planGeneratedID refuses a change that cannot be applied, and fills in an id the user did
// not choose. Without that the id is unknown until apply, which leaves every plan for a new
// item reading "known after apply" where its identity should be.
func (e element[T]) planGeneratedID(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	e.refuseUnwritable(req, resp)
	if req.Plan.Raw.IsNull() || resp.Diagnostics.HasError() {
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, idPath, &id)...)
	if id.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, idPath, types.StringValue(newAltaID()))...)
	}
}

// generatedID returns the planned id, or a new one when the plan left it unset.
func generatedID(id types.String) types.String {
	if id.IsUnknown() || id.ValueString() == "" {
		return types.StringValue(newAltaID())
	}
	return id
}

// siteAttributes are the attributes every single-item resource carries.
func siteAttributes(idDescription string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"site_id": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Alta site id.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"device_id": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Router device id: its MAC address, lowercase without separators.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"id": schema.StringAttribute{
			Optional: true, Computed: true,
			MarkdownDescription: idDescription,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
		},
	}
}

// with adds attributes to a base set, so the shared ones are declared once.
func with(base map[string]schema.Attribute, extra map[string]schema.Attribute) map[string]schema.Attribute {
	for name, attribute := range extra {
		base[name] = attribute
	}
	return base
}

// altaIDAlphabet is the one nanoid uses, which is what the portal generates ids with.
const altaIDAlphabet = "useandom-26T198340PX75pxJACKVERYMINDBUSHWOLFGQZbfghjklqvwyzrict"

// newAltaID returns a 6-character id shaped like the ones the portal creates, for an item
// whose id the user did not choose.
func newAltaID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		panic(err) // crypto/rand does not fail on any platform this runs on
	}
	for i, b := range buf {
		buf[i] = altaIDAlphabet[int(b)%len(altaIDAlphabet)]
	}
	return string(buf)
}

// importParts splits "<site_id>/<device_id>/<id>", the import form of a single item.
func importParts(id string, diags *diag.Diagnostics, label string) (site, device, item string, ok bool) {
	parts := strings.SplitN(id, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		diags.AddError("Invalid import id",
			fmt.Sprintf("Use <site_id>/<device_id>/<%s id> to import a %s.", label, label))
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
