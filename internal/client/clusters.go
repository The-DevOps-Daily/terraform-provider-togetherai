package client

import (
	"context"
	"net/http"
	"net/url"
)

// Cluster statuses from GPUClusterInfo.status.
const (
	ClusterStatusReady    = "Ready"
	ClusterStatusDegraded = "Degraded"
	ClusterStatusDeleting = "Deleting"
)

// OIDCConfig is the OIDCConfig schema.
type OIDCConfig struct {
	IssuerURL      string `json:"issuer_url"`
	ClientID       string `json:"client_id"`
	UsernameClaim  string `json:"username_claim"`
	UsernamePrefix string `json:"username_prefix"`
	GroupClaim     string `json:"group_claim"`
	GroupPrefix    string `json:"group_prefix"`
	CACert         string `json:"ca_cert,omitempty"`
}

// ClusterCreateRequest is GPUClusterCreateRequestCommon plus the NVIDIA
// selectors of both anyOf variants.
type ClusterCreateRequest struct {
	ClusterType          string               `json:"cluster_type,omitempty"`
	Region               string               `json:"region"`
	GPUType              string               `json:"gpu_type"`
	NumGPUs              int64                `json:"num_gpus"`
	ClusterName          string               `json:"cluster_name"`
	DurationDays         *int64               `json:"duration_days,omitempty"`
	SharedVolume         *VolumeCreateRequest `json:"shared_volume,omitempty"`
	VolumeID             string               `json:"volume_id,omitempty"`
	BillingType          string               `json:"billing_type"`
	AutoScaleMaxGPUs     *int64               `json:"auto_scale_max_gpus,omitempty"`
	SlurmShmSizeGiB      *int64               `json:"slurm_shm_size_gib,omitempty"`
	CapacityPoolID       string               `json:"capacity_pool_id,omitempty"`
	ReservationStartTime string               `json:"reservation_start_time,omitempty"`
	ReservationEndTime   string               `json:"reservation_end_time,omitempty"`
	InstallTraefik       *bool                `json:"install_traefik,omitempty"`
	CUDAVersion          string               `json:"cuda_version,omitempty"`
	NvidiaDriverVersion  string               `json:"nvidia_driver_version,omitempty"`
	NvidiaVersionID      string               `json:"nvidia_version_id,omitempty"`
	SlurmImage           string               `json:"slurm_image,omitempty"`
	OIDCConfig           *OIDCConfig          `json:"oidc_config,omitempty"`
	ProjectID            string               `json:"project_id,omitempty"`
	NumCapacityPoolGPUs  *int64               `json:"num_capacity_pool_gpus,omitempty"`
	AutoScale            *bool                `json:"auto_scale,omitempty"`
	NumPreemptibleGPUs   *int64               `json:"num_preemptible_gpus,omitempty"`
	NumReservedGPUs      *int64               `json:"num_reserved_gpus,omitempty"`
}

// ClusterUpdateRequest is GPUClusterUpdateRequest. Omitted fields keep their
// current value.
type ClusterUpdateRequest struct {
	ClusterType         string `json:"cluster_type,omitempty"`
	NumGPUs             *int64 `json:"num_gpus,omitempty"`
	ReservationEndTime  string `json:"reservation_end_time,omitempty"`
	NumReservedGPUs     *int64 `json:"num_reserved_gpus,omitempty"`
	NumPreemptibleGPUs  *int64 `json:"num_preemptible_gpus,omitempty"`
	NumCapacityPoolGPUs *int64 `json:"num_capacity_pool_gpus,omitempty"`
}

// ClusterVolume is GPUClusterVolume.
type ClusterVolume struct {
	VolumeID   string `json:"volume_id"`
	VolumeName string `json:"volume_name"`
	SizeTiB    int64  `json:"size_tib"`
	Status     string `json:"status"`
}

// ClusterNode holds the fields the provider reads from
// GPUClusterControlPlaneNode and GPUClusterGPUWorkerNode.
type ClusterNode struct {
	NodeID     string  `json:"node_id"`
	Status     string  `json:"status"`
	HostName   string  `json:"host_name"`
	NumCPUs    int64   `json:"num_cpu_cores"`
	NumGPUs    int64   `json:"num_gpus"`
	MemoryGiB  float64 `json:"memory_gib"`
	PublicIPv4 string  `json:"public_ipv4"`
	InstanceID string  `json:"instance_id"`
}

// Cluster is GPUClusterInfo (the fields the provider uses).
type Cluster struct {
	ClusterID                string          `json:"cluster_id"`
	ClusterType              string          `json:"cluster_type"`
	Region                   string          `json:"region"`
	GPUType                  string          `json:"gpu_type"`
	ClusterName              string          `json:"cluster_name"`
	DurationHours            *int64          `json:"duration_hours"`
	Volumes                  []ClusterVolume `json:"volumes"`
	Status                   string          `json:"status"`
	ControlPlaneNodes        []ClusterNode   `json:"control_plane_nodes"`
	GPUWorkerNodes           []ClusterNode   `json:"gpu_worker_nodes"`
	KubeConfig               string          `json:"kube_config"`
	NumGPUs                  int64           `json:"num_gpus"`
	SlurmShmSizeGiB          *int64          `json:"slurm_shm_size_gib"`
	CapacityPoolID           string          `json:"capacity_pool_id"`
	ReservationStartTime     string          `json:"reservation_start_time"`
	ReservationEndTime       string          `json:"reservation_end_time"`
	InstallTraefik           *bool           `json:"install_traefik"`
	CUDAVersion              string          `json:"cuda_version"`
	NvidiaDriverVersion      string          `json:"nvidia_driver_version"`
	CreatedAt                string          `json:"created_at"`
	OIDCConfig               *OIDCConfig     `json:"oidc_config"`
	ProjectID                string          `json:"project_id"`
	NumCPUWorkers            int64           `json:"num_cpu_workers"`
	DesiredPreemptibleGPUs   int64           `json:"desired_preemptible_gpus"`
	AllocatedPreemptibleGPUs int64           `json:"allocated_preemptible_gpus"`
	NumCapacityPoolGPUs      int64           `json:"num_capacity_pool_gpus"`
	NumReservedGPUs          int64           `json:"num_reserved_gpus"`
	BillingType              string          `json:"billing_type"`
	FirstReadyAt             string          `json:"first_ready_at"`
	ControlPlaneReady        *bool           `json:"control_plane_ready"`
	NvidiaDriverVersionID    string          `json:"nvidia_driver_version_id"`
}

// CreateCluster calls POST /v1/compute/clusters.
func (c *Client) CreateCluster(ctx context.Context, req ClusterCreateRequest) (*Cluster, error) {
	var out Cluster
	if err := c.do(ctx, http.MethodPost, "/v1/compute/clusters", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCluster calls GET /v1/compute/clusters/{cluster_id}.
func (c *Client) GetCluster(ctx context.Context, id string) (*Cluster, error) {
	var out Cluster
	if err := c.do(ctx, http.MethodGet, "/v1/compute/clusters/"+pathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListClusters calls GET /v1/compute/clusters. projectID may be empty.
func (c *Client) ListClusters(ctx context.Context, projectID string) ([]Cluster, error) {
	q := url.Values{}
	if projectID != "" {
		q.Set("projectId", projectID)
	}
	var out struct {
		Clusters []Cluster `json:"clusters"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/compute/clusters", q, nil, &out); err != nil {
		return nil, err
	}
	return out.Clusters, nil
}

// UpdateCluster calls PUT /v1/compute/clusters/{cluster_id}.
func (c *Client) UpdateCluster(ctx context.Context, id string, req ClusterUpdateRequest) (*Cluster, error) {
	var out Cluster
	if err := c.do(ctx, http.MethodPut, "/v1/compute/clusters/"+pathEscape(id), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteCluster calls DELETE /v1/compute/clusters/{cluster_id}.
func (c *Client) DeleteCluster(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/compute/clusters/"+pathEscape(id), nil, nil, nil)
}

// DriverVersion is ClusterDriverVersionInfo.
type DriverVersion struct {
	ID                  string `json:"id"`
	CUDAVersion         string `json:"cuda_version"`
	NvidiaDriverVersion string `json:"nvidia_driver_version"`
	OS                  string `json:"os"`
}

// Region is one entry of RegionListResponse.regions.
type Region struct {
	Name                   string          `json:"name"`
	SupportedInstanceTypes []string        `json:"supported_instance_types"`
	DriverVersions         []DriverVersion `json:"driver_versions"`
}

// ListRegions calls GET /v1/compute/regions.
func (c *Client) ListRegions(ctx context.Context) ([]Region, error) {
	var out struct {
		Regions []Region `json:"regions"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/compute/regions", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Regions, nil
}
