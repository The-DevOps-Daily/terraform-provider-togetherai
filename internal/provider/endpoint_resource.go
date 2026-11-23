package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/client"
)

var (
	_ resource.Resource                = &endpointResource{}
	_ resource.ResourceWithImportState = &endpointResource{}
)

// NewEndpointResource returns the togetherai_endpoint resource.
func NewEndpointResource() resource.Resource { return &endpointResource{} }

type endpointResource struct{ data *providerData }

type endpointModel struct {
	ID            types.String `tfsdk:"id"`
	ProjectID     types.String `tfsdk:"project_id"`
	Name          types.String `tfsdk:"name"`
	Visibility    types.String `tfsdk:"visibility"`
	QualifiedName types.String `tfsdk:"qualified_name"`
	EndpointType  types.String `tfsdk:"endpoint_type"`
	CreatedAt     types.String `tfsdk:"created_at"`
}

func (r *endpointResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_endpoint"
}

func (r *endpointResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A dedicated inference endpoint: the stable name clients send requests to (`model = qualified_name`). " +
			"Add `togetherai_deployment` resources to run a model on it, and a `togetherai_endpoint_traffic_split` to route requests to them. " +
			"This resource does not manage the traffic split, so a split set by another resource, the CLI or the console is not drift here.\n\n" +
			"**Cost:** an endpoint on its own does not bill. Deployments bill per running replica.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Endpoint ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				Optional: true, Computed: true, MarkdownDescription: "Project that owns the endpoint. Defaults to the provider's project. Changing it replaces the endpoint.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Endpoint name, either bare (`chat`) or qualified with the project slug (`my-project/chat`). Updated in place; this changes the name clients use.",
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"visibility": schema.StringAttribute{
				Optional: true, Computed: true,
				MarkdownDescription: "`VISIBILITY_PRIVATE` (project only, the default) or `VISIBILITY_INTERNAL` (shared with the organization). Updated in place.",
				Default:             stringdefault.StaticString(client.VisibilityPrivate),
				Validators:          []validator.String{stringvalidator.OneOf(client.VisibilityPrivate, client.VisibilityInternal)},
			},
			"qualified_name": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Project-qualified name `<project_slug>/<name>`. Pass it as `model` in inference requests to `https://api-inference.together.ai`.",
			},
			"endpoint_type": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Serving class, such as `ENDPOINT_TYPE_DEDICATED`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Creation time.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *endpointResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureResource(req, resp)
}

func flattenEndpoint(e *client.Endpoint, m *endpointModel) {
	m.ID = types.StringValue(e.ID)
	m.ProjectID = types.StringValue(e.ProjectID)
	m.Name = keepName(m.Name, e.Name)
	m.Visibility = types.StringValue(e.Visibility)
	m.QualifiedName = types.StringValue(e.Name)
	m.EndpointType = strOrNull(e.EndpointType)
	m.CreatedAt = strOrNull(e.CreatedAt)
}

func (r *endpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan endpointModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, diags := r.data.resolveProject(ctx, plan.ProjectID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.data.client.CreateEndpoint(ctx, project, client.EndpointCreateRequest{
		Name:       plan.Name.ValueString(),
		Visibility: plan.Visibility.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating endpoint", err.Error())
		return
	}
	if e.ProjectID == "" {
		e.ProjectID = project
	}
	flattenEndpoint(e, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *endpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state endpointModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, diags := r.data.resolveProject(ctx, state.ProjectID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	e, err := r.data.client.GetEndpoint(ctx, project, state.ID.ValueString())
	if client.IsNotFound(err) {
		tflog.Warn(ctx, "endpoint not found, removing from state", map[string]any{"endpoint_id": state.ID.ValueString()})
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading endpoint", err.Error())
		return
	}
	if e.ProjectID == "" {
		e.ProjectID = project
	}
	flattenEndpoint(e, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *endpointResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state endpointModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var mask []string
	up := client.EndpointUpdate{}
	if !plan.Name.Equal(state.Name) {
		mask = append(mask, "name")
		up.Name = plan.Name.ValueString()
	}
	if !plan.Visibility.Equal(state.Visibility) {
		mask = append(mask, "visibility")
		up.Visibility = plan.Visibility.ValueString()
	}
	project := state.ProjectID.ValueString()
	var e *client.Endpoint
	var err error
	if len(mask) > 0 {
		e, err = r.data.client.UpdateEndpoint(ctx, project, state.ID.ValueString(), mask, up)
	} else {
		e, err = r.data.client.GetEndpoint(ctx, project, state.ID.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Error updating endpoint", err.Error())
		return
	}
	if e.ProjectID == "" {
		e.ProjectID = project
	}
	flattenEndpoint(e, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *endpointResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state endpointModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.data.client.DeleteEndpoint(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error deleting endpoint",
			err.Error()+"\n\nAn endpoint can only be deleted once it has no deployments. Deployments created outside Terraform must be deleted first.")
	}
}

// ImportState accepts "<endpoint_id>" (provider project) or
// "<project_id>/<endpoint_id>".
func (r *endpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, 1, 2)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("%s. Use <endpoint_id> or <project_id>/<endpoint_id>.", err))
		return
	}
	id := parts[len(parts)-1]
	if len(parts) == 2 {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
