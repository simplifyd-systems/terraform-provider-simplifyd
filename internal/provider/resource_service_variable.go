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
	_ resource.Resource                = (*serviceVariableResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceVariableResource)(nil)
	_ resource.ResourceWithImportState = (*serviceVariableResource)(nil)
)

func NewServiceVariableResource() resource.Resource { return &serviceVariableResource{} }

type serviceVariableResource struct{ pd *providerData }

type serviceVariableModel struct {
	ID        types.String `tfsdk:"id"`
	Workspace types.String `tfsdk:"workspace"`
	Project   types.String `tfsdk:"project"`
	Env       types.String `tfsdk:"env"`
	Service   types.String `tfsdk:"service"`
	Name      types.String `tfsdk:"name"`
	Value     types.String `tfsdk:"value"`
	Slug      types.String `tfsdk:"slug"`
}

func (r *serviceVariableResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_variable"
}

func (r *serviceVariableResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An environment variable set on a service.\n\n" +
			"Values are stored in Terraform state. Use a secret store (Vault, SSM, `ephemeral` " +
			"resources) as the source rather than literals in `.tf` files.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<workspace>/<project>/<env>/<service>/<variable>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"workspace": schema.StringAttribute{MarkdownDescription: workspaceDoc, Optional: true, PlanModifiers: replace},
			"project":   schema.StringAttribute{MarkdownDescription: projectDoc, Optional: true, PlanModifiers: replace},
			"env":       schema.StringAttribute{MarkdownDescription: envDoc, Optional: true, PlanModifiers: replace},
			"service": schema.StringAttribute{
				MarkdownDescription: "Slug of the service the variable belongs to.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Variable name. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "Variable value.",
				Required:            true,
				Sensitive:           true,
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Server-assigned slug.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *serviceVariableResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.pd = req.ProviderData.(*providerData)
}

func (r *serviceVariableResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceVariableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s, diags := resolveScope(r.pd, plan.Workspace, plan.Project, plan.Env, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	svc := plan.Service.ValueString()
	v, err := s.services(r.pd.client).Variables(svc).Set(ctx, plan.Name.ValueString(), plan.Value.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Setting service variable", err.Error())
		return
	}

	plan.Workspace = types.StringValue(s.workspace)
	plan.Project = types.StringValue(s.project)
	plan.Env = types.StringValue(s.env)
	plan.Slug = types.StringValue(v.Slug)
	plan.ID = types.StringValue(makeID(s.workspace, s.project, s.env, svc, v.Slug))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceVariableResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceVariableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed variable ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	vars, err := s.services(r.pd.client).Variables(parts[3]).List(ctx)
	if err != nil {
		if gone(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Listing service variables", err.Error())
		return
	}

	for _, v := range vars {
		if v.Slug == parts[4] {
			state.Workspace = types.StringValue(s.workspace)
			state.Project = types.StringValue(s.project)
			state.Env = types.StringValue(s.env)
			state.Service = types.StringValue(parts[3])
			state.Name = types.StringValue(v.Name)
			state.Value = types.StringValue(v.Value)
			state.Slug = types.StringValue(v.Slug)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *serviceVariableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceVariableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed variable ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	if _, err := s.services(r.pd.client).Variables(parts[3]).Update(ctx, parts[4], plan.Value.ValueString()); err != nil {
		resp.Diagnostics.AddError("Updating service variable", err.Error())
		return
	}

	plan.ID = state.ID
	plan.Slug = state.Slug
	plan.Workspace = types.StringValue(s.workspace)
	plan.Project = types.StringValue(s.project)
	plan.Env = types.StringValue(s.env)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceVariableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceVariableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed variable ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}
	if err := s.services(r.pd.client).Variables(parts[3]).Delete(ctx, parts[4]); err != nil && !gone(err) {
		resp.Diagnostics.AddError("Deleting service variable", err.Error())
	}
}

func (r *serviceVariableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parseID(req.ID, 5); err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			"Expected `<workspace>/<project>/<env>/<service>/<variable>`: "+err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
