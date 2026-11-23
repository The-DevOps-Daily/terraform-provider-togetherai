package provider

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/mockapi"
)

type dmiConfig struct {
	EndpointName         string
	Visibility           string
	DeploymentName       string
	MinReplicas          int
	MaxReplicas          string // "" leaves max_replicas unset
	InactiveTimeout      int
	Weight               float64
	ScaleDownWindow      string
	MaxConcurrentPerRepl string
}

func (c dmiConfig) hcl(provider string) string {
	maxLine := ""
	if c.MaxReplicas != "" {
		maxLine = "max_replicas = " + c.MaxReplicas
	}
	windowLine := ""
	if c.ScaleDownWindow != "" {
		windowLine = fmt.Sprintf("scale_down_window = %q", c.ScaleDownWindow)
	}
	concLine := ""
	if c.MaxConcurrentPerRepl != "" {
		concLine = "max_concurrent_requests_per_replica = " + c.MaxConcurrentPerRepl
	}
	return provider + fmt.Sprintf(`
data "togetherai_supported_models" "gemma" {
  search  = "gemma-4-E4B-it"
  product = "PRODUCT_DEDICATED"
}

locals {
  profile = data.togetherai_supported_models.gemma.models[0].deployment_profiles[0]
}

resource "togetherai_endpoint" "chat" {
  name       = %q
  visibility = %q
}

resource "togetherai_deployment" "v1" {
  endpoint_id = togetherai_endpoint.chat.id
  name        = %q
  model       = local.profile.model
  config      = local.profile.config

  autoscaling = {
    min_replicas = %d
    %s
    %s
  }
  inactive_timeout = %d
  %s
}

resource "togetherai_endpoint_traffic_split" "chat" {
  endpoint_id = togetherai_endpoint.chat.id
  routes = [{
    deployment_id = togetherai_deployment.v1.id
    weight        = %g
  }]
}
`, c.EndpointName, c.Visibility, c.DeploymentName, c.MinReplicas, maxLine, windowLine, c.InactiveTimeout, concLine, c.Weight)
}

func TestMockEndpointDeploymentTrafficSplit(t *testing.T) {
	m, provider := newMock(t)
	base := dmiConfig{EndpointName: "chat", Visibility: "VISIBILITY_PRIVATE", DeploymentName: "v1", MinReplicas: 1, MaxReplicas: "2", Weight: 1}
	var depID string

	renamed := base
	renamed.EndpointName = "chat-prod"
	renamed.Visibility = "VISIBILITY_INTERNAL"
	renamed.DeploymentName = "v1-renamed"
	renamed.MinReplicas = 2
	renamed.MaxReplicas = ""
	renamed.InactiveTimeout = 60
	renamed.ScaleDownWindow = "600s"
	renamed.MaxConcurrentPerRepl = "4"

	stopped := renamed
	stopped.MinReplicas = 0
	stopped.Weight = 0

	restarted := stopped
	restarted.MinReplicas = 1
	restarted.Weight = 1

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			m.Lock()
			defer m.Unlock()
			if len(m.Endpoints) != 0 || len(m.Deployments) != 0 {
				return fmt.Errorf("left after destroy: %d endpoints, %d deployments", len(m.Endpoints), len(m.Deployments))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: base.hcl(provider),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_endpoint.chat", "qualified_name", "mock-project/chat"),
					resource.TestCheckResourceAttr("togetherai_endpoint.chat", "project_id", "proj_mock1"),
					resource.TestCheckResourceAttr("togetherai_endpoint.chat", "endpoint_type", "ENDPOINT_TYPE_DEDICATED"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "state", "DEPLOYMENT_STATE_READY"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "qualified_name", "mock-project/chat/v1"),
					// The configured model stays unpinned; the pinned revision is separate.
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "model", "projects/proj_public/models/ml_gemma"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "model_revision", "projects/proj_public/models/ml_gemma/revisions/rev_1"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "hardware", "1xH100"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "autoscaling.max_replicas", "2"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "autoscaling.scale_down_window", "300s"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "autoscaling.scaling_metrics.#", "1"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "max_concurrent_requests_per_replica", "7"),
					resource.TestCheckResourceAttr("togetherai_endpoint_traffic_split.chat", "routes.#", "1"),
					resource.TestCheckResourceAttrWith("togetherai_deployment.v1", "id", func(v string) error { depID = v; return nil }),
				),
			},
			{
				// Rename both, widen visibility, raise min (max follows) and
				// tune autoscaling: all in place.
				Config: renamed.hcl(provider),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_endpoint.chat", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("togetherai_deployment.v1", plancheck.ResourceActionUpdate),
					plancheck.ExpectResourceAction("togetherai_endpoint_traffic_split.chat", plancheck.ResourceActionNoop),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_endpoint.chat", "qualified_name", "mock-project/chat-prod"),
					resource.TestCheckResourceAttr("togetherai_endpoint.chat", "visibility", "VISIBILITY_INTERNAL"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "name", "v1-renamed"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "autoscaling.min_replicas", "2"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "autoscaling.max_replicas", "2"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "autoscaling.scale_down_window", "600s"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "inactive_timeout", "60"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "max_concurrent_requests_per_replica", "4"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "state", "DEPLOYMENT_STATE_READY"),
				),
			},
			{
				// Stop: min = max = 0 waits for STOPPED, keeps the deployment.
				Config: stopped.hcl(provider),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "state", "DEPLOYMENT_STATE_STOPPED"),
					resource.TestCheckResourceAttr("togetherai_deployment.v1", "autoscaling.max_replicas", "0"),
					resource.TestCheckResourceAttr("togetherai_endpoint_traffic_split.chat", "routes.0.weight", "0"),
					resource.TestCheckResourceAttrWith("togetherai_deployment.v1", "id", func(v string) error {
						if v != depID {
							return fmt.Errorf("deployment was replaced")
						}
						return nil
					}),
				),
			},
			{
				// Start it again.
				Config: restarted.hcl(provider),
				Check:  resource.TestCheckResourceAttr("togetherai_deployment.v1", "state", "DEPLOYMENT_STATE_READY"),
			},
			{
				ResourceName:      "togetherai_endpoint.chat",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "togetherai_endpoint_traffic_split.chat",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName: "togetherai_deployment.v1",
				ImportState:  true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					r := s.RootModule().Resources["togetherai_deployment.v1"]
					return "proj_mock1/" + r.Primary.Attributes["endpoint_id"] + "/" + r.Primary.ID, nil
				},
				ImportStateVerify: true,
				// Import sees the pinned model revision; the config uses the
				// unpinned name for the same weights.
				ImportStateVerifyIgnore: []string{"model", "timeouts"},
			},
			{
				// Deleted in the console: the deployment is planned again and
				// the split follows the new ID.
				PreConfig: func() { m.DeleteDeploymentNow(depID) },
				Config:    restarted.hcl(provider),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_deployment.v1", plancheck.ResourceActionCreate),
					plancheck.ExpectResourceAction("togetherai_endpoint_traffic_split.chat", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttrPair("togetherai_endpoint_traffic_split.chat", "routes.0.deployment_id", "togetherai_deployment.v1", "id"),
			},
			{
				// A traffic split changed in the console is drift.
				PreConfig: func() {
					m.Lock()
					defer m.Unlock()
					for _, e := range m.Endpoints {
						e.TrafficSplit = nil
					}
				},
				Config: restarted.hcl(provider),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_endpoint_traffic_split.chat", plancheck.ResourceActionUpdate),
				}},
			},
			{
				// A new model replaces the deployment.
				PreConfig: func() {
					m.Lock()
					defer m.Unlock()
					m.SupportedModels[0].DeploymentProfiles[0]["model"] = "projects/proj_public/models/ml_gemma_v2"
				},
				Config: restarted.hcl(provider),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_deployment.v1", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.TestCheckResourceAttr("togetherai_deployment.v1", "model", "projects/proj_public/models/ml_gemma_v2"),
			},
		},
	})
}

func TestMockDeploymentDeleteDetachesFromUnmanagedSplit(t *testing.T) {
	m, provider := newMock(t)
	cfg := provider + `
resource "togetherai_endpoint" "e" {
  name = "solo"
}

resource "togetherai_deployment" "d" {
  endpoint_id  = togetherai_endpoint.e.id
  name         = "d1"
  model        = "projects/proj_public/models/ml_qwen"
  autoscaling  = { min_replicas = 1 }
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			m.Lock()
			defer m.Unlock()
			if len(m.Endpoints) != 0 || len(m.Deployments) != 0 {
				return fmt.Errorf("left after destroy: %d endpoints, %d deployments", len(m.Endpoints), len(m.Deployments))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					// config defaults to the model's only profile.
					resource.TestCheckResourceAttr("togetherai_deployment.d", "config", "projects/proj_public/configs/cr_qwen_fp8"),
					resource.TestCheckResourceAttr("togetherai_deployment.d", "autoscaling.max_replicas", "1"),
					resource.TestCheckResourceAttr("togetherai_deployment.d", "state", "DEPLOYMENT_STATE_READY"),
				),
			},
			{
				// The CLI routed traffic to it. The endpoint resource does not
				// own the split, so this is not drift, and destroy must still
				// detach, stop and delete the running deployment.
				PreConfig: func() {
					m.Lock()
					defer m.Unlock()
					for _, e := range m.Endpoints {
						for id := range m.Deployments {
							e.TrafficSplit = []mockapi.TrafficSplitEntry{{DeploymentID: id, Weight: 1}}
						}
					}
				},
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})
}

func TestMockDeploymentFailed(t *testing.T) {
	m, provider := newMock(t)
	m.StepsPerState = 2
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			PreConfig: func() {
				go func() {
					// Fail the deployment as soon as it exists.
					for {
						m.Lock()
						done := false
						for _, d := range m.Deployments {
							d.State, d.Message = "DEPLOYMENT_STATE_FAILED", "Cannot place replicas: insufficient GPU capacity"
							done = true
						}
						m.Unlock()
						if done {
							return
						}
						time.Sleep(time.Millisecond)
					}
				}()
			},
			Config: provider + `
resource "togetherai_endpoint" "e" {
  name = "failing"
}

resource "togetherai_deployment" "d" {
  endpoint_id = togetherai_endpoint.e.id
  name        = "d1"
  model       = "projects/proj_public/models/ml_qwen"
  autoscaling = { min_replicas = 1 }
}
`,
			ExpectError: regexp.MustCompile(`(?s)DEPLOYMENT_STATE_FAILED.*insufficient\s+GPU\s+capacity`),
		}},
	})
}

func TestMockDeploymentValidation(t *testing.T) {
	_, provider := newMock(t)
	cases := map[string]struct{ attrs, err string }{
		"bad model":      {`model = "google/gemma"` + "\n  autoscaling = { min_replicas = 1 }", `projects/\{projectId\}/models`},
		"bad window":     {`model = "projects/p/models/m"` + "\n  autoscaling = { min_replicas = 1, scale_up_window = \"5m\" }", `duration in seconds`},
		"bad timeout":    {`model = "projects/p/models/m"` + "\n  autoscaling = { min_replicas = 1 }\n  inactive_timeout = 10", `between 30 and 1440`},
		"bad metric":     {`model = "projects/p/models/m"` + "\n  autoscaling = { min_replicas = 1, scaling_metrics = [{ name = \"qps\", type = \"METRIC_TARGET_TYPE_VALUE\", target = 1 }] }", `value must be one of`},
		"placement both": {`model = "projects/p/models/m"` + "\n  autoscaling = { min_replicas = 1 }\n  placement = { profile = \"pp_1\", regions = [\"us-*\"] }", `cannot be specified when`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config: provider + `
resource "togetherai_deployment" "d" {
  endpoint_id = "ep_1"
  name        = "d"
  ` + tc.attrs + `
}
`,
					ExpectError: regexp.MustCompile(tc.err),
				}},
			})
		})
	}
}

func TestMockEndpointProjectSelection(t *testing.T) {
	m, provider := newMock(t)
	m.Lock()
	m.Projects = append(m.Projects, mockapi.Project{ID: "proj_mock2", Name: "Second", Slug: "second", OrganizationID: "org_mock", OrganizationName: "Mock org"})
	m.Unlock()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Two projects and none configured: ask the user to pick.
				Config:      provider + `resource "togetherai_endpoint" "e" { name = "x" }`,
				ExpectError: regexp.MustCompile(`can see 2 projects \(proj_mock1, proj_mock2\)`),
			},
			{
				Config: provider + `
data "togetherai_projects" "all" {}

resource "togetherai_endpoint" "e" {
  project_id = [for p in data.togetherai_projects.all.projects : p.id if p.slug == "second"][0]
  name       = "second/x"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_endpoint.e", "project_id", "proj_mock2"),
					resource.TestCheckResourceAttr("togetherai_endpoint.e", "name", "second/x"),
					resource.TestCheckResourceAttr("togetherai_endpoint.e", "qualified_name", "second/x"),
					resource.TestCheckResourceAttr("data.togetherai_projects.all", "projects.#", "2"),
				),
			},
		},
	})
}

func TestMockEndpointDuplicateName(t *testing.T) {
	_, provider := newMock(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: provider + `
resource "togetherai_endpoint" "a" { name = "same" }
resource "togetherai_endpoint" "b" {
  name       = "same"
  depends_on = [togetherai_endpoint.a]
}
`,
			ExpectError: regexp.MustCompile(`(?s)HTTP 409.*already exists`),
		}},
	})
}
