package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
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
	_ resource.Resource                = &trafficSplitResource{}
	_ resource.ResourceWithImportState = &trafficSplitResource{}
)

// NewEndpointTrafficSplitResource returns togetherai_endpoint_traffic_split.
func NewEndpointTrafficSplitResource() resource.Resource { return &trafficSplitResource{} }

type trafficSplitResource struct{ data *providerData }

type trafficRoute struct {
	DeploymentID types.String  `tfsdk:"deployment_id"`
	Weight       types.Float64 `tfsdk:"weight"`
}

type trafficSplitModel struct {
	ID         types.String   `tfsdk:"id"`
	ProjectID  types.String   `tfsdk:"project_id"`
	EndpointID types.String   `tfsdk:"endpoint_id"`
	Routes     []trafficRoute `tfsdk:"routes"`
}

func (r *trafficSplitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_endpoint_traffic_split"
}

func (r *trafficSplitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The live traffic split of a dedicated endpoint: which deployments receive requests sent to the endpoint name, and their weights. " +
			"A deployment's share is `weight * ready replicas`. It is a separate resource because deployments need the endpoint's ID, and the split needs the deployments' IDs.\n\n" +
			"Destroying this resource sets an empty split, which stops routing live traffic to the endpoint. Use one split resource per endpoint.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Same as `endpoint_id`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				Optional: true, Computed: true, MarkdownDescription: "Project of the endpoint. Defaults to the provider's project.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"endpoint_id": schema.StringAttribute{
				Required: true, MarkdownDescription: "Endpoint ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"routes": schema.SetNestedAttribute{
				Required: true, MarkdownDescription: "Deployments in the live split. An empty set stops routing live traffic.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"deployment_id": schema.StringAttribute{Required: true, MarkdownDescription: "Deployment ID under this endpoint."},
					"weight": schema.Float64Attribute{
						Required: true, MarkdownDescription: "Non-negative weight per ready replica. `0` keeps the deployment in the split without traffic.",
						Validators: []validator.Float64{float64validator.AtLeast(0)},
					},
				}},
			},
		},
	}
}

func (r *trafficSplitResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureResource(req, resp)
}

func routesToAPI(routes []trafficRoute) []client.TrafficSplitEntry {
	out := make([]client.TrafficSplitEntry, 0, len(routes))
	for _, rt := range routes {
		out = append(out, client.TrafficSplitEntry{DeploymentID: rt.DeploymentID.ValueString(), Weight: rt.Weight.ValueFloat64()})
	}
	return out
}

func routesFromAPI(split []client.TrafficSplitEntry) []trafficRoute {
	out := make([]trafficRoute, 0, len(split))
	for _, t := range split {
		out = append(out, trafficRoute{DeploymentID: types.StringValue(t.DeploymentID), Weight: types.Float64Value(t.Weight)})
	}
	return out
}

func (r *trafficSplitResource) put(ctx context.Context, m *trafficSplitModel) error {
	split := routesToAPI(m.Routes)
	e, err := r.data.client.UpdateEndpoint(ctx, m.ProjectID.ValueString(), m.EndpointID.ValueString(), []string{"trafficSplit"}, client.EndpointUpdate{TrafficSplit: &split})
	if err != nil {
		return err
	}
	m.ID = types.StringValue(e.ID)
	m.Routes = routesFromAPI(e.TrafficSplit)
	return nil
}

func (r *trafficSplitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan trafficSplitModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, diags := r.data.resolveProject(ctx, plan.ProjectID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ProjectID = types.StringValue(project)
	if err := r.put(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error setting endpoint traffic split", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *trafficSplitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state trafficSplitModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, diags := r.data.resolveProject(ctx, state.ProjectID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.data.client.GetEndpoint(ctx, project, state.EndpointID.ValueString())
	if client.IsNotFound(err) {
		tflog.Warn(ctx, "endpoint not found, removing its traffic split from state", map[string]any{"endpoint_id": state.EndpointID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading endpoint traffic split", err.Error())
		return
	}
	state.ID = types.StringValue(e.ID)
	state.ProjectID = types.StringValue(project)
	state.Routes = routesFromAPI(e.TrafficSplit)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *trafficSplitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state trafficSplitModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ProjectID = state.ProjectID
	if err := r.put(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error updating endpoint traffic split", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *trafficSplitResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state trafficSplitModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	empty := []client.TrafficSplitEntry{}
	_, err := r.data.client.UpdateEndpoint(ctx, state.ProjectID.ValueString(), state.EndpointID.ValueString(), []string{"trafficSplit"}, client.EndpointUpdate{TrafficSplit: &empty})
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error clearing endpoint traffic split", err.Error())
	}
}

// ImportState accepts "<endpoint_id>" or "<project_id>/<endpoint_id>".
func (r *trafficSplitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, 1, 2)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("%s. Use <endpoint_id> or <project_id>/<endpoint_id>.", err))
		return
	}
	if len(parts) == 2 {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("endpoint_id"), parts[len(parts)-1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[len(parts)-1])...)
}
