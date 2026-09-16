// Package resources implements the provider's Terraform resources.
package resources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/device"
	"github.com/TwilightCoders/terraform-provider-alta/internal/txn"
)

// CloudReader reads the cloud objects a router's configuration is compiled from.
type CloudReader interface {
	Site(ctx context.Context, siteID string) (cloud.Object, error)
	State(ctx context.Context, siteID string) (cloud.State, error)
}

// Transactor applies changes as gated, commit-confirmed transactions.
type Transactor interface {
	Ready(ctx context.Context) error
	Recover(ctx context.Context) (bool, error)
	Run(ctx context.Context, change txn.Change) (txn.Result, error)
}

// DeviceHooks manages what the provider keeps on the router itself: hook scripts it runs
// for itself, and files that must survive the rebuild of /etc.
type DeviceHooks interface {
	Put(ctx context.Context, h device.Hook) (device.HookState, error)
	Get(ctx context.Context, h device.Hook) (device.HookState, error)
	Delete(ctx context.Context, h device.Hook, destroy string) error
	// SourceLine is the one line post-cfg.sh needs, so the provider can name it when the
	// router is missing it. That file is not the provider's to write.
	SourceLine() string
	PutFile(ctx context.Context, f device.File) (device.FileState, error)
	GetFile(ctx context.Context, f device.File) (device.FileState, error)
	DeleteFile(ctx context.Context, f device.File) error
}

// ProviderData is what the provider hands to resources.
type ProviderData struct {
	Cloud CloudReader
	// Transactions is nil when the provider has no router connection; reads still work.
	Transactions Transactor
	// Hooks is nil without a router connection.
	Hooks DeviceHooks
	// ReadOnly refuses every change, for importing and planning against production.
	ReadOnly bool
}

// providerData extracts ProviderData from a Configure request.
func providerData(raw any, diags *diag.Diagnostics) *ProviderData {
	if raw == nil {
		return nil // provider not yet configured (validation or import planning)
	}
	data, ok := raw.(*ProviderData)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *resources.ProviderData, got %T", raw))
		return nil
	}
	return data
}
