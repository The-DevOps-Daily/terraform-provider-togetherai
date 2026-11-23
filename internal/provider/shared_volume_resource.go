package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/client"
)

var (
	_ resource.Resource                = &sharedVolumeResource{}
	_ resource.ResourceWithImportState = &sharedVolumeResource{}
	_ resource.ResourceWithModifyPlan  = &sharedVolumeResource{}
)

// NewSharedVolumeResource returns the togetherai_shared_volume resource.
func NewSharedVolumeResource() resource.Resource { return &sharedVolumeResource{} }

type sharedVolumeResource struct{ data *providerData }

type sharedVolumeModel struct {
	ID                     types.String   `tfsdk:"id"`
	Name                   types.String   `tfsdk:"name"`
	SizeTiB                types.Int64    `tfsdk:"size_tib"`
	Region                 types.String   `tfsdk:"region"`
	IsLifecycleIndependent types.Bool     `tfsdk:"is_lifecycle_independent"`
	ProjectID              types.String   `tfsdk:"project_id"`
	InstanceClusterID      types.String   `tfsdk:"instance_cluster_id"`
	Status                 types.String   `tfsdk:"status"`
	Timeouts               timeouts.Value `tfsdk:"timeouts"`
}

func (r *sharedVolumeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_shared_volume"
}

func (r *sharedVolumeResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A shared volume (high-bandwidth shared filesystem) for GPU clusters. A volume created on its own outlives any cluster it is attached to, " +
			"and keeps billing until it is deleted. It cannot be deleted while a cluster uses it.\n\n" +
			"The size can only grow: Terraform rejects a smaller `size_tib` at plan time, because shrinking a volume needs Together AI's account team.\n\n" +
			"A volume created on its own goes to the region's datacenter with the most free GPUs, so attaching it to a cluster can fail. " +
			"Set `instance_cluster_id`, or create the volume with the cluster (`shared_volume` on `togetherai_gpu_cluster`).\n\n" +
			"**Cost:** $0.16 per GiB per month on the public price list, so a 1 TiB volume is about $164 per month (about $0.22 per hour).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Volume ID (`volume_id`).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Volume name: lowercase letters, digits, hyphens and dots, up to 44 characters. Changing it replaces the volume (and its data).",
				Validators:    []validator.String{stringvalidator.RegexMatches(volumeNameRe, "must be 1-44 lowercase letters, digits, hyphens or dots")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"size_tib": schema.Int64Attribute{
				Required: true, MarkdownDescription: "Size in whole TiB. Can be increased in place.",
				Validators: []validator.Int64{int64validator.AtLeast(1)},
			},
			"region": schema.StringAttribute{
				Required: true, MarkdownDescription: "Region, such as `us-central-8`. Create-only. The API does not return it, so after an import set it to the volume's real region.",
				PlanModifiers: []planmodifier.String{stringReplaceIfStateKnown()},
			},
			"is_lifecycle_independent": schema.BoolAttribute{
				Optional: true, MarkdownDescription: "Keep the volume when a cluster it is attached to is decommissioned. The docs say a volume created on its own is always independent. Create-only.",
				PlanModifiers: []planmodifier.Bool{boolReplaceIfStateKnown()},
			},
			"project_id": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Project that owns the volume. Defaults to the API key's project. Create-only.",
				PlanModifiers: []planmodifier.String{stringReplaceIfStateKnown()},
			},
			"instance_cluster_id": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Place the volume on the same infrastructure as this GPU cluster. Create-only.",
				PlanModifiers: []planmodifier.String{stringReplaceIfStateKnown()},
			},
			"status":   schema.StringAttribute{Computed: true, MarkdownDescription: "Volume status, such as `available` or `bound`."},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *sharedVolumeResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state, plan sharedVolumeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	replacing := planReplaces(ctx, req, []string{"name"}, []string{"region", "is_lifecycle_independent", "project_id", "instance_cluster_id"})
	if !replacing && !plan.SizeTiB.IsUnknown() && plan.SizeTiB.ValueInt64() < state.SizeTiB.ValueInt64() {
		resp.Diagnostics.AddAttributeError(path.Root("size_tib"), "Cannot shrink a shared volume",
			fmt.Sprintf("size_tib can only grow (%d -> %d). To decrease a volume's size, contact your Together AI account team.", state.SizeTiB.ValueInt64(), plan.SizeTiB.ValueInt64()))
	}
}

func (r *sharedVolumeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureResource(req, resp)
}

// volumeSettled reports whether a volume is usable, and fails on a terminal
// state.
func volumeSettled(v *client.Volume) (bool, error) {
	switch v.Status {
	case client.VolumeStatusAvailable, client.VolumeStatusBound:
		return true, nil
	case client.VolumeStatusFailed:
		return false, fmt.Errorf("volume %s is in status failed", v.VolumeID)
	case client.VolumeStatusAccessRevoked:
		return false, fmt.Errorf("volume %s is in status access_revoked (check the account's credits)", v.VolumeID)
	}
	return false, nil
}

func (r *sharedVolumeResource) waitSettled(ctx context.Context, id string, timeout time.Duration) (*client.Volume, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last *client.Volume
	err := waitFor(ctx, r.data.pollInterval, func(ctx context.Context) (bool, string, error) {
		v, err := r.data.client.GetVolume(ctx, id)
		if err != nil {
			return false, "", err
		}
		last = v
		done, err := volumeSettled(v)
		return done, v.Status, err
	})
	return last, err
}

func (r *sharedVolumeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sharedVolumeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, 30*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := client.VolumeCreateRequest{
		VolumeName:        plan.Name.ValueString(),
		SizeTiB:           plan.SizeTiB.ValueInt64(),
		Region:            plan.Region.ValueString(),
		ProjectID:         plan.ProjectID.ValueString(),
		InstanceClusterID: plan.InstanceClusterID.ValueString(),
	}
	if !plan.IsLifecycleIndependent.IsNull() {
		in.IsLifecycleIndependent = boolp(plan.IsLifecycleIndependent.ValueBool())
	}
	v, err := r.data.client.CreateVolume(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Error creating shared volume", err.Error())
		return
	}
	last, err := r.waitSettled(ctx, v.VolumeID, timeout)
	if last == nil {
		last = v
	}
	flattenVolume(last, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err != nil {
		resp.Diagnostics.AddError("Shared volume did not become available",
			fmt.Sprintf("Volume %s was created but is not usable: %s. It is kept in state as tainted.", v.VolumeID, err))
	}
}

func flattenVolume(v *client.Volume, m *sharedVolumeModel) {
	m.ID = types.StringValue(v.VolumeID)
	m.Name = types.StringValue(v.VolumeName)
	m.SizeTiB = types.Int64Value(v.SizeTiB)
	m.Status = types.StringValue(v.Status)
}

func (r *sharedVolumeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sharedVolumeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.data.client.GetVolume(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		tflog.Warn(ctx, "shared volume not found, removing from state", map[string]any{"volume_id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading shared volume", err.Error())
		return
	}
	if v.Status == client.VolumeStatusDeleting {
		resp.Diagnostics.AddWarning("Shared volume is being deleted",
			fmt.Sprintf("Volume %s has status deleting. It stays in the state until the API no longer returns it.", state.ID.ValueString()))
	}
	flattenVolume(v, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *sharedVolumeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	clearImported(ctx, resp)
	var plan, state sharedVolumeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Update(ctx, 30*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	var v *client.Volume
	var err error
	if !plan.SizeTiB.Equal(state.SizeTiB) {
		if _, err = r.data.client.UpdateVolume(ctx, client.VolumeUpdateRequest{VolumeID: id, SizeTiB: i64p(plan.SizeTiB.ValueInt64())}); err != nil {
			resp.Diagnostics.AddError("Error resizing shared volume", err.Error())
			return
		}
		v, err = r.waitSettled(ctx, id, timeout)
	} else {
		v, err = r.data.client.GetVolume(ctx, id)
	}
	if v != nil {
		flattenVolume(v, &plan)
	}
	plan.ID = state.ID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if err != nil {
		resp.Diagnostics.AddError("Error updating shared volume", err.Error())
	}
}

func (r *sharedVolumeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sharedVolumeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, 30*time.Minute)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if err := r.data.client.DeleteVolume(ctx, id); err != nil {
		if client.IsNotFound(err) {
			return
		}
		if v, gerr := r.data.client.GetVolume(ctx, id); gerr != nil || v.Status != client.VolumeStatusDeleting {
			resp.Diagnostics.AddError("Error deleting shared volume",
				err.Error()+"\n\nA volume cannot be deleted while a GPU cluster uses it. Delete or detach the cluster first.")
			return
		}
	}
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := waitFor(wctx, r.data.pollInterval, func(ctx context.Context) (bool, string, error) {
		v, err := r.data.client.GetVolume(ctx, id)
		if client.IsNotFound(err) {
			return true, "", nil
		}
		if err != nil {
			return false, "", err
		}
		return false, v.Status, nil
	})
	if err != nil {
		resp.Diagnostics.AddError("Shared volume was not deleted", fmt.Sprintf("Delete of volume %s was accepted but it still exists: %s", id, err))
	}
}

func (r *sharedVolumeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	markImported(ctx, resp)
}
