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
	_ resource.Resource                = (*environmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*environmentResource)(nil)
	_ resource.ResourceWithImportState = (*environmentResource)(nil)
)

func NewEnvironmentResource() resource.Resource { return &environmentResource{} }

type environmentResource struct{ pd *providerData }

type environmentModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
	Slug types.String `tfsdk:"slug"`
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

	s, diags := resolveScope(r.pd, types.StringNull(), false)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An environment-scoped token is pinned to one environment and cannot make
	// more. Say so here rather than letting the API return a bare 403.
	if r.pd.envPinned {
		resp.Diagnostics.AddError("Cannot create environments with this token",
			"The API token is scoped to a single environment ("+s.env+"). Creating environments "+
				"needs a project-scoped token.")
		return
	}

	env, err := r.pd.client.Workspace(s.workspace).Project(s.project).CreateEnv(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Creating environment", err.Error())
		return
	}

	plan.ID = types.StringValue(makeID(s.workspace, s.project, env.Slug))
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
	plan.Name = types.StringValue(env.Name)
	plan.Slug = types.StringValue(env.Slug)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *environmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
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

	// Destroys every service in the environment, synchronously.
	if err := r.pd.client.Workspace(ws).Project(proj).Env(slug).Delete(ctx); err != nil {
		if gone(err) {
			return
		}

		// A project must keep at least one environment, so the API refuses to
		// delete the last one with 409. The provider cannot destroy the project
		// itself — that is above a project token's authority — so this is a real
		// failure rather than something to shrug off.
		if cloud.IsConflict(err) {
			resp.Diagnostics.AddError("Deleting environment",
				"A project must keep at least one environment, so "+slug+" cannot be deleted while "+
					"it is the only one. Create another environment first, or delete the whole "+
					"project from the Simplifyd console.")
			return
		}

		resp.Diagnostics.AddError("Deleting environment",
			"The environment was not deleted and still exists, along with any services in it. "+
				"It remains in Terraform state.\n\n"+err.Error())
		return
	}
}

func (r *environmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parseID(req.ID, 3); err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			"Expected `<workspace>/<project>/<env>`: "+err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
