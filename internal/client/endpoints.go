package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Endpoint visibility values.
const (
	VisibilityPrivate  = "VISIBILITY_PRIVATE"
	VisibilityInternal = "VISIBILITY_INTERNAL"
)

// TrafficSplitEntry is DE.TrafficSplitEntry.
type TrafficSplitEntry struct {
	DeploymentID string  `json:"deploymentId"`
	Weight       float64 `json:"weight"`
}

// DeploymentSummary is DE.DeploymentSummary (the fields the provider uses).
type DeploymentSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// Endpoint is DE.Endpoint.
type Endpoint struct {
	ID              string              `json:"id"`
	ProjectID       string              `json:"projectId"`
	Name            string              `json:"name"`
	CreatedAt       string              `json:"createdAt"`
	UpdatedAt       string              `json:"updatedAt"`
	Etag            string              `json:"etag"`
	TrafficSplit    []TrafficSplitEntry `json:"trafficSplit"`
	Visibility      string              `json:"visibility"`
	EndpointType    string              `json:"endpointType"`
	Deployments     []DeploymentSummary `json:"deployments"`
	ActiveRolloutID string              `json:"activeRolloutId"`
}

// EndpointCreateRequest is DE.CreateEndpointRequest.
type EndpointCreateRequest struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility,omitempty"`
}

// EndpointUpdate is DE.EndpointUpdate. Only the fields named in the update
// mask are applied.
type EndpointUpdate struct {
	Name       string `json:"name,omitempty"`
	Etag       string `json:"etag,omitempty"`
	Visibility string `json:"visibility,omitempty"`
	// TrafficSplit is a pointer so that an empty list ("stop routing live
	// traffic") is sent as [] instead of being left out.
	TrafficSplit *[]TrafficSplitEntry `json:"trafficSplit,omitempty"`
}

func endpointsPath(projectID string) string {
	return "/v2/projects/" + pathEscape(projectID) + "/endpoints"
}

// CreateEndpoint calls POST /v2/projects/{projectId}/endpoints.
func (c *Client) CreateEndpoint(ctx context.Context, projectID string, req EndpointCreateRequest) (*Endpoint, error) {
	var out Endpoint
	if err := c.do(ctx, http.MethodPost, endpointsPath(projectID), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetEndpoint calls GET /v2/projects/{projectId}/endpoints/{id}.
func (c *Client) GetEndpoint(ctx context.Context, projectID, id string) (*Endpoint, error) {
	var out Endpoint
	if err := c.do(ctx, http.MethodGet, endpointsPath(projectID)+"/"+pathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateEndpoint calls PATCH /v2/projects/{projectId}/endpoints/{id} with an
// explicit updateMask, so fields this call does not own are left alone.
func (c *Client) UpdateEndpoint(ctx context.Context, projectID, id string, mask []string, req EndpointUpdate) (*Endpoint, error) {
	q := url.Values{}
	if len(mask) > 0 {
		q.Set("updateMask", strings.Join(mask, ","))
	}
	var out Endpoint
	if err := c.do(ctx, http.MethodPatch, endpointsPath(projectID)+"/"+pathEscape(id), q, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteEndpoint calls DELETE /v2/projects/{projectId}/endpoints/{id}.
func (c *Client) DeleteEndpoint(ctx context.Context, projectID, id string) error {
	return c.do(ctx, http.MethodDelete, endpointsPath(projectID)+"/"+pathEscape(id), nil, nil, nil)
}

// Deployment states from DE.DeploymentStatus.state.
const (
	DeploymentStateProvisioning = "DEPLOYMENT_STATE_PROVISIONING"
	DeploymentStateReady        = "DEPLOYMENT_STATE_READY"
	DeploymentStateScaling      = "DEPLOYMENT_STATE_SCALING"
	DeploymentStateDegraded     = "DEPLOYMENT_STATE_DEGRADED"
	DeploymentStateFailed       = "DEPLOYMENT_STATE_FAILED"
	DeploymentStateStopped      = "DEPLOYMENT_STATE_STOPPED"
	DeploymentStateStopping     = "DEPLOYMENT_STATE_STOPPING"
)

// ScalingMetric is DE.ScalingMetric.
type ScalingMetric struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Target     float64 `json:"target"`
	Percentile string  `json:"percentile,omitempty"`
}

// Autoscaling is DE.Autoscaling (scaleUp and scaleDown rate rules are not
// managed by the provider yet).
type Autoscaling struct {
	MinReplicas       *int64          `json:"minReplicas,omitempty"`
	MaxReplicas       *int64          `json:"maxReplicas,omitempty"`
	ScaleDownWindow   string          `json:"scaleDownWindow,omitempty"`
	ScaleUpWindow     string          `json:"scaleUpWindow,omitempty"`
	ScaleToZeroWindow string          `json:"scaleToZeroWindow,omitempty"`
	ScalingMetrics    []ScalingMetric `json:"scalingMetrics,omitempty"`
}

// InlinePlacement is DE.InlinePlacement (without compliancePolicy).
type InlinePlacement struct {
	Regions    []string `json:"regions,omitempty"`
	Constraint string   `json:"constraint,omitempty"`
}

// Placement is the DE.Placement oneOf: set exactly one of Inline or Profile.
type Placement struct {
	Inline  *InlinePlacement `json:"inline,omitempty"`
	Profile string           `json:"profile,omitempty"`
}

// DeploymentStatus is DE.DeploymentStatus.
type DeploymentStatus struct {
	State             string `json:"state"`
	ReadyReplicas     int64  `json:"readyReplicas"`
	Message           string `json:"message"`
	ScheduledReplicas int64  `json:"scheduledReplicas"`
}

// Deployment is DE.Deployment (the fields the provider uses).
type Deployment struct {
	ID                              string           `json:"id"`
	ProjectID                       string           `json:"projectId"`
	EndpointID                      string           `json:"endpointId"`
	Name                            string           `json:"name"`
	CreatedAt                       string           `json:"createdAt"`
	UpdatedAt                       string           `json:"updatedAt"`
	Model                           string           `json:"model"`
	Autoscaling                     Autoscaling      `json:"autoscaling"`
	Config                          string           `json:"config"`
	Etag                            string           `json:"etag"`
	Hardware                        string           `json:"hardware"`
	TrafficMode                     string           `json:"trafficMode"`
	MaxConcurrentRequestsPerReplica string           `json:"maxConcurrentRequestsPerReplica"`
	InactiveTimeout                 *int64           `json:"inactiveTimeout"`
	DesiredReplicas                 int64            `json:"desiredReplicas"`
	Status                          DeploymentStatus `json:"status"`
	Placement                       *Placement       `json:"placement"`
}

// DeploymentCreateRequest is DE.CreateDeploymentRequest (without the
// deprecated *Id fields).
type DeploymentCreateRequest struct {
	Name                            string      `json:"name"`
	Model                           string      `json:"model,omitempty"`
	Autoscaling                     Autoscaling `json:"autoscaling"`
	Config                          string      `json:"config,omitempty"`
	InactiveTimeout                 *int64      `json:"inactiveTimeout,omitempty"`
	MaxConcurrentRequestsPerReplica string      `json:"maxConcurrentRequestsPerReplica,omitempty"`
	Placement                       *Placement  `json:"placement,omitempty"`
}

// DeploymentUpdateRequest is DE.UpdateDeploymentRequest.
type DeploymentUpdateRequest struct {
	Name                            string       `json:"name,omitempty"`
	Autoscaling                     *Autoscaling `json:"autoscaling,omitempty"`
	InactiveTimeout                 *int64       `json:"inactiveTimeout,omitempty"`
	MaxConcurrentRequestsPerReplica string       `json:"maxConcurrentRequestsPerReplica,omitempty"`
	Etag                            string       `json:"etag,omitempty"`
}

func deploymentsPath(projectID, endpointID string) string {
	return endpointsPath(projectID) + "/" + pathEscape(endpointID) + "/deployments"
}

// CreateDeployment calls POST /v2/projects/{projectId}/endpoints/{endpointId}/deployments.
func (c *Client) CreateDeployment(ctx context.Context, projectID, endpointID string, req DeploymentCreateRequest) (*Deployment, error) {
	var out Deployment
	if err := c.do(ctx, http.MethodPost, deploymentsPath(projectID, endpointID), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDeployment calls GET .../deployments/{id}.
func (c *Client) GetDeployment(ctx context.Context, projectID, endpointID, id string) (*Deployment, error) {
	var out Deployment
	if err := c.do(ctx, http.MethodGet, deploymentsPath(projectID, endpointID)+"/"+pathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateDeployment calls PATCH .../deployments/{id} with an explicit updateMask.
func (c *Client) UpdateDeployment(ctx context.Context, projectID, endpointID, id string, mask []string, req DeploymentUpdateRequest) (*Deployment, error) {
	q := url.Values{}
	if len(mask) > 0 {
		q.Set("updateMask", strings.Join(mask, ","))
	}
	var out Deployment
	if err := c.do(ctx, http.MethodPatch, deploymentsPath(projectID, endpointID)+"/"+pathEscape(id), q, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDeployment calls DELETE .../deployments/{id}.
func (c *Client) DeleteDeployment(ctx context.Context, projectID, endpointID, id string) error {
	return c.do(ctx, http.MethodDelete, deploymentsPath(projectID, endpointID)+"/"+pathEscape(id), nil, nil, nil)
}
