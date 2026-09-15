// Package resources implements the provider's Terraform resources.
package resources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/TwilightCoders/terraform-provider-alta-labs/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta-labs/internal/txn"
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

// ProviderData is what the provider hands to resources.
type ProviderData struct {
	Cloud CloudReader
	// Transactions is nil when the provider has no router connection; reads still work.
	Transactions Transactor
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
