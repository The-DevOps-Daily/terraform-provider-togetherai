package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/client"
	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/mockapi"
)

func newMock(t *testing.T) (*mockapi.Server, *client.Client) {
	t.Helper()
	m := mockapi.New()
	t.Cleanup(m.Close)
	c := client.New(mockapi.APIKey, m.URL(), "test")
	c.MinBackoff = time.Millisecond
	c.MaxBackoff = 5 * time.Millisecond
	return m, c
}

func i64(v int64) *int64 { return &v }

func TestBearerAuthAndUserAgent(t *testing.T) {
	var gotAuth, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotUA = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"regions":[]}`))
	}))
	defer srv.Close()
	c := client.New("secret-key", srv.URL+"/", "terraform-provider-togetherai/test")
	if _, err := c.ListRegions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotUA != "terraform-provider-togetherai/test" {
		t.Errorf("User-Agent = %q", gotUA)
	}
}

func TestDefaultBaseURL(t *testing.T) {
	if got := client.New("k", "", "").BaseURL(); got != "https://api.together.ai" {
		t.Fatalf("BaseURL = %q", got)
	}
}

func TestKeyErrorStopsRequests(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	c := client.New("", srv.URL, "")
	c.KeyError = errors.New("missing key")
	if _, err := c.ListRegions(context.Background()); err == nil || err.Error() != "missing key" {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("request was sent without a key")
	}
}

func TestErrorBodies(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want       string
	}{
		{"ErrorData", `{"error":{"message":"num_gpus must be a multiple of 8","type":"invalid_request_error","param":"num_gpus","code":null}}`, 400,
			"returned HTTP 400: num_gpus must be a multiple of 8 (type=invalid_request_error, param=num_gpus)"},
		{"numeric code", `{"code":5,"message":"cluster not found"}`, 404, "returned HTTP 404: cluster not found (code=5)"},
		{"plain text", `upstream connect error`, 403, "returned HTTP 403: upstream connect error"},
		{"empty", ``, 403, "returned HTTP 403"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := client.New("k", srv.URL, "")
			_, err := c.GetCluster(context.Background(), "cl_1")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "GET /v1/compute/clusters/cl_1") {
				t.Fatalf("err %q does not name the request", err)
			}
			if client.StatusCode(err) != tc.status {
				t.Fatalf("StatusCode = %d", client.StatusCode(err))
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	_, c := newMock(t)
	_, err := c.GetCluster(context.Background(), "cl_missing")
	if !client.IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false", err)
	}
	if client.IsNotFound(errors.New("other")) {
		t.Fatal("IsNotFound on a plain error")
	}
}

func TestUnauthorizedIsNotRetried(t *testing.T) {
	m, _ := newMock(t)
	c := client.New("wrong", m.URL(), "")
	c.MinBackoff = time.Millisecond
	_, err := c.ListRegions(context.Background())
	if client.StatusCode(err) != http.StatusUnauthorized || !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("err = %v", err)
	}
	if n := m.Requests["GET /v1/compute/regions"]; n != 0 {
		t.Fatalf("handler ran %d times", n)
	}
}

func TestRetriesOn5xxAndRetryAfter(t *testing.T) {
	m, c := newMock(t)
	m.FailNext("GET /v1/compute/regions", http.StatusServiceUnavailable, 2)
	m.FailNext("GET /v1/compute/regions", http.StatusTooManyRequests, 1)
	regions, err := c.ListRegions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 2 {
		t.Fatalf("got %d regions", len(regions))
	}
}

func TestRetriesGiveUp(t *testing.T) {
	m, c := newMock(t)
	c.MaxRetries = 2
	m.FailNext("GET /v1/compute/regions", http.StatusBadGateway, 10)
	_, err := c.ListRegions(context.Background())
	if client.StatusCode(err) != http.StatusBadGateway {
		t.Fatalf("err = %v", err)
	}
}

func TestPostNotRetriedOn5xxButOn429(t *testing.T) {
	var calls atomic.Int32
	status := http.StatusInternalServerError
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"boom","type":"server_error"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"volume_id":"vol_1","volume_name":"v","size_tib":1,"status":"provisioning"}`))
	}))
	defer srv.Close()
	c := client.New("k", srv.URL, "")
	c.MinBackoff = time.Millisecond

	if _, err := c.CreateVolume(context.Background(), client.VolumeCreateRequest{VolumeName: "v", SizeTiB: 1, Region: "r"}); client.StatusCode(err) != 500 {
		t.Fatalf("POST after 500: err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("POST was retried after a 500 (%d calls)", calls.Load())
	}

	calls.Store(0)
	status = http.StatusTooManyRequests
	if _, err := c.CreateVolume(context.Background(), client.VolumeCreateRequest{VolumeName: "v", SizeTiB: 1, Region: "r"}); err != nil {
		t.Fatalf("POST after 429: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestQuota429NotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"GPU quota exceeded: this deployment would put your project at 116 of 100 H100 GPUs","type":"resource_exhausted"}}`))
	}))
	defer srv.Close()
	c := client.New("k", srv.URL, "")
	c.MinBackoff = time.Millisecond
	_, err := c.CreateDeployment(context.Background(), "p", "e", client.DeploymentCreateRequest{Name: "d"})
	if err == nil || !strings.Contains(err.Error(), "GPU quota exceeded") {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("quota 429 retried: %d calls", calls.Load())
	}
}

func TestContextCancelStopsRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := client.New("k", srv.URL, "")
	c.MinBackoff = time.Hour
	c.MaxBackoff = time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.ListRegions(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("retry did not stop on context cancel")
	}
}

func TestProjectsPagination(t *testing.T) {
	m, c := newMock(t)
	m.Lock()
	for i := 0; i < 23; i++ {
		m.Projects = append(m.Projects, mockapi.Project{ID: "proj_" + string(rune('a'+i)), Name: "p", Slug: "p"})
	}
	m.Unlock()
	ps, err := c.ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 24 {
		t.Fatalf("got %d projects, want 24", len(ps))
	}
}

func TestSupportedModelsPaginationAndFilter(t *testing.T) {
	m, c := newMock(t)
	all, err := c.ListSupportedModels(context.Background(), client.SupportedModelsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d models, want 3 (the mock pages by 2)", len(all))
	}
	if n := m.Requests["GET /v2/supported-models"]; n != 2 {
		t.Fatalf("requests = %d, want 2 pages", n)
	}
	ded, err := c.ListSupportedModels(context.Background(), client.SupportedModelsFilter{Product: "PRODUCT_DEDICATED", Search: "gemma"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ded) != 1 || ded[0].Name != "google/gemma-4-E4B-it" || len(ded[0].DeploymentProfiles) != 1 {
		t.Fatalf("filtered = %+v", ded)
	}
	if ded[0].DeploymentProfiles[0].Config != "projects/proj_public/configs/cr_gemma_bf16" {
		t.Fatalf("profile = %+v", ded[0].DeploymentProfiles[0])
	}
}

func TestInstanceTypes(t *testing.T) {
	_, c := newMock(t)
	its, err := c.ListInstanceTypes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(its) != 2 || its[0].ID != "1xnvidia-h100-80gb" || its[0].PriceCentsPerHour != 549 || its[0].Regions[0].Name != "us-east-1" {
		t.Fatalf("instance types = %+v", its)
	}
}

func TestClusterLifecycle(t *testing.T) {
	m, c := newMock(t)
	ctx := context.Background()
	cl, err := c.CreateCluster(ctx, client.ClusterCreateRequest{
		ClusterType: "KUBERNETES", Region: "us-central-8", GPUType: "H100_SXM", NumGPUs: 8, ClusterName: "c1",
		BillingType: "ON_DEMAND", NvidiaVersionID: "nv_570_128",
		SharedVolume: &client.VolumeCreateRequest{VolumeName: "data", SizeTiB: 1, Region: "us-central-8"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cl.Status != "WaitingForControlPlaneNodes" || cl.CUDAVersion != "12.8" || len(cl.Volumes) != 1 {
		t.Fatalf("created = %+v", cl)
	}
	for i := 0; i < 5 && cl.Status != client.ClusterStatusReady; i++ {
		if cl, err = c.GetCluster(ctx, cl.ClusterID); err != nil {
			t.Fatal(err)
		}
	}
	if cl.Status != client.ClusterStatusReady || cl.KubeConfig == "" || len(cl.GPUWorkerNodes) != 1 {
		t.Fatalf("not ready: %+v", cl)
	}
	up, err := c.UpdateCluster(ctx, cl.ClusterID, client.ClusterUpdateRequest{NumGPUs: i64(16)})
	if err != nil {
		t.Fatal(err)
	}
	if up.NumGPUs != 16 || up.Status == client.ClusterStatusReady {
		t.Fatalf("update = %+v", up)
	}
	_, err = c.UpdateCluster(ctx, cl.ClusterID, client.ClusterUpdateRequest{NumGPUs: i64(12)})
	if client.StatusCode(err) != 400 {
		t.Fatalf("num_gpus 12: err = %v", err)
	}
	if err := c.DeleteCluster(ctx, cl.ClusterID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_, err = c.GetCluster(ctx, cl.ClusterID)
		if client.IsNotFound(err) {
			break
		}
	}
	if !client.IsNotFound(err) {
		t.Fatalf("cluster still there: %v", err)
	}
	if len(m.Volumes) != 0 {
		t.Fatalf("inline volume left behind: %v", m.Volumes)
	}
}

func TestClusterCreateValidation(t *testing.T) {
	_, c := newMock(t)
	_, err := c.CreateCluster(context.Background(), client.ClusterCreateRequest{
		Region: "us-central-8", GPUType: "H100_SXM", NumGPUs: 8, ClusterName: "c1", BillingType: "ON_DEMAND", NvidiaVersionID: "nv_570_128",
	})
	if client.StatusCode(err) != 400 || !strings.Contains(err.Error(), "shared volume is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestVolumeLifecycle(t *testing.T) {
	_, c := newMock(t)
	ctx := context.Background()
	v, err := c.CreateVolume(ctx, client.VolumeCreateRequest{VolumeName: "my-data", SizeTiB: 1, Region: "us-central-8"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != client.VolumeStatusProvisioning {
		t.Fatalf("status = %s", v.Status)
	}
	if v, err = c.GetVolume(ctx, v.VolumeID); err != nil || v.Status != client.VolumeStatusAvailable {
		t.Fatalf("get = %+v, %v", v, err)
	}
	if _, err := c.UpdateVolume(ctx, client.VolumeUpdateRequest{VolumeID: v.VolumeID, SizeTiB: i64(2)}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateVolume(ctx, client.VolumeUpdateRequest{VolumeID: v.VolumeID, SizeTiB: i64(1)}); client.StatusCode(err) != 400 {
		t.Fatalf("shrink: err = %v", err)
	}
	vols, err := c.ListVolumes(ctx, "")
	if err != nil || len(vols) != 1 {
		t.Fatalf("list = %v, %v", vols, err)
	}
	if err := c.DeleteVolume(ctx, v.VolumeID); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	m, _ := newMock(t)
	req, _ := http.NewRequest(http.MethodPost, m.URL()+"/v2/projects/proj_mock1/endpoints", strings.NewReader(`{"name":"e","inactive_timeout":5}`))
	req.Header.Set("Authorization", "Bearer "+mockapi.APIKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("status = %d", res.StatusCode)
	}
}

func TestEndpointAndDeploymentLifecycle(t *testing.T) {
	_, c := newMock(t)
	ctx := context.Background()
	p := "proj_mock1"
	ep, err := c.CreateEndpoint(ctx, p, client.EndpointCreateRequest{Name: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Name != "mock-project/chat" || ep.Visibility != client.VisibilityPrivate {
		t.Fatalf("endpoint = %+v", ep)
	}
	if _, err := c.CreateEndpoint(ctx, p, client.EndpointCreateRequest{Name: "chat"}); client.StatusCode(err) != 409 {
		t.Fatalf("duplicate: err = %v", err)
	}
	d, err := c.CreateDeployment(ctx, p, ep.ID, client.DeploymentCreateRequest{
		Name: "v1", Model: "projects/proj_public/models/ml_gemma", Config: "projects/proj_public/configs/cr_gemma_bf16",
		Autoscaling: client.Autoscaling{MinReplicas: i64(1), MaxReplicas: i64(2)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.Status.State != client.DeploymentStateProvisioning || d.Model != "projects/proj_public/models/ml_gemma/revisions/rev_1" || d.MaxConcurrentRequestsPerReplica != "7" {
		t.Fatalf("deployment = %+v", d)
	}
	empty := []client.TrafficSplitEntry{{DeploymentID: d.ID, Weight: 1}}
	if ep, err = c.UpdateEndpoint(ctx, p, ep.ID, []string{"trafficSplit"}, client.EndpointUpdate{TrafficSplit: &empty}); err != nil || len(ep.TrafficSplit) != 1 {
		t.Fatalf("split = %+v, %v", ep, err)
	}
	if err := c.DeleteDeployment(ctx, p, ep.ID, d.ID); client.StatusCode(err) != 400 {
		t.Fatalf("delete running: err = %v", err)
	}
	if err := c.DeleteEndpoint(ctx, p, ep.ID); client.StatusCode(err) != 400 {
		t.Fatalf("delete endpoint with deployments: err = %v", err)
	}
	none := []client.TrafficSplitEntry{}
	if ep, err = c.UpdateEndpoint(ctx, p, ep.ID, []string{"trafficSplit"}, client.EndpointUpdate{TrafficSplit: &none}); err != nil || len(ep.TrafficSplit) != 0 {
		t.Fatalf("clear split = %+v, %v", ep, err)
	}
	if _, err := c.UpdateDeployment(ctx, p, ep.ID, d.ID, []string{"autoscaling.minReplicas", "autoscaling.maxReplicas"},
		client.DeploymentUpdateRequest{Autoscaling: &client.Autoscaling{MinReplicas: i64(0), MaxReplicas: i64(0)}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if d, err = c.GetDeployment(ctx, p, ep.ID, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	if d.Status.State != client.DeploymentStateStopped {
		t.Fatalf("state = %s", d.Status.State)
	}
	if err := c.DeleteDeployment(ctx, p, ep.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteEndpoint(ctx, p, ep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetEndpoint(ctx, p, ep.ID); !client.IsNotFound(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestErrorRedactsAPIKey(t *testing.T) {
	const key = "tgp_v1_secret-key-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key ` + key + `","type":"auth_error"}}`))
	}))
	defer srv.Close()
	_, err := client.New(key, srv.URL, "").GetCluster(context.Background(), "cl_1")
	if err == nil || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "invalid key <redacted>") {
		t.Fatalf("err = %v, want the key redacted", err)
	}
}
