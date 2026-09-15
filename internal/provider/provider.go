// Package provider wires the Alta Labs provider into terraform-plugin-framework.
package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
	"github.com/TwilightCoders/terraform-provider-alta/internal/device"
	"github.com/TwilightCoders/terraform-provider-alta/internal/resources"
	"github.com/TwilightCoders/terraform-provider-alta/internal/txn"
)

// Address is the registry address Terraform uses to locate this provider.
const Address = "registry.terraform.io/twilightcoders/alta"

// Environment variables read when the corresponding attribute is not set.
const (
	EnvEmail    = "ALTA_LABS_EMAIL"
	EnvPassword = "ALTA_LABS_PASSWORD"
	EnvAgent    = "SSH_AUTH_SOCK"
)

var _ provider.Provider = (*Provider)(nil)

// Provider is the Alta Labs provider.
type Provider struct {
	version string
	// build turns validated settings into resource dependencies; tests substitute fakes.
	build func(Settings) (*resources.ProviderData, error)
}

// New returns a constructor for the provider at the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &Provider{version: version, build: Build}
	}
}

type config struct {
	Email       types.String       `tfsdk:"email"`
	Password    types.String       `tfsdk:"password"`
	ReadOnly    types.Bool         `tfsdk:"read_only"`
	SSH         *sshConfig         `tfsdk:"ssh"`
	Transaction *transactionConfig `tfsdk:"transaction"`
	Probes      *probesConfig      `tfsdk:"probes"`
}

type sshConfig struct {
	Host               types.String `tfsdk:"host"`
	Port               types.Int64  `tfsdk:"port"`
	User               types.String `tfsdk:"user"`
	PrivateKey         types.String `tfsdk:"private_key"`
	HostKeyFingerprint types.String `tfsdk:"host_key_fingerprint"`
}

type transactionConfig struct {
	ConfirmWindow types.String `tfsdk:"confirm_window"`
	PushTimeout   types.String `tfsdk:"push_timeout"`
	Settle        types.String `tfsdk:"settle"`
}

type probesConfig struct {
	WANTarget  types.String `tfsdk:"wan_target"`
	DNSServer  types.String `tfsdk:"dns_server"`
	DNSName    types.String `tfsdk:"dns_name"`
	LANTargets []string     `tfsdk:"lan_targets"`
}

func (p *Provider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "alta"
	resp.Version = p.version
}

func (p *Provider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Alta Labs routers through the Alta cloud, so the portal always shows what Terraform applied.",
		Attributes: map[string]schema.Attribute{
			"email": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Alta account email. Defaults to `$" + EnvEmail + "`. The account must not require MFA.",
			},
			"password": schema.StringAttribute{
				Optional: true, Sensitive: true,
				MarkdownDescription: "Alta account password. Defaults to `$" + EnvPassword + "`.",
			},
			"read_only": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Refuse any plan that would write to the Alta cloud. " +
					"Use it to import and plan against a production router with a guarantee of no changes.",
			},
			"ssh": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Connection to the router, required for changes: it pauses the router's cloud agent, " +
					"verifies the push and rolls it back if health probes fail.",
				Attributes: map[string]schema.Attribute{
					"host": schema.StringAttribute{Required: true, MarkdownDescription: "Router address."},
					"port": schema.Int64Attribute{Optional: true, MarkdownDescription: "SSH port. Defaults to 22."},
					"user": schema.StringAttribute{Optional: true, MarkdownDescription: "SSH user. Defaults to `root`."},
					"private_key": schema.StringAttribute{
						Optional: true, Sensitive: true,
						MarkdownDescription: "PEM private key. Defaults to the agent at `$" + EnvAgent + "`.",
					},
					"host_key_fingerprint": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The router's host key, as printed by `ssh-keygen -lf`, e.g. `SHA256:AbCdEf…`.",
					},
				},
			},
			"transaction": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Timing of the commit-confirmed transaction.",
				Attributes: map[string]schema.Attribute{
					"confirm_window": duration("How long the router waits for confirmation before rolling back on its own. Defaults to `5m`."),
					"push_timeout":   duration("How long to wait for the cloud's push once the router resumes. Defaults to `90s`."),
					"settle":         duration("How long to wait after the push before probing. Defaults to `45s`."),
				},
			},
			"probes": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "What healthy looks like from the router. A probe that passed before a change must pass after it.",
				Attributes: map[string]schema.Attribute{
					"wan_target": schema.StringAttribute{Optional: true, MarkdownDescription: "Pinged to prove internet access. Defaults to `1.1.1.1`."},
					"dns_server": schema.StringAttribute{Optional: true, MarkdownDescription: "Resolver to test, e.g. the LAN's DNS server."},
					"dns_name": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "Name to resolve through `dns_server`. Must contain `" + device.NoncePlaceholder +
							"` and fall under a wildcard record, e.g. `" + device.NoncePlaceholder + ".example.net`, so each probe needs a real lookup.",
					},
					"lan_targets": schema.ListAttribute{
						Optional: true, ElementType: types.StringType,
						MarkdownDescription: "Hosts pinged to prove LAN reachability.",
					},
				},
			},
		},
	}
}

func duration(description string) schema.StringAttribute {
	return schema.StringAttribute{Optional: true, MarkdownDescription: description}
}

// Settings is validated provider configuration.
type Settings struct {
	Email, Password string
	ReadOnly        bool
	// SSH is nil when no router connection is configured.
	SSH    *device.SSHConfig
	Gate   device.Options
	Settle time.Duration
}

func (p *Provider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg config
	if resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...); resp.Diagnostics.HasError() {
		return
	}
	settings, diags := cfg.settings()
	if resp.Diagnostics.Append(diags...); resp.Diagnostics.HasError() {
		return
	}

	data, err := p.build(settings)
	if err != nil {
		resp.Diagnostics.AddError("Configuring the Alta provider", err.Error())
		return
	}
	resp.ResourceData = data
	resp.DataSourceData = data
}

// settings validates cfg.
func (c config) settings() (Settings, diag.Diagnostics) {
	var diags diag.Diagnostics
	invalid := func(at path.Path, msg string) { diags.AddAttributeError(at, "Invalid provider configuration", msg) }
	s := Settings{
		Email:    valueOrEnv(c.Email, EnvEmail),
		Password: valueOrEnv(c.Password, EnvPassword),
		ReadOnly: c.ReadOnly.ValueBool(),
	}
	if s.Email == "" || s.Password == "" {
		invalid(path.Root("email"), "Set email and password in the provider block or $"+EnvEmail+" and $"+EnvPassword+".")
	}

	durations := map[string]*time.Duration{"confirm_window": &s.Gate.Window, "push_timeout": &s.Gate.PushTimeout, "settle": &s.Settle}
	if c.Transaction != nil {
		for name, raw := range map[string]types.String{"confirm_window": c.Transaction.ConfirmWindow, "push_timeout": c.Transaction.PushTimeout, "settle": c.Transaction.Settle} {
			if raw.IsNull() {
				continue
			}
			d, err := time.ParseDuration(raw.ValueString())
			if err != nil || d <= 0 {
				invalid(path.Root("transaction").AtName(name), "Must be a positive duration such as 90s or 5m.")
				continue
			}
			*durations[name] = d
		}
	}

	s.Gate.Probes = device.ProbeSpec{WANTarget: "1.1.1.1"}
	if p := c.Probes; p != nil {
		if !p.WANTarget.IsNull() {
			s.Gate.Probes.WANTarget = p.WANTarget.ValueString()
		}
		s.Gate.Probes.DNSServer, s.Gate.Probes.DNSName, s.Gate.Probes.LANTargets = p.DNSServer.ValueString(), p.DNSName.ValueString(), p.LANTargets
		switch {
		case (s.Gate.Probes.DNSName == "") != (s.Gate.Probes.DNSServer == ""):
			invalid(path.Root("probes"), "Set dns_server and dns_name together.")
		case s.Gate.Probes.DNSName != "" && !strings.Contains(s.Gate.Probes.DNSName, device.NoncePlaceholder):
			invalid(path.Root("probes").AtName("dns_name"), "Must contain "+device.NoncePlaceholder+" so the lookup cannot be answered from a cache.")
		}
	}

	if c.SSH != nil {
		s.SSH = &device.SSHConfig{
			Host:               c.SSH.Host.ValueString(),
			Port:               int(c.SSH.Port.ValueInt64()),
			User:               c.SSH.User.ValueString(),
			PrivateKey:         []byte(c.SSH.PrivateKey.ValueString()),
			AgentSocket:        os.Getenv(EnvAgent),
			HostKeyFingerprint: c.SSH.HostKeyFingerprint.ValueString(),
		}
	}
	return s, diags
}

// Build composes the production dependencies.
func Build(s Settings) (*resources.ProviderData, error) {
	client := cloud.NewClient(cloud.Config{Email: s.Email, Password: s.Password})
	data := &resources.ProviderData{Cloud: client, ReadOnly: s.ReadOnly}
	if s.SSH == nil {
		return data, nil
	}
	runner, err := device.NewSSHRunner(*s.SSH)
	if err != nil {
		return nil, fmt.Errorf("router connection: %w", err)
	}
	data.Transactions = txn.New(client, device.NewGate(runner, s.Gate), txn.Options{Settle: s.Settle})
	return data, nil
}

func (p *Provider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{resources.NewRouterConfig}
}

func (p *Provider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}

func valueOrEnv(v types.String, env string) string {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueString()
	}
	return os.Getenv(env)
}
