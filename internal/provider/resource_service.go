package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

var (
	_ resource.Resource                = (*serviceResource)(nil)
	_ resource.ResourceWithConfigure   = (*serviceResource)(nil)
	_ resource.ResourceWithImportState = (*serviceResource)(nil)
)

func NewServiceResource() resource.Resource { return &serviceResource{} }

type serviceResource struct{ pd *providerData }

type serviceModel struct {
	ID  types.String `tfsdk:"id"`
	Env types.String `tfsdk:"env"`

	Name     types.String `tfsdk:"name"`
	Type     types.String `tfsdk:"type"`
	VCPUs    types.Int64  `tfsdk:"vcpus"`
	Memory   types.Int64  `tfsdk:"memory"`
	Replicas types.Int64  `tfsdk:"replicas"`

	Docker       *dockerModel       `tfsdk:"docker"`
	Postgres     *postgresModel     `tfsdk:"postgres"`
	Redis        *redisModel        `tfsdk:"redis"`
	Kafka        *kafkaModel        `tfsdk:"kafka"`
	IPsecGateway *ipsecGatewayModel `tfsdk:"ipsec_gateway"`

	Deploy types.Bool `tfsdk:"deploy"`

	Slug            types.String `tfsdk:"slug"`
	Status          types.String `tfsdk:"status"`
	PrivateHostname types.String `tfsdk:"private_hostname"`
}

type dockerModel struct {
	Image            types.String `tfsdk:"image"`
	Tag              types.String `tfsdk:"tag"`
	StartCommand     types.String `tfsdk:"start_command"`
	StartCommandArgs types.List   `tfsdk:"start_command_args"`
}

type postgresModel struct {
	StorageGB  types.Int64  `tfsdk:"storage_gb"`
	Mode       types.String `tfsdk:"mode"`
	Parameters types.Map    `tfsdk:"parameters"`
}

type redisModel struct {
	StorageGB types.Int64  `tfsdk:"storage_gb"`
	Mode      types.String `tfsdk:"mode"`
	Replicas  types.Int64  `tfsdk:"replicas"`
}

type kafkaModel struct {
	StorageGB   types.Int64  `tfsdk:"storage_gb"`
	Mode        types.String `tfsdk:"mode"`
	Brokers     types.Int64  `tfsdk:"brokers"`
	Controllers types.Int64  `tfsdk:"controllers"`
	Version     types.String `tfsdk:"version"`
}

type ipsecGatewayModel struct {
	LocalSubnets types.List   `tfsdk:"local_subnets"`
	PublicIP     types.String `tfsdk:"public_ip"`
	VNI          types.Int64  `tfsdk:"vni"`
}

func (r *serviceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service"
}

func (r *serviceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceStr := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		MarkdownDescription: "A deployable service inside an environment.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<workspace>/<project>/<env>/<service>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"env": schema.StringAttribute{MarkdownDescription: envDoc, Optional: true, PlanModifiers: replaceStr},

			"name": schema.StringAttribute{
				MarkdownDescription: "Service name.",
				Required:            true,
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Service type: `docker`, `postgres`, `redis`, `kafka`, `s3_bucket`, " +
					"`http_gateway`, `ipsec_gateway`, or `zerodata_proxy`. Changing this forces replacement.",
				Required:      true,
				PlanModifiers: replaceStr,
				Validators: []validator.String{
					stringvalidator.OneOf("docker", "postgres", "redis", "kafka", "s3_bucket",
						"http_gateway", "ipsec_gateway", "zerodata_proxy"),
				},
			},
			"vcpus": schema.Int64Attribute{
				MarkdownDescription: "vCPU allocation.",
				Optional:            true,
				Computed:            true,
			},
			"memory": schema.Int64Attribute{
				MarkdownDescription: "Memory allocation in MiB.",
				Optional:            true,
				Computed:            true,
			},
			"replicas": schema.Int64Attribute{
				MarkdownDescription: "Replica count.",
				Optional:            true,
				Computed:            true,
			},
			"deploy": schema.BoolAttribute{
				MarkdownDescription: "Approve the resulting changeset and roll out a deployment after " +
					"create/update, waiting for it to reach a terminal state. Set to `false` to stage " +
					"changes without deploying (they remain in the service's pending changeset).",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},

			"slug":             schema.StringAttribute{MarkdownDescription: "Server-assigned slug.", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"status":           schema.StringAttribute{MarkdownDescription: "Current lifecycle status.", Computed: true},
			"private_hostname": schema.StringAttribute{MarkdownDescription: "In-cluster DNS name reachable from the same project.", Computed: true},
		},
		Blocks: map[string]schema.Block{},
		// Type-specific configuration. Exactly one should be set, matching `type`.
	}

	resp.Schema.Attributes["docker"] = schema.SingleNestedAttribute{
		MarkdownDescription: "Configuration for `type = \"docker\"` services.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"image": schema.StringAttribute{MarkdownDescription: "Image repository.", Required: true},
			"tag":   schema.StringAttribute{MarkdownDescription: "Image tag. Defaults to `latest`.", Optional: true, Computed: true},
			"start_command": schema.StringAttribute{
				MarkdownDescription: "Overrides the image entrypoint.", Optional: true,
			},
			"start_command_args": schema.ListAttribute{
				MarkdownDescription: "Arguments passed to `start_command`.",
				ElementType:         types.StringType,
				Optional:            true,
			},
		},
	}
	resp.Schema.Attributes["postgres"] = schema.SingleNestedAttribute{
		MarkdownDescription: "Configuration for `type = \"postgres\"` services. Changes force replacement.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"storage_gb": schema.Int64Attribute{MarkdownDescription: "Volume size in GB.", Optional: true, Computed: true},
			"mode": schema.StringAttribute{
				MarkdownDescription: "`standalone` or `replication`.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{stringvalidator.OneOf("standalone", "replication")},
			},
			"parameters": schema.MapAttribute{
				MarkdownDescription: "Customer-tunable PostgreSQL server settings, e.g. `work_mem = \"16MB\"`. " +
					"Only the platform's allowlist is accepted; `shared_buffers` and `effective_cache_size` " +
					"are derived from the service's memory limit and cannot be set here. " +
					"The map is applied as a whole: removing an entry restores its platform default, and " +
					"a setting that needs a restart is applied by the next deployment, not immediately. " +
					"Unlike the rest of this block, changes apply in place rather than forcing replacement.",
				ElementType: types.StringType,
				Optional:    true,
			},
		},
	}
	resp.Schema.Attributes["redis"] = schema.SingleNestedAttribute{
		MarkdownDescription: "Configuration for `type = \"redis\"` services. Changes force replacement.",
		Optional:            true,
		Attributes: map[string]schema.Attribute{
			"storage_gb": schema.Int64Attribute{MarkdownDescription: "Volume size in GB.", Optional: true, Computed: true},
			"mode": schema.StringAttribute{
				MarkdownDescription: "`standalone`, `replication`, or `cluster`.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{stringvalidator.OneOf("standalone", "replication", "cluster")},
			},
			"replicas": schema.Int64Attribute{MarkdownDescription: "Redis replica count.", Optional: true, Computed: true},
		},
	}
	// Kafka has no update actions in the API at all: pool sizes, storage and
	// version are only read after creation, and a version change needs a
	// multi-step rolling procedure the platform does not perform implicitly.
	// Everything here therefore forces replacement rather than silently
	// diverging from the running cluster.
	resp.Schema.Attributes["kafka"] = schema.SingleNestedAttribute{
		MarkdownDescription: "Configuration for `type = \"kafka\"` services. Changes force replacement.",
		Optional:            true,
		PlanModifiers:       []planmodifier.Object{objectplanmodifier.RequiresReplace()},
		Attributes: map[string]schema.Attribute{
			"storage_gb": schema.Int64Attribute{
				MarkdownDescription: "Volume size in GB, per broker. Controllers get a fixed 10 GB each.",
				Optional:            true, Computed: true,
			},
			"mode": schema.StringAttribute{
				MarkdownDescription: "`standalone` (one node carrying both roles, for development) or " +
					"`cluster` (separate broker and controller pools).",
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{stringvalidator.OneOf("standalone", "cluster")},
			},
			"brokers": schema.Int64Attribute{
				MarkdownDescription: "Broker count. Cluster mode only; standalone pins it to 1. " +
					"Internal topic replication is clamped to this, so a one-broker cluster is not replicated.",
				Optional: true, Computed: true,
			},
			"controllers": schema.Int64Attribute{
				MarkdownDescription: "Controller count. Cluster mode only; standalone pins it to 1. " +
					"Keep it odd so the KRaft quorum can tolerate a failure. Controllers are billed " +
					"as nodes and carry 10 GB of storage each.",
				Optional: true, Computed: true,
			},
			"version": schema.StringAttribute{
				MarkdownDescription: "Kafka version. Defaults to the platform's current version.",
				Optional:            true, Computed: true,
			},
		},
	}
	resp.Schema.Attributes["ipsec_gateway"] = schema.SingleNestedAttribute{
		MarkdownDescription: "Configuration for `type = \"ipsec_gateway\"` services — a site-to-site " +
			"VPN gateway. Tunnels are separate `simplifyd_ipsec_connection` resources. " +
			"A gateway is billed per tunnel-minute rather than on compute, so `vcpus`, `memory` " +
			"and `replicas` do not apply to it.",
		Optional: true,
		Attributes: map[string]schema.Attribute{
			"local_subnets": schema.ListAttribute{
				MarkdownDescription: "Ranges this environment presents to counterparties. " +
					"Omit to seed it with the gateway's own address. The API has no update action " +
					"for this, so changing it forces replacement — and replacement means a new " +
					"public address, which counterparties have pinned in their firewalls.",
				ElementType:   types.StringType,
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplace()},
			},
			"public_ip": schema.StringAttribute{
				MarkdownDescription: "Fixed public address the gateway terminates IKEv2 on. " +
					"This is the peer address counterparties configure.",
				Computed: true,
			},
			"vni": schema.Int64Attribute{
				MarkdownDescription: "Platform-allocated overlay identifier, fixed for the gateway's life.",
				Computed:            true,
			},
		},
	}
}

func (r *serviceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.pd = req.ProviderData.(*providerData)
}

func (r *serviceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	s, diags := resolveScope(r.pd, plan.Env, true)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	svcs := s.services(r.pd.client)

	in := cloud.CreateServiceInput{
		Name:   plan.Name.ValueString(),
		Type:   cloud.ServiceType(plan.Type.ValueString()),
		VCPUs:  uint(plan.VCPUs.ValueInt64()),
		Memory: uint(plan.Memory.ValueInt64()),
	}
	switch in.Type {
	case cloud.ServiceTypeDocker:
		if plan.Docker == nil {
			resp.Diagnostics.AddError("Missing docker block", "`docker` is required when type = \"docker\".")
			return
		}
		in.Docker = &cloud.DockerInput{
			Image: plan.Docker.Image.ValueString(),
			Tag:   plan.Docker.Tag.ValueString(),
		}
	case cloud.ServiceTypePostgres:
		in.Postgres = &cloud.PostgresInput{}
		if plan.Postgres != nil {
			in.Postgres.StorageGB = uint64(plan.Postgres.StorageGB.ValueInt64())
			in.Postgres.Mode = plan.Postgres.Mode.ValueString()
		}
	case cloud.ServiceTypeRedis:
		in.Redis = &cloud.RedisInput{}
		if plan.Redis != nil {
			in.Redis.StorageGB = uint64(plan.Redis.StorageGB.ValueInt64())
			in.Redis.Mode = plan.Redis.Mode.ValueString()
			in.Redis.Replicas = int(plan.Redis.Replicas.ValueInt64())
		}
	case cloud.ServiceTypeKafka:
		in.Kafka = &cloud.KafkaInput{}
		if plan.Kafka != nil {
			in.Kafka.StorageGB = uint64(plan.Kafka.StorageGB.ValueInt64())
			in.Kafka.Mode = plan.Kafka.Mode.ValueString()
			in.Kafka.Brokers = int(plan.Kafka.Brokers.ValueInt64())
			in.Kafka.Controllers = int(plan.Kafka.Controllers.ValueInt64())
			in.Kafka.Version = plan.Kafka.Version.ValueString()
		}
	case cloud.ServiceTypeIPsecGateway:
		in.IPsecGateway = &cloud.IPsecGatewayInput{}
		if plan.IPsecGateway != nil {
			subnets, d := toStrings(ctx, plan.IPsecGateway.LocalSubnets)
			resp.Diagnostics.Append(d...)
			if resp.Diagnostics.HasError() {
				return
			}
			in.IPsecGateway.LocalSubnets = subnets
		}
	}

	svc, err := svcs.Create(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Creating service", err.Error())
		return
	}

	// Create does not accept every field; apply the remainder as updates.
	resp.Diagnostics.Append(r.applyUpdates(ctx, svcs, svc.Slug, nil, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Deploy.ValueBool() {
		resp.Diagnostics.Append(r.deploy(ctx, svcs, svc.Slug)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	final, err := svcs.Get(ctx, svc.Slug)
	if err != nil {
		resp.Diagnostics.AddError("Reading back service", err.Error())
		return
	}
	resp.Diagnostics.Append(r.apply(ctx, &plan, s, final)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 4)
	if err != nil {
		resp.Diagnostics.AddError("Malformed service ID", err.Error())
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

	resp.Diagnostics.Append(r.apply(ctx, &state, s, svc)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serviceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 4)
	if err != nil {
		resp.Diagnostics.AddError("Malformed service ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}
	svcs := s.services(r.pd.client)
	slug := parts[3]

	resp.Diagnostics.Append(r.applyUpdates(ctx, svcs, slug, &state, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if plan.Deploy.ValueBool() {
		resp.Diagnostics.Append(r.deploy(ctx, svcs, slug)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	svc, err := svcs.Get(ctx, slug)
	if err != nil {
		resp.Diagnostics.AddError("Reading back service", err.Error())
		return
	}
	resp.Diagnostics.Append(r.apply(ctx, &plan, s, svc)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	parts, err := parseID(state.ID.ValueString(), 4)
	if err != nil {
		resp.Diagnostics.AddError("Malformed service ID", err.Error())
		return
	}
	s := scope{workspace: parts[0], project: parts[1], env: parts[2]}

	if err := s.services(r.pd.client).Delete(ctx, parts[3]); err != nil && !gone(err) {
		resp.Diagnostics.AddError("Deleting service", err.Error())
	}
}

func (r *serviceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := parseID(req.ID, 4); err != nil {
		resp.Diagnostics.AddError("Invalid import ID",
			"Expected `<workspace>/<project>/<env>/<service>`: "+err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ── update plumbing ───────────────────────────────────────────────────────────

// applyUpdates translates a plan/state diff into the API's action-based PATCH
// calls. The API mutates one concern per request ("name", "vcpus", "image", …),
// so a single Terraform update can fan out into several calls. Passing a nil
// prior means "apply everything that is set", which is what Create needs for
// fields its POST body does not accept.
func (r *serviceResource) applyUpdates(
	ctx context.Context,
	svcs *cloud.ServicesClient,
	slug string,
	prior, plan *serviceModel,
) diag.Diagnostics {
	var diags diag.Diagnostics

	send := func(in cloud.UpdateServiceInput) bool {
		tflog.Debug(ctx, "patching service", map[string]any{"slug": slug, "action": in.Action})
		if _, err := svcs.Update(ctx, slug, in); err != nil {
			diags.AddError(fmt.Sprintf("Updating service (%s)", in.Action), err.Error())
			return false
		}
		return true
	}

	changedStr := func(get func(*serviceModel) types.String) bool {
		if get(plan).IsNull() || get(plan).IsUnknown() {
			return false
		}
		return prior == nil || get(prior).ValueString() != get(plan).ValueString()
	}
	changedInt := func(get func(*serviceModel) types.Int64) bool {
		if get(plan).IsNull() || get(plan).IsUnknown() {
			return false
		}
		return prior == nil || get(prior).ValueInt64() != get(plan).ValueInt64()
	}

	if prior != nil && changedStr(func(m *serviceModel) types.String { return m.Name }) {
		if !send(cloud.UpdateServiceInput{Action: "name", Name: plan.Name.ValueString()}) {
			return diags
		}
	}
	if changedInt(func(m *serviceModel) types.Int64 { return m.VCPUs }) {
		if !send(cloud.UpdateServiceInput{Action: "vcpus", VCPUs: uint(plan.VCPUs.ValueInt64())}) {
			return diags
		}
	}
	if changedInt(func(m *serviceModel) types.Int64 { return m.Memory }) {
		if !send(cloud.UpdateServiceInput{Action: "memory", Memory: uint(plan.Memory.ValueInt64())}) {
			return diags
		}
	}
	if changedInt(func(m *serviceModel) types.Int64 { return m.Replicas }) {
		if !send(cloud.UpdateServiceInput{Action: "replicas", Replicas: uint(plan.Replicas.ValueInt64())}) {
			return diags
		}
	}

	if plan.Docker != nil {
		var priorDocker *dockerModel
		if prior != nil {
			priorDocker = prior.Docker
		}
		imageChanged := priorDocker == nil ||
			priorDocker.Image.ValueString() != plan.Docker.Image.ValueString() ||
			priorDocker.Tag.ValueString() != plan.Docker.Tag.ValueString()
		// On create the image is already set by the POST body; only patch on change.
		if prior != nil && imageChanged {
			if !send(cloud.UpdateServiceInput{
				Action: "image",
				Image:  plan.Docker.Image.ValueString(),
				Tag:    plan.Docker.Tag.ValueString(),
			}) {
				return diags
			}
		}

		cmd := plan.Docker.StartCommand
		cmdChanged := !cmd.IsNull() && !cmd.IsUnknown() &&
			(priorDocker == nil || priorDocker.StartCommand.ValueString() != cmd.ValueString())
		if cmdChanged {
			args, d := toStrings(ctx, plan.Docker.StartCommandArgs)
			diags.Append(d...)
			if diags.HasError() {
				return diags
			}
			if !send(cloud.UpdateServiceInput{
				Action:           "start_command",
				StartCommand:     cmd.ValueString(),
				StartCommandArgs: args,
			}) {
				return diags
			}
		}
	}

	// Postgres parameters are not part of the action-based PATCH: they have
	// their own endpoint that replaces the whole map, so an emptied map is a
	// reset to platform defaults rather than a no-op.
	if plan.Postgres != nil && !plan.Postgres.Parameters.IsUnknown() {
		var priorParams types.Map
		if prior != nil && prior.Postgres != nil {
			priorParams = prior.Postgres.Parameters
		}
		if prior == nil || !priorParams.Equal(plan.Postgres.Parameters) {
			params, d := toStringMap(ctx, plan.Postgres.Parameters)
			diags.Append(d...)
			if diags.HasError() {
				return diags
			}
			// Nothing to do on create when the map was never set; on update a
			// null map is a deliberate reset and does need the call.
			if prior != nil || len(params) > 0 {
				if params == nil {
					params = map[string]string{}
				}
				tflog.Debug(ctx, "replacing postgres parameters", map[string]any{"slug": slug, "count": len(params)})
				if _, err := svcs.UpdatePostgresParameters(ctx, slug, cloud.UpdatePostgresParametersInput{
					Parameters: params,
				}); err != nil {
					diags.AddError("Updating Postgres parameters", err.Error())
					return diags
				}
			}
		}
	}

	return diags
}

// deploy approves any pending changeset and rolls out, blocking until the
// deployment reaches a terminal state so that `terraform apply` failing means
// the rollout actually failed.
func (r *serviceResource) deploy(ctx context.Context, svcs *cloud.ServicesClient, slug string) diag.Diagnostics {
	var diags diag.Diagnostics

	dep, err := svcs.Deploy(ctx, slug, cloud.DeployOptions{AutoApproveChangeSets: true})
	if err != nil {
		diags.AddError("Deploying service", err.Error())
		return diags
	}

	final, err := svcs.WaitForDeployment(ctx, slug, dep.Slug, 5*time.Second)
	if err != nil {
		diags.AddError("Waiting for deployment", err.Error())
		return diags
	}
	if final.Status == cloud.DeploymentStatusFailed {
		diags.AddError("Deployment failed",
			fmt.Sprintf("Deployment %s for service %s finished with status %q. "+
				"Check the service logs in the Simplifyd dashboard.", final.Slug, slug, final.Status))
	}
	return diags
}

func (r *serviceResource) apply(ctx context.Context, m *serviceModel, s scope, svc *cloud.Service) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(makeID(s.workspace, s.project, s.env, svc.Slug))
	m.Env = types.StringValue(s.env)
	m.Name = types.StringValue(svc.Name)
	m.Type = types.StringValue(string(svc.Type))
	m.VCPUs = types.Int64Value(int64(svc.VCPUs))
	m.Memory = types.Int64Value(int64(svc.Memory))
	m.Replicas = types.Int64Value(int64(svc.Replicas))
	m.Slug = types.StringValue(svc.Slug)
	m.Status = types.StringValue(string(svc.Status))
	m.PrivateHostname = types.StringValue(svc.PrivateHostname)

	if svc.Docker != nil {
		if m.Docker == nil {
			m.Docker = &dockerModel{StartCommandArgs: types.ListNull(types.StringType)}
		}
		m.Docker.Image = types.StringValue(svc.Docker.Image)
		m.Docker.Tag = types.StringValue(svc.Docker.Tag)
		if svc.Docker.StartCommand != "" {
			m.Docker.StartCommand = types.StringValue(svc.Docker.StartCommand)
		}
	}
	if svc.Redis != nil && m.Redis != nil {
		m.Redis.Mode = types.StringValue(svc.Redis.Mode)
		m.Redis.Replicas = types.Int64Value(int64(svc.Redis.Replicas))
	}
	if svc.Postgres != nil && m.Postgres != nil {
		// Preserve null vs empty: a config that never set parameters must not
		// pick up {} as drift the moment the platform reports an empty map.
		if len(svc.Postgres.Parameters) > 0 || !m.Postgres.Parameters.IsNull() {
			params, d := types.MapValueFrom(ctx, types.StringType, svc.Postgres.Parameters)
			diags.Append(d...)
			if !diags.HasError() {
				m.Postgres.Parameters = params
			}
		}
	}
	if svc.Kafka != nil && m.Kafka != nil {
		m.Kafka.StorageGB = types.Int64Value(int64(svc.Kafka.StorageGB))
		m.Kafka.Mode = types.StringValue(svc.Kafka.Mode)
		m.Kafka.Brokers = types.Int64Value(int64(svc.Kafka.Brokers))
		m.Kafka.Controllers = types.Int64Value(int64(svc.Kafka.Controllers))
		m.Kafka.Version = types.StringValue(svc.Kafka.Version)
	}
	if svc.IPsecGateway != nil {
		if m.IPsecGateway == nil {
			m.IPsecGateway = &ipsecGatewayModel{LocalSubnets: types.ListNull(types.StringType)}
		}
		m.IPsecGateway.PublicIP = types.StringValue(svc.IPsecGateway.PublicIP)
		m.IPsecGateway.VNI = types.Int64Value(int64(svc.IPsecGateway.VNI))
		subnets, d := types.ListValueFrom(ctx, types.StringType, svc.IPsecGateway.LocalSubnets)
		diags.Append(d...)
		if !diags.HasError() {
			m.IPsecGateway.LocalSubnets = subnets
		}
	}
	return diags
}

func toStringMap(ctx context.Context, m types.Map) (map[string]string, diag.Diagnostics) {
	if m.IsNull() || m.IsUnknown() {
		return nil, nil
	}
	out := map[string]string{}
	diags := m.ElementsAs(ctx, &out, false)
	return out, diags
}

func toStrings(ctx context.Context, l types.List) ([]string, diag.Diagnostics) {
	if l.IsNull() || l.IsUnknown() {
		return nil, nil
	}
	var out []string
	diags := l.ElementsAs(ctx, &out, false)
	return out, diags
}
