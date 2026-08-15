package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = (*environmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*environmentResource)(nil)
	_ resource.ResourceWithImportState = (*environmentResource)(nil)
)

func NewEnvironmentResource() resource.Resource { return &environmentResource{} }

type environmentResource struct{ pd *providerData }

type environmentModel struct {
	ID        types.String `tfsdk:"id"`
	Workspace types.String `tfsdk:"workspace"`
	Project   types.String `tfsdk:"project"`
	Name      types.String `tfsdk:"name"`
	Slug      types.String `tfsdk:"slug"`
}

func (r *environmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment"
}

func (r *environmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A deployment environment within a project (e.g. `production`, `staging`).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<workspace>/<project>/<env>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"workspace": schema.StringAttribute{
				MarkdownDescription: workspaceDoc,
				Optional:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"project": schema.StringAttribute{
				MarkdownDescription: projectDoc,
				Optional:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Human-readable environment name.",
				Required:            true,
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Server-assigned slug.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *environmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.pd = req.ProviderData.(*providerData)
}

func (r *environmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan environmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s, diags := resolveScope(r.pd, plan.Workspace, plan.Project, types.StringNull(), false)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	env, err := r.pd.client.Workspace(s.workspace).Project(s.project).CreateEnv(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Creating environment", err.Error())
		return
	}

	plan.ID = types.StringValue(makeID(s.workspace, s.project, env.Slug))
	plan.Workspace = types.StringValue(s.workspace)
	plan.Project = types.StringValue(s.project)
	plan.Name = types.StringValue(env.Name)
	plan.Slug = types.StringValue(env.Slug)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *environmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state environmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 3)
	if err != nil {
		resp.Diagnostics.AddError("Malformed environment ID", err.Error())
		return
	}
	ws, proj, slug := parts[0], parts[1], parts[2]

	env, err := r.pd.client.Workspace(ws).Project(proj).Env(slug).Get(ctx)
	if err != nil {
		if gone(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading environment", err.Error())
		return
	}

	state.Workspace = types.StringValue(ws)
	state.Project = types.StringValue(proj)
	state.Name = types.StringValue(env.Name)
	state.Slug = types.StringValue(env.Slug)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *environmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state environmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 3)
	if err != nil {
		resp.Diagnostics.AddError("Malformed environment ID", err.Error())
		return
	}

	env, err := r.pd.client.Workspace(parts[0]).Project(parts[1]).Env(parts[2]).Update(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Updating environment", err.Error())
		return
	}

	plan.ID = state.ID
	plan.Workspace = types.StringValue(parts[0])
	plan.Project = types.StringValue(parts[1])
	plan.Name = types.StringValue(env.Name)
	plan.Slug = types.StringValue(env.Slug)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *environmentResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddWarning(
		"Environment not deleted",
		"The Simplifyd API does not support deleting environments. The environment has been "+
			"removed from Terraform state but still exists, along with any services in it.",
	)
}

func (r *environmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parseID(req.ID, 3); err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			"Expected `<workspace>/<project>/<env>`: "+err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
