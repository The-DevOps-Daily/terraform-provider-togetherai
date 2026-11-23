package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// maxPages stops a pagination loop if the API keeps returning a cursor.
const maxPages = 1000

// Project is the Project schema from GET /v1/projects.
type Project struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
}

// ListProjects calls GET /v1/projects and follows next_cursor to the end.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var all []Project
	after := ""
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("limit", "100")
		if after != "" {
			q.Set("after", after)
		}
		var out struct {
			Data       []Project `json:"data"`
			NextCursor *string   `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, "/v1/projects", q, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if out.NextCursor == nil || *out.NextCursor == "" || *out.NextCursor == after {
			return all, nil
		}
		after = *out.NextCursor
	}
	return nil, fmt.Errorf("GET /v1/projects: gave up after %d pages", maxPages)
}

// InstanceTypeRegion is DE.Region (name only plus headroom).
type InstanceTypeRegion struct {
	Name     string `json:"name"`
	Headroom *struct {
		Value    *int64 `json:"value"`
		Relation string `json:"relation"`
	} `json:"headroom"`
}

// InstanceType is DE.InferenceInstanceType.
type InstanceType struct {
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	Description       string               `json:"description"`
	GPUType           string               `json:"gpuType"`
	GPUCount          int64                `json:"gpuCount"`
	GPUMemoryGiB      int64                `json:"gpuMemoryGib"`
	PriceCentsPerHour int64                `json:"priceCentsPerHour"`
	Regions           []InstanceTypeRegion `json:"regions"`
}

// ListInstanceTypes calls GET /v2/public/inference-instance-types. The spec
// says the catalog is returned in full, but next_cursor is followed anyway.
func (c *Client) ListInstanceTypes(ctx context.Context) ([]InstanceType, error) {
	var all []InstanceType
	after := ""
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		if after != "" {
			q.Set("after", after)
		}
		var out struct {
			Data       []InstanceType `json:"data"`
			NextCursor *string        `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, "/v2/public/inference-instance-types", q, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if out.NextCursor == nil || *out.NextCursor == "" || *out.NextCursor == after {
			return all, nil
		}
		after = *out.NextCursor
	}
	return nil, fmt.Errorf("GET /v2/public/inference-instance-types: gave up after %d pages", maxPages)
}

// DeploymentProfile is DE.SupportedModelDeploymentProfile.
type DeploymentProfile struct {
	ProfileID                 string `json:"profileId"`
	CertifiedConfigRevisionID string `json:"certifiedConfigRevisionId"`
	CertifiedModelRevisionID  string `json:"certifiedModelRevisionId"`
	GPUType                   string `json:"gpuType"`
	GPUCount                  int64  `json:"gpuCount"`
	Quantization              string `json:"quantization"`
	Config                    string `json:"config"`
	Model                     string `json:"model"`
	Parallelism               string `json:"parallelism"`
	ModelName                 string `json:"modelName"`
	AdapterMode               string `json:"adapterMode"`
}

// SupportedModel is DE.SupportedModel (the fields the provider exposes).
type SupportedModel struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	DisplayName        string              `json:"displayName"`
	Description        string              `json:"description"`
	Publisher          string              `json:"publisher"`
	Status             string              `json:"status"`
	ContextLength      string              `json:"contextLength"`
	Products           []string            `json:"products"`
	Capabilities       []string            `json:"capabilities"`
	InputModalities    []string            `json:"inputModalities"`
	BaseModel          string              `json:"baseModel"`
	DeploymentProfiles []DeploymentProfile `json:"deploymentProfiles"`
}

// SupportedModelsFilter holds the optional list filters.
type SupportedModelsFilter struct {
	Modality    string
	Product     string
	Search      string
	AdapterMode string
}

// ListSupportedModels calls GET /v2/supported-models and follows next_cursor.
func (c *Client) ListSupportedModels(ctx context.Context, f SupportedModelsFilter) ([]SupportedModel, error) {
	var all []SupportedModel
	after := ""
	for page := 0; page < maxPages; page++ {
		q := url.Values{}
		q.Set("limit", "100")
		if f.Modality != "" {
			q.Set("modality", f.Modality)
		}
		if f.Product != "" {
			q.Set("product", f.Product)
		}
		if f.Search != "" {
			q.Set("search", f.Search)
		}
		if f.AdapterMode != "" {
			q.Set("adapterMode", f.AdapterMode)
		}
		if after != "" {
			q.Set("after", after)
		}
		var out struct {
			Data       []SupportedModel `json:"data"`
			NextCursor *string          `json:"next_cursor"`
		}
		if err := c.do(ctx, http.MethodGet, "/v2/supported-models", q, nil, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
		if out.NextCursor == nil || *out.NextCursor == "" || *out.NextCursor == after {
			return all, nil
		}
		after = *out.NextCursor
	}
	return nil, fmt.Errorf("GET /v2/supported-models: gave up after %d pages", maxPages)
}
