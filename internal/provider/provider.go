// Package provider implements the Together AI Terraform provider.
package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/client"
)

var _ provider.Provider = &togetherProvider{}

type togetherProvider struct {
	version string
}

// New returns a provider factory for the given build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &togetherProvider{version: version}
	}
}

type togetherProviderModel struct {
	APIKey    types.String `tfsdk:"api_key"`
	BaseURL   types.String `tfsdk:"base_url"`
	ProjectID types.String `tfsdk:"project_id"`
}

// providerData is what resources and data sources receive in Configure.
type providerData struct {
	client       *client.Client
	pollInterval time.Duration

	mu        sync.Mutex
	projectID string
	// projectErr is set when the provider's project_id is unknown, so no
	// call falls back to another project.
	projectErr error
}

// defaultProject returns the configured project, or the only project the API
// key can see. The /v2 endpoint APIs need a project ID in the path.
func (p *providerData) defaultProject(ctx context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.projectErr != nil {
		return "", p.projectErr
	}
	if p.projectID != "" {
		return p.projectID, nil
	}
	projects, err := p.client.ListProjects(ctx)
	if err != nil {
		return "", fmt.Errorf("looking up the default project: %w", err)
	}
	switch len(projects) {
	case 0:
		return "", errors.New("the API key can see no projects. Set project_id on the resource or the provider, or TOGETHER_PROJECT_ID")
	case 1:
		p.projectID = projects[0].ID
		return p.projectID, nil
	}
	ids := make([]string, 0, len(projects))
	for _, pr := range projects {
		ids = append(ids, pr.ID)
	}
	return "", fmt.Errorf("the API key can see %d projects (%s). Set project_id on the resource or the provider, or TOGETHER_PROJECT_ID", len(projects), strings.Join(ids, ", "))
}

// resolveProject returns the resource's project_id when set, else the default.
func (p *providerData) resolveProject(ctx context.Context, v types.String) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" {
		return v.ValueString(), diags
	}
	id, err := p.defaultProject(ctx)
	if err != nil {
		diags.AddError("Cannot pick a Together AI project", err.Error())
	}
	return id, diags
}

func (p *togetherProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "togetherai"
	resp.Version = p.version
}

func (p *togetherProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Community provider for [Together AI](https://www.together.ai) infrastructure: GPU clusters with shared storage, " +
			"and dedicated inference endpoints with their deployments and traffic split. It is not affiliated with Together AI.\n\n" +
			"This is an early release (v0.x): resource schemas and behaviour may change between minor versions.\n\n" +
			"The API key is checked when a resource or data source calls the API, so `terraform validate` and plans with nothing to read work without one.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				MarkdownDescription: "Together AI API key. Can also be set with the `TOGETHER_API_KEY` environment variable. " +
					"GPU cluster kubeconfigs are only returned to keys of a project admin.",
				Optional:  true,
				Sensitive: true,
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "Base URL of the Together AI API. Defaults to `https://api.together.ai`. Can also be set with the `TOGETHER_BASE_URL` environment variable.",
				Optional:            true,
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Default project for dedicated endpoints and deployments (the `/v2/projects/{projectId}` APIs). " +
					"Can also be set with the `TOGETHER_PROJECT_ID` environment variable. When it is not set and the key can see exactly one project, that project is used.",
				Optional: true,
			},
		},
	}
}

func (p *togetherProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config togetherProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var keyErr error
	apiKey := os.Getenv("TOGETHER_API_KEY")
	switch {
	case config.APIKey.IsUnknown():
		apiKey = ""
		keyErr = errors.New("the Together AI api_key is unknown when this call is made. Set it to a value known at plan time, or use the TOGETHER_API_KEY environment variable")
	case !config.APIKey.IsNull():
		apiKey = config.APIKey.ValueString()
	}
	if keyErr == nil && apiKey == "" {
		keyErr = errors.New("missing Together AI API key. Set the api_key argument of the togetherai provider or the TOGETHER_API_KEY environment variable")
	}

	// An unknown base_url or project_id must not fall back to the default
	// API or another project: a read there could 404 and drop resources
	// from the state.
	baseURL := os.Getenv("TOGETHER_BASE_URL")
	switch {
	case config.BaseURL.IsUnknown():
		baseURL = ""
		if keyErr == nil {
			keyErr = errors.New("the Together AI base_url is unknown when this call is made. Set it to a value known at plan time, or use the TOGETHER_BASE_URL environment variable")
		}
	case !config.BaseURL.IsNull():
		baseURL = config.BaseURL.ValueString()
	}

	var projectErr error
	projectID := os.Getenv("TOGETHER_PROJECT_ID")
	switch {
	case config.ProjectID.IsUnknown():
		projectID = ""
		projectErr = errors.New("the provider's project_id is unknown when this call is made. Set it to a value known at plan time, set project_id on the resource, or use the TOGETHER_PROJECT_ID environment variable")
	case !config.ProjectID.IsNull():
		projectID = config.ProjectID.ValueString()
	}

	c := client.New(apiKey, baseURL, "terraform-provider-togetherai/"+p.version)
	c.KeyError = keyErr

	poll := 15 * time.Second
	// TOGETHER_POLL_INTERVAL is a test knob: the mock API tests poll fast.
	if v := os.Getenv("TOGETHER_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			poll = d
			c.MinBackoff = d
			c.MaxBackoff = 10 * d
		}
	}

	data := &providerData{client: c, pollInterval: poll, projectID: projectID, projectErr: projectErr}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *togetherProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewGPUClusterResource,
		NewSharedVolumeResource,
		NewEndpointResource,
		NewEndpointTrafficSplitResource,
		NewDeploymentResource,
	}
}

func (p *togetherProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewGPUClusterDataSource,
		NewGPUClusterRegionsDataSource,
		NewHardwareDataSource,
		NewSupportedModelsDataSource,
		NewProjectsDataSource,
	}
}
