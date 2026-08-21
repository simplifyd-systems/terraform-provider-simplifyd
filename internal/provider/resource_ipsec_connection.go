package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

var (
	_ resource.Resource                = (*ipsecConnectionResource)(nil)
	_ resource.ResourceWithConfigure   = (*ipsecConnectionResource)(nil)
	_ resource.ResourceWithImportState = (*ipsecConnectionResource)(nil)
)

func NewIPsecConnectionResource() resource.Resource { return &ipsecConnectionResource{} }

type ipsecConnectionResource struct{ pd *providerData }

type ipsecConnectionModel struct {
	ID      types.String `tfsdk:"id"`
	Env     types.String `tfsdk:"env"`
	Service types.String `tfsdk:"service"`

	Name          types.String `tfsdk:"name"`
	RemoteGateway types.String `tfsdk:"remote_gateway"`
	RemoteSubnets types.List   `tfsdk:"remote_subnets"`
	LocalSubnets  types.List   `tfsdk:"local_subnets"`
	LocalID       types.String `tfsdk:"local_id"`
	RemoteID      types.String `tfsdk:"remote_id"`
	PSK           types.String `tfsdk:"psk"`
	IKEProposal   types.String `tfsdk:"ike_proposal"`
	ESPProposal   types.String `tfsdk:"esp_proposal"`
	IKELifetime   types.String `tfsdk:"ike_lifetime"`
	Lifetime      types.String `tfsdk:"lifetime"`
	StartAction   types.String `tfsdk:"start_action"`

	Slug   types.String `tfsdk:"slug"`
	HasPSK types.Bool   `tfsdk:"has_psk"`
}

func (r *ipsecConnectionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipsec_connection"
}

func (r *ipsecConnectionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceStr := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		MarkdownDescription: "A site-to-site IPsec tunnel on a VPN gateway service.\n\n" +
			"Changes are stored when applied but reach the running gateway on its next deployment. " +
			"A tunnel without a pre-shared key comes up and fails authentication, so set `psk` " +
			"unless the counterparty's key is being loaded another way.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<workspace>/<project>/<env>/<gateway>/<connection>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"env": schema.StringAttribute{MarkdownDescription: envDoc, Optional: true, Computed: true, PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown(),
			}},
			"service": schema.StringAttribute{
				MarkdownDescription: "Slug of the `ipsec_gateway` service terminating this tunnel.",
				Required:            true,
				PlanModifiers:       replaceStr,
			},

			"name": schema.StringAttribute{
				MarkdownDescription: "Connection name, used in the gateway's swanctl configuration.",
				Required:            true,
			},
			"remote_gateway": schema.StringAttribute{
				MarkdownDescription: "Counterparty's public IKE endpoint (address or hostname).",
				Required:            true,
			},
			"remote_subnets": schema.ListAttribute{
				MarkdownDescription: "Ranges reachable through the counterparty. At least one is required.",
				ElementType:         types.StringType,
				Required:            true,
			},
			"local_subnets": schema.ListAttribute{
				MarkdownDescription: "Narrows the gateway's own local subnets for this tunnel. " +
					"Omit to inherit the gateway's set.",
				ElementType: types.StringType,
				Optional:    true,
			},
			"local_id": schema.StringAttribute{
				MarkdownDescription: "IKE identity this side presents. Defaults to the gateway's public address.",
				Optional:            true,
			},
			"remote_id": schema.StringAttribute{
				MarkdownDescription: "IKE identity expected from the counterparty.",
				Optional:            true,
			},
			"psk": schema.StringAttribute{
				MarkdownDescription: "Pre-shared key. Write-only: the API never returns it, so it is " +
					"stored in Terraform state and compared from there — source it from a secret " +
					"store rather than a literal. Changing it rotates the key, which takes effect " +
					"on the gateway's next deployment.",
				Optional:  true,
				Sensitive: true,
			},
			"ike_proposal": schema.StringAttribute{
				MarkdownDescription: "IKE (phase 1) algorithm proposal, e.g. `aes256-sha256-modp2048`. " +
					"Counterparties routinely mandate an exact set.",
				Optional: true,
				Computed: true,
			},
			"esp_proposal": schema.StringAttribute{
				MarkdownDescription: "ESP (phase 2) algorithm proposal.",
				Optional:            true,
				Computed:            true,
			},
			"ike_lifetime": schema.StringAttribute{
				MarkdownDescription: "IKE SA lifetime, e.g. `4h`.",
				Optional:            true,
				Computed:            true,
			},
			"lifetime": schema.StringAttribute{
				MarkdownDescription: "Child SA lifetime, e.g. `1h`.",
				Optional:            true,
				Computed:            true,
			},
			"start_action": schema.StringAttribute{
				MarkdownDescription: "What the gateway does with the tunnel on load: `start`, `trap`, or `none`.",
				Optional:            true,
				Computed:            true,
			},

			"slug": schema.StringAttribute{
				MarkdownDescription: "Server-assigned slug.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"has_psk": schema.BoolAttribute{
				MarkdownDescription: "Whether the platform holds a pre-shared key for this tunnel.",
				Computed:            true,
			},
		},
	}
}

func (r *ipsecConnectionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.pd = req.ProviderData.(*providerData)
}

func (m *ipsecConnectionModel) toInput(ctx context.Context) (cloud.IPsecConnectionInput, diag.Diagnostics) {
	var diags diag.Diagnostics

	remote, d := toStrings(ctx, m.RemoteSubnets)
	diags.Append(d...)
	local, d := toStrings(ctx, m.LocalSubnets)
	diags.Append(d...)

	return cloud.IPsecConnectionInput{
		Name:          m.Name.ValueString(),
		RemoteGateway: m.RemoteGateway.ValueString(),
		RemoteSubnets: remote,
		LocalSubnets:  local,
		LocalID:       m.LocalID.ValueString(),
		RemoteID:      m.RemoteID.ValueString(),
		IKEProposal:   m.IKEProposal.ValueString(),
		ESPProposal:   m.ESPProposal.ValueString(),
		IKELifetime:   m.IKELifetime.ValueString(),
		Lifetime:      m.Lifetime.ValueString(),
		StartAction:   m.StartAction.ValueString(),
	}, diags
}

// applyRemote copies the server's view back into the model. The pre-shared key
// is never returned, so it is left exactly as configured.
func (m *ipsecConnectionModel) applyRemote(ctx context.Context, conn *cloud.IPsecConnection) diag.Diagnostics {
	var diags diag.Diagnostics

	m.Name = types.StringValue(conn.Name)
	m.RemoteGateway = types.StringValue(conn.RemoteGateway)
	m.LocalID = optionalString(m.LocalID, conn.LocalID)
	m.RemoteID = optionalString(m.RemoteID, conn.RemoteID)
	m.IKEProposal = types.StringValue(conn.IKEProposal)
	m.ESPProposal = types.StringValue(conn.ESPProposal)
	m.IKELifetime = types.StringValue(conn.IKELifetime)
	m.Lifetime = types.StringValue(conn.Lifetime)
	m.StartAction = types.StringValue(conn.StartAction)
	m.Slug = types.StringValue(conn.Slug)
	m.HasPSK = types.BoolValue(conn.HasPSK)

	remote, d := types.ListValueFrom(ctx, types.StringType, conn.RemoteSubnets)
	diags.Append(d...)
	if !diags.HasError() {
		m.RemoteSubnets = remote
	}
	// Preserve null vs empty: an inherited local set must not show as [] drift.
	if len(conn.LocalSubnets) > 0 || !m.LocalSubnets.IsNull() {
		local, d := types.ListValueFrom(ctx, types.StringType, conn.LocalSubnets)
		diags.Append(d...)
		if !diags.HasError() {
			m.LocalSubnets = local
		}
	}
	return diags
}

// optionalString keeps an unset optional attribute null when the API reports it
// empty, so an omitted field does not read back as "" drift.
func optionalString(current types.String, remote string) types.String {
	if remote == "" && current.IsNull() {
		return current
	}
	return types.StringValue(remote)
}

func (r *ipsecConnectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ipsecConnectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s, diags := resolveScope(r.pd, plan.Env, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	in, d := plan.toInput(ctx)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	in.PSK = plan.PSK.ValueString()

	gateway := plan.Service.ValueString()
	conn, err := s.services(r.pd.client).IPsec(gateway).Add(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Adding IPsec connection", err.Error())
		return
	}

	resp.Diagnostics.Append(plan.applyRemote(ctx, conn)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.Env = types.StringValue(s.env)
	plan.ID = types.StringValue(makeID(s.workspace, s.project, s.env, gateway, conn.Slug))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ipsecConnectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ipsecConnectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed IPsec connection ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	conns, err := s.services(r.pd.client).IPsec(parts[3]).List(ctx)
	if err != nil {
		if gone(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Reading IPsec connections", err.Error())
		return
	}

	for i := range conns {
		if conns[i].Slug != parts[4] {
			continue
		}
		state.Env = types.StringValue(s.env)
		state.Service = types.StringValue(parts[3])
		resp.Diagnostics.Append(state.applyRemote(ctx, &conns[i])...)
		if resp.Diagnostics.HasError() {
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *ipsecConnectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ipsecConnectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed IPsec connection ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}
	ipsec := s.services(r.pd.client).IPsec(parts[3])

	in, d := plan.toInput(ctx)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	conn, err := ipsec.Update(ctx, parts[4], in)
	if err != nil {
		resp.Diagnostics.AddError("Updating IPsec connection", err.Error())
		return
	}

	// The settings update deliberately ignores the key, so a changed one is a
	// separate rotation call.
	if plan.PSK.ValueString() != "" && plan.PSK.ValueString() != state.PSK.ValueString() {
		if err := ipsec.RotatePSK(ctx, parts[4], plan.PSK.ValueString()); err != nil {
			resp.Diagnostics.AddError("Rotating IPsec pre-shared key", err.Error())
			return
		}
		conn.HasPSK = true
	}

	resp.Diagnostics.Append(plan.applyRemote(ctx, conn)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	plan.Env = types.StringValue(s.env)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ipsecConnectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ipsecConnectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 5)
	if err != nil {
		resp.Diagnostics.AddError("Malformed IPsec connection ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	if err := s.services(r.pd.client).IPsec(parts[3]).Delete(ctx, parts[4]); err != nil && !gone(err) {
		resp.Diagnostics.AddError("Deleting IPsec connection", err.Error())
	}
}

func (r *ipsecConnectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parseID(req.ID, 5); err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			"Expected `<workspace>/<project>/<env>/<gateway>/<connection>`: "+err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
