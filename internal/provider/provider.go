// Package provider implements the Terraform provider for Simplifyd Cloud.
package provider

import (
	"context"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

// Ensure simplifydProvider satisfies the provider interface.
var _ provider.Provider = (*simplifydProvider)(nil)

// tokenPrefix marks a project token. Personal JWTs are deliberately rejected:
// a JWT carries a human's full authority across every workspace they belong to,
// which is far more than any Terraform run needs, and it expires mid-apply.
const tokenPrefix = "sk_proj_"

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
// but a project token already pins workspace and project, so those are read
// from the token rather than restated in configuration. Only the environment
// can vary, and only for a project-scoped token.
type providerData struct {
	client    *cloud.Client
	workspace string
	project   string
	env       string
	// envPinned is true when the token is scoped to a single environment, in
	// which case no resource may name a different one.
	envPinned bool
}

type providerModel struct {
	APIToken types.String `tfsdk:"api_token"`
	Env      types.String `tfsdk:"env"`
}

func (p *simplifydProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "simplifyd"
	resp.Version = p.version
}

func (p *simplifydProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Simplifyd Cloud environments and services.\n\n" +
			"Authentication is a project token (`sk_proj_*`), which already identifies " +
			"the workspace and project it belongs to — neither is configured here. " +
			"Personal JWTs are not accepted.",
		Attributes: map[string]schema.Attribute{
			"api_token": schema.StringAttribute{
				MarkdownDescription: "Simplifyd project token (`sk_proj_*`). " +
					"May also be set with the `SIMPLIFYD_API_TOKEN` environment variable, " +
					"which is the recommended place for it.",
				Optional:  true,
				Sensitive: true,
			},
			"env": schema.StringAttribute{
				MarkdownDescription: "Default environment slug for resources that do not set one. " +
					"May also be set with the `SIMPLIFYD_ENV` environment variable. " +
					"Omit it entirely when the token is scoped to a single environment — " +
					"that environment is then used everywhere.",
				Optional: true,
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
			"Set the provider's api_token attribute or the SIMPLIFYD_API_TOKEN environment variable. "+
				"Create a project token in the Simplifyd console under Project → Settings → Tokens.",
		)
		return
	}
	if !strings.HasPrefix(token, tokenPrefix) {
		resp.Diagnostics.AddAttributeError(
			pathRoot("api_token"),
			"Unsupported credential",
			"This provider accepts project tokens (`"+tokenPrefix+"...`) only. The value supplied does not "+
				"look like one — personal JWTs are rejected because they carry account-wide authority "+
				"and expire mid-apply. Create a project token in the Simplifyd console under "+
				"Project → Settings → Tokens.",
		)
		return
	}

	client := cloud.NewClient(cloud.WithToken(token))

	// The token knows its own workspace and project; ask once at configure time
	// so nothing downstream has to make the operator restate them. This doubles
	// as credential validation, turning an expired token into a clear error
	// before the first resource is planned instead of a 401 mid-apply.
	tokenScope, err := client.TokenScope(ctx)
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			pathRoot("api_token"),
			"Could not verify Simplifyd API token",
			"Reading the token's scope from "+cloud.DefaultBaseURL+" failed: "+err.Error(),
		)
		return
	}
	if !tokenScope.IsProjectToken() {
		resp.Diagnostics.AddAttributeError(
			pathRoot("api_token"),
			"Unsupported credential",
			"The API reports this credential as \""+tokenScope.Kind+"\". This provider requires a project token.",
		)
		return
	}

	data := &providerData{
		client:    client,
		workspace: tokenScope.Workspace,
		project:   tokenScope.Project,
		env:       tokenScope.Env,
		envPinned: tokenScope.Env != "",
	}

	// An explicit env is only meaningful for a project-scoped token. For an
	// env-scoped one it can only agree with the token or be wrong, and silently
	// ignoring a wrong value would apply changes to an environment the config
	// does not name.
	if configured := firstNonEmpty(cfg.Env.ValueString(), os.Getenv("SIMPLIFYD_ENV")); configured != "" {
		if data.envPinned && configured != data.env {
			resp.Diagnostics.AddAttributeError(
				pathRoot("env"),
				"Environment conflicts with token scope",
				"The token is scoped to environment "+data.env+", but env is set to "+configured+
					". Remove env, or use a project-scoped token.",
			)
			return
		}
		data.env = configured
	}

	resp.DataSourceData = data
	resp.ResourceData = data
}

func (p *simplifydProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewEnvironmentResource,
		NewServiceResource,
		NewServiceVariableResource,
		NewServiceConfigResource,
		NewIngressResource,
		NewGatewayRouteResource,
		NewIPsecConnectionResource,
		// No project resource: creating a project is a workspace-level action,
		// and a project token has no authority above its own project.
		// TODO: NewPrivateAccessGrantResource, NewCloudAccountResource.
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
