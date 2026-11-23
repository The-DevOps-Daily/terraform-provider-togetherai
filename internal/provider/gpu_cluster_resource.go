package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/client"
)

var (
	_ resource.Resource                     = &gpuClusterResource{}
	_ resource.ResourceWithImportState      = &gpuClusterResource{}
	_ resource.ResourceWithConfigValidators = &gpuClusterResource{}
	_ resource.ResourceWithValidateConfig   = &gpuClusterResource{}
	_ resource.ResourceWithModifyPlan       = &gpuClusterResource{}
)

// NewGPUClusterResource returns the togetherai_gpu_cluster resource.
func NewGPUClusterResource() resource.Resource { return &gpuClusterResource{} }

type gpuClusterResource struct{ data *providerData }

var (
	gpuTypes     = []string{"H100_SXM", "H200_SXM", "RTX_6000_PCI", "L40_PCIE", "B200_SXM", "H100_SXM_INF", "B300_SXM", "GB200_NVL"}
	billingTypes = []string{"RESERVED", "ON_DEMAND", "SCHEDULED_CAPACITY"}
	clusterTypes = []string{"KUBERNETES", "SLURM"}
)

type sharedVolumeInline struct {
	Name                   types.String `tfsdk:"name"`
	SizeTiB                types.Int64  `tfsdk:"size_tib"`
	IsLifecycleIndependent types.Bool   `tfsdk:"is_lifecycle_independent"`
}

type oidcConfigModel struct {
	IssuerURL      types.String `tfsdk:"issuer_url"`
	ClientID       types.String `tfsdk:"client_id"`
	UsernameClaim  types.String `tfsdk:"username_claim"`
	UsernamePrefix types.String `tfsdk:"username_prefix"`
	GroupClaim     types.String `tfsdk:"group_claim"`
	GroupPrefix    types.String `tfsdk:"group_prefix"`
	CACert         types.String `tfsdk:"ca_cert"`
}

type gpuClusterModel struct {
	ID                   types.String        `tfsdk:"id"`
	Name                 types.String        `tfsdk:"name"`
	ClusterType          types.String        `tfsdk:"cluster_type"`
	Region               types.String        `tfsdk:"region"`
	GPUType              types.String        `tfsdk:"gpu_type"`
	NumGPUs              types.Int64         `tfsdk:"num_gpus"`
	BillingType          types.String        `tfsdk:"billing_type"`
	DurationDays         types.Int64         `tfsdk:"duration_days"`
	VolumeID             types.String        `tfsdk:"volume_id"`
	SharedVolume         *sharedVolumeInline `tfsdk:"shared_volume"`
	NvidiaVersionID      types.String        `tfsdk:"nvidia_version_id"`
	CUDAVersion          types.String        `tfsdk:"cuda_version"`
	NvidiaDriverVersion  types.String        `tfsdk:"nvidia_driver_version"`
	AutoScale            types.Bool          `tfsdk:"auto_scale"`
	AutoScaleMaxGPUs     types.Int64         `tfsdk:"auto_scale_max_gpus"`
	NumPreemptibleGPUs   types.Int64         `tfsdk:"num_preemptible_gpus"`
	NumReservedGPUs      types.Int64         `tfsdk:"num_reserved_gpus"`
	CapacityPoolID       types.String        `tfsdk:"capacity_pool_id"`
	NumCapacityPoolGPUs  types.Int64         `tfsdk:"num_capacity_pool_gpus"`
	ReservationStartTime types.String        `tfsdk:"reservation_start_time"`
	ReservationEndTime   types.String        `tfsdk:"reservation_end_time"`
	InstallTraefik       types.Bool          `tfsdk:"install_traefik"`
	SlurmShmSizeGiB      types.Int64         `tfsdk:"slurm_shm_size_gib"`
	SlurmImage           types.String        `tfsdk:"slurm_image"`
	ProjectID            types.String        `tfsdk:"project_id"`
	OIDCConfig           *oidcConfigModel    `tfsdk:"oidc_config"`

	Status                   types.String   `tfsdk:"status"`
	KubeConfig               types.String   `tfsdk:"kube_config"`
	CreatedAt                types.String   `tfsdk:"created_at"`
	CurrentNumGPUs           types.Int64    `tfsdk:"current_num_gpus"`
	AllocatedPreemptibleGPUs types.Int64    `tfsdk:"allocated_preemptible_gpus"`
	DurationHours            types.Int64    `tfsdk:"duration_hours"`
	Volumes                  types.List     `tfsdk:"volumes"`
	ControlPlaneNodes        types.List     `tfsdk:"control_plane_nodes"`
	GPUWorkerNodes           types.List     `tfsdk:"gpu_worker_nodes"`
	Timeouts                 timeouts.Value `tfsdk:"timeouts"`
}

var clusterVolumeAttrTypes = map[string]attr.Type{
	"volume_id":   types.StringType,
	"volume_name": types.StringType,
	"size_tib":    types.Int64Type,
	"status":      types.StringType,
}

var clusterNodeAttrTypes = map[string]attr.Type{
	"node_id":     types.StringType,
	"host_name":   types.StringType,
	"status":      types.StringType,
	"num_gpus":    types.Int64Type,
	"public_ipv4": types.StringType,
}

func clusterVolumeSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"volume_id":   schema.StringAttribute{Computed: true, MarkdownDescription: "Volume ID."},
		"volume_name": schema.StringAttribute{Computed: true, MarkdownDescription: "Volume name."},
		"size_tib":    schema.Int64Attribute{Computed: true, MarkdownDescription: "Size in TiB."},
		"status":      schema.StringAttribute{Computed: true, MarkdownDescription: "Volume status."},
	}
}

func clusterNodeSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"node_id":     schema.StringAttribute{Computed: true, MarkdownDescription: "Node ID."},
		"host_name":   schema.StringAttribute{Computed: true, MarkdownDescription: "Host name."},
		"status":      schema.StringAttribute{Computed: true, MarkdownDescription: "Node status."},
		"num_gpus":    schema.Int64Attribute{Computed: true, MarkdownDescription: "GPUs on the node (0 for control plane nodes)."},
		"public_ipv4": schema.StringAttribute{Computed: true, MarkdownDescription: "Public IPv4 address, when the node has one."},
	}
}

func (r *gpuClusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gpu_cluster"
}

func (r *gpuClusterResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Together AI GPU cluster (Instant Cluster) running Kubernetes or Slurm.\n\n" +
			"Create waits until the cluster status is `Ready`, which usually takes 10 minutes to an hour, and then exports the admin `kube_config`. " +
			"Every cluster needs a shared volume: attach an existing one with `volume_id`, or create one with the cluster with `shared_volume`. " +
			"Prefer `shared_volume` for a new volume, because a volume created on its own may land in a datacenter that does not have your GPU type.\n\n" +
			"**Cost:** an on-demand cluster bills per GPU hour from creation until it is deleted (for example 8 x H100 at $3.99 = about $32 per hour). " +
			"Reserved clusters are charged for the whole reservation up front and cannot be refunded.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Cluster ID (`cluster_id`).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Cluster name (`cluster_name`). Changing it replaces the cluster.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"cluster_type": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "`KUBERNETES` or `SLURM`. The API picks the default when it is not set. Updated in place.",
				Validators:          []validator.String{stringvalidator.OneOf(clusterTypes...)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"region": schema.StringAttribute{
				Required: true, MarkdownDescription: "Region, such as `us-central-8`. See the `togetherai_gpu_cluster_regions` data source. Changing it replaces the cluster.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"gpu_type": schema.StringAttribute{
				Required: true, MarkdownDescription: "GPU type. One of `" + joinCodes(gpuTypes) + "`. Changing it replaces the cluster.",
				Validators:    []validator.String{stringvalidator.OneOf(gpuTypes...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"num_gpus": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "Number of standard (non-preemptible) GPUs, a multiple of 8 and at least 8. Updated in place (scale up or down); it cannot go below `num_reserved_gpus`. " +
					"With `auto_scale = true` the autoscaler owns the GPU count: this is the lower bound, the provider does not report the live count as drift, and changing it is rejected.",
				Validators: []validator.Int64{int64validator.AtLeast(8), multipleOf8()},
			},
			"billing_type": schema.StringAttribute{
				Required: true, MarkdownDescription: "`ON_DEMAND`, `RESERVED` (set `duration_days`) or `SCHEDULED_CAPACITY` (set `capacity_pool_id` and the reservation times). Changing it replaces the cluster.",
				Validators:    []validator.String{stringvalidator.OneOf(billingTypes...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"duration_days": schema.Int64Attribute{
				Optional: true, MarkdownDescription: "Reservation length in days for `RESERVED` billing. Create-only.",
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: []planmodifier.Int64{int64ReplaceIfStateKnown()},
			},
			"volume_id": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "ID of an existing shared volume to attach. Exactly one of `volume_id` and `shared_volume` must be set. " +
					"With `shared_volume`, this is the ID of the volume created with the cluster. The volume cannot be swapped on an existing cluster.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringReplaceIfStateKnown()},
			},
			"shared_volume": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Create a shared volume with the cluster, in the cluster's region. Create-only.",
				PlanModifiers:       []planmodifier.Object{objectReplaceIfStateKnown()},
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required: true, MarkdownDescription: "Volume name: lowercase letters, digits, hyphens and dots, up to 44 characters.",
						Validators: []validator.String{stringvalidator.RegexMatches(volumeNameRe, "must be 1-44 lowercase letters, digits, hyphens or dots")},
					},
					"size_tib": schema.Int64Attribute{
						Required: true, MarkdownDescription: "Size in whole TiB.",
						Validators: []validator.Int64{int64validator.AtLeast(1)},
					},
					"is_lifecycle_independent": schema.BoolAttribute{
						Optional: true, MarkdownDescription: "When true, the volume is kept (and keeps billing) after the cluster is deleted.",
					},
				},
			},
			"nvidia_version_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Region-specific NVIDIA catalog ID (`driver_versions[*].id` in `togetherai_gpu_cluster_regions`). " +
					"Set this, or `cuda_version` with `nvidia_driver_version`. Create-only.",
				PlanModifiers: []planmodifier.String{stringReplaceIfStateKnown()},
			},
			"cuda_version": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Legacy CUDA selector, such as `12.8`. Must be set together with `nvidia_driver_version`. Computed from `nvidia_version_id` when not set. Create-only.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringReplaceIfStateKnown()},
			},
			"nvidia_driver_version": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Legacy NVIDIA driver selector, such as `570`. Must be set together with `cuda_version`. Create-only.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringReplaceIfStateKnown()},
			},
			"auto_scale": schema.BoolAttribute{
				Optional: true, MarkdownDescription: "Enable the Kubernetes cluster autoscaler between `num_gpus` and `auto_scale_max_gpus`. It cannot be turned on or off after create.",
				PlanModifiers: []planmodifier.Bool{boolReplaceIfStateKnown()},
			},
			"auto_scale_max_gpus": schema.Int64Attribute{
				Optional: true, MarkdownDescription: "Maximum GPUs for the autoscaler, a multiple of 8 and at least `num_gpus`. Create-only.",
				Validators:    []validator.Int64{multipleOf8()},
				PlanModifiers: []planmodifier.Int64{int64ReplaceIfStateKnown()},
			},
			"num_preemptible_gpus": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Target number of discounted preemptible GPUs (`desired_preemptible_gpus`), a multiple of 8. Updated in place. Together fills it asynchronously; see `allocated_preemptible_gpus`.",
				Validators:          []validator.Int64{int64validator.AtLeast(0), multipleOf8()},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"num_reserved_gpus": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Prepaid reserved GPUs (`RESERVED` and `SCHEDULED_CAPACITY` billing). Defaults to `num_gpus` for `RESERVED`. Can only be increased.",
				Validators:          []validator.Int64{int64validator.AtLeast(0), multipleOf8()},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"capacity_pool_id": schema.StringAttribute{
				Optional: true, Computed: true, MarkdownDescription: "Capacity pool for `SCHEDULED_CAPACITY` billing. Create-only.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringReplaceIfStateKnown()},
			},
			"num_capacity_pool_gpus": schema.Int64Attribute{
				Optional: true, Computed: true, MarkdownDescription: "GPUs drawn from the capacity pool, a multiple of 8, not more than `num_gpus`. Updated in place.",
				Validators:    []validator.Int64{int64validator.AtLeast(0), multipleOf8()},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"reservation_start_time": schema.StringAttribute{
				Optional: true, Computed: true, MarkdownDescription: "RFC 3339 start of a scheduled reservation. Create-only.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringReplaceIfStateKnown()},
				Validators:    []validator.String{rfc3339()},
			},
			"reservation_end_time": schema.StringAttribute{
				Optional: true, Computed: true, MarkdownDescription: "RFC 3339 end of the reservation. Updated in place for prepaid clusters.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:    []validator.String{rfc3339()},
			},
			"install_traefik": schema.BoolAttribute{
				Optional: true, Computed: true, MarkdownDescription: "Install the Traefik ingress controller (Kubernetes only). Create-only.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolReplaceIfStateKnown()},
			},
			"slurm_shm_size_gib": schema.Int64Attribute{
				Optional: true, Computed: true, MarkdownDescription: "Shared memory size in GiB. Required when `cluster_type = \"SLURM\"`. Create-only.",
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64ReplaceIfStateKnown()},
			},
			"slurm_image": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Custom Slurm image. Create-only.",
				PlanModifiers: []planmodifier.String{stringReplaceIfStateKnown()},
			},
			"project_id": schema.StringAttribute{
				Optional: true, Computed: true, MarkdownDescription: "Project that owns the cluster. Defaults to the API key's project. Create-only.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringReplaceIfStateKnown()},
			},
			"oidc_config": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "OIDC authentication for the cluster's Kubernetes API. Create-only.",
				PlanModifiers:       []planmodifier.Object{objectReplaceIfStateKnown()},
				Attributes: map[string]schema.Attribute{
					"issuer_url":      schema.StringAttribute{Required: true, MarkdownDescription: "OIDC issuer URL, such as `https://accounts.google.com`."},
					"client_id":       schema.StringAttribute{Required: true, MarkdownDescription: "OIDC client ID."},
					"username_claim":  schema.StringAttribute{Required: true, MarkdownDescription: "JWT claim used as the username, such as `email`."},
					"username_prefix": schema.StringAttribute{Required: true, MarkdownDescription: "Prefix added to the username, such as `oidc:`."},
					"group_claim":     schema.StringAttribute{Required: true, MarkdownDescription: "JWT claim with the user's groups, such as `groups`."},
					"group_prefix":    schema.StringAttribute{Required: true, MarkdownDescription: "Prefix added to group names, such as `oidc:`."},
					"ca_cert":         schema.StringAttribute{Optional: true, MarkdownDescription: "PEM CA certificate of the issuer, for a private CA."},
				},
			},

			"status": schema.StringAttribute{Computed: true, MarkdownDescription: "Cluster status, such as `Ready` or `Degraded`."},
			"kube_config": schema.StringAttribute{
				Computed: true, Sensitive: true,
				MarkdownDescription: "Admin kubeconfig (YAML) once the cluster is ready. The API only returns it to a project admin's key; otherwise it is null.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Creation time.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"current_num_gpus":           schema.Int64Attribute{Computed: true, MarkdownDescription: "Standard GPU count the API reports now. Differs from `num_gpus` while the autoscaler has scaled the cluster."},
			"allocated_preemptible_gpus": schema.Int64Attribute{Computed: true, MarkdownDescription: "Preemptible GPUs allocated now."},
			"duration_hours": schema.Int64Attribute{
				Computed: true, MarkdownDescription: "Reservation length in hours, as reported by the API.",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"volumes": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "Shared volumes attached to the cluster.",
				NestedObject:  schema.NestedAttributeObject{Attributes: clusterVolumeSchema()},
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
			"control_plane_nodes": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "Control plane nodes.",
				NestedObject: schema.NestedAttributeObject{Attributes: clusterNodeSchema()},
			},
			"gpu_worker_nodes": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "GPU worker nodes.",
				NestedObject: schema.NestedAttributeObject{Attributes: clusterNodeSchema()},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *gpuClusterResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("volume_id"), path.MatchRoot("shared_volume")),
		resourcevalidator.AtLeastOneOf(path.MatchRoot("nvidia_version_id"), path.MatchRoot("cuda_version")),
		resourcevalidator.RequiredTogether(path.MatchRoot("cuda_version"), path.MatchRoot("nvidia_driver_version")),
	}
}

func (r *gpuClusterResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg gpuClusterModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.ClusterType.ValueString() == "SLURM" && cfg.SlurmShmSizeGiB.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("slurm_shm_size_gib"), "Missing slurm_shm_size_gib", "slurm_shm_size_gib is required when cluster_type is SLURM.")
	}
	if cfg.AutoScale.ValueBool() && cfg.AutoScaleMaxGPUs.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("auto_scale_max_gpus"), "Missing auto_scale_max_gpus", "auto_scale_max_gpus is required when auto_scale is true.")
	}
	if !cfg.AutoScaleMaxGPUs.IsNull() && !cfg.AutoScaleMaxGPUs.IsUnknown() && !cfg.NumGPUs.IsUnknown() && cfg.AutoScaleMaxGPUs.ValueInt64() < cfg.NumGPUs.ValueInt64() {
		resp.Diagnostics.AddAttributeError(path.Root("auto_scale_max_gpus"), "Invalid auto_scale_max_gpus", "auto_scale_max_gpus must be at least num_gpus.")
	}
	if cfg.BillingType.ValueString() == "SCHEDULED_CAPACITY" && cfg.ReservationEndTime.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("reservation_end_time"), "Missing reservation_end_time", "reservation_end_time is required for SCHEDULED_CAPACITY billing.")
	}
}

// ModifyPlan rejects changes the API refuses, so they fail at plan time.
func (r *gpuClusterResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state, plan gpuClusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if planReplaces(ctx, req, []string{"name", "region", "gpu_type", "billing_type"}, []string{
		"auto_scale", "auto_scale_max_gpus", "capacity_pool_id", "cuda_version", "duration_days", "install_traefik",
		"nvidia_driver_version", "nvidia_version_id", "oidc_config", "project_id", "reservation_start_time",
		"shared_volume", "slurm_image", "slurm_shm_size_gib", "volume_id",
	}) {
		return
	}
	if state.AutoScale.ValueBool() && plan.AutoScale.ValueBool() && !plan.NumGPUs.IsUnknown() && plan.NumGPUs.ValueInt64() != state.NumGPUs.ValueInt64() {
		resp.Diagnostics.AddAttributeError(path.Root("num_gpus"), "Cannot change num_gpus on an autoscaled cluster",
			"The API rejects updates to a cluster with auto_scale enabled (\"cluster update is not allowed when auto scale is enabled\"). The autoscaler owns the GPU count.")
	}
	if !plan.NumReservedGPUs.IsUnknown() && !state.NumReservedGPUs.IsNull() && plan.NumReservedGPUs.ValueInt64() < state.NumReservedGPUs.ValueInt64() {
		resp.Diagnostics.AddAttributeError(path.Root("num_reserved_gpus"), "Cannot decrease num_reserved_gpus",
			"Reserved GPUs can only be increased. To reduce a reservation, contact your Together AI account team.")
	}
}

func (r *gpuClusterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureResource(req, resp)
}

func (r *gpuClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gpuClusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, 90*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	in := client.ClusterCreateRequest{
		ClusterType:          plan.ClusterType.ValueString(),
		Region:               plan.Region.ValueString(),
		GPUType:              plan.GPUType.ValueString(),
		NumGPUs:              plan.NumGPUs.ValueInt64(),
		ClusterName:          plan.Name.ValueString(),
		BillingType:          plan.BillingType.ValueString(),
		VolumeID:             plan.VolumeID.ValueString(),
		CapacityPoolID:       plan.CapacityPoolID.ValueString(),
		ReservationStartTime: plan.ReservationStartTime.ValueString(),
		ReservationEndTime:   plan.ReservationEndTime.ValueString(),
		CUDAVersion:          plan.CUDAVersion.ValueString(),
		NvidiaDriverVersion:  plan.NvidiaDriverVersion.ValueString(),
		NvidiaVersionID:      plan.NvidiaVersionID.ValueString(),
		SlurmImage:           plan.SlurmImage.ValueString(),
		ProjectID:            plan.ProjectID.ValueString(),
	}
	if !plan.DurationDays.IsNull() {
		in.DurationDays = i64p(plan.DurationDays.ValueInt64())
	}
	if plan.SharedVolume != nil {
		in.SharedVolume = &client.VolumeCreateRequest{
			VolumeName: plan.SharedVolume.Name.ValueString(),
			SizeTiB:    plan.SharedVolume.SizeTiB.ValueInt64(),
			Region:     plan.Region.ValueString(),
			ProjectID:  plan.ProjectID.ValueString(),
		}
		if !plan.SharedVolume.IsLifecycleIndependent.IsNull() {
			in.SharedVolume.IsLifecycleIndependent = boolp(plan.SharedVolume.IsLifecycleIndependent.ValueBool())
		}
	}
	if !plan.AutoScale.IsNull() {
		in.AutoScale = boolp(plan.AutoScale.ValueBool())
	}
	if !plan.AutoScaleMaxGPUs.IsNull() {
		in.AutoScaleMaxGPUs = i64p(plan.AutoScaleMaxGPUs.ValueInt64())
	}
	if !plan.NumPreemptibleGPUs.IsUnknown() && !plan.NumPreemptibleGPUs.IsNull() {
		in.NumPreemptibleGPUs = i64p(plan.NumPreemptibleGPUs.ValueInt64())
	}
	if !plan.NumReservedGPUs.IsUnknown() && !plan.NumReservedGPUs.IsNull() {
		in.NumReservedGPUs = i64p(plan.NumReservedGPUs.ValueInt64())
	}
	if !plan.NumCapacityPoolGPUs.IsUnknown() && !plan.NumCapacityPoolGPUs.IsNull() {
		in.NumCapacityPoolGPUs = i64p(plan.NumCapacityPoolGPUs.ValueInt64())
	}
	if !plan.InstallTraefik.IsUnknown() && !plan.InstallTraefik.IsNull() {
		in.InstallTraefik = boolp(plan.InstallTraefik.ValueBool())
	}
	if !plan.SlurmShmSizeGiB.IsUnknown() && !plan.SlurmShmSizeGiB.IsNull() {
		in.SlurmShmSizeGiB = i64p(plan.SlurmShmSizeGiB.ValueInt64())
	}
	if plan.OIDCConfig != nil {
		in.OIDCConfig = &client.OIDCConfig{
			IssuerURL: plan.OIDCConfig.IssuerURL.ValueString(), ClientID: plan.OIDCConfig.ClientID.ValueString(),
			UsernameClaim: plan.OIDCConfig.UsernameClaim.ValueString(), UsernamePrefix: plan.OIDCConfig.UsernamePrefix.ValueString(),
			GroupClaim: plan.OIDCConfig.GroupClaim.ValueString(), GroupPrefix: plan.OIDCConfig.GroupPrefix.ValueString(),
			CACert: plan.OIDCConfig.CACert.ValueString(),
		}
	}

	created, err := r.data.client.CreateCluster(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Error creating GPU cluster", err.Error())
		return
	}
	tflog.Info(ctx, "created GPU cluster, waiting for Ready", map[string]any{"cluster_id": created.ClusterID})

	last, err := r.waitReady(ctx, created.ClusterID, timeout)
	if last == nil {
		last = created
	}
	resp.Diagnostics.Append(flattenCluster(last, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err != nil {
		// The cluster exists and is billing: keep it in state (Terraform
		// marks it tainted) so a later apply or destroy can clean it up.
		resp.Diagnostics.AddError("GPU cluster did not become Ready",
			fmt.Sprintf("Cluster %s was created but did not reach status Ready: %s. It is kept in state as tainted; run terraform destroy or apply again to replace it.", created.ClusterID, err))
	}
}

// waitReady polls until the cluster is Ready and returns the last read.
func (r *gpuClusterResource) waitReady(ctx context.Context, id string, timeout time.Duration) (*client.Cluster, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last *client.Cluster
	err := waitFor(ctx, r.data.pollInterval, func(ctx context.Context) (bool, string, error) {
		c, err := r.data.client.GetCluster(ctx, id)
		if err != nil {
			if client.IsNotFound(err) {
				return false, "", fmt.Errorf("cluster %s disappeared while waiting for it: %w", id, err)
			}
			return false, "", err
		}
		last = c
		if c.Status == client.ClusterStatusDeleting {
			return false, c.Status, fmt.Errorf("cluster %s is being deleted", id)
		}
		return c.Status == client.ClusterStatusReady, c.Status, nil
	})
	return last, err
}

func (r *gpuClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gpuClusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := r.data.client.GetCluster(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		tflog.Warn(ctx, "GPU cluster not found, removing from state", map[string]any{"cluster_id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading GPU cluster", err.Error())
		return
	}
	if c.Status == client.ClusterStatusDeleting {
		// Kept in state until GET returns 404, so a destroy still waits for
		// it before deleting the volumes it uses.
		resp.Diagnostics.AddWarning("GPU cluster is being deleted",
			fmt.Sprintf("Cluster %s has status Deleting. It stays in the state until the API no longer returns it.", c.ClusterID))
	}
	resp.Diagnostics.Append(flattenCluster(c, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gpuClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	clearImported(ctx, resp)
	var plan, state gpuClusterModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Update(ctx, 90*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var up client.ClusterUpdateRequest
	changed := false
	if !plan.ClusterType.IsUnknown() && !plan.ClusterType.Equal(state.ClusterType) {
		up.ClusterType = plan.ClusterType.ValueString()
		changed = true
	}
	if !plan.NumGPUs.Equal(state.NumGPUs) && !plan.AutoScale.ValueBool() {
		up.NumGPUs = i64p(plan.NumGPUs.ValueInt64())
		changed = true
	}
	if !plan.ReservationEndTime.IsUnknown() && !plan.ReservationEndTime.IsNull() && !plan.ReservationEndTime.Equal(state.ReservationEndTime) {
		up.ReservationEndTime = plan.ReservationEndTime.ValueString()
		changed = true
	}
	if !plan.NumReservedGPUs.IsUnknown() && !plan.NumReservedGPUs.IsNull() && !plan.NumReservedGPUs.Equal(state.NumReservedGPUs) {
		up.NumReservedGPUs = i64p(plan.NumReservedGPUs.ValueInt64())
		changed = true
	}
	if !plan.NumPreemptibleGPUs.IsUnknown() && !plan.NumPreemptibleGPUs.IsNull() && !plan.NumPreemptibleGPUs.Equal(state.NumPreemptibleGPUs) {
		up.NumPreemptibleGPUs = i64p(plan.NumPreemptibleGPUs.ValueInt64())
		changed = true
	}
	if !plan.NumCapacityPoolGPUs.IsUnknown() && !plan.NumCapacityPoolGPUs.IsNull() && !plan.NumCapacityPoolGPUs.Equal(state.NumCapacityPoolGPUs) {
		up.NumCapacityPoolGPUs = i64p(plan.NumCapacityPoolGPUs.ValueInt64())
		changed = true
	}

	id := state.ID.ValueString()
	var last *client.Cluster
	var err error
	if changed {
		last, err = r.data.client.UpdateCluster(ctx, id, up)
		if err != nil {
			resp.Diagnostics.AddError("Error updating GPU cluster", err.Error())
			return
		}
		var ready *client.Cluster
		ready, err = r.waitReady(ctx, id, timeout)
		if ready != nil {
			last = ready
		}
	} else {
		// Only create-only fields that were null after an import changed:
		// record them without calling the API.
		last, err = r.data.client.GetCluster(ctx, id)
		if err != nil {
			resp.Diagnostics.AddError("Error reading GPU cluster", err.Error())
			return
		}
	}
	plan.ID = state.ID
	resp.Diagnostics.Append(flattenCluster(last, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err != nil {
		resp.Diagnostics.AddError("GPU cluster did not become Ready after the update", fmt.Sprintf("Cluster %s: %s", id, err))
	}
}

func (r *gpuClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gpuClusterModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, 60*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.data.client.DeleteCluster(ctx, id); err != nil {
		if client.IsNotFound(err) {
			return
		}
		// A cluster already being deleted (for example after a delete
		// timeout) may refuse a second DELETE; wait for it instead.
		if c, gerr := r.data.client.GetCluster(ctx, id); gerr != nil || c.Status != client.ClusterStatusDeleting {
			resp.Diagnostics.AddError("Error deleting GPU cluster", err.Error())
			return
		}
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := waitFor(wctx, r.data.pollInterval, func(ctx context.Context) (bool, string, error) {
		c, err := r.data.client.GetCluster(ctx, id)
		if client.IsNotFound(err) {
			return true, "", nil
		}
		if err != nil {
			return false, "", err
		}
		return false, c.Status, nil
	})
	if err != nil {
		resp.Diagnostics.AddError("GPU cluster was not deleted", fmt.Sprintf("Delete of cluster %s was accepted but the cluster still exists: %s", id, err))
	}
}

func (r *gpuClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	markImported(ctx, resp)
}

// flattenCluster copies the API view into m. Fields the API does not return
// (create-only inputs) keep the value already in m.
func flattenCluster(c *client.Cluster, m *gpuClusterModel) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(c.ClusterID)
	m.Name = types.StringValue(c.ClusterName)
	m.ClusterType = strOrNull(c.ClusterType)
	m.Region = types.StringValue(c.Region)
	m.GPUType = types.StringValue(c.GPUType)
	m.BillingType = strOrNull(c.BillingType)
	// With auto_scale the autoscaler moves the live count; num_gpus is the
	// configured floor, and the live value goes to current_num_gpus.
	if !m.AutoScale.ValueBool() || m.NumGPUs.IsNull() || m.NumGPUs.IsUnknown() {
		m.NumGPUs = types.Int64Value(c.NumGPUs)
	}
	m.CurrentNumGPUs = types.Int64Value(c.NumGPUs)
	m.CUDAVersion = strOrNull(c.CUDAVersion)
	m.NvidiaDriverVersion = strOrNull(c.NvidiaDriverVersion)
	m.NumPreemptibleGPUs = types.Int64Value(c.DesiredPreemptibleGPUs)
	m.AllocatedPreemptibleGPUs = types.Int64Value(c.AllocatedPreemptibleGPUs)
	m.NumReservedGPUs = types.Int64Value(c.NumReservedGPUs)
	m.NumCapacityPoolGPUs = types.Int64Value(c.NumCapacityPoolGPUs)
	m.CapacityPoolID = strOrNull(c.CapacityPoolID)
	m.ReservationStartTime = keepTime(m.ReservationStartTime, c.ReservationStartTime)
	m.ReservationEndTime = keepTime(m.ReservationEndTime, c.ReservationEndTime)
	if c.InstallTraefik != nil {
		m.InstallTraefik = types.BoolValue(*c.InstallTraefik)
	} else if m.InstallTraefik.IsUnknown() {
		m.InstallTraefik = types.BoolNull()
	}
	if c.SlurmShmSizeGiB != nil {
		m.SlurmShmSizeGiB = types.Int64Value(*c.SlurmShmSizeGiB)
	} else if m.SlurmShmSizeGiB.IsUnknown() {
		m.SlurmShmSizeGiB = types.Int64Null()
	}
	m.ProjectID = strOrNull(c.ProjectID)
	// The API's OIDC config wins, so a change made outside Terraform shows
	// as drift. When the API leaves ca_cert out, the configured one is kept.
	if c.OIDCConfig != nil {
		caCert := strOrNull(c.OIDCConfig.CACert)
		if c.OIDCConfig.CACert == "" && m.OIDCConfig != nil {
			caCert = m.OIDCConfig.CACert
		}
		m.OIDCConfig = &oidcConfigModel{
			IssuerURL: types.StringValue(c.OIDCConfig.IssuerURL), ClientID: types.StringValue(c.OIDCConfig.ClientID),
			UsernameClaim: types.StringValue(c.OIDCConfig.UsernameClaim), UsernamePrefix: types.StringValue(c.OIDCConfig.UsernamePrefix),
			GroupClaim: types.StringValue(c.OIDCConfig.GroupClaim), GroupPrefix: types.StringValue(c.OIDCConfig.GroupPrefix),
			CACert: caCert,
		}
	}

	m.Status = types.StringValue(c.Status)
	m.KubeConfig = strOrNull(c.KubeConfig)
	m.CreatedAt = strOrNull(c.CreatedAt)
	if c.DurationHours != nil {
		m.DurationHours = types.Int64Value(*c.DurationHours)
	} else {
		m.DurationHours = types.Int64Null()
	}

	vols := make([]attr.Value, 0, len(c.Volumes))
	for _, v := range c.Volumes {
		obj, d := types.ObjectValue(clusterVolumeAttrTypes, map[string]attr.Value{
			"volume_id": types.StringValue(v.VolumeID), "volume_name": types.StringValue(v.VolumeName),
			"size_tib": types.Int64Value(v.SizeTiB), "status": types.StringValue(v.Status),
		})
		diags.Append(d...)
		vols = append(vols, obj)
	}
	list, d := types.ListValue(types.ObjectType{AttrTypes: clusterVolumeAttrTypes}, vols)
	diags.Append(d...)
	m.Volumes = list
	attached := false
	for _, v := range c.Volumes {
		if v.VolumeID == m.VolumeID.ValueString() {
			attached = true
		}
	}
	if !attached && len(c.Volumes) > 0 {
		// Unset, unknown, or no longer attached: show the volume the API has.
		m.VolumeID = types.StringValue(c.Volumes[0].VolumeID)
	} else if m.VolumeID.IsUnknown() {
		m.VolumeID = types.StringNull()
	}

	m.ControlPlaneNodes, d = nodeList(c.ControlPlaneNodes)
	diags.Append(d...)
	m.GPUWorkerNodes, d = nodeList(c.GPUWorkerNodes)
	diags.Append(d...)
	return diags
}

func nodeList(nodes []client.ClusterNode) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	vals := make([]attr.Value, 0, len(nodes))
	for _, n := range nodes {
		obj, d := types.ObjectValue(clusterNodeAttrTypes, map[string]attr.Value{
			"node_id": types.StringValue(n.NodeID), "host_name": types.StringValue(n.HostName), "status": types.StringValue(n.Status),
			"num_gpus": types.Int64Value(n.NumGPUs), "public_ipv4": strOrNull(n.PublicIPv4),
		})
		diags.Append(d...)
		vals = append(vals, obj)
	}
	list, d := types.ListValue(types.ObjectType{AttrTypes: clusterNodeAttrTypes}, vals)
	diags.Append(d...)
	return list, diags
}
