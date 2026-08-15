// Package provider implements the Terraform provider for Simplifyd Cloud.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Ensure simplifydProvider satisfies the provider interface.
var _ provider.Provider = (*simplifydProvider)(nil)

// New returns a provider factory for the given build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &simplifydProvider{version: version}
	}
}

type simplifydProvider struct {
	version string
}

// providerData is handed to every resource and data source via Configure.
//
// The Simplifyd API is path-scoped (/workspaces/:ws/projects/:proj/envs/:env/...),
// so the provider carries default scope slugs that individual resources may
// override. This keeps ordinary configs from repeating the same three
// attributes on every resource.
type providerData struct {
	client    *cloud.Client
	workspace string
	project   string
	env       string
}

type providerModel struct {
	APIToken  types.String `tfsdk:"api_token"`
	APIURL    types.String `tfsdk:"api_url"`
	Workspace types.String `tfsdk:"workspace"`
	Project   types.String `tfsdk:"project"`
	Env       types.String `tfsdk:"env"`
}

func (p *simplifydProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "simplifyd"
	resp.Version = p.version
}

func (p *simplifydProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Simplifyd Cloud projects, environments, and services.",
		Attributes: map[string]schema.Attribute{
			"api_token": schema.StringAttribute{
				MarkdownDescription: "Simplifyd API token (`sk_proj_*`) or JWT. " +
					"May also be set with the `SIMPLIFYD_API_TOKEN` environment variable. " +
					"Prefer a workspace-scoped machine token over a personal JWT.",
				Optional:  true,
				Sensitive: true,
			},
			"api_url": schema.StringAttribute{
				MarkdownDescription: "API base URL. Defaults to `" + cloud.DefaultBaseURL + "`. " +
					"May also be set with the `SIMPLIFYD_API_URL` environment variable.",
				Optional: true,
			},
			"workspace": schema.StringAttribute{
				MarkdownDescription: "Default workspace slug for resources that do not set one. " +
					"May also be set with the `SIMPLIFYD_WORKSPACE` environment variable.",
				Optional: true,
			},
			"project": schema.StringAttribute{
				MarkdownDescription: "Default project slug. May also be set with `SIMPLIFYD_PROJECT`.",
				Optional:            true,
			},
			"env": schema.StringAttribute{
				MarkdownDescription: "Default environment slug. May also be set with `SIMPLIFYD_ENV`.",
				Optional:            true,
			},
		},
	}
}

func (p *simplifydProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	token := firstNonEmpty(cfg.APIToken.ValueString(), os.Getenv("SIMPLIFYD_API_TOKEN"))
	if token == "" {
		resp.Diagnostics.AddAttributeError(
			pathRoot("api_token"),
			"Missing Simplifyd API token",
			"Set the provider's api_token attribute or the SIMPLIFYD_API_TOKEN environment variable.",
		)
		return
	}

	opts := []cloud.Option{cloud.WithToken(token)}
	if url := firstNonEmpty(cfg.APIURL.ValueString(), os.Getenv("SIMPLIFYD_API_URL")); url != "" {
		opts = append(opts, cloud.WithBaseURL(url))
	}

	data := &providerData{
		client:    cloud.NewClient(opts...),
		workspace: firstNonEmpty(cfg.Workspace.ValueString(), os.Getenv("SIMPLIFYD_WORKSPACE")),
		project:   firstNonEmpty(cfg.Project.ValueString(), os.Getenv("SIMPLIFYD_PROJECT")),
		env:       firstNonEmpty(cfg.Env.ValueString(), os.Getenv("SIMPLIFYD_ENV")),
	}

	resp.DataSourceData = data
	resp.ResourceData = data
}

func (p *simplifydProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewProjectResource,
		NewEnvironmentResource,
		NewServiceResource,
		NewServiceVariableResource,
		NewServiceConfigResource,
		NewIngressResource,
		// TODO: NewPrivateAccessGrantResource, NewCloudAccountResource,
		// NewWorkspaceMemberResource, NewProjectTokenResource.
	}
}

func (p *simplifydProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewServiceDataSource,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
