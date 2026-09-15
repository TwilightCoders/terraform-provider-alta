// Package provider wires the Alta Labs provider into terraform-plugin-framework.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Address is the registry address Terraform uses to locate this provider.
const Address = "registry.terraform.io/twilightcoders/alta-labs"

var _ provider.Provider = (*Provider)(nil)

// Provider is the Alta Labs provider.
type Provider struct {
	version string
}

// New returns a constructor for the provider at the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &Provider{version: version}
	}
}

func (p *Provider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "alta"
	resp.Version = p.version
}

func (p *Provider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Alta Labs routers through the Alta cloud, so the portal always shows what Terraform applied.",
	}
}

func (p *Provider) Configure(_ context.Context, _ provider.ConfigureRequest, _ *provider.ConfigureResponse) {
}

func (p *Provider) Resources(_ context.Context) []func() resource.Resource {
	return nil
}

func (p *Provider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}
