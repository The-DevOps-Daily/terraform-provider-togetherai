// Package mockapi is an in-memory stand-in for the parts of the Together AI
// API the provider uses. It follows testdata/together-openapi.yaml and the
// public docs: request and response shapes, required fields, enums, async
// states (clusters, volumes, deployments), delete preconditions, 404s and the
// {"error": {...}} error body. Tests drive it through the exported fields and
// methods to simulate drift and failures.
package mockapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// APIKey is the only key the mock accepts.
const APIKey = "mock-api-key"

// Server is the mock API.
type Server struct {
	mu  sync.Mutex
	seq int
	srv *httptest.Server

	// StepsPerState is how many GETs an async resource needs to move to its
	// next state. 1 means every GET advances it.
	StepsPerState int
	// DeleteSteps is how many GETs a deleting cluster or volume still
	// answers (status "Deleting"/"deleting") before it returns 404.
	DeleteSteps int

	Projects        []Project
	Regions         []Region
	InstanceTypes   []InstanceType
	SupportedModels []SupportedModel

	Clusters    map[string]*Cluster
	Volumes     map[string]*Volume
	Endpoints   map[string]*Endpoint
	Deployments map[string]*Deployment

	// FailNext makes the next N requests whose "METHOD path" has the given
	// prefix fail with the given status, then succeed.
	failures []failure

	// Requests counts handled requests by route pattern ("METHOD /path/{id}").
	Requests map[string]int
}

type failure struct {
	prefix string
	status int
	left   int
}

// Project mirrors the Project schema.
type Project struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Slug             string `json:"slug"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
}

// DriverVersion mirrors ClusterDriverVersionInfo.
type DriverVersion struct {
	ID                  string `json:"id"`
	CUDAVersion         string `json:"cuda_version"`
	NvidiaDriverVersion string `json:"nvidia_driver_version"`
	OS                  string `json:"os"`
}

// Region mirrors one RegionListResponse entry.
type Region struct {
	Name                   string          `json:"name"`
	SupportedInstanceTypes []string        `json:"supported_instance_types"`
	DriverVersions         []DriverVersion `json:"driver_versions"`
}

// InstanceType mirrors DE.InferenceInstanceType.
type InstanceType struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Description       string           `json:"description"`
	GPUType           string           `json:"gpuType"`
	GPUCount          int64            `json:"gpuCount"`
	GPUMemoryGiB      int64            `json:"gpuMemoryGib"`
	PriceCentsPerHour int64            `json:"priceCentsPerHour"`
	Regions           []map[string]any `json:"regions"`
}

// SupportedModel mirrors DE.SupportedModel.
type SupportedModel struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	DisplayName        string           `json:"displayName"`
	Description        string           `json:"description"`
	InputModalities    []string         `json:"inputModalities"`
	OutputModalities   []string         `json:"outputModalities"`
	Products           []string         `json:"products"`
	Capabilities       []string         `json:"capabilities"`
	Publisher          string           `json:"publisher"`
	Status             string           `json:"status"`
	DisplayType        string           `json:"displayType"`
	BaseModelID        string           `json:"baseModelId"`
	BaseModel          string           `json:"baseModel"`
	ContextLength      string           `json:"contextLength"`
	DeploymentProfiles []map[string]any `json:"deploymentProfiles"`
	CreatedAt          string           `json:"createdAt"`
	UpdatedAt          string           `json:"updatedAt"`
}

// Cluster is the mock's record of a GPU cluster.
type Cluster struct {
	Info map[string]any
	// Fields the mock needs to reason about.
	AutoScale     bool
	InlineVolume  string
	stateIdx      int
	gets          int
	deleting      bool
	deleteGets    int
	scalingTarget int64
}

// Volume is the mock's record of a shared volume.
type Volume struct {
	ID, Name, Region, Status string
	SizeTiB                  int64
	ProjectID                string
	BoundTo                  string
	LifecycleIndependent     bool
	gets                     int
	deleteGets               int
}

// Endpoint is the mock's record of an endpoint.
type Endpoint struct {
	ID, ProjectID, Name, Visibility string
	CreatedAt, UpdatedAt            string
	TrafficSplit                    []TrafficSplitEntry
	Etag                            int
}

// TrafficSplitEntry mirrors DE.TrafficSplitEntry.
type TrafficSplitEntry struct {
	DeploymentID string  `json:"deploymentId"`
	Weight       float64 `json:"weight"`
}

// Deployment is the mock's record of a deployment.
type Deployment struct {
	ID, ProjectID, EndpointID, Name string
	Model, Config, Hardware         string
	CreatedAt, UpdatedAt            string
	Autoscaling                     map[string]any
	InactiveTimeout                 int64
	MaxConcurrent                   string
	Placement                       map[string]any
	State                           string
	Message                         string
	gets                            int
	Etag                            int
}

// New starts a mock server with a small catalog. Close it with t.Cleanup.
func New() *Server {
	s := &Server{
		StepsPerState: 1,
		DeleteSteps:   1,
		Clusters:      map[string]*Cluster{},
		Volumes:       map[string]*Volume{},
		Endpoints:     map[string]*Endpoint{},
		Deployments:   map[string]*Deployment{},
		Requests:      map[string]int{},
	}
	s.Projects = []Project{{ID: "proj_mock1", Name: "Mock project", Slug: "mock-project", OrganizationID: "org_mock", OrganizationName: "Mock org"}}
	s.Regions = []Region{
		{Name: "us-central-8", SupportedInstanceTypes: []string{"H100_SXM", "H200_SXM"}, DriverVersions: []DriverVersion{
			{ID: "nv_570_128", CUDAVersion: "12.8", NvidiaDriverVersion: "570", OS: "ubuntu-22.04"},
			{ID: "nv_550_124", CUDAVersion: "12.4", NvidiaDriverVersion: "550", OS: "ubuntu-22.04"},
		}},
		{Name: "us-west-2", SupportedInstanceTypes: []string{"B200_SXM"}, DriverVersions: []DriverVersion{
			{ID: "nv_570_128_w", CUDAVersion: "12.8", NvidiaDriverVersion: "570", OS: "ubuntu-24.04"},
		}},
	}
	s.InstanceTypes = []InstanceType{
		{ID: "1xnvidia-h100-80gb", Name: "1x H100 80GB", Description: "One NVIDIA H100 80GB", GPUType: "H100", GPUCount: 1, GPUMemoryGiB: 80, PriceCentsPerHour: 549,
			Regions: []map[string]any{{"name": "us-east-1", "headroom": map[string]any{"value": 4, "relation": "RELATION_GTE"}}}},
		{ID: "1xnvidia-b200-180gb", Name: "1x B200 180GB", Description: "One NVIDIA B200 180GB", GPUType: "B200", GPUCount: 1, GPUMemoryGiB: 180, PriceCentsPerHour: 899,
			Regions: []map[string]any{{"name": "us-west-2"}}},
	}
	now := "2026-11-01T00:00:00Z"
	s.SupportedModels = []SupportedModel{
		{ID: "ml_gemma", Name: "google/gemma-4-E4B-it", DisplayName: "Gemma 4 E4B IT", InputModalities: []string{"MODALITY_TEXT"}, OutputModalities: []string{"MODALITY_TEXT"},
			Products: []string{"PRODUCT_DEDICATED", "PRODUCT_SERVERLESS"}, Capabilities: []string{"CAPABILITY_CHAT"}, Publisher: "Google", Status: "SUPPORTED_MODEL_STATUS_RECOMMENDED",
			DisplayType: "chat", BaseModelID: "ml_gemma", BaseModel: "projects/proj_public/models/ml_gemma", ContextLength: "131072", CreatedAt: now, UpdatedAt: now,
			DeploymentProfiles: []map[string]any{{
				"profileId": "cr_gemma_bf16", "certifiedConfigRevisionId": "cr_gemma_bf16", "certifiedModelRevisionId": "rev_gemma_1",
				"gpuType": "H100", "gpuCount": 1, "quantization": "BF16", "performanceBenchmarks": map[string]any{},
				"config": "projects/proj_public/configs/cr_gemma_bf16", "model": "projects/proj_public/models/ml_gemma",
				"parallelism": "TP1", "modelName": "google/gemma-4-E4B-it", "adapterMode": "ADAPTER_MODE_DISABLED",
			}}},
		{ID: "ml_qwen", Name: "Qwen/Qwen3.5-9B-FP8", DisplayName: "Qwen 3.5 9B FP8", InputModalities: []string{"MODALITY_TEXT"}, OutputModalities: []string{"MODALITY_TEXT"},
			Products: []string{"PRODUCT_DEDICATED"}, Capabilities: []string{"CAPABILITY_CHAT"}, Publisher: "Qwen", Status: "SUPPORTED_MODEL_STATUS_SUPPORTED",
			DisplayType: "chat", BaseModelID: "ml_qwen", BaseModel: "projects/proj_public/models/ml_qwen", ContextLength: "32768", CreatedAt: now, UpdatedAt: now,
			DeploymentProfiles: []map[string]any{{
				"profileId": "cr_qwen_fp8", "certifiedConfigRevisionId": "cr_qwen_fp8", "certifiedModelRevisionId": "rev_qwen_1",
				"gpuType": "H100", "gpuCount": 1, "quantization": "FP8", "performanceBenchmarks": map[string]any{},
				"config": "projects/proj_public/configs/cr_qwen_fp8", "model": "projects/proj_public/models/ml_qwen",
				"parallelism": "TP1", "modelName": "Qwen/Qwen3.5-9B-FP8",
			}}},
		{ID: "ml_embed", Name: "BAAI/bge-large-en-v1.5", DisplayName: "BGE large", InputModalities: []string{"MODALITY_TEXT"}, OutputModalities: []string{"MODALITY_TEXT"},
			Products: []string{"PRODUCT_SERVERLESS"}, Capabilities: []string{"CAPABILITY_EMBEDDING"}, Publisher: "BAAI", Status: "SUPPORTED_MODEL_STATUS_SUPPORTED",
			DisplayType: "embedding", BaseModelID: "", BaseModel: "", CreatedAt: now, UpdatedAt: now, DeploymentProfiles: []map[string]any{}},
	}

	mux := http.NewServeMux()
	s.routes(mux)
	s.srv = httptest.NewServer(s.middleware(mux))
	return s
}

// URL is the base URL to point the client at.
func (s *Server) URL() string { return s.srv.URL }

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// FailNext makes the next n requests whose "METHOD /path" starts with prefix
// fail with status.
func (s *Server) FailNext(prefix string, status, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failure{prefix: prefix, status: status, left: n})
}

// Lock and Unlock let tests change the stored records safely.
func (s *Server) Lock()   { s.mu.Lock() }
func (s *Server) Unlock() { s.mu.Unlock() }

func (s *Server) nextID(prefix string) string {
	s.seq++
	return fmt.Sprintf("%s_%04d", prefix, s.seq)
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+APIKey {
			writeErr(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API key provided.")
			return
		}
		s.mu.Lock()
		key := r.Method + " " + r.URL.Path
		for i := range s.failures {
			f := &s.failures[i]
			if f.left > 0 && strings.HasPrefix(key, f.prefix) {
				f.left--
				s.mu.Unlock()
				if f.status == http.StatusTooManyRequests {
					w.Header().Set("Retry-After", "0")
				}
				writeErr(w, f.status, "server_error", "injected failure")
				return
			}
		}
		s.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) count(r *http.Request) {
	s.Requests[r.Pattern]++
}

// writeErr writes the ErrorData shape: {"error": {"message", "type", "code"}}.
func writeErr(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg, "type": typ, "param": nil, "code": nil}})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// decode reads a JSON body and rejects unknown fields, as the management API
// does (`unknown field "..."`, HTTP 400).
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, _ := io.ReadAll(r.Body)
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, "json: unknown field ") {
			msg = "unknown field " + strings.TrimPrefix(msg, "json: unknown field ")
		}
		writeErr(w, http.StatusBadRequest, "invalid_request_error", msg)
		return false
	}
	return true
}

func (s *Server) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/projects", s.listProjects)
	mux.HandleFunc("GET /v1/compute/regions", s.listRegions)

	mux.HandleFunc("POST /v1/compute/clusters", s.createCluster)
	mux.HandleFunc("GET /v1/compute/clusters", s.listClusters)
	mux.HandleFunc("GET /v1/compute/clusters/{cluster_id}", s.getCluster)
	mux.HandleFunc("PUT /v1/compute/clusters/{cluster_id}", s.updateCluster)
	mux.HandleFunc("DELETE /v1/compute/clusters/{cluster_id}", s.deleteCluster)

	mux.HandleFunc("POST /v1/compute/clusters/storage/volumes", s.createVolume)
	mux.HandleFunc("PUT /v1/compute/clusters/storage/volumes", s.updateVolume)
	mux.HandleFunc("GET /v1/compute/clusters/storage/volumes", s.listVolumes)
	mux.HandleFunc("GET /v1/compute/clusters/storage/volumes/{volume_id}", s.getVolume)
	mux.HandleFunc("DELETE /v1/compute/clusters/storage/volumes/{volume_id}", s.deleteVolume)

	mux.HandleFunc("POST /v2/projects/{projectId}/endpoints", s.createEndpoint)
	mux.HandleFunc("GET /v2/projects/{projectId}/endpoints/{id}", s.getEndpoint)
	mux.HandleFunc("PATCH /v2/projects/{projectId}/endpoints/{id}", s.updateEndpoint)
	mux.HandleFunc("DELETE /v2/projects/{projectId}/endpoints/{id}", s.deleteEndpoint)

	mux.HandleFunc("POST /v2/projects/{projectId}/endpoints/{endpointId}/deployments", s.createDeployment)
	mux.HandleFunc("GET /v2/projects/{projectId}/endpoints/{endpointId}/deployments/{id}", s.getDeployment)
	mux.HandleFunc("PATCH /v2/projects/{projectId}/endpoints/{endpointId}/deployments/{id}", s.updateDeployment)
	mux.HandleFunc("DELETE /v2/projects/{projectId}/endpoints/{endpointId}/deployments/{id}", s.deleteDeployment)

	mux.HandleFunc("GET /v2/public/inference-instance-types", s.listInstanceTypes)
	mux.HandleFunc("GET /v2/supported-models", s.listSupportedModels)
}

// ---------- projects, regions, catalog ----------

// pageOf returns one page and the next cursor. Page sizes are capped at
// maxLimit (small on purpose) so the tests exercise cursor following.
func pageOf[T any](items []T, r *http.Request, maxLimit int) ([]T, string) {
	limit := maxLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < maxLimit {
			limit = n
		}
	}
	start := 0
	if a := r.URL.Query().Get("after"); a != "" {
		if n, err := strconv.Atoi(strings.TrimPrefix(a, "cur_")); err == nil {
			start = n
		}
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	next := ""
	if end < len(items) {
		next = "cur_" + strconv.Itoa(end)
	}
	return items[start:end], next
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	page, next := pageOf(s.Projects, r, 10)
	// ListProjectsResponse: next_cursor is required and empty on the last page.
	writeJSON(w, map[string]any{"object": "list", "data": page, "next_cursor": next})
}

func (s *Server) listRegions(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	writeJSON(w, map[string]any{"regions": s.Regions})
}

func (s *Server) listInstanceTypes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	writeJSON(w, map[string]any{"object": "list", "data": s.InstanceTypes, "next_cursor": nil})
}

var validModality = []string{"MODALITY_TEXT", "MODALITY_IMAGE", "MODALITY_AUDIO", "MODALITY_VIDEO"}
var validProduct = []string{"PRODUCT_SERVERLESS", "PRODUCT_DEDICATED", "PRODUCT_FINE_TUNING"}

func (s *Server) listSupportedModels(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	q := r.URL.Query()
	if m := q.Get("modality"); m != "" && !slices.Contains(validModality, m) {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "invalid modality "+m)
		return
	}
	if p := q.Get("product"); p != "" && !slices.Contains(validProduct, p) {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "invalid product "+p)
		return
	}
	var out []SupportedModel
	for _, m := range s.SupportedModels {
		if p := q.Get("product"); p != "" && !slices.Contains(m.Products, p) {
			continue
		}
		if mo := q.Get("modality"); mo != "" && !slices.Contains(m.InputModalities, mo) {
			continue
		}
		if se := strings.ToLower(q.Get("search")); se != "" &&
			!strings.Contains(strings.ToLower(m.ID+" "+m.Name+" "+m.Description), se) {
			continue
		}
		out = append(out, m)
	}
	page, next := pageOf(out, r, 2)
	var cursor any
	if next != "" {
		cursor = next
	}
	writeJSON(w, map[string]any{"object": "list", "data": page, "next_cursor": cursor})
}

// ---------- GPU clusters ----------

var clusterStates = []string{"WaitingForControlPlaneNodes", "WaitingForDataPlaneNodes", "InstallingDrivers", "Ready"}
var gpuTypes = []string{"H100_SXM", "H200_SXM", "RTX_6000_PCI", "L40_PCIE", "B200_SXM", "H100_SXM_INF", "B300_SXM", "GB200_NVL"}
var billingTypes = []string{"RESERVED", "ON_DEMAND", "SCHEDULED_CAPACITY"}
var volumeNameRe = regexp.MustCompile(`^[a-z0-9.-]{1,44}$`)

type clusterCreate struct {
	ClusterType          string         `json:"cluster_type"`
	Region               string         `json:"region"`
	GPUType              string         `json:"gpu_type"`
	NumGPUs              *int64         `json:"num_gpus"`
	ClusterName          string         `json:"cluster_name"`
	DurationDays         *int64         `json:"duration_days"`
	SharedVolume         *volumeCreate  `json:"shared_volume"`
	VolumeID             string         `json:"volume_id"`
	BillingType          string         `json:"billing_type"`
	AutoScaleMaxGPUs     *int64         `json:"auto_scale_max_gpus"`
	SlurmShmSizeGiB      *int64         `json:"slurm_shm_size_gib"`
	CapacityPoolID       string         `json:"capacity_pool_id"`
	ReservationStartTime string         `json:"reservation_start_time"`
	ReservationEndTime   string         `json:"reservation_end_time"`
	InstallTraefik       *bool          `json:"install_traefik"`
	CUDAVersion          string         `json:"cuda_version"`
	NvidiaDriverVersion  string         `json:"nvidia_driver_version"`
	NvidiaVersionID      string         `json:"nvidia_version_id"`
	SlurmImage           string         `json:"slurm_image"`
	OIDCConfig           map[string]any `json:"oidc_config"`
	ProjectID            string         `json:"project_id"`
	NumCapacityPoolGPUs  *int64         `json:"num_capacity_pool_gpus"`
	AutoScale            *bool          `json:"auto_scale"`
	NumPreemptibleGPUs   *int64         `json:"num_preemptible_gpus"`
	NumReservedGPUs      *int64         `json:"num_reserved_gpus"`
}

func (s *Server) region(name string) *Region {
	for i := range s.Regions {
		if s.Regions[i].Name == name {
			return &s.Regions[i]
		}
	}
	return nil
}

func (s *Server) createCluster(w http.ResponseWriter, r *http.Request) {
	var req clusterCreate
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, "invalid_request_error", msg) }
	switch {
	case req.Region == "", req.GPUType == "", req.NumGPUs == nil, req.ClusterName == "", req.BillingType == "":
		bad("region, gpu_type, num_gpus, cluster_name and billing_type are required")
		return
	case !slices.Contains(gpuTypes, req.GPUType):
		bad("invalid gpu_type " + req.GPUType)
		return
	case !slices.Contains(billingTypes, req.BillingType):
		bad("invalid billing_type " + req.BillingType)
		return
	case req.ClusterType != "" && req.ClusterType != "KUBERNETES" && req.ClusterType != "SLURM":
		bad("invalid cluster_type " + req.ClusterType)
		return
	case *req.NumGPUs < 8 || *req.NumGPUs%8 != 0:
		bad("num_gpus must be a multiple of 8")
		return
	case req.VolumeID == "" && req.SharedVolume == nil:
		bad("a shared volume is required: set volume_id or shared_volume")
		return
	case req.VolumeID != "" && req.SharedVolume != nil:
		bad("set only one of volume_id and shared_volume")
		return
	case req.AutoScale != nil && *req.AutoScale && (req.AutoScaleMaxGPUs == nil || *req.AutoScaleMaxGPUs < *req.NumGPUs || *req.AutoScaleMaxGPUs%8 != 0):
		bad("auto_scale_max_gpus must be a multiple of 8 and at least num_gpus")
		return
	case req.ClusterType == "SLURM" && req.SlurmShmSizeGiB == nil:
		bad("slurm_shm_size_gib is required for SLURM clusters")
		return
	case req.BillingType == "SCHEDULED_CAPACITY" && (req.ReservationEndTime == ""):
		bad("reservation_end_time is required for SCHEDULED_CAPACITY billing")
		return
	}
	reg := s.region(req.Region)
	if reg == nil {
		bad("unknown region " + req.Region)
		return
	}
	if !slices.Contains(reg.SupportedInstanceTypes, req.GPUType) {
		bad(fmt.Sprintf("gpu_type %s is not available in region %s", req.GPUType, req.Region))
		return
	}
	var drv *DriverVersion
	switch {
	case req.NvidiaVersionID == "" && (req.CUDAVersion == "" || req.NvidiaDriverVersion == ""):
		bad("set nvidia_version_id, or cuda_version together with nvidia_driver_version")
		return
	case req.NvidiaVersionID != "" && (req.CUDAVersion != "") != (req.NvidiaDriverVersion != ""):
		bad("cuda_version and nvidia_driver_version must be set together")
		return
	}
	for i := range reg.DriverVersions {
		d := &reg.DriverVersions[i]
		if req.NvidiaVersionID != "" && d.ID != req.NvidiaVersionID {
			continue
		}
		if req.CUDAVersion != "" && (d.CUDAVersion != req.CUDAVersion || d.NvidiaDriverVersion != req.NvidiaDriverVersion) {
			continue
		}
		drv = d
		break
	}
	if drv == nil {
		bad("the NVIDIA version is not available in region " + req.Region)
		return
	}
	project := req.ProjectID
	if project == "" {
		project = s.Projects[0].ID
	}

	id := s.nextID("cl")
	var volumes []map[string]any
	c := &Cluster{AutoScale: req.AutoScale != nil && *req.AutoScale}
	if req.VolumeID != "" {
		v := s.Volumes[req.VolumeID]
		if v == nil {
			writeErr(w, http.StatusNotFound, "not_found", "volume "+req.VolumeID+" not found")
			return
		}
		if v.Region != req.Region {
			bad(fmt.Sprintf("Shared volume you selected does not exist in the datacenter in region %s providing %s", req.Region, req.GPUType))
			return
		}
		if v.BoundTo != "" {
			bad("volume " + v.ID + " is already attached to another cluster")
			return
		}
		v.BoundTo = id
		v.Status = "bound"
		volumes = append(volumes, volJSON(v))
	} else {
		sv := req.SharedVolume
		if !volumeNameRe.MatchString(sv.VolumeName) || sv.SizeTiB < 1 || sv.Region == "" {
			bad("invalid shared_volume: volume_name must be 1-44 lowercase letters, digits, hyphens or dots, size_tib at least 1, region required")
			return
		}
		if sv.Region != req.Region {
			bad("shared_volume.region must match the cluster region")
			return
		}
		v := &Volume{ID: s.nextID("vol"), Name: sv.VolumeName, Region: sv.Region, SizeTiB: sv.SizeTiB, Status: "bound", BoundTo: id, ProjectID: project,
			LifecycleIndependent: sv.IsLifecycleIndependent != nil && *sv.IsLifecycleIndependent}
		s.Volumes[v.ID] = v
		c.InlineVolume = v.ID
		volumes = append(volumes, volJSON(v))
	}

	clusterType := req.ClusterType
	if clusterType == "" {
		clusterType = "KUBERNETES"
	}
	reserved := int64(0)
	if req.BillingType == "RESERVED" {
		reserved = *req.NumGPUs
	}
	if req.NumReservedGPUs != nil {
		reserved = *req.NumReservedGPUs
	}
	info := map[string]any{
		"cluster_id":                 id,
		"cluster_type":               clusterType,
		"region":                     req.Region,
		"gpu_type":                   req.GPUType,
		"cluster_name":               req.ClusterName,
		"cuda_version":               drv.CUDAVersion,
		"nvidia_driver_version":      drv.NvidiaDriverVersion,
		"nvidia_driver_version_id":   drv.ID,
		"volumes":                    volumes,
		"status":                     clusterStates[0],
		"control_plane_nodes":        []any{},
		"gpu_worker_nodes":           []any{},
		"kube_config":                "",
		"num_gpus":                   *req.NumGPUs,
		"project_id":                 project,
		"num_cpu_workers":            0,
		"phase_transitions":          []any{},
		"desired_preemptible_gpus":   deref(req.NumPreemptibleGPUs),
		"allocated_preemptible_gpus": 0,
		"num_capacity_pool_gpus":     deref(req.NumCapacityPoolGPUs),
		"num_reserved_gpus":          reserved,
		"billing_type":               req.BillingType,
		"add_ons":                    []any{},
		"install_traefik":            req.InstallTraefik != nil && *req.InstallTraefik,
		"created_at":                 now(),
	}
	if req.DurationDays != nil {
		info["duration_hours"] = *req.DurationDays * 24
	}
	if req.SlurmShmSizeGiB != nil {
		info["slurm_shm_size_gib"] = *req.SlurmShmSizeGiB
	}
	if req.CapacityPoolID != "" {
		info["capacity_pool_id"] = req.CapacityPoolID
	}
	if req.ReservationStartTime != "" {
		info["reservation_start_time"] = normTime(req.ReservationStartTime)
	}
	if req.ReservationEndTime != "" {
		info["reservation_end_time"] = normTime(req.ReservationEndTime)
	}
	if req.OIDCConfig != nil {
		info["oidc_config"] = req.OIDCConfig
	}
	c.Info = info
	c.scalingTarget = *req.NumGPUs
	s.Clusters[id] = c
	writeJSON(w, info)
}

// normTime returns the timestamp the way a Go/proto API echoes it: UTC,
// second precision, Z suffix. Tests use it to check the provider does not
// report a format change as drift.
func normTime(v string) string {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return v
	}
	return t.UTC().Format(time.RFC3339)
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func volJSON(v *Volume) map[string]any {
	return map[string]any{"volume_id": v.ID, "volume_name": v.Name, "size_tib": v.SizeTiB, "status": v.Status}
}

func (s *Server) advanceCluster(c *Cluster) {
	c.gets++
	if c.gets < s.StepsPerState {
		return
	}
	c.gets = 0
	if c.stateIdx < len(clusterStates)-1 {
		c.stateIdx++
	}
	c.Info["status"] = clusterStates[c.stateIdx]
	if clusterStates[c.stateIdx] == "Ready" {
		id := c.Info["cluster_id"].(string)
		c.Info["control_plane_ready"] = true
		c.Info["kube_config"] = "apiVersion: v1\nkind: Config\nclusters:\n- name: " + id + "\n  cluster:\n    server: https://" + id + ".k8s.mock.together.ai:6443\n    certificate-authority-data: TU9DSw==\ncontexts:\n- name: " + id + "\n  context:\n    cluster: " + id + "\n    user: admin\ncurrent-context: " + id + "\nusers:\n- name: admin\n  user:\n    token: mock-cluster-token\n"
		c.Info["control_plane_nodes"] = []any{map[string]any{"node_id": "cp-1", "status": "Ready", "host_name": id + "-cp-1", "num_cpu_cores": 8, "memory_gib": 32, "network": "10.0.0.0/24", "phase_transitions": []any{}, "public_ipv4": "203.0.113.10"}}
		var workers []any
		for i := int64(0); i < c.scalingTarget/8; i++ {
			workers = append(workers, map[string]any{"node_id": fmt.Sprintf("gpu-%d", i+1), "status": "Ready", "host_name": fmt.Sprintf("%s-gpu-%d", id, i+1), "num_cpu_cores": 192, "num_gpus": 8, "memory_gib": 2048, "networks": []string{"ib0"}, "phase_transitions": []any{}, "instance_id": fmt.Sprintf("inst-%d", i+1)})
		}
		c.Info["gpu_worker_nodes"] = workers
		if c.Info["first_ready_at"] == nil {
			c.Info["first_ready_at"] = now()
		}
	}
}

func (s *Server) getCluster(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	c := s.Clusters[r.PathValue("cluster_id")]
	if c == nil {
		writeErr(w, http.StatusNotFound, "not_found", "cluster not found")
		return
	}
	if c.deleting {
		c.deleteGets++
		if c.deleteGets > s.DeleteSteps {
			s.finishClusterDelete(c)
			writeErr(w, http.StatusNotFound, "not_found", "cluster not found")
			return
		}
	} else {
		s.advanceCluster(c)
	}
	writeJSON(w, c.Info)
}

func (s *Server) listClusters(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	ids := make([]string, 0, len(s.Clusters))
	for id := range s.Clusters {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []any
	for _, id := range ids {
		out = append(out, s.Clusters[id].Info)
	}
	writeJSON(w, map[string]any{"clusters": out})
}

func (s *Server) updateCluster(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterType         string `json:"cluster_type"`
		NumGPUs             *int64 `json:"num_gpus"`
		ReservationEndTime  string `json:"reservation_end_time"`
		NumReservedGPUs     *int64 `json:"num_reserved_gpus"`
		NumPreemptibleGPUs  *int64 `json:"num_preemptible_gpus"`
		NumCapacityPoolGPUs *int64 `json:"num_capacity_pool_gpus"`
	}
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	c := s.Clusters[r.PathValue("cluster_id")]
	if c == nil || c.deleting {
		writeErr(w, http.StatusNotFound, "not_found", "cluster not found")
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, "invalid_request_error", msg) }
	if c.AutoScale {
		writeErr(w, http.StatusForbidden, "permission_denied", "cluster update is not allowed when auto scale is enabled")
		return
	}
	reserved := c.Info["num_reserved_gpus"].(int64)
	if req.NumReservedGPUs != nil {
		if *req.NumReservedGPUs < reserved {
			bad("num_reserved_gpus cannot be decreased")
			return
		}
		reserved = *req.NumReservedGPUs
	}
	if req.NumGPUs != nil {
		if *req.NumGPUs < 8 || *req.NumGPUs%8 != 0 {
			bad("num_gpus must be a multiple of 8")
			return
		}
		if *req.NumGPUs < reserved {
			bad("num_gpus cannot be lower than the reserved GPU count")
			return
		}
	}
	if req.NumPreemptibleGPUs != nil && *req.NumPreemptibleGPUs%8 != 0 {
		bad("num_preemptible_gpus must be a multiple of 8")
		return
	}
	if req.ReservationEndTime != "" && c.Info["billing_type"] == "ON_DEMAND" {
		bad("reservation_end_time is only accepted for prepaid clusters")
		return
	}
	if req.ClusterType != "" {
		if req.ClusterType != "KUBERNETES" && req.ClusterType != "SLURM" {
			bad("invalid cluster_type")
			return
		}
		c.Info["cluster_type"] = req.ClusterType
	}
	c.Info["num_reserved_gpus"] = reserved
	if req.NumPreemptibleGPUs != nil {
		c.Info["desired_preemptible_gpus"] = *req.NumPreemptibleGPUs
	}
	if req.NumCapacityPoolGPUs != nil {
		c.Info["num_capacity_pool_gpus"] = *req.NumCapacityPoolGPUs
	}
	if req.ReservationEndTime != "" {
		c.Info["reservation_end_time"] = normTime(req.ReservationEndTime)
	}
	if req.NumGPUs != nil && *req.NumGPUs != c.Info["num_gpus"].(int64) {
		c.Info["num_gpus"] = *req.NumGPUs
		c.scalingTarget = *req.NumGPUs
		// Scaling: the cluster goes back through the data-plane states.
		c.stateIdx = 1
		c.gets = 0
		c.Info["status"] = clusterStates[1]
	}
	writeJSON(w, c.Info)
}

func (s *Server) deleteCluster(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	id := r.PathValue("cluster_id")
	c := s.Clusters[id]
	if c == nil {
		writeErr(w, http.StatusNotFound, "not_found", "cluster not found")
		return
	}
	c.deleting = true
	c.Info["status"] = "Deleting"
	writeJSON(w, map[string]any{"cluster_id": id})
}

func (s *Server) finishClusterDelete(c *Cluster) {
	id := c.Info["cluster_id"].(string)
	delete(s.Clusters, id)
	for vid, v := range s.Volumes {
		if v.BoundTo != id {
			continue
		}
		v.BoundTo = ""
		if vid == c.InlineVolume && !v.LifecycleIndependent {
			delete(s.Volumes, vid)
			continue
		}
		v.Status = "available"
	}
}

// DeleteClusterNow removes a cluster as if it was deleted in the console.
func (s *Server) DeleteClusterNow(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.Clusters[id]; c != nil {
		s.finishClusterDelete(c)
	}
}

// ---------- shared volumes ----------

type volumeCreate struct {
	VolumeName             string `json:"volume_name"`
	SizeTiB                int64  `json:"size_tib"`
	Region                 string `json:"region"`
	IsLifecycleIndependent *bool  `json:"is_lifecycle_independent"`
	ProjectID              string `json:"project_id"`
	InstanceClusterID      string `json:"instance_cluster_id"`
}

func (s *Server) createVolume(w http.ResponseWriter, r *http.Request) {
	var req volumeCreate
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	if req.VolumeName == "" || req.Region == "" || req.SizeTiB == 0 {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "volume_name, size_tib and region are required")
		return
	}
	if !volumeNameRe.MatchString(req.VolumeName) {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "volume_name may contain only lowercase letters, digits, hyphens and dots, up to 44 characters")
		return
	}
	if req.SizeTiB < 1 {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "size_tib must be at least 1")
		return
	}
	if s.region(req.Region) == nil {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "unknown region "+req.Region)
		return
	}
	if req.InstanceClusterID != "" && s.Clusters[req.InstanceClusterID] == nil {
		writeErr(w, http.StatusNotFound, "not_found", "cluster "+req.InstanceClusterID+" not found")
		return
	}
	project := req.ProjectID
	if project == "" {
		project = s.Projects[0].ID
	}
	v := &Volume{ID: s.nextID("vol"), Name: req.VolumeName, Region: req.Region, SizeTiB: req.SizeTiB, Status: "provisioning", ProjectID: project, LifecycleIndependent: true}
	s.Volumes[v.ID] = v
	writeJSON(w, volJSON(v))
}

func (s *Server) getVolume(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	id := r.PathValue("volume_id")
	v := s.Volumes[id]
	if v == nil {
		writeErr(w, http.StatusNotFound, "not_found", "volume not found")
		return
	}
	switch v.Status {
	case "provisioning", "scheduled":
		v.gets++
		if v.gets >= s.StepsPerState {
			v.gets = 0
			v.Status = "available"
			if v.BoundTo != "" {
				v.Status = "bound"
			}
		}
	case "deleting":
		v.deleteGets++
		if v.deleteGets > s.DeleteSteps {
			delete(s.Volumes, id)
			writeErr(w, http.StatusNotFound, "not_found", "volume not found")
			return
		}
	}
	writeJSON(w, volJSON(v))
}

func (s *Server) listVolumes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	ids := make([]string, 0, len(s.Volumes))
	for id := range s.Volumes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []any
	for _, id := range ids {
		out = append(out, volJSON(s.Volumes[id]))
	}
	writeJSON(w, map[string]any{"volumes": out})
}

func (s *Server) updateVolume(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VolumeID string `json:"volume_id"`
		SizeTiB  *int64 `json:"size_tib"`
	}
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	if req.VolumeID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "volume_id is required")
		return
	}
	v := s.Volumes[req.VolumeID]
	if v == nil || v.Status == "deleting" {
		writeErr(w, http.StatusNotFound, "not_found", "volume not found")
		return
	}
	if req.SizeTiB != nil {
		if *req.SizeTiB < v.SizeTiB {
			writeErr(w, http.StatusBadRequest, "invalid_request_error", "volume size cannot be decreased; contact your account team")
			return
		}
		if *req.SizeTiB != v.SizeTiB {
			v.SizeTiB = *req.SizeTiB
			v.Status = "provisioning"
			v.gets = 0
		}
	}
	writeJSON(w, volJSON(v))
}

func (s *Server) deleteVolume(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	v := s.Volumes[r.PathValue("volume_id")]
	if v == nil {
		writeErr(w, http.StatusNotFound, "not_found", "volume not found")
		return
	}
	if v.BoundTo != "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "volume is attached to cluster "+v.BoundTo+" and cannot be deleted")
		return
	}
	v.Status = "deleting"
	writeJSON(w, map[string]any{"success": true})
}

// DeleteVolumeNow removes a volume as if it was deleted in the console.
func (s *Server) DeleteVolumeNow(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Volumes, id)
}

// ---------- endpoints ----------

func (s *Server) project(id string) *Project {
	for i := range s.Projects {
		if s.Projects[i].ID == id {
			return &s.Projects[i]
		}
	}
	return nil
}

var visibilities = []string{"VISIBILITY_PRIVATE", "VISIBILITY_INTERNAL"}

// bareName strips a "<project_slug>/" prefix. A supplied slug must match.
func bareName(p *Project, name string) (string, bool) {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		if name[:i] != p.Slug {
			return "", false
		}
		return name[i+1:], true
	}
	return name, true
}

func (s *Server) endpointJSON(e *Endpoint) map[string]any {
	p := s.project(e.ProjectID)
	var deps []any
	for _, d := range s.Deployments {
		if d.EndpointID == e.ID {
			deps = append(deps, map[string]any{
				"id": d.ID, "name": p.Slug + "/" + e.Name + "/" + d.Name, "state": d.State, "model": d.Model, "modelId": "",
				"trafficMode": "TRAFFIC_MODE_LIVE", "createdAt": d.CreatedAt, "autoscaling": d.Autoscaling, "estimatedEffectiveTrafficShare": 0,
			})
		}
	}
	if deps == nil {
		deps = []any{}
	}
	split := e.TrafficSplit
	if split == nil {
		split = []TrafficSplitEntry{}
	}
	return map[string]any{
		"id": e.ID, "projectId": e.ProjectID, "name": p.Slug + "/" + e.Name,
		"createdAt": e.CreatedAt, "updatedAt": e.UpdatedAt, "etag": strconv.Itoa(e.Etag),
		"deployments": deps, "trafficSplit": split, "visibility": e.Visibility, "endpointType": "ENDPOINT_TYPE_DEDICATED",
	}
}

func (s *Server) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		Visibility string `json:"visibility"`
	}
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	p := s.project(r.PathValue("projectId"))
	if p == nil {
		writeErr(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	name, ok := bareName(p, req.Name)
	if !ok || name == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "name is required and a supplied project slug must match the project")
		return
	}
	if req.Visibility == "" {
		req.Visibility = "VISIBILITY_PRIVATE"
	}
	if !slices.Contains(visibilities, req.Visibility) {
		writeErr(w, http.StatusBadRequest, "invalid_request_error", "invalid visibility "+req.Visibility)
		return
	}
	for _, e := range s.Endpoints {
		if e.ProjectID == p.ID && e.Name == name {
			writeErr(w, http.StatusConflict, "already_exists", "an endpoint named "+name+" already exists")
			return
		}
	}
	e := &Endpoint{ID: s.nextID("ep"), ProjectID: p.ID, Name: name, Visibility: req.Visibility, CreatedAt: now(), UpdatedAt: now(), Etag: 1}
	s.Endpoints[e.ID] = e
	writeJSON(w, s.endpointJSON(e))
}

func (s *Server) lookupEndpoint(w http.ResponseWriter, r *http.Request, idKey string) *Endpoint {
	e := s.Endpoints[r.PathValue(idKey)]
	if e == nil || e.ProjectID != r.PathValue("projectId") {
		writeErr(w, http.StatusNotFound, "not_found", "endpoint not found")
		return nil
	}
	return e
}

func (s *Server) getEndpoint(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	if e := s.lookupEndpoint(w, r, "id"); e != nil {
		writeJSON(w, s.endpointJSON(e))
	}
}

func (s *Server) updateEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         *string              `json:"name"`
		Etag         string               `json:"etag"`
		Visibility   *string              `json:"visibility"`
		TrafficSplit *[]TrafficSplitEntry `json:"trafficSplit"`
	}
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	e := s.lookupEndpoint(w, r, "id")
	if e == nil {
		return
	}
	if req.Etag != "" && req.Etag != strconv.Itoa(e.Etag) {
		writeErr(w, http.StatusConflict, "aborted", "etag mismatch")
		return
	}
	mask := maskOf(r, map[string]bool{"name": req.Name != nil, "visibility": req.Visibility != nil, "trafficSplit": req.TrafficSplit != nil})
	for f := range mask {
		if f != "name" && f != "visibility" && f != "trafficSplit" {
			writeErr(w, http.StatusBadRequest, "invalid_request_error", "invalid update mask path "+f)
			return
		}
	}
	p := s.project(e.ProjectID)
	if mask["name"] {
		n, ok := bareName(p, deref2(req.Name))
		if !ok || n == "" {
			writeErr(w, http.StatusBadRequest, "invalid_request_error", "invalid name")
			return
		}
		e.Name = n
	}
	if mask["visibility"] {
		if !slices.Contains(visibilities, deref2(req.Visibility)) {
			writeErr(w, http.StatusBadRequest, "invalid_request_error", "invalid visibility")
			return
		}
		e.Visibility = deref2(req.Visibility)
	}
	if mask["trafficSplit"] {
		var split []TrafficSplitEntry
		if req.TrafficSplit != nil {
			split = *req.TrafficSplit
		}
		for _, t := range split {
			d := s.Deployments[t.DeploymentID]
			if d == nil || d.EndpointID != e.ID {
				writeErr(w, http.StatusBadRequest, "invalid_request_error", "deployment "+t.DeploymentID+" is not under this endpoint")
				return
			}
			if t.Weight < 0 {
				writeErr(w, http.StatusBadRequest, "invalid_request_error", "weight must be non-negative")
				return
			}
		}
		e.TrafficSplit = split
	}
	e.Etag++
	e.UpdatedAt = now()
	writeJSON(w, s.endpointJSON(e))
}

func deref2(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// maskOf returns the update mask paths: the updateMask query parameter, or
// the populated fields when it is not set.
func maskOf(r *http.Request, populated map[string]bool) map[string]bool {
	out := map[string]bool{}
	if m := r.URL.Query().Get("updateMask"); m != "" {
		for _, p := range strings.Split(m, ",") {
			out[strings.TrimSpace(p)] = true
		}
		return out
	}
	for k, v := range populated {
		if v {
			out[k] = true
		}
	}
	return out
}

func (s *Server) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	e := s.lookupEndpoint(w, r, "id")
	if e == nil {
		return
	}
	for _, d := range s.Deployments {
		if d.EndpointID == e.ID {
			writeErr(w, http.StatusBadRequest, "failed_precondition", "the endpoint has deployments; delete them first")
			return
		}
	}
	delete(s.Endpoints, e.ID)
	writeJSON(w, map[string]any{})
}

// DeleteEndpointNow removes an endpoint and its deployments out of band.
func (s *Server) DeleteEndpointNow(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for did, d := range s.Deployments {
		if d.EndpointID == id {
			delete(s.Deployments, did)
		}
	}
	delete(s.Endpoints, id)
}

// ---------- deployments ----------

var durationRe = regexp.MustCompile(`^-?(?:0|[1-9][0-9]{0,11})(?:\.[0-9]{1,9})?s$`)
var modelRe = regexp.MustCompile(`^projects/[^/]+/models/[^/]+(/revisions/[^/]+)?$`)
var configRe = regexp.MustCompile(`^projects/[^/]+/configs/[^/]+$`)
var metricNames = []string{"active_sessions", "cache_hit_rate", "decoding_speed", "e2e_latency", "gpu_utilization", "inflight_requests", "throughput_per_replica", "token_utilization", "ttft"}
var metricTypes = []string{"METRIC_TARGET_TYPE_VALUE", "METRIC_TARGET_TYPE_UTILIZATION", "METRIC_TARGET_TYPE_AVERAGE_VALUE"}

type autoscalingReq struct {
	MinReplicas       *int64           `json:"minReplicas"`
	MaxReplicas       *int64           `json:"maxReplicas"`
	ScaleDownWindow   *string          `json:"scaleDownWindow"`
	ScaleUpWindow     *string          `json:"scaleUpWindow"`
	ScaleToZeroWindow *string          `json:"scaleToZeroWindow"`
	ScalingMetrics    []map[string]any `json:"scalingMetrics"`
	ScaleUp           map[string]any   `json:"scaleUp"`
	ScaleDown         map[string]any   `json:"scaleDown"`
}

func validateAutoscaling(a *autoscalingReq) string {
	if a.MinReplicas != nil && *a.MinReplicas < 0 {
		return "autoscaling.minReplicas must be >= 0"
	}
	if a.MaxReplicas != nil && *a.MaxReplicas < 0 {
		return "autoscaling.maxReplicas must be >= 0"
	}
	for _, w := range []*string{a.ScaleDownWindow, a.ScaleUpWindow, a.ScaleToZeroWindow} {
		if w != nil && !durationRe.MatchString(*w) {
			return "invalid duration " + *w
		}
	}
	for _, m := range a.ScalingMetrics {
		name, _ := m["name"].(string)
		typ, _ := m["type"].(string)
		if !slices.Contains(metricNames, name) || !slices.Contains(metricTypes, typ) {
			return "invalid scaling metric"
		}
		if _, ok := m["target"].(float64); !ok {
			return "scaling metric target is required"
		}
	}
	return ""
}

func (s *Server) deploymentJSON(d *Deployment) map[string]any {
	p := s.project(d.ProjectID)
	e := s.Endpoints[d.EndpointID]
	ready := int64(0)
	if d.State == "DEPLOYMENT_STATE_READY" {
		ready = asInt(d.Autoscaling["minReplicas"])
	}
	out := map[string]any{
		"id": d.ID, "projectId": d.ProjectID, "endpointId": d.EndpointID,
		"name":      p.Slug + "/" + e.Name + "/" + d.Name,
		"createdAt": d.CreatedAt, "updatedAt": d.UpdatedAt,
		"modelId": "", "modelRevisionId": "", "model": d.Model,
		"autoscaling": d.Autoscaling, "configId": "", "config": d.Config,
		"etag": strconv.Itoa(d.Etag), "hardware": d.Hardware, "trafficMode": "TRAFFIC_MODE_LIVE",
		"maxConcurrentRequestsPerReplica": d.MaxConcurrent,
		"desiredReplicas":                 asInt(d.Autoscaling["minReplicas"]),
		"status":                          map[string]any{"state": d.State, "message": d.Message, "readyReplicas": ready, "scheduledReplicas": ready},
		"runtimeInfo":                     map[string]any{"engineType": "vllm"},
	}
	if d.InactiveTimeout != 0 {
		out["inactiveTimeout"] = d.InactiveTimeout
	}
	if d.Placement != nil {
		out["placement"] = d.Placement
	}
	return out
}

func asInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}

func (s *Server) hardwareForConfig(config string) (string, bool) {
	for _, m := range s.SupportedModels {
		for _, p := range m.DeploymentProfiles {
			if p["config"] == config {
				return fmt.Sprintf("%vx%v", p["gpuCount"], p["gpuType"]), true
			}
		}
	}
	return "", false
}

func (s *Server) defaultConfigFor(model string) string {
	base := model
	if i := strings.Index(base, "/revisions/"); i >= 0 {
		base = base[:i]
	}
	for _, m := range s.SupportedModels {
		for _, p := range m.DeploymentProfiles {
			if p["model"] == base {
				return p["config"].(string)
			}
		}
	}
	return ""
}

func (s *Server) createDeployment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                            string          `json:"name"`
		Model                           string          `json:"model"`
		Autoscaling                     *autoscalingReq `json:"autoscaling"`
		Config                          string          `json:"config"`
		InactiveTimeout                 *int64          `json:"inactiveTimeout"`
		MaxConcurrentRequestsPerReplica string          `json:"maxConcurrentRequestsPerReplica"`
		Placement                       map[string]any  `json:"placement"`
	}
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	e := s.lookupEndpoint(w, r, "endpointId")
	if e == nil {
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, "invalid_request_error", msg) }
	if req.Name == "" || req.Autoscaling == nil {
		bad("name and autoscaling are required")
		return
	}
	if msg := validateAutoscaling(req.Autoscaling); msg != "" {
		bad(msg)
		return
	}
	if !modelRe.MatchString(req.Model) {
		bad("model must be projects/{projectId}/models/{modelId}[/revisions/{revisionId}]")
		return
	}
	if req.Config == "" {
		req.Config = s.defaultConfigFor(req.Model)
	}
	if !configRe.MatchString(req.Config) {
		bad("config must be projects/{projectId}/configs/{configRevisionId}")
		return
	}
	hw, ok := s.hardwareForConfig(req.Config)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "config "+req.Config+" not found")
		return
	}
	if req.InactiveTimeout != nil && *req.InactiveTimeout != 0 && (*req.InactiveTimeout < 30 || *req.InactiveTimeout > 1440) {
		bad("inactiveTimeout must be 0 or between 30 and 1440")
		return
	}
	if req.MaxConcurrentRequestsPerReplica != "" {
		if _, err := strconv.ParseInt(req.MaxConcurrentRequestsPerReplica, 10, 64); err != nil {
			bad("maxConcurrentRequestsPerReplica must be an integer")
			return
		}
	}
	if req.Placement != nil {
		_, hasInline := req.Placement["inline"]
		_, hasProfile := req.Placement["profile"]
		if hasInline == hasProfile {
			bad("placement must set exactly one of inline or profile")
			return
		}
	}
	for _, d := range s.Deployments {
		if d.EndpointID == e.ID && d.Name == req.Name {
			writeErr(w, http.StatusConflict, "already_exists", "a deployment named "+req.Name+" already exists on this endpoint")
			return
		}
	}
	model := req.Model
	if !strings.Contains(model, "/revisions/") {
		model += "/revisions/rev_1"
	}
	minR := deref(req.Autoscaling.MinReplicas)
	maxR := minR
	if req.Autoscaling.MaxReplicas != nil {
		maxR = *req.Autoscaling.MaxReplicas
	}
	if maxR < minR {
		bad("autoscaling.maxReplicas must be >= minReplicas")
		return
	}
	as := map[string]any{"minReplicas": minR, "maxReplicas": maxR, "scaleDownWindow": "300s", "scaleUpWindow": "0s", "scaleToZeroWindow": "0s",
		"scalingMetrics": []any{map[string]any{"name": "inflight_requests", "type": "METRIC_TARGET_TYPE_AVERAGE_VALUE", "target": 8.0}}}
	applyWindows(as, req.Autoscaling)
	mc := req.MaxConcurrentRequestsPerReplica
	if mc == "" {
		mc = "7"
	}
	d := &Deployment{
		ID: s.nextID("dep"), ProjectID: e.ProjectID, EndpointID: e.ID, Name: req.Name,
		Model: model, Config: req.Config, Hardware: hw, CreatedAt: now(), UpdatedAt: now(),
		Autoscaling: as, InactiveTimeout: deref(req.InactiveTimeout), MaxConcurrent: mc, Placement: req.Placement, Etag: 1,
	}
	if maxR == 0 {
		d.State, d.Message = "DEPLOYMENT_STATE_STOPPED", "Stopped"
	} else {
		d.State, d.Message = "DEPLOYMENT_STATE_PROVISIONING", "Scheduling replicas"
	}
	s.Deployments[d.ID] = d
	writeJSON(w, s.deploymentJSON(d))
}

func applyWindows(as map[string]any, a *autoscalingReq) {
	if a.ScaleDownWindow != nil {
		as["scaleDownWindow"] = *a.ScaleDownWindow
	}
	if a.ScaleUpWindow != nil {
		as["scaleUpWindow"] = *a.ScaleUpWindow
	}
	if a.ScaleToZeroWindow != nil {
		as["scaleToZeroWindow"] = *a.ScaleToZeroWindow
	}
	if a.ScalingMetrics != nil {
		ms := make([]any, 0, len(a.ScalingMetrics))
		for _, m := range a.ScalingMetrics {
			ms = append(ms, m)
		}
		as["scalingMetrics"] = ms
	}
}

func (s *Server) lookupDeployment(w http.ResponseWriter, r *http.Request) *Deployment {
	d := s.Deployments[r.PathValue("id")]
	if d == nil || d.ProjectID != r.PathValue("projectId") || d.EndpointID != r.PathValue("endpointId") {
		writeErr(w, http.StatusNotFound, "not_found", "deployment not found")
		return nil
	}
	return d
}

func (s *Server) advanceDeployment(d *Deployment) {
	d.gets++
	if d.gets < s.StepsPerState {
		return
	}
	d.gets = 0
	switch d.State {
	case "DEPLOYMENT_STATE_PROVISIONING":
		d.State, d.Message = "DEPLOYMENT_STATE_SCALING", "Starting replicas"
	case "DEPLOYMENT_STATE_SCALING":
		d.State, d.Message = "DEPLOYMENT_STATE_READY", "All replicas ready"
	case "DEPLOYMENT_STATE_STOPPING":
		d.State, d.Message = "DEPLOYMENT_STATE_STOPPED", "Stopped"
	}
}

func (s *Server) getDeployment(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	if d := s.lookupDeployment(w, r); d != nil {
		s.advanceDeployment(d)
		writeJSON(w, s.deploymentJSON(d))
	}
}

func (s *Server) updateDeployment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                            *string         `json:"name"`
		Autoscaling                     *autoscalingReq `json:"autoscaling"`
		InactiveTimeout                 *int64          `json:"inactiveTimeout"`
		MaxConcurrentRequestsPerReplica *string         `json:"maxConcurrentRequestsPerReplica"`
		Etag                            string          `json:"etag"`
	}
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	d := s.lookupDeployment(w, r)
	if d == nil {
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, "invalid_request_error", msg) }
	if req.Etag != "" && req.Etag != strconv.Itoa(d.Etag) {
		writeErr(w, http.StatusConflict, "aborted", "etag mismatch")
		return
	}
	populated := map[string]bool{"name": req.Name != nil, "inactiveTimeout": req.InactiveTimeout != nil, "maxConcurrentRequestsPerReplica": req.MaxConcurrentRequestsPerReplica != nil}
	if req.Autoscaling != nil {
		populated["autoscaling"] = true
	}
	mask := maskOf(r, populated)
	allowed := []string{"name", "inactiveTimeout", "maxConcurrentRequestsPerReplica", "autoscaling", "autoscaling.minReplicas", "autoscaling.maxReplicas",
		"autoscaling.scaleDownWindow", "autoscaling.scaleUpWindow", "autoscaling.scaleToZeroWindow", "autoscaling.scalingMetrics"}
	for f := range mask {
		if !slices.Contains(allowed, f) {
			bad("invalid update mask path " + f)
			return
		}
	}
	if req.Autoscaling != nil {
		if msg := validateAutoscaling(req.Autoscaling); msg != "" {
			bad(msg)
			return
		}
	}
	if mask["inactiveTimeout"] && req.InactiveTimeout != nil && *req.InactiveTimeout != 0 && (*req.InactiveTimeout < 30 || *req.InactiveTimeout > 1440) {
		bad("inactiveTimeout must be 0 or between 30 and 1440")
		return
	}

	minR, maxR := asInt(d.Autoscaling["minReplicas"]), asInt(d.Autoscaling["maxReplicas"])
	wasStopped := maxR == 0
	if req.Autoscaling != nil {
		if (mask["autoscaling"] || mask["autoscaling.minReplicas"]) && req.Autoscaling.MinReplicas != nil {
			minR = *req.Autoscaling.MinReplicas
		}
		if (mask["autoscaling"] || mask["autoscaling.maxReplicas"]) && req.Autoscaling.MaxReplicas != nil {
			maxR = *req.Autoscaling.MaxReplicas
		}
	}
	if maxR < minR {
		bad("autoscaling.maxReplicas must be >= minReplicas")
		return
	}
	starting := wasStopped && maxR > 0
	stopping := !wasStopped && maxR == 0
	if starting {
		if d.State == "DEPLOYMENT_STATE_FAILED" {
			bad("the deployment is in a terminal FAILED state and cannot be restarted; create a new deployment")
			return
		}
		if d.State != "DEPLOYMENT_STATE_STOPPED" {
			bad("the deployment must be stopped before it can be restarted")
			return
		}
	}
	if mask["name"] && req.Name != nil {
		if *req.Name == "" {
			bad("name cannot be empty")
			return
		}
		d.Name = *req.Name
	}
	if mask["inactiveTimeout"] && req.InactiveTimeout != nil {
		d.InactiveTimeout = *req.InactiveTimeout
	}
	if mask["maxConcurrentRequestsPerReplica"] && req.MaxConcurrentRequestsPerReplica != nil {
		d.MaxConcurrent = *req.MaxConcurrentRequestsPerReplica
	}
	d.Autoscaling["minReplicas"], d.Autoscaling["maxReplicas"] = minR, maxR
	if req.Autoscaling != nil {
		sub := &autoscalingReq{}
		if mask["autoscaling"] || mask["autoscaling.scaleDownWindow"] {
			sub.ScaleDownWindow = req.Autoscaling.ScaleDownWindow
		}
		if mask["autoscaling"] || mask["autoscaling.scaleUpWindow"] {
			sub.ScaleUpWindow = req.Autoscaling.ScaleUpWindow
		}
		if mask["autoscaling"] || mask["autoscaling.scaleToZeroWindow"] {
			sub.ScaleToZeroWindow = req.Autoscaling.ScaleToZeroWindow
		}
		if mask["autoscaling"] || mask["autoscaling.scalingMetrics"] {
			sub.ScalingMetrics = req.Autoscaling.ScalingMetrics
		}
		applyWindows(d.Autoscaling, sub)
	}
	switch {
	case stopping:
		d.State, d.Message = "DEPLOYMENT_STATE_STOPPING", "Scaling down"
	case starting:
		d.State, d.Message = "DEPLOYMENT_STATE_PROVISIONING", "Scheduling replicas"
	case maxR > 0 && d.State == "DEPLOYMENT_STATE_READY" && (mask["autoscaling"] || mask["autoscaling.minReplicas"]):
		d.State, d.Message = "DEPLOYMENT_STATE_SCALING", "Starting replicas"
	}
	d.gets = 0
	d.Etag++
	d.UpdatedAt = now()
	writeJSON(w, s.deploymentJSON(d))
}

func (s *Server) deleteDeployment(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.count(r)
	d := s.lookupDeployment(w, r)
	if d == nil {
		return
	}
	if d.State != "DEPLOYMENT_STATE_STOPPED" && d.State != "DEPLOYMENT_STATE_FAILED" {
		writeErr(w, http.StatusBadRequest, "failed_precondition", "the deployment must be stopped before it can be deleted")
		return
	}
	if e := s.Endpoints[d.EndpointID]; e != nil {
		for _, t := range e.TrafficSplit {
			if t.DeploymentID == d.ID {
				writeErr(w, http.StatusBadRequest, "failed_precondition", "the deployment stands in an endpoint's traffic split and cannot be deleted; remove it from the traffic split (it may be standing at weight 0), then delete")
				return
			}
		}
	}
	delete(s.Deployments, d.ID)
	writeJSON(w, map[string]any{})
}

// DeleteDeploymentNow removes a deployment out of band (and from the split).
func (s *Server) DeleteDeploymentNow(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.Deployments[id]
	if d == nil {
		return
	}
	if e := s.Endpoints[d.EndpointID]; e != nil {
		var keep []TrafficSplitEntry
		for _, t := range e.TrafficSplit {
			if t.DeploymentID != id {
				keep = append(keep, t)
			}
		}
		e.TrafficSplit = keep
	}
	delete(s.Deployments, id)
}
