package client

import (
	"context"
	"net/http"
	"net/url"
)

// Shared volume statuses from GPUClustersSharedVolume.status.
const (
	VolumeStatusScheduled     = "scheduled"
	VolumeStatusAvailable     = "available"
	VolumeStatusBound         = "bound"
	VolumeStatusProvisioning  = "provisioning"
	VolumeStatusDeleting      = "deleting"
	VolumeStatusFailed        = "failed"
	VolumeStatusAccessRevoked = "access_revoked"
	VolumeStatusUnknown       = "unknown"
)

// VolumeCreateRequest is GPUClustersSharedVolumeCreateRequest.
type VolumeCreateRequest struct {
	VolumeName             string `json:"volume_name"`
	SizeTiB                int64  `json:"size_tib"`
	Region                 string `json:"region"`
	IsLifecycleIndependent *bool  `json:"is_lifecycle_independent,omitempty"`
	ProjectID              string `json:"project_id,omitempty"`
	InstanceClusterID      string `json:"instance_cluster_id,omitempty"`
}

// VolumeUpdateRequest is GPUClustersSharedVolumeUpdateRequest.
type VolumeUpdateRequest struct {
	VolumeID string `json:"volume_id"`
	SizeTiB  *int64 `json:"size_tib,omitempty"`
}

// Volume is GPUClustersSharedVolume.
type Volume struct {
	VolumeID   string `json:"volume_id"`
	VolumeName string `json:"volume_name"`
	SizeTiB    int64  `json:"size_tib"`
	Status     string `json:"status"`
}

// CreateVolume calls POST /v1/compute/clusters/storage/volumes.
func (c *Client) CreateVolume(ctx context.Context, req VolumeCreateRequest) (*Volume, error) {
	var out Volume
	if err := c.do(ctx, http.MethodPost, "/v1/compute/clusters/storage/volumes", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetVolume calls GET /v1/compute/clusters/storage/volumes/{volume_id}.
func (c *Client) GetVolume(ctx context.Context, id string) (*Volume, error) {
	var out Volume
	if err := c.do(ctx, http.MethodGet, "/v1/compute/clusters/storage/volumes/"+pathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListVolumes calls GET /v1/compute/clusters/storage/volumes.
func (c *Client) ListVolumes(ctx context.Context, projectID string) ([]Volume, error) {
	q := url.Values{}
	if projectID != "" {
		q.Set("projectId", projectID)
	}
	var out struct {
		Volumes []Volume `json:"volumes"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/compute/clusters/storage/volumes", q, nil, &out); err != nil {
		return nil, err
	}
	return out.Volumes, nil
}

// UpdateVolume calls PUT /v1/compute/clusters/storage/volumes. The volume ID
// goes in the body, not the path.
func (c *Client) UpdateVolume(ctx context.Context, req VolumeUpdateRequest) (*Volume, error) {
	var out Volume
	if err := c.do(ctx, http.MethodPut, "/v1/compute/clusters/storage/volumes", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteVolume calls DELETE /v1/compute/clusters/storage/volumes/{volume_id}.
func (c *Client) DeleteVolume(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/compute/clusters/storage/volumes/"+pathEscape(id), nil, nil, nil)
}
