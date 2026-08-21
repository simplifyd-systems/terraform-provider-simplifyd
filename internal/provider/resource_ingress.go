package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

var (
	_ resource.Resource                = (*ingressResource)(nil)
	_ resource.ResourceWithConfigure   = (*ingressResource)(nil)
	_ resource.ResourceWithImportState = (*ingressResource)(nil)
)

func NewIngressResource() resource.Resource { return &ingressResource{} }

type ingressResource struct{ pd *providerData }

type ingressModel struct {
	ID      types.String `tfsdk:"id"`
	Env     types.String `tfsdk:"env"`
	Service types.String `tfsdk:"service"`

	Protocol            types.String `tfsdk:"protocol"`
	Port                types.Int64  `tfsdk:"port"`
	CustomFQDN          types.String `tfsdk:"custom_fqdn"`
	AllowedSourceRanges types.List   `tfsdk:"allowed_source_ranges"`

	Slug       types.String `tfsdk:"slug"`
	VanityFQDN types.String `tfsdk:"vanity_fqdn"`
}

func (r *ingressResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ingress"
}

func (r *ingressResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceStr := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An externally reachable port on a service.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<workspace>/<project>/<env>/<service>/<ingress>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"env": schema.StringAttribute{MarkdownDescription: envDoc, Optional: true, Computed: true, PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown(),
			}},
			"service": schema.StringAttribute{
				MarkdownDescription: "Slug of the service exposing the port.",
				Required:            true,
				PlanModifiers:       replaceStr,
			},
			"protocol": schema.StringAttribute{
				MarkdownDescription: "`HTTP`, `gRPC`, or `TCP`. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       replaceStr,
				Validators:          []validator.String{stringvalidator.OneOf("HTTP", "gRPC", "TCP")},
			},
			"port": schema.Int64Attribute{
				MarkdownDescription: "Container port to expose. Changing it forces replacement.",
				Required:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"custom_fqdn": schema.StringAttribute{
				MarkdownDescription: "Optional custom domain to attach. Point a CNAME at `vanity_fqdn` " +
					"before applying, or certificate issuance will not complete.",
				Optional:      true,
				PlanModifiers: replaceStr,
			},
			"allowed_source_ranges": schema.ListAttribute{
				MarkdownDescription: "Client IP allowlist as CIDRs (bare IPs are treated as `/32`). " +
					"TCP/UDP ports only. Omit or leave empty to allow all sources. " +
					"Updates apply to the live load balancer without a redeploy.",
				ElementType: types.StringType,
				Optional:    true,
			},
			"slug":        schema.StringAttribute{MarkdownDescription: "Server-assigned slug.", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"vanity_fqdn": schema.StringAttribute{MarkdownDescription: "Platform-assigned hostname for this port.", Computed: true},
		},
	}
}

func (r *ingressResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.pd = req.ProviderData.(*providerData)
}

func (r *ingressResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ingressModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s, diags := resolveScope(r.pd, plan.Env, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	ranges, d := toStrings(ctx, plan.AllowedSourceRanges)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	svc := plan.Service.ValueString()
	port, err := s.services(r.pd.client).Ingress(svc).Add(ctx, cloud.AddIngressInput{
		Protocol:            plan.Protocol.ValueString(),
		Port:                int(plan.Port.ValueInt64()),
		CustomFQDN:          plan.CustomFQDN.ValueString(),
		AllowedSourceRanges: ranges,
	})
	if err != nil {
		resp.Diagnostics.AddError("Adding ingress port", err.Error())
		return
	}
	plan.Env = types.StringValue(s.env)
	plan.Slug = types.StringValue(port.Slug)
	plan.VanityFQDN = types.StringValue(port.VanityFQDN)
	plan.ID = types.StringValue(makeID(s.workspace, s.project, s.env, svc, port.Slug))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ingressResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ingressModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed ingress ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	svc, err := s.services(r.pd.client).Get(ctx, parts[3])
	if err != nil {
		if gone(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading service", err.Error())
		return
	}

	for _, p := range svc.Ingress {
		if p.Slug != parts[4] {
			continue
		}
		state.Env = types.StringValue(s.env)
		state.Service = types.StringValue(parts[3])
		state.Protocol = types.StringValue(p.Protocol)
		state.Port = types.Int64Value(int64(p.Port))
		state.Slug = types.StringValue(p.Slug)
		state.VanityFQDN = types.StringValue(p.VanityFQDN)

		// Preserve null vs empty: an unset allowlist must not show as [] drift.
		if len(p.AllowedSourceRanges) > 0 || !state.AllowedSourceRanges.IsNull() {
			list, d := types.ListValueFrom(ctx, types.StringType, p.AllowedSourceRanges)
			resp.Diagnostics.Append(d...)
			if resp.Diagnostics.HasError() {
				return
			}
			state.AllowedSourceRanges = list
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *ingressResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ingressModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed ingress ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	// Everything except the allowlist is RequiresReplace, so this is the only
	// in-place change the API supports.
	ranges, d := toStrings(ctx, plan.AllowedSourceRanges)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := s.services(r.pd.client).Ingress(parts[3]).SetSourceRanges(ctx, parts[4], ranges); err != nil {
		resp.Diagnostics.AddError("Updating ingress source ranges", err.Error())
		return
	}

	plan.ID = state.ID
	plan.Slug = state.Slug
	plan.VanityFQDN = state.VanityFQDN
	plan.Env = types.StringValue(s.env)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ingressResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ingressModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed ingress ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}
	if err := s.services(r.pd.client).Ingress(parts[3]).Delete(ctx, parts[4]); err != nil && !gone(err) {
		resp.Diagnostics.AddError("Deleting ingress port", err.Error())
	}
}

func (r *ingressResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parseID(req.ID, 5); err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			"Expected `<workspace>/<project>/<env>/<service>/<ingress>`: "+err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
