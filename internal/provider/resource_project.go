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
	_ resource.Resource                = (*projectResource)(nil)
	_ resource.ResourceWithConfigure   = (*projectResource)(nil)
	_ resource.ResourceWithImportState = (*projectResource)(nil)
)

func NewProjectResource() resource.Resource { return &projectResource{} }

type projectResource struct{ pd *providerData }

type projectModel struct {
	ID          types.String `tfsdk:"id"`
	Workspace   types.String `tfsdk:"workspace"`
	Name        types.String `tfsdk:"name"`
	Slug        types.String `tfsdk:"slug"`
	NetworkSlug types.String `tfsdk:"network_slug"`
}

func (r *projectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A project groups environments under a workspace.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<workspace>/<project>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"workspace": schema.StringAttribute{
				MarkdownDescription: workspaceDoc,
				Optional:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Human-readable project name.",
				Required:            true,
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Server-assigned slug, derived from the name at creation.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"network_slug": schema.StringAttribute{
				MarkdownDescription: "Slug of the private network backing this project.",
				Computed:            true,
			},
		},
	}
}

func (r *projectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.pd = req.ProviderData.(*providerData)
}

func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ws := firstNonEmpty(plan.Workspace.ValueString(), r.pd.workspace)
	if ws == "" {
		resp.Diagnostics.AddAttributeError(pathRoot("workspace"), "Missing workspace",
			"Set `workspace` on the resource or a default on the provider block.")
		return
	}

	proj, err := r.pd.client.Workspace(ws).CreateProject(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Creating project", err.Error())
		return
	}

	r.apply(&plan, ws, proj)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state projectModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 2)
	if err != nil {
		resp.Diagnostics.AddError("Malformed project ID", err.Error())
		return
	}
	ws, slug := parts[0], parts[1]

	proj, err := r.pd.client.Workspace(ws).Project(slug).Get(ctx)
	if err != nil {
		if gone(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading project", err.Error())
		return
	}

	r.apply(&state, ws, proj)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *projectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state projectModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 2)
	if err != nil {
		resp.Diagnostics.AddError("Malformed project ID", err.Error())
		return
	}
	ws, slug := parts[0], parts[1]

	proj, err := r.pd.client.Workspace(ws).Project(slug).Update(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Updating project", err.Error())
		return
	}

	r.apply(&plan, ws, proj)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *projectResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	// The Simplifyd API exposes no project deletion endpoint, so destroy can
	// only forget the resource. Warn loudly rather than silently leaking it.
	resp.Diagnostics.AddWarning(
		"Project not deleted",
		"The Simplifyd API does not support deleting projects. The project has been removed "+
			"from Terraform state but still exists and may still incur cost. Delete it from the "+
			"dashboard if that was not intended.",
	)
}

func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parseID(req.ID, 2); err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			"Expected `<workspace>/<project>`: "+err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *projectResource) apply(m *projectModel, ws string, p *cloud.Project) {
	m.ID = types.StringValue(makeID(ws, p.Slug))
	m.Workspace = types.StringValue(ws)
	m.Name = types.StringValue(p.Name)
	m.Slug = types.StringValue(p.Slug)
	m.NetworkSlug = types.StringValue(p.NetworkSlug)
}
