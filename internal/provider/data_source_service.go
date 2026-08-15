package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = (*serviceDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*serviceDataSource)(nil)
)

func NewServiceDataSource() datasource.DataSource { return &serviceDataSource{} }

type serviceDataSource struct{ pd *providerData }

type serviceDataSourceModel struct {
	ID        types.String `tfsdk:"id"`
	Workspace types.String `tfsdk:"workspace"`
	Project   types.String `tfsdk:"project"`
	Env       types.String `tfsdk:"env"`
	Slug      types.String `tfsdk:"slug"`

	Name            types.String `tfsdk:"name"`
	Type            types.String `tfsdk:"type"`
	Status          types.String `tfsdk:"status"`
	VCPUs           types.Int64  `tfsdk:"vcpus"`
	Memory          types.Int64  `tfsdk:"memory"`
	Replicas        types.Int64  `tfsdk:"replicas"`
	PrivateHostname types.String `tfsdk:"private_hostname"`
	VanityFQDNs     types.List   `tfsdk:"vanity_fqdns"`
}

func (d *serviceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service"
}

func (d *serviceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Look up an existing service by slug — useful for wiring a service " +
			"managed elsewhere into this configuration.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{MarkdownDescription: "`<workspace>/<project>/<env>/<service>`.", Computed: true},
			"workspace": schema.StringAttribute{MarkdownDescription: workspaceDoc, Optional: true},
			"project":   schema.StringAttribute{MarkdownDescription: projectDoc, Optional: true},
			"env":       schema.StringAttribute{MarkdownDescription: envDoc, Optional: true},
			"slug":      schema.StringAttribute{MarkdownDescription: "Slug of the service to look up.", Required: true},

			"name":             schema.StringAttribute{Computed: true},
			"type":             schema.StringAttribute{Computed: true},
			"status":           schema.StringAttribute{Computed: true},
			"vcpus":            schema.Int64Attribute{Computed: true},
			"memory":           schema.Int64Attribute{Computed: true},
			"replicas":         schema.Int64Attribute{Computed: true},
			"private_hostname": schema.StringAttribute{MarkdownDescription: "In-cluster DNS name.", Computed: true},
			"vanity_fqdns": schema.ListAttribute{
				MarkdownDescription: "Platform-assigned hostnames across all ingress ports.",
				ElementType:         types.StringType,
				Computed:            true,
			},
		},
	}
}

func (d *serviceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	d.pd = req.ProviderData.(*providerData)
}

func (d *serviceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg serviceDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s, diags := resolveScope(d.pd, cfg.Workspace, cfg.Project, cfg.Env, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	svc, err := s.services(d.pd.client).Get(ctx, cfg.Slug.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading service", err.Error())
		return
	}

	fqdns := make([]string, 0, len(svc.Ingress))
	for _, p := range svc.Ingress {
		if p.VanityFQDN != "" {
			fqdns = append(fqdns, p.VanityFQDN)
		}
	}
	list, d2 := types.ListValueFrom(ctx, types.StringType, fqdns)
	resp.Diagnostics.Append(d2...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg.ID = types.StringValue(makeID(s.workspace, s.project, s.env, svc.Slug))
	cfg.Workspace = types.StringValue(s.workspace)
	cfg.Project = types.StringValue(s.project)
	cfg.Env = types.StringValue(s.env)
	cfg.Name = types.StringValue(svc.Name)
	cfg.Type = types.StringValue(string(svc.Type))
	cfg.Status = types.StringValue(string(svc.Status))
	cfg.VCPUs = types.Int64Value(int64(svc.VCPUs))
	cfg.Memory = types.Int64Value(int64(svc.Memory))
	cfg.Replicas = types.Int64Value(int64(svc.Replicas))
	cfg.PrivateHostname = types.StringValue(svc.PrivateHostname)
	cfg.VanityFQDNs = list

	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
