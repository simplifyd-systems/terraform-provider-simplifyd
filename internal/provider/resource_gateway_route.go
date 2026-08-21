package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

var (
	_ resource.Resource                = (*gatewayRouteResource)(nil)
	_ resource.ResourceWithConfigure   = (*gatewayRouteResource)(nil)
	_ resource.ResourceWithImportState = (*gatewayRouteResource)(nil)
)

func NewGatewayRouteResource() resource.Resource { return &gatewayRouteResource{} }

type gatewayRouteResource struct{ pd *providerData }

type gatewayRouteModel struct {
	ID      types.String `tfsdk:"id"`
	Env     types.String `tfsdk:"env"`
	Service types.String `tfsdk:"service"`

	PathPrefix  types.String `tfsdk:"path_prefix"`
	BackendSlug types.String `tfsdk:"backend_slug"`
	BackendPort types.Int64  `tfsdk:"backend_port"`
	StripPrefix types.Bool   `tfsdk:"strip_prefix"`
	Priority    types.Int64  `tfsdk:"priority"`

	Slug types.String `tfsdk:"slug"`
}

func (r *gatewayRouteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gateway_route"
}

func (r *gatewayRouteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceStr := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		MarkdownDescription: "A path-prefix route on an HTTP gateway service, forwarding to a backend " +
			"service in the same environment.\n\n" +
			"Routes are stored when applied but reach the running gateway on its next deployment, " +
			"so redeploy the gateway (or leave `deploy = true` on it and touch it) to make a change live.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<workspace>/<project>/<env>/<gateway>/<route>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"env": schema.StringAttribute{MarkdownDescription: envDoc, Optional: true, Computed: true, PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown(),
			}},
			"service": schema.StringAttribute{
				MarkdownDescription: "Slug of the `http_gateway` service this route belongs to.",
				Required:            true,
				PlanModifiers:       replaceStr,
			},

			"path_prefix": schema.StringAttribute{
				MarkdownDescription: "Request path prefix to match, e.g. `/api`. Unique per gateway.",
				Required:            true,
			},
			"backend_slug": schema.StringAttribute{
				MarkdownDescription: "Slug of the backend service in the same environment. " +
					"Use the target service's `slug` attribute rather than its name.",
				Required: true,
			},
			"backend_port": schema.Int64Attribute{
				MarkdownDescription: "Port on the backend service to forward to.",
				Required:            true,
			},
			"strip_prefix": schema.BoolAttribute{
				MarkdownDescription: "Remove `path_prefix` from the path before forwarding. " +
					"Defaults to `false`, which passes the full path through.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"priority": schema.Int64Attribute{
				MarkdownDescription: "Ordering for overlapping prefixes; the higher priority wins. " +
					"Defaults to `0`.",
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(0),
			},

			"slug": schema.StringAttribute{
				MarkdownDescription: "Server-assigned slug.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *gatewayRouteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.pd = req.ProviderData.(*providerData)
}

func (m *gatewayRouteModel) toInput() cloud.GatewayRouteInput {
	return cloud.GatewayRouteInput{
		PathPrefix:  m.PathPrefix.ValueString(),
		BackendSlug: m.BackendSlug.ValueString(),
		BackendPort: uint(m.BackendPort.ValueInt64()),
		StripPrefix: m.StripPrefix.ValueBool(),
		Priority:    int(m.Priority.ValueInt64()),
	}
}

func (r *gatewayRouteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gatewayRouteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s, diags := resolveScope(r.pd, plan.Env, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	gateway := plan.Service.ValueString()
	route, err := s.services(r.pd.client).GatewayRoutes(gateway).Add(ctx, plan.toInput())
	if err != nil {
		resp.Diagnostics.AddError("Adding gateway route", err.Error())
		return
	}

	plan.Env = types.StringValue(s.env)
	plan.Slug = types.StringValue(route.Slug)
	plan.ID = types.StringValue(makeID(s.workspace, s.project, s.env, gateway, route.Slug))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *gatewayRouteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gatewayRouteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed gateway route ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	routes, err := s.services(r.pd.client).GatewayRoutes(parts[3]).List(ctx)
	if err != nil {
		if gone(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading gateway routes", err.Error())
		return
	}

	for _, route := range routes {
		if route.Slug != parts[4] {
			continue
		}
		state.Env = types.StringValue(s.env)
		state.Service = types.StringValue(parts[3])
		state.PathPrefix = types.StringValue(route.PathPrefix)
		state.BackendSlug = types.StringValue(route.BackendSlug)
		state.BackendPort = types.Int64Value(int64(route.BackendPort))
		state.StripPrefix = types.BoolValue(route.StripPrefix)
		state.Priority = types.Int64Value(int64(route.Priority))
		state.Slug = types.StringValue(route.Slug)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *gatewayRouteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state gatewayRouteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed gateway route ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	// The API replaces the whole route, so every field is sent even when only
	// one changed.
	if _, err := s.services(r.pd.client).GatewayRoutes(parts[3]).Update(ctx, parts[4], plan.toInput()); err != nil {
		resp.Diagnostics.AddError("Updating gateway route", err.Error())
		return
	}

	plan.ID = state.ID
	plan.Slug = state.Slug
	plan.Env = types.StringValue(s.env)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *gatewayRouteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gatewayRouteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed gateway route ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	if err := s.services(r.pd.client).GatewayRoutes(parts[3]).Delete(ctx, parts[4]); err != nil && !gone(err) {
		resp.Diagnostics.AddError("Deleting gateway route", err.Error())
	}
}

func (r *gatewayRouteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parseID(req.ID, 5); err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			"Expected `<workspace>/<project>/<env>/<gateway>/<route>`: "+err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
