package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

var (
	_ resource.Resource                = (*serviceConfigResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceConfigResource)(nil)
	_ resource.ResourceWithImportState = (*serviceConfigResource)(nil)
)

func NewServiceConfigResource() resource.Resource { return &serviceConfigResource{} }

type serviceConfigResource struct{ pd *providerData }

type serviceConfigModel struct {
	ID        types.String `tfsdk:"id"`
	Env       types.String `tfsdk:"env"`
	Service   types.String `tfsdk:"service"`
	Name      types.String `tfsdk:"name"`
	Content   types.String `tfsdk:"content"`
	MountPath types.String `tfsdk:"mount_path"`
	Slug      types.String `tfsdk:"slug"`
}

func (r *serviceConfigResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_config"
}

func (r *serviceConfigResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A static config file mounted into a service's container.\n\n" +
			"`content` supports `${{VAR_NAME}}` interpolation, resolved at deploy time against the " +
			"service's variables plus the `SERVICE`, `ENVIRONMENT`, and `PROJECT` system variables. " +
			"In Terraform, escape those as `$${{VAR_NAME}}` so HCL does not try to interpolate them.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<workspace>/<project>/<env>/<service>/<config>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"env": schema.StringAttribute{MarkdownDescription: envDoc, Optional: true, Computed: true, PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown(),
			}},
			"service": schema.StringAttribute{
				MarkdownDescription: "Slug of the service to mount the file into.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"name":       schema.StringAttribute{MarkdownDescription: "Display name for the config.", Required: true},
			"content":    schema.StringAttribute{MarkdownDescription: "File contents.", Required: true},
			"mount_path": schema.StringAttribute{MarkdownDescription: "Absolute path inside the container, e.g. `/etc/app/config.yaml`.", Required: true},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Server-assigned slug.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *serviceConfigResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.pd = req.ProviderData.(*providerData)
}

func (r *serviceConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s, diags := resolveScope(r.pd, plan.Env, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	svc := plan.Service.ValueString()
	cfg, err := s.services(r.pd.client).Configs(svc).Create(ctx, cloud.CreateConfigInput{
		Name:      plan.Name.ValueString(),
		Content:   plan.Content.ValueString(),
		MountPath: plan.MountPath.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Creating service config", err.Error())
		return
	}
	plan.Env = types.StringValue(s.env)
	plan.Slug = types.StringValue(cfg.Slug)
	plan.ID = types.StringValue(makeID(s.workspace, s.project, s.env, svc, cfg.Slug))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed config ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	// Configs have no dedicated GET; they are returned inline on the service.
	svc, err := s.services(r.pd.client).Get(ctx, parts[3])
	if err != nil {
		if gone(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading service", err.Error())
		return
	}

	for _, cfg := range svc.Configs {
		if cfg.Slug == parts[4] {
			state.Env = types.StringValue(s.env)
			state.Service = types.StringValue(parts[3])
			state.Name = types.StringValue(cfg.Name)
			state.Content = types.StringValue(cfg.Content)
			state.MountPath = types.StringValue(cfg.MountPath)
			state.Slug = types.StringValue(cfg.Slug)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *serviceConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceConfigModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed config ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	if _, err := s.services(r.pd.client).Configs(parts[3]).Update(ctx, parts[4], cloud.UpdateConfigInput{
		Name:      plan.Name.ValueString(),
		Content:   plan.Content.ValueString(),
		MountPath: plan.MountPath.ValueString(),
	}); err != nil {
		resp.Diagnostics.AddError("Updating service config", err.Error())
		return
	}

	plan.ID = state.ID
	plan.Slug = state.Slug
	plan.Env = types.StringValue(s.env)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceConfigModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed config ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}
	if err := s.services(r.pd.client).Configs(parts[3]).Delete(ctx, parts[4]); err != nil && !gone(err) {
		resp.Diagnostics.AddError("Deleting service config", err.Error())
	}
}

func (r *serviceConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parseID(req.ID, 5); err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			"Expected `<workspace>/<project>/<env>/<service>/<config>`: "+err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
