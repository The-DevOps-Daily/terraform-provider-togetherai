package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/client"
)

var (
	_ datasource.DataSource = &projectsDataSource{}
	_ datasource.DataSource = &regionsDataSource{}
	_ datasource.DataSource = &hardwareDataSource{}
	_ datasource.DataSource = &supportedModelsDataSource{}
	_ datasource.DataSource = &gpuClusterDataSource{}
)

func strList(vals []string) []types.String {
	out := make([]types.String, 0, len(vals))
	for _, v := range vals {
		out = append(out, types.StringValue(v))
	}
	return out
}

// ---------- togetherai_projects ----------

// NewProjectsDataSource returns the togetherai_projects data source.
func NewProjectsDataSource() datasource.DataSource { return &projectsDataSource{} }

type projectsDataSource struct{ data *providerData }

type projectItem struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Slug             types.String `tfsdk:"slug"`
	OrganizationID   types.String `tfsdk:"organization_id"`
	OrganizationName types.String `tfsdk:"organization_name"`
}

type projectsModel struct {
	Projects []projectItem `tfsdk:"projects"`
}

func (d *projectsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_projects"
}

func (d *projectsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Projects the API key can access. Use a project's `id` as `project_id` for endpoints and deployments.",
		Attributes: map[string]schema.Attribute{
			"projects": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "Projects.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":                schema.StringAttribute{Computed: true, MarkdownDescription: "Project ID."},
					"name":              schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
					"slug":              schema.StringAttribute{Computed: true, MarkdownDescription: "Project slug, the first part of endpoint names."},
					"organization_id":   schema.StringAttribute{Computed: true, MarkdownDescription: "Organization ID."},
					"organization_name": schema.StringAttribute{Computed: true, MarkdownDescription: "Organization name."},
				}},
			},
		},
	}
}

func (d *projectsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureDataSource(req, resp)
}

func (d *projectsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	ps, err := d.data.client.ListProjects(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing projects", err.Error())
		return
	}
	m := projectsModel{Projects: []projectItem{}}
	for _, p := range ps {
		m.Projects = append(m.Projects, projectItem{
			ID: types.StringValue(p.ID), Name: types.StringValue(p.Name), Slug: types.StringValue(p.Slug),
			OrganizationID: types.StringValue(p.OrganizationID), OrganizationName: types.StringValue(p.OrganizationName),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------- togetherai_gpu_cluster_regions ----------

// NewGPUClusterRegionsDataSource returns togetherai_gpu_cluster_regions.
func NewGPUClusterRegionsDataSource() datasource.DataSource { return &regionsDataSource{} }

type regionsDataSource struct{ data *providerData }

type driverVersionItem struct {
	ID                  types.String `tfsdk:"id"`
	CUDAVersion         types.String `tfsdk:"cuda_version"`
	NvidiaDriverVersion types.String `tfsdk:"nvidia_driver_version"`
	OS                  types.String `tfsdk:"os"`
}

type regionItem struct {
	Name                   types.String        `tfsdk:"name"`
	SupportedInstanceTypes []types.String      `tfsdk:"supported_instance_types"`
	DriverVersions         []driverVersionItem `tfsdk:"driver_versions"`
}

type regionsModel struct {
	Regions []regionItem `tfsdk:"regions"`
}

func (d *regionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gpu_cluster_regions"
}

func (d *regionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU cluster regions with their GPU types and NVIDIA driver versions (`GET /v1/compute/regions`). " +
			"Availability changes over time. The response has no prices; see https://www.together.ai/pricing#gpu-clusters.",
		Attributes: map[string]schema.Attribute{
			"regions": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "Regions.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":                     schema.StringAttribute{Computed: true, MarkdownDescription: "Region name, for `togetherai_gpu_cluster.region`."},
					"supported_instance_types": schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "GPU types, for `gpu_type`."},
					"driver_versions": schema.ListNestedAttribute{
						Computed: true, MarkdownDescription: "NVIDIA driver and CUDA combinations.",
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"id":                    schema.StringAttribute{Computed: true, MarkdownDescription: "Catalog ID, for `nvidia_version_id`."},
							"cuda_version":          schema.StringAttribute{Computed: true, MarkdownDescription: "CUDA version."},
							"nvidia_driver_version": schema.StringAttribute{Computed: true, MarkdownDescription: "NVIDIA driver version."},
							"os":                    schema.StringAttribute{Computed: true, MarkdownDescription: "OS image family."},
						}},
					},
				}},
			},
		},
	}
}

func (d *regionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureDataSource(req, resp)
}

func (d *regionsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	regions, err := d.data.client.ListRegions(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing GPU cluster regions", err.Error())
		return
	}
	m := regionsModel{Regions: []regionItem{}}
	for _, r := range regions {
		item := regionItem{Name: types.StringValue(r.Name), SupportedInstanceTypes: strList(r.SupportedInstanceTypes), DriverVersions: []driverVersionItem{}}
		for _, dv := range r.DriverVersions {
			item.DriverVersions = append(item.DriverVersions, driverVersionItem{
				ID: types.StringValue(dv.ID), CUDAVersion: types.StringValue(dv.CUDAVersion),
				NvidiaDriverVersion: types.StringValue(dv.NvidiaDriverVersion), OS: types.StringValue(dv.OS),
			})
		}
		m.Regions = append(m.Regions, item)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------- togetherai_hardware ----------

// NewHardwareDataSource returns togetherai_hardware.
func NewHardwareDataSource() datasource.DataSource { return &hardwareDataSource{} }

type hardwareDataSource struct{ data *providerData }

type hardwareItem struct {
	ID                types.String   `tfsdk:"id"`
	Name              types.String   `tfsdk:"name"`
	Description       types.String   `tfsdk:"description"`
	GPUType           types.String   `tfsdk:"gpu_type"`
	GPUCount          types.Int64    `tfsdk:"gpu_count"`
	GPUMemoryGiB      types.Int64    `tfsdk:"gpu_memory_gib"`
	PriceCentsPerHour types.Int64    `tfsdk:"price_cents_per_hour"`
	Regions           []types.String `tfsdk:"regions"`
}

type hardwareModel struct {
	Hardware []hardwareItem `tfsdk:"hardware"`
}

func (d *hardwareDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_hardware"
}

func (d *hardwareDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Hardware instance types for dedicated inference deployments, with on-demand prices and regions (`GET /v2/public/inference-instance-types`).",
		Attributes: map[string]schema.Attribute{
			"hardware": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "Instance types.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":                   schema.StringAttribute{Computed: true, MarkdownDescription: "Instance type ID, such as `1xnvidia-h100-80gb`."},
					"name":                 schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
					"description":          schema.StringAttribute{Computed: true, MarkdownDescription: "Description."},
					"gpu_type":             schema.StringAttribute{Computed: true, MarkdownDescription: "GPU model, such as `H100`."},
					"gpu_count":            schema.Int64Attribute{Computed: true, MarkdownDescription: "GPUs per replica."},
					"gpu_memory_gib":       schema.Int64Attribute{Computed: true, MarkdownDescription: "Memory per GPU in GiB."},
					"price_cents_per_hour": schema.Int64Attribute{Computed: true, MarkdownDescription: "On-demand price of one replica in US cents per hour."},
					"regions":              schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Regions where it is offered."},
				}},
			},
		},
	}
}

func (d *hardwareDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureDataSource(req, resp)
}

func (d *hardwareDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	its, err := d.data.client.ListInstanceTypes(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error listing inference hardware", err.Error())
		return
	}
	m := hardwareModel{Hardware: []hardwareItem{}}
	for _, it := range its {
		regions := make([]string, 0, len(it.Regions))
		for _, r := range it.Regions {
			regions = append(regions, r.Name)
		}
		m.Hardware = append(m.Hardware, hardwareItem{
			ID: types.StringValue(it.ID), Name: types.StringValue(it.Name), Description: types.StringValue(it.Description),
			GPUType: types.StringValue(it.GPUType), GPUCount: types.Int64Value(it.GPUCount), GPUMemoryGiB: types.Int64Value(it.GPUMemoryGiB),
			PriceCentsPerHour: types.Int64Value(it.PriceCentsPerHour), Regions: strList(regions),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------- togetherai_supported_models ----------

// NewSupportedModelsDataSource returns togetherai_supported_models.
func NewSupportedModelsDataSource() datasource.DataSource { return &supportedModelsDataSource{} }

type supportedModelsDataSource struct{ data *providerData }

type profileItem struct {
	ProfileID    types.String `tfsdk:"profile_id"`
	Model        types.String `tfsdk:"model"`
	Config       types.String `tfsdk:"config"`
	ModelName    types.String `tfsdk:"model_name"`
	GPUType      types.String `tfsdk:"gpu_type"`
	GPUCount     types.Int64  `tfsdk:"gpu_count"`
	Quantization types.String `tfsdk:"quantization"`
	Parallelism  types.String `tfsdk:"parallelism"`
	AdapterMode  types.String `tfsdk:"adapter_mode"`
}

type modelItem struct {
	ID                 types.String   `tfsdk:"id"`
	Name               types.String   `tfsdk:"name"`
	DisplayName        types.String   `tfsdk:"display_name"`
	Description        types.String   `tfsdk:"description"`
	Publisher          types.String   `tfsdk:"publisher"`
	Status             types.String   `tfsdk:"status"`
	ContextLength      types.String   `tfsdk:"context_length"`
	BaseModel          types.String   `tfsdk:"base_model"`
	Products           []types.String `tfsdk:"products"`
	Capabilities       []types.String `tfsdk:"capabilities"`
	DeploymentProfiles []profileItem  `tfsdk:"deployment_profiles"`
}

type supportedModelsModel struct {
	Search      types.String `tfsdk:"search"`
	Product     types.String `tfsdk:"product"`
	Modality    types.String `tfsdk:"modality"`
	AdapterMode types.String `tfsdk:"adapter_mode"`
	Models      []modelItem  `tfsdk:"models"`
}

func (d *supportedModelsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_supported_models"
}

func (d *supportedModelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Together-hosted base models that can be deployed for dedicated inference, with their certified deployment profiles (`GET /v2/supported-models`). " +
			"A profile's `model` and `config` are the values `togetherai_deployment` needs.",
		Attributes: map[string]schema.Attribute{
			"search": schema.StringAttribute{Optional: true, MarkdownDescription: "Case-insensitive search across model IDs, names and descriptions."},
			"product": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Filter by product: `PRODUCT_SERVERLESS`, `PRODUCT_DEDICATED` or `PRODUCT_FINE_TUNING`.",
				Validators: []validator.String{stringvalidator.OneOf("PRODUCT_SERVERLESS", "PRODUCT_DEDICATED", "PRODUCT_FINE_TUNING")},
			},
			"modality": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Filter by input modality: `MODALITY_TEXT`, `MODALITY_IMAGE`, `MODALITY_AUDIO` or `MODALITY_VIDEO`.",
				Validators: []validator.String{stringvalidator.OneOf("MODALITY_TEXT", "MODALITY_IMAGE", "MODALITY_AUDIO", "MODALITY_VIDEO")},
			},
			"adapter_mode": schema.StringAttribute{
				Optional: true, MarkdownDescription: "Only models with a profile in this adapter mode: `ADAPTER_MODE_FIXED`, `ADAPTER_MODE_DYNAMIC` or `ADAPTER_MODE_DISABLED`.",
				Validators: []validator.String{stringvalidator.OneOf("ADAPTER_MODE_FIXED", "ADAPTER_MODE_DYNAMIC", "ADAPTER_MODE_DISABLED")},
			},
			"models": schema.ListNestedAttribute{
				Computed: true, MarkdownDescription: "Matching models.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":             schema.StringAttribute{Computed: true, MarkdownDescription: "Model ID."},
					"name":           schema.StringAttribute{Computed: true, MarkdownDescription: "Model name used for inference, such as `google/gemma-4-E4B-it`."},
					"display_name":   schema.StringAttribute{Computed: true, MarkdownDescription: "Display name."},
					"description":    schema.StringAttribute{Computed: true, MarkdownDescription: "Description."},
					"publisher":      schema.StringAttribute{Computed: true, MarkdownDescription: "Publisher."},
					"status":         schema.StringAttribute{Computed: true, MarkdownDescription: "Catalog status, such as `SUPPORTED_MODEL_STATUS_RECOMMENDED`."},
					"context_length": schema.StringAttribute{Computed: true, MarkdownDescription: "Maximum context length."},
					"base_model":     schema.StringAttribute{Computed: true, MarkdownDescription: "Base model resource name."},
					"products":       schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Products the model is offered in."},
					"capabilities":   schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Capabilities, such as `CAPABILITY_CHAT`."},
					"deployment_profiles": schema.ListNestedAttribute{
						Computed: true, MarkdownDescription: "Certified deployment profiles.",
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"profile_id":   schema.StringAttribute{Computed: true, MarkdownDescription: "Profile ID."},
							"model":        schema.StringAttribute{Computed: true, MarkdownDescription: "Model resource name, for `togetherai_deployment.model`."},
							"config":       schema.StringAttribute{Computed: true, MarkdownDescription: "Config resource name, for `togetherai_deployment.config`."},
							"model_name":   schema.StringAttribute{Computed: true, MarkdownDescription: "Qualified model name."},
							"gpu_type":     schema.StringAttribute{Computed: true, MarkdownDescription: "GPU type."},
							"gpu_count":    schema.Int64Attribute{Computed: true, MarkdownDescription: "GPUs per replica."},
							"quantization": schema.StringAttribute{Computed: true, MarkdownDescription: "Quantization, such as `BF16` or `FP8`."},
							"parallelism":  schema.StringAttribute{Computed: true, MarkdownDescription: "Parallelism, such as `TP1`."},
							"adapter_mode": schema.StringAttribute{Computed: true, MarkdownDescription: "Adapter serving mode."},
						}},
					},
				}},
			},
		},
	}
}

func (d *supportedModelsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureDataSource(req, resp)
}

func (d *supportedModelsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m supportedModelsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	models, err := d.data.client.ListSupportedModels(ctx, client.SupportedModelsFilter{
		Search: m.Search.ValueString(), Product: m.Product.ValueString(), Modality: m.Modality.ValueString(), AdapterMode: m.AdapterMode.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error listing supported models", err.Error())
		return
	}
	m.Models = []modelItem{}
	for _, sm := range models {
		item := modelItem{
			ID: types.StringValue(sm.ID), Name: types.StringValue(sm.Name), DisplayName: types.StringValue(sm.DisplayName),
			Description: strOrNull(sm.Description), Publisher: types.StringValue(sm.Publisher), Status: types.StringValue(sm.Status),
			ContextLength: strOrNull(sm.ContextLength), BaseModel: strOrNull(sm.BaseModel),
			Products: strList(sm.Products), Capabilities: strList(sm.Capabilities), DeploymentProfiles: []profileItem{},
		}
		for _, p := range sm.DeploymentProfiles {
			item.DeploymentProfiles = append(item.DeploymentProfiles, profileItem{
				ProfileID: types.StringValue(p.ProfileID), Model: strOrNull(p.Model), Config: strOrNull(p.Config), ModelName: strOrNull(p.ModelName),
				GPUType: types.StringValue(p.GPUType), GPUCount: types.Int64Value(p.GPUCount), Quantization: strOrNull(p.Quantization),
				Parallelism: strOrNull(p.Parallelism), AdapterMode: strOrNull(p.AdapterMode),
			})
		}
		m.Models = append(m.Models, item)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------- togetherai_gpu_cluster ----------

// NewGPUClusterDataSource returns the togetherai_gpu_cluster data source.
func NewGPUClusterDataSource() datasource.DataSource { return &gpuClusterDataSource{} }

type gpuClusterDataSource struct{ data *providerData }

type gpuClusterDSModel struct {
	ID          types.String   `tfsdk:"id"`
	Name        types.String   `tfsdk:"name"`
	ClusterType types.String   `tfsdk:"cluster_type"`
	Region      types.String   `tfsdk:"region"`
	GPUType     types.String   `tfsdk:"gpu_type"`
	NumGPUs     types.Int64    `tfsdk:"num_gpus"`
	BillingType types.String   `tfsdk:"billing_type"`
	Status      types.String   `tfsdk:"status"`
	ProjectID   types.String   `tfsdk:"project_id"`
	KubeConfig  types.String   `tfsdk:"kube_config"`
	VolumeIDs   []types.String `tfsdk:"volume_ids"`
}

func (d *gpuClusterDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gpu_cluster"
}

func (d *gpuClusterDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads an existing GPU cluster by ID, for example to pass its kubeconfig to the `kubernetes` or `helm` provider in another workspace.",
		Attributes: map[string]schema.Attribute{
			"id":           schema.StringAttribute{Required: true, MarkdownDescription: "Cluster ID."},
			"name":         schema.StringAttribute{Computed: true, MarkdownDescription: "Cluster name."},
			"cluster_type": schema.StringAttribute{Computed: true, MarkdownDescription: "`KUBERNETES` or `SLURM`."},
			"region":       schema.StringAttribute{Computed: true, MarkdownDescription: "Region."},
			"gpu_type":     schema.StringAttribute{Computed: true, MarkdownDescription: "GPU type."},
			"num_gpus":     schema.Int64Attribute{Computed: true, MarkdownDescription: "Standard GPU count."},
			"billing_type": schema.StringAttribute{Computed: true, MarkdownDescription: "Billing type."},
			"status":       schema.StringAttribute{Computed: true, MarkdownDescription: "Status."},
			"project_id":   schema.StringAttribute{Computed: true, MarkdownDescription: "Project ID."},
			"kube_config":  schema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "Admin kubeconfig (YAML). Only returned to a project admin's key."},
			"volume_ids":   schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Attached shared volume IDs."},
		},
	}
}

func (d *gpuClusterDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.data = configureDataSource(req, resp)
}

func (d *gpuClusterDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m gpuClusterDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.data.client.GetCluster(ctx, m.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading GPU cluster", err.Error())
		return
	}
	m.Name = types.StringValue(c.ClusterName)
	m.ClusterType = strOrNull(c.ClusterType)
	m.Region = types.StringValue(c.Region)
	m.GPUType = types.StringValue(c.GPUType)
	m.NumGPUs = types.Int64Value(c.NumGPUs)
	m.BillingType = strOrNull(c.BillingType)
	m.Status = types.StringValue(c.Status)
	m.ProjectID = strOrNull(c.ProjectID)
	m.KubeConfig = strOrNull(c.KubeConfig)
	m.VolumeIDs = []types.String{}
	for _, v := range c.Volumes {
		m.VolumeIDs = append(m.VolumeIDs, types.StringValue(v.VolumeID))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
