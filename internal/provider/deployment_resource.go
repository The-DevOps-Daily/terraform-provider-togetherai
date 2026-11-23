package provider

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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
	_ resource.Resource                   = &deploymentResource{}
	_ resource.ResourceWithImportState    = &deploymentResource{}
	_ resource.ResourceWithValidateConfig = &deploymentResource{}
	_ resource.ResourceWithModifyPlan     = &deploymentResource{}
)

// NewDeploymentResource returns the togetherai_deployment resource.
func NewDeploymentResource() resource.Resource { return &deploymentResource{} }

type deploymentResource struct{ data *providerData }

var (
	scalingMetricNames = []string{"active_sessions", "cache_hit_rate", "decoding_speed", "e2e_latency", "gpu_utilization", "inflight_requests", "throughput_per_replica", "token_utilization", "ttft"}
	scalingMetricTypes = []string{"METRIC_TARGET_TYPE_VALUE", "METRIC_TARGET_TYPE_UTILIZATION", "METRIC_TARGET_TYPE_AVERAGE_VALUE"}
	placementEnforce   = []string{"ENFORCEMENT_REQUIRED", "ENFORCEMENT_PREFERRED"}
)

var scalingMetricAttrTypes = map[string]attr.Type{
	"name":       types.StringType,
	"type":       types.StringType,
	"target":     types.Float64Type,
	"percentile": types.StringType,
}

type autoscalingModel struct {
	MinReplicas       types.Int64  `tfsdk:"min_replicas"`
	MaxReplicas       types.Int64  `tfsdk:"max_replicas"`
	ScaleUpWindow     types.String `tfsdk:"scale_up_window"`
	ScaleDownWindow   types.String `tfsdk:"scale_down_window"`
	ScaleToZeroWindow types.String `tfsdk:"scale_to_zero_window"`
	ScalingMetrics    types.List   `tfsdk:"scaling_metrics"`
}

type placementModel struct {
	Profile    types.String `tfsdk:"profile"`
	Regions    types.List   `tfsdk:"regions"`
	Constraint types.String `tfsdk:"constraint"`
}

type deploymentModel struct {
	ID                              types.String      `tfsdk:"id"`
	ProjectID                       types.String      `tfsdk:"project_id"`
	EndpointID                      types.String      `tfsdk:"endpoint_id"`
	Name                            types.String      `tfsdk:"name"`
	Model                           types.String      `tfsdk:"model"`
	Config                          types.String      `tfsdk:"config"`
	Autoscaling                     *autoscalingModel `tfsdk:"autoscaling"`
	InactiveTimeout                 types.Int64       `tfsdk:"inactive_timeout"`
	MaxConcurrentRequestsPerReplica types.Int64       `tfsdk:"max_concurrent_requests_per_replica"`
	Placement                       *placementModel   `tfsdk:"placement"`

	QualifiedName   types.String   `tfsdk:"qualified_name"`
	ModelRevision   types.String   `tfsdk:"model_revision"`
	Hardware        types.String   `tfsdk:"hardware"`
	State           types.String   `tfsdk:"state"`
	StatusMessage   types.String   `tfsdk:"status_message"`
	ReadyReplicas   types.Int64    `tfsdk:"ready_replicas"`
	DesiredReplicas types.Int64    `tfsdk:"desired_replicas"`
	TrafficMode     types.String   `tfsdk:"traffic_mode"`
	CreatedAt       types.String   `tfsdk:"created_at"`
	Timeouts        timeouts.Value `tfsdk:"timeouts"`
}

func (r *deploymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deployment"
}

func durationValidators() []validator.String {
	return []validator.String{stringvalidator.RegexMatches(durationRe, "must be a duration in seconds such as \"300s\" or \"0.5s\"")}
}

func (r *deploymentResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A model deployment on a dedicated endpoint: a model plus a deployment config (hardware, quantization) and replica bounds.\n\n" +
			"Create and replica changes wait until the deployment is `DEPLOYMENT_STATE_READY`, or `DEPLOYMENT_STATE_STOPPED` when `max_replicas = 0`. " +
			"Set `min_replicas` and `max_replicas` to `0` to stop the deployment without deleting it; raise them to start it again.\n\n" +
			"Destroy removes the deployment from the endpoint's traffic split, stops it, waits for `DEPLOYMENT_STATE_STOPPED`, and then deletes it, which is the order the API requires.\n\n" +
			"**Cost:** billed per minute for each ready replica (for example $5.49 per hour for one H100 replica). A stopped deployment does not bill.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Deployment ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				Optional: true, Computed: true, MarkdownDescription: "Project of the endpoint. Defaults to the provider's project.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"endpoint_id": schema.StringAttribute{
				Required: true, MarkdownDescription: "Endpoint ID (`togetherai_endpoint.<name>.id`).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Deployment name within the endpoint. Updated in place.",
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"model": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Model resource name `projects/{projectId}/models/{modelId}[/revisions/{revisionId}]`, such as a `deployment_profiles[*].model` from `togetherai_supported_models`. " +
					"Without a revision, the latest revision is pinned at create time (see `model_revision`). Changing it replaces the deployment.",
				Validators:    []validator.String{stringvalidator.RegexMatches(modelRe, "must look like projects/{projectId}/models/{modelId}[/revisions/{revisionId}]")},
				PlanModifiers: []planmodifier.String{modelRequiresReplace()},
			},
			"config": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Deployment config `projects/{projectId}/configs/{configRevisionId}` (hardware, quantization, parallelism), such as a `deployment_profiles[*].config`. " +
					"Set it when the model has more than one deployment profile. Changing it replaces the deployment.",
				Validators:    []validator.String{stringvalidator.RegexMatches(configRe, "must look like projects/{projectId}/configs/{configRevisionId}")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplaceIfConfigured()},
			},
			"autoscaling": schema.SingleNestedAttribute{
				Required: true, MarkdownDescription: "Replica bounds and autoscaling. Updated in place without restarting replicas.",
				Attributes: map[string]schema.Attribute{
					"min_replicas": schema.Int64Attribute{
						Required: true, MarkdownDescription: "Minimum replicas. These run and bill all the time. `0` with `max_replicas = 0` stops the deployment.",
						Validators: []validator.Int64{int64validator.AtLeast(0)},
					},
					"max_replicas": schema.Int64Attribute{
						Optional: true, Computed: true, MarkdownDescription: "Maximum replicas. Defaults to `min_replicas`.",
						Validators:    []validator.Int64{int64validator.AtLeast(0)},
						PlanModifiers: []planmodifier.Int64{maxReplicasDefault()},
					},
					"scale_up_window": schema.StringAttribute{
						Optional: true, Computed: true, MarkdownDescription: "Stabilization window before scaling up, in seconds, such as `\"60s\"`. Defaults to the API's value.",
						Validators: durationValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
					},
					"scale_down_window": schema.StringAttribute{
						Optional: true, Computed: true, MarkdownDescription: "Time a lower replica recommendation must stay stable before scaling down, such as `\"300s\"` (the API default is 5 minutes).",
						Validators: durationValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
					},
					"scale_to_zero_window": schema.StringAttribute{
						Optional: true, Computed: true, MarkdownDescription: "Idle period after which the deployment stops and releases its replicas, such as `\"1800s\"`.",
						Validators: durationValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
					},
					"scaling_metrics": schema.ListNestedAttribute{
						Optional: true, Computed: true,
						MarkdownDescription: "Metrics and targets that drive replica recommendations. When not set, the API uses in-flight requests per replica.",
						Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
						PlanModifiers:       []planmodifier.List{listplanmodifier.UseStateForUnknown()},
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"name": schema.StringAttribute{
								Required: true, MarkdownDescription: "Metric name: `" + joinCodes(scalingMetricNames) + "`.",
								Validators: []validator.String{stringvalidator.OneOf(scalingMetricNames...)},
							},
							"type": schema.StringAttribute{
								Required: true, MarkdownDescription: "Target type: `" + joinCodes(scalingMetricTypes) + "`.",
								Validators: []validator.String{stringvalidator.OneOf(scalingMetricTypes...)},
							},
							"target": schema.Float64Attribute{Required: true, MarkdownDescription: "Target value, read according to `type` (utilization is 0-100)."},
							"percentile": schema.StringAttribute{
								Optional: true, MarkdownDescription: "Percentile for latency metrics: `p50`, `p90`, `p95` or `p99`.",
								Validators: []validator.String{stringvalidator.OneOf("p50", "p90", "p95", "p99")},
							},
						}},
					},
				},
			},
			"inactive_timeout": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Minutes without a request before the deployment stops by itself. `0` disables it; otherwise 30 to 1440. Updated in place.",
				Validators:          []validator.Int64{inactiveTimeout()},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"max_concurrent_requests_per_replica": schema.Int64Attribute{
				Optional: true, Computed: true,
				MarkdownDescription: "Maximum in-flight requests per replica. Defaults to the config's limit minus one. The API lowers larger values to that maximum; the provider then reports an error asking you to use the lower value. Updated in place.",
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"placement": schema.SingleNestedAttribute{
				Optional: true, MarkdownDescription: "Where replicas may run. Set `profile`, or `regions` with `constraint`. Placement cannot change on an existing deployment, so changing it replaces the deployment.",
				PlanModifiers: []planmodifier.Object{objectReplaceIfStateKnown()},
				Attributes: map[string]schema.Attribute{
					"profile": schema.StringAttribute{
						Optional: true, MarkdownDescription: "Placement profile ID (`pp_...`).",
						Validators: []validator.String{stringvalidator.ConflictsWith(path.MatchRelative().AtParent().AtName("regions"), path.MatchRelative().AtParent().AtName("constraint"))},
					},
					"regions": schema.ListAttribute{
						Optional: true, ElementType: types.StringType,
						MarkdownDescription: "Region names or patterns such as `us-*` or `us-east-*`.",
						Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
					},
					"constraint": schema.StringAttribute{
						Optional: true, MarkdownDescription: "`ENFORCEMENT_REQUIRED` or `ENFORCEMENT_PREFERRED`.",
						Validators: []validator.String{stringvalidator.OneOf(placementEnforce...)},
					},
				},
			},

			"qualified_name": schema.StringAttribute{Computed: true, MarkdownDescription: "`<project_slug>/<endpoint_name>/<name>`. Pass it as `model` to send a request to this deployment only, bypassing the traffic split."},
			"model_revision": schema.StringAttribute{
				Computed: true, MarkdownDescription: "The pinned model revision `projects/{projectId}/models/{modelId}/revisions/{revisionId}`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"hardware": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Hardware selected by the config, such as GPU type and count.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"state":            schema.StringAttribute{Computed: true, MarkdownDescription: "Lifecycle state, such as `DEPLOYMENT_STATE_READY`."},
			"status_message":   schema.StringAttribute{Computed: true, MarkdownDescription: "Human-readable status from the API."},
			"ready_replicas":   schema.Int64Attribute{Computed: true, MarkdownDescription: "Replicas serving traffic (and billing) now."},
			"desired_replicas": schema.Int64Attribute{Computed: true, MarkdownDescription: "Replicas the autoscaler wants now."},
			"traffic_mode": schema.StringAttribute{
				Computed: true, MarkdownDescription: "`TRAFFIC_MODE_LIVE` or `TRAFFIC_MODE_SHADOW`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Creation time.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

// sameModel reports whether two model resource names point at the same
// weights, where one may be the unpinned form of the other.
func sameModel(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/revisions/") || strings.HasPrefix(b, a+"/revisions/")
}

func modelRequiresReplace() planmodifier.String {
	desc := "Changing the model replaces the deployment, unless the new value is the unpinned form of the old one, or pins the revision the deployment already runs."
	return stringplanmodifier.RequiresReplaceIf(func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
		if req.StateValue.IsNull() || req.PlanValue.IsUnknown() {
			resp.RequiresReplace = !req.StateValue.IsNull()
			return
		}
		prior, planned := req.StateValue.ValueString(), req.PlanValue.ValueString()
		if strings.HasPrefix(planned, prior+"/revisions/") {
			// Pinning the unpinned model: same deployment only if it is the
			// revision the deployment already runs.
			var running types.String
			resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("model_revision"), &running)...)
			resp.RequiresReplace = running.ValueString() != planned
			return
		}
		resp.RequiresReplace = !sameModel(prior, planned)
	}, desc, desc)
}

type maxReplicasDefaultModifier struct{}

// maxReplicasDefault plans max_replicas as min_replicas when it is not set,
// matching the API's create default, so changing min alone keeps max == min.
func maxReplicasDefault() planmodifier.Int64 { return maxReplicasDefaultModifier{} }

func (maxReplicasDefaultModifier) Description(context.Context) string {
	return "Defaults to min_replicas."
}

func (m maxReplicasDefaultModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (maxReplicasDefaultModifier) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if !req.ConfigValue.IsNull() {
		return
	}
	var minReplicas types.Int64
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("autoscaling").AtName("min_replicas"), &minReplicas)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.PlanValue = minReplicas
}

func (r *deploymentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg deploymentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if p := cfg.Placement; p != nil && p.Profile.IsNull() && p.Regions.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("placement"), "Incomplete placement", "Set placement.profile, or placement.regions (with an optional constraint).")
	}
	if a := cfg.Autoscaling; a != nil && !a.MinReplicas.IsNull() && !a.MinReplicas.IsUnknown() && !a.MaxReplicas.IsNull() && !a.MaxReplicas.IsUnknown() &&
		a.MaxReplicas.ValueInt64() < a.MinReplicas.ValueInt64() {
		resp.Diagnostics.AddAttributeError(path.Root("autoscaling").AtName("max_replicas"), "Invalid replica bounds", "max_replicas must be at least min_replicas.")
	}
}

// ModifyPlan turns a plan with no input changes into a no-op. Terraform does
// not copy prior values into Optional+Computed attributes nested in
// autoscaling, so without this every plan would show the read-only status
// attributes as "known after apply".
func (r *deploymentResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || !req.Plan.Raw.IsFullyKnown() && !inputsKnown(ctx, req, resp) {
		return
	}
	var plan, state deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || plan.Autoscaling == nil || state.Autoscaling == nil {
		return
	}
	pa, sa := plan.Autoscaling, state.Autoscaling
	same := plan.Name.Equal(state.Name) && plan.Model.Equal(state.Model) && plan.Config.Equal(state.Config) &&
		plan.EndpointID.Equal(state.EndpointID) && plan.ProjectID.Equal(state.ProjectID) &&
		plan.InactiveTimeout.Equal(state.InactiveTimeout) && plan.MaxConcurrentRequestsPerReplica.Equal(state.MaxConcurrentRequestsPerReplica) &&
		pa.MinReplicas.Equal(sa.MinReplicas) && pa.MaxReplicas.Equal(sa.MaxReplicas) &&
		pa.ScaleUpWindow.Equal(sa.ScaleUpWindow) && pa.ScaleDownWindow.Equal(sa.ScaleDownWindow) &&
		pa.ScaleToZeroWindow.Equal(sa.ScaleToZeroWindow) && pa.ScalingMetrics.Equal(sa.ScalingMetrics) &&
		samePlacement(plan.Placement, state.Placement)
	if !same {
		return
	}
	state.Timeouts = plan.Timeouts
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &state)...)
}

// inputsKnown reports whether every configurable attribute in the plan is
// known. Read-only attributes may be unknown.
func inputsKnown(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) bool {
	for _, p := range []path.Path{path.Root("name"), path.Root("model"), path.Root("config"), path.Root("endpoint_id"), path.Root("project_id"),
		path.Root("autoscaling"), path.Root("inactive_timeout"), path.Root("max_concurrent_requests_per_replica"), path.Root("placement")} {
		var v attr.Value
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, p, &v)...)
		if resp.Diagnostics.HasError() || v == nil || !isFullyKnown(v) {
			return false
		}
	}
	return true
}

func isFullyKnown(v attr.Value) bool {
	if v.IsUnknown() {
		return false
	}
	if v.IsNull() {
		return true
	}
	switch t := v.(type) {
	case types.Object:
		for _, a := range t.Attributes() {
			if !isFullyKnown(a) {
				return false
			}
		}
	case types.List:
		for _, e := range t.Elements() {
			if !isFullyKnown(e) {
				return false
			}
		}
	}
	return true
}

func samePlacement(a, b *placementModel) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Profile.Equal(b.Profile) && a.Regions.Equal(b.Regions) && a.Constraint.Equal(b.Constraint)
}

func (r *deploymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureResource(req, resp)
}

func metricsToAPI(ctx context.Context, l types.List) ([]client.ScalingMetric, diag.Diagnostics) {
	if l.IsNull() || l.IsUnknown() {
		return nil, nil
	}
	var items []struct {
		Name       types.String  `tfsdk:"name"`
		Type       types.String  `tfsdk:"type"`
		Target     types.Float64 `tfsdk:"target"`
		Percentile types.String  `tfsdk:"percentile"`
	}
	diags := l.ElementsAs(ctx, &items, false)
	out := make([]client.ScalingMetric, 0, len(items))
	for _, it := range items {
		out = append(out, client.ScalingMetric{Name: it.Name.ValueString(), Type: it.Type.ValueString(), Target: it.Target.ValueFloat64(), Percentile: it.Percentile.ValueString()})
	}
	return out, diags
}

func metricsFromAPI(ms []client.ScalingMetric) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	vals := make([]attr.Value, 0, len(ms))
	for _, m := range ms {
		obj, d := types.ObjectValue(scalingMetricAttrTypes, map[string]attr.Value{
			"name": types.StringValue(m.Name), "type": types.StringValue(m.Type), "target": types.Float64Value(m.Target), "percentile": strOrNull(m.Percentile),
		})
		diags.Append(d...)
		vals = append(vals, obj)
	}
	l, d := types.ListValue(types.ObjectType{AttrTypes: scalingMetricAttrTypes}, vals)
	diags.Append(d...)
	return l, diags
}

func knownStr(v types.String) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return v.ValueString()
}

// flattenDeployment copies the API view into m, keeping the configured form
// of name and model when the API returns the qualified or pinned form.
func flattenDeployment(d *client.Deployment, m *deploymentModel) diag.Diagnostics {
	var diags diag.Diagnostics
	m.ID = types.StringValue(d.ID)
	if d.ProjectID != "" {
		m.ProjectID = types.StringValue(d.ProjectID)
	}
	if d.EndpointID != "" {
		m.EndpointID = types.StringValue(d.EndpointID)
	}
	m.Name = keepName(m.Name, d.Name)
	m.QualifiedName = types.StringValue(d.Name)
	if m.Model.IsNull() || m.Model.IsUnknown() || !sameModel(m.Model.ValueString(), d.Model) {
		m.Model = types.StringValue(d.Model)
	}
	m.ModelRevision = strOrNull(d.Model)
	m.Config = strOrNull(d.Config)
	m.Hardware = strOrNull(d.Hardware)
	m.TrafficMode = strOrNull(d.TrafficMode)
	m.CreatedAt = strOrNull(d.CreatedAt)
	m.State = strOrNull(d.Status.State)
	m.StatusMessage = strOrNull(d.Status.Message)
	m.ReadyReplicas = types.Int64Value(d.Status.ReadyReplicas)
	m.DesiredReplicas = types.Int64Value(d.DesiredReplicas)
	if d.InactiveTimeout != nil {
		m.InactiveTimeout = types.Int64Value(*d.InactiveTimeout)
	} else {
		m.InactiveTimeout = types.Int64Value(0)
	}
	if d.MaxConcurrentRequestsPerReplica != "" {
		n, err := strconv.ParseInt(d.MaxConcurrentRequestsPerReplica, 10, 64)
		if err != nil {
			diags.AddError("Unexpected API value", fmt.Sprintf("maxConcurrentRequestsPerReplica %q is not an integer", d.MaxConcurrentRequestsPerReplica))
		}
		m.MaxConcurrentRequestsPerReplica = types.Int64Value(n)
	} else if m.MaxConcurrentRequestsPerReplica.IsUnknown() {
		m.MaxConcurrentRequestsPerReplica = types.Int64Null()
	}

	if m.Autoscaling == nil {
		m.Autoscaling = &autoscalingModel{}
	}
	a := d.Autoscaling
	if a.MinReplicas != nil {
		m.Autoscaling.MinReplicas = types.Int64Value(*a.MinReplicas)
	} else {
		m.Autoscaling.MinReplicas = types.Int64Value(0)
	}
	if a.MaxReplicas != nil {
		m.Autoscaling.MaxReplicas = types.Int64Value(*a.MaxReplicas)
	} else {
		m.Autoscaling.MaxReplicas = m.Autoscaling.MinReplicas
	}
	m.Autoscaling.ScaleUpWindow = keepDuration(m.Autoscaling.ScaleUpWindow, a.ScaleUpWindow)
	m.Autoscaling.ScaleDownWindow = keepDuration(m.Autoscaling.ScaleDownWindow, a.ScaleDownWindow)
	m.Autoscaling.ScaleToZeroWindow = keepDuration(m.Autoscaling.ScaleToZeroWindow, a.ScaleToZeroWindow)
	l, ld := metricsFromAPI(a.ScalingMetrics)
	diags.Append(ld...)
	m.Autoscaling.ScalingMetrics = l

	samePlacementProfile := m.Placement != nil && d.Placement != nil && !m.Placement.Profile.IsNull() && m.Placement.Profile.ValueString() == d.Placement.Profile
	if d.Placement != nil && !samePlacementProfile {
		p := &placementModel{Profile: strOrNull(d.Placement.Profile), Regions: types.ListNull(types.StringType), Constraint: types.StringNull()}
		if d.Placement.Inline != nil {
			regions := make([]attr.Value, 0, len(d.Placement.Inline.Regions))
			for _, rg := range d.Placement.Inline.Regions {
				regions = append(regions, types.StringValue(rg))
			}
			rl, rd := types.ListValue(types.StringType, regions)
			diags.Append(rd...)
			p.Regions = rl
			p.Constraint = strOrNull(d.Placement.Inline.Constraint)
		}
		m.Placement = p
	}
	return diags
}

// keepDuration keeps the configured spelling of a duration ("60.0s") when it
// is the same length of time as the API's ("60s").
func keepDuration(prior types.String, api string) types.String {
	if api == "" {
		if prior.IsUnknown() {
			return types.StringNull()
		}
		if prior.IsNull() {
			return prior
		}
		return types.StringNull()
	}
	if !prior.IsNull() && !prior.IsUnknown() {
		a, errA := time.ParseDuration(prior.ValueString())
		b, errB := time.ParseDuration(api)
		if errA == nil && errB == nil && a == b {
			return prior
		}
	}
	return types.StringValue(api)
}

func targetState(maxReplicas int64) string {
	if maxReplicas == 0 {
		return client.DeploymentStateStopped
	}
	return client.DeploymentStateReady
}

// waitState polls until the deployment reaches target. FAILED is terminal.
func (r *deploymentResource) waitState(ctx context.Context, project, endpoint, id, target string, timeout time.Duration) (*client.Deployment, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last *client.Deployment
	err := waitFor(ctx, r.data.pollInterval, func(ctx context.Context) (bool, string, error) {
		d, err := r.data.client.GetDeployment(ctx, project, endpoint, id)
		if err != nil {
			return false, "", err
		}
		last = d
		st := d.Status.State
		switch {
		case st == target:
			return true, st, nil
		case st == client.DeploymentStateFailed:
			return false, st, fmt.Errorf("deployment %s is in the terminal state DEPLOYMENT_STATE_FAILED: %s", id, d.Status.Message)
		case target == client.DeploymentStateReady && st == client.DeploymentStateStopped:
			// An idle timeout or scale-to-zero window stopped it again.
			return true, st, nil
		}
		return false, st + " (" + d.Status.Message + ")", nil
	})
	return last, err
}

func (r *deploymentResource) checkConcurrency(plan *deploymentModel, configured types.Int64, diags *diag.Diagnostics) {
	if configured.IsNull() || configured.IsUnknown() {
		return
	}
	if !plan.MaxConcurrentRequestsPerReplica.Equal(configured) {
		diags.AddAttributeError(path.Root("max_concurrent_requests_per_replica"), "The API lowered max_concurrent_requests_per_replica",
			fmt.Sprintf("You set %d, and the API stored %d (the config's per-replica limit minus one). Set max_concurrent_requests_per_replica to %d or less.",
				configured.ValueInt64(), plan.MaxConcurrentRequestsPerReplica.ValueInt64(), plan.MaxConcurrentRequestsPerReplica.ValueInt64()))
	}
}

func (r *deploymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, 60*time.Minute)
	resp.Diagnostics.Append(diags...)
	project, pd := r.data.resolveProject(ctx, plan.ProjectID)
	resp.Diagnostics.Append(pd...)
	if resp.Diagnostics.HasError() {
		return
	}
	endpoint := plan.EndpointID.ValueString()

	as := client.Autoscaling{
		MinReplicas:       i64p(plan.Autoscaling.MinReplicas.ValueInt64()),
		MaxReplicas:       i64p(plan.Autoscaling.MaxReplicas.ValueInt64()),
		ScaleUpWindow:     knownStr(plan.Autoscaling.ScaleUpWindow),
		ScaleDownWindow:   knownStr(plan.Autoscaling.ScaleDownWindow),
		ScaleToZeroWindow: knownStr(plan.Autoscaling.ScaleToZeroWindow),
	}
	metrics, md := metricsToAPI(ctx, plan.Autoscaling.ScalingMetrics)
	resp.Diagnostics.Append(md...)
	as.ScalingMetrics = metrics
	in := client.DeploymentCreateRequest{
		Name:        plan.Name.ValueString(),
		Model:       plan.Model.ValueString(),
		Config:      knownStr(plan.Config),
		Autoscaling: as,
	}
	if !plan.InactiveTimeout.IsNull() && !plan.InactiveTimeout.IsUnknown() {
		in.InactiveTimeout = i64p(plan.InactiveTimeout.ValueInt64())
	}
	configuredConcurrency := plan.MaxConcurrentRequestsPerReplica
	if !configuredConcurrency.IsNull() && !configuredConcurrency.IsUnknown() {
		in.MaxConcurrentRequestsPerReplica = strconv.FormatInt(configuredConcurrency.ValueInt64(), 10)
	}
	if plan.Placement != nil {
		p := &client.Placement{Profile: knownStr(plan.Placement.Profile)}
		if !plan.Placement.Regions.IsNull() {
			var regions []string
			resp.Diagnostics.Append(plan.Placement.Regions.ElementsAs(ctx, &regions, false)...)
			p.Inline = &client.InlinePlacement{Regions: regions, Constraint: knownStr(plan.Placement.Constraint)}
		}
		in.Placement = p
	}
	if resp.Diagnostics.HasError() {
		return
	}

	d, err := r.data.client.CreateDeployment(ctx, project, endpoint, in)
	if err != nil {
		resp.Diagnostics.AddError("Error creating deployment", err.Error())
		return
	}
	if d.ProjectID == "" {
		d.ProjectID = project
	}
	plan.ProjectID = types.StringValue(project)
	last, werr := r.waitState(ctx, project, endpoint, d.ID, targetState(plan.Autoscaling.MaxReplicas.ValueInt64()), timeout)
	if last == nil {
		last = d
	}
	if last.ProjectID == "" {
		last.ProjectID = project
	}
	resp.Diagnostics.Append(flattenDeployment(last, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	r.checkConcurrency(&plan, configuredConcurrency, &resp.Diagnostics)
	if werr != nil {
		resp.Diagnostics.AddError("Deployment did not become ready",
			fmt.Sprintf("Deployment %s was created but %s. It is kept in state as tainted. A deployment bills only for ready replicas.", d.ID, werr))
	}
}

func (r *deploymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state deploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, diags := r.data.resolveProject(ctx, state.ProjectID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	d, err := r.data.client.GetDeployment(ctx, project, state.EndpointID.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		tflog.Warn(ctx, "deployment not found, removing from state", map[string]any{"deployment_id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading deployment", err.Error())
		return
	}
	if d.ProjectID == "" {
		d.ProjectID = project
	}
	resp.Diagnostics.Append(flattenDeployment(d, &state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *deploymentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	clearImported(ctx, resp)
	var plan, state deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Update(ctx, 60*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, endpoint, id := state.ProjectID.ValueString(), state.EndpointID.ValueString(), state.ID.ValueString()

	var mask []string
	up := client.DeploymentUpdateRequest{}
	as := &client.Autoscaling{}
	if !plan.Name.Equal(state.Name) {
		mask = append(mask, "name")
		up.Name = plan.Name.ValueString()
	}
	replicasChanged := !plan.Autoscaling.MinReplicas.Equal(state.Autoscaling.MinReplicas) || !plan.Autoscaling.MaxReplicas.Equal(state.Autoscaling.MaxReplicas)
	if replicasChanged {
		mask = append(mask, "autoscaling.minReplicas", "autoscaling.maxReplicas")
		as.MinReplicas = i64p(plan.Autoscaling.MinReplicas.ValueInt64())
		as.MaxReplicas = i64p(plan.Autoscaling.MaxReplicas.ValueInt64())
	}
	for _, w := range []struct {
		mask        string
		plan, state types.String
		dst         *string
	}{
		{"autoscaling.scaleUpWindow", plan.Autoscaling.ScaleUpWindow, state.Autoscaling.ScaleUpWindow, &as.ScaleUpWindow},
		{"autoscaling.scaleDownWindow", plan.Autoscaling.ScaleDownWindow, state.Autoscaling.ScaleDownWindow, &as.ScaleDownWindow},
		{"autoscaling.scaleToZeroWindow", plan.Autoscaling.ScaleToZeroWindow, state.Autoscaling.ScaleToZeroWindow, &as.ScaleToZeroWindow},
	} {
		if !w.plan.IsUnknown() && !w.plan.IsNull() && !w.plan.Equal(w.state) {
			mask = append(mask, w.mask)
			*w.dst = w.plan.ValueString()
		}
	}
	if !plan.Autoscaling.ScalingMetrics.IsUnknown() && !plan.Autoscaling.ScalingMetrics.IsNull() && !plan.Autoscaling.ScalingMetrics.Equal(state.Autoscaling.ScalingMetrics) {
		ms, md := metricsToAPI(ctx, plan.Autoscaling.ScalingMetrics)
		resp.Diagnostics.Append(md...)
		as.ScalingMetrics = ms
		mask = append(mask, "autoscaling.scalingMetrics")
	}
	if slices.ContainsFunc(mask, func(s string) bool { return strings.HasPrefix(s, "autoscaling.") }) {
		up.Autoscaling = as
	}
	if !plan.InactiveTimeout.IsUnknown() && !plan.InactiveTimeout.IsNull() && !plan.InactiveTimeout.Equal(state.InactiveTimeout) {
		mask = append(mask, "inactiveTimeout")
		up.InactiveTimeout = i64p(plan.InactiveTimeout.ValueInt64())
	}
	configuredConcurrency := plan.MaxConcurrentRequestsPerReplica
	if !configuredConcurrency.IsUnknown() && !configuredConcurrency.IsNull() && !configuredConcurrency.Equal(state.MaxConcurrentRequestsPerReplica) {
		mask = append(mask, "maxConcurrentRequestsPerReplica")
		up.MaxConcurrentRequestsPerReplica = strconv.FormatInt(configuredConcurrency.ValueInt64(), 10)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	starting := replicasChanged && state.Autoscaling.MaxReplicas.ValueInt64() == 0 && plan.Autoscaling.MaxReplicas.ValueInt64() > 0
	if starting {
		// The API only restarts a deployment that has fully stopped.
		if _, err := r.waitState(ctx, project, endpoint, id, client.DeploymentStateStopped, timeout); err != nil {
			resp.Diagnostics.AddError("Deployment cannot be restarted", err.Error())
			return
		}
	}

	var d *client.Deployment
	var err error
	if len(mask) > 0 {
		d, err = r.data.client.UpdateDeployment(ctx, project, endpoint, id, mask, up)
		if err != nil {
			resp.Diagnostics.AddError("Error updating deployment", err.Error())
			return
		}
	} else {
		d, err = r.data.client.GetDeployment(ctx, project, endpoint, id)
		if err != nil {
			resp.Diagnostics.AddError("Error reading deployment", err.Error())
			return
		}
	}
	var werr error
	if replicasChanged {
		var last *client.Deployment
		last, werr = r.waitState(ctx, project, endpoint, id, targetState(plan.Autoscaling.MaxReplicas.ValueInt64()), timeout)
		if last != nil {
			d = last
		}
	}
	if d.ProjectID == "" {
		d.ProjectID = project
	}
	plan.ID = state.ID
	resp.Diagnostics.Append(flattenDeployment(d, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	r.checkConcurrency(&plan, configuredConcurrency, &resp.Diagnostics)
	if werr != nil {
		resp.Diagnostics.AddError("Deployment did not reach the requested state", werr.Error())
	}
}

func (r *deploymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state deploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, 30*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, endpoint, id := state.ProjectID.ValueString(), state.EndpointID.ValueString(), state.ID.ValueString()
	c := r.data.client

	// 1. Take it out of the endpoint's traffic split (even at weight 0).
	e, err := c.GetEndpoint(ctx, project, endpoint)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading the deployment's endpoint", err.Error())
		return
	}
	if slices.ContainsFunc(e.TrafficSplit, func(t client.TrafficSplitEntry) bool { return t.DeploymentID == id }) {
		keep := make([]client.TrafficSplitEntry, 0, len(e.TrafficSplit))
		for _, t := range e.TrafficSplit {
			if t.DeploymentID != id {
				keep = append(keep, t)
			}
		}
		if _, err := c.UpdateEndpoint(ctx, project, endpoint, []string{"trafficSplit"}, client.EndpointUpdate{TrafficSplit: &keep, Etag: e.Etag}); err != nil {
			resp.Diagnostics.AddError("Error removing the deployment from the traffic split", err.Error())
			return
		}
	}

	// 2. Stop it and wait for STOPPED.
	d, err := c.GetDeployment(ctx, project, endpoint, id)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading deployment", err.Error())
		return
	}
	if st := d.Status.State; st != client.DeploymentStateStopped && st != client.DeploymentStateFailed {
		if st != client.DeploymentStateStopping {
			zero := &client.Autoscaling{MinReplicas: i64p(0), MaxReplicas: i64p(0)}
			if _, err := c.UpdateDeployment(ctx, project, endpoint, id, []string{"autoscaling.minReplicas", "autoscaling.maxReplicas"}, client.DeploymentUpdateRequest{Autoscaling: zero}); err != nil {
				resp.Diagnostics.AddError("Error stopping deployment before delete", err.Error())
				return
			}
		}
		wctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		err := waitFor(wctx, r.data.pollInterval, func(ctx context.Context) (bool, string, error) {
			d, err := c.GetDeployment(ctx, project, endpoint, id)
			if err != nil {
				return false, "", err
			}
			st := d.Status.State
			return st == client.DeploymentStateStopped || st == client.DeploymentStateFailed, st, nil
		})
		if client.IsNotFound(err) {
			return
		}
		if err != nil {
			resp.Diagnostics.AddError("Deployment did not stop", err.Error())
			return
		}
	}

	// 3. Delete.
	if err := c.DeleteDeployment(ctx, project, endpoint, id); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting deployment", err.Error())
	}
}

// ImportState accepts "<endpoint_id>/<deployment_id>" or
// "<project_id>/<endpoint_id>/<deployment_id>".
func (r *deploymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, 2, 3)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("%s. Use <endpoint_id>/<deployment_id> or <project_id>/<endpoint_id>/<deployment_id>.", err))
		return
	}
	if len(parts) == 3 {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
		parts = parts[1:]
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("endpoint_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
	markImported(ctx, resp)
}
