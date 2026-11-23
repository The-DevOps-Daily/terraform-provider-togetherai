package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/The-DevOps-Daily/terraform-provider-togetherai/internal/client"
)

// Acceptance tests against the real Together AI API. They run only with
// TF_ACC=1 and TOGETHER_API_KEY, and they create real resources. Tests that
// cost money need a second opt-in:
//
//   - no extra env: data sources, endpoints, a stopped deployment (no replica
//     runs, so nothing bills)
//   - TOGETHER_ACC_EXPENSIVE=1: the cluster tests (a 1-2 TiB shared volume for
//     a few minutes, and an 8-GPU on-demand cluster) and a running deployment
//     (one replica)
//
// The README lists the expected cost of each test.

func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("TOGETHER_API_KEY") == "" {
		t.Skip("TOGETHER_API_KEY is not set; skipping a test against the real Together AI API")
	}
}

func requireOptIn(t *testing.T, env, what string) {
	t.Helper()
	if os.Getenv(env) != "1" {
		t.Skipf("%s costs money; set %s=1 to run it (see README for the cost)", what, env)
	}
}

func accName(prefix string) string {
	return prefix + "-" + acctest.RandStringFromCharSet(8, "abcdefghijklmnopqrstuvwxyz0123456789")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func accClient() *client.Client {
	return client.New(os.Getenv("TOGETHER_API_KEY"), os.Getenv("TOGETHER_BASE_URL"), "terraform-provider-togetherai/acctest")
}

// checkGone verifies every resource of type rtype in state is gone from the
// API (404, or status Deleting for clusters).
func checkGone(rtype string, get func(ctx context.Context, rs *terraform.ResourceState) error) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for _, rs := range s.RootModule().Resources {
			if rs.Type != rtype {
				continue
			}
			if err := get(context.Background(), rs); err == nil {
				return fmt.Errorf("%s %s still exists after destroy", rtype, rs.Primary.ID)
			} else if !client.IsNotFound(err) && !strings.Contains(err.Error(), "deleting") {
				return err
			}
		}
		return nil
	}
}

func TestAccDataSources(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
data "togetherai_projects" "all" {}
data "togetherai_gpu_cluster_regions" "all" {}
data "togetherai_hardware" "all" {}
data "togetherai_supported_models" "dedicated" {
  product = "PRODUCT_DEDICATED"
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.togetherai_projects.all", "projects.0.id"),
				resource.TestCheckResourceAttrSet("data.togetherai_projects.all", "projects.0.slug"),
				resource.TestCheckResourceAttrSet("data.togetherai_gpu_cluster_regions.all", "regions.0.name"),
				resource.TestCheckResourceAttrSet("data.togetherai_gpu_cluster_regions.all", "regions.0.driver_versions.0.id"),
				resource.TestCheckResourceAttrSet("data.togetherai_hardware.all", "hardware.0.id"),
				resource.TestCheckResourceAttrSet("data.togetherai_hardware.all", "hardware.0.price_cents_per_hour"),
				resource.TestCheckResourceAttrSet("data.togetherai_supported_models.dedicated", "models.0.name"),
				resource.TestCheckResourceAttrSet("data.togetherai_supported_models.dedicated", "models.0.deployment_profiles.0.config"),
			),
		}},
	})
}

func TestAccEndpoint(t *testing.T) {
	name := accName("tfacc")
	cfg := func(n, vis string) string {
		return fmt.Sprintf(`
resource "togetherai_endpoint" "e" {
  name       = %q
  visibility = %q
}
`, n, vis)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: checkGone("togetherai_endpoint", func(ctx context.Context, rs *terraform.ResourceState) error {
			_, err := accClient().GetEndpoint(ctx, rs.Primary.Attributes["project_id"], rs.Primary.ID)
			return err
		}),
		Steps: []resource.TestStep{
			{
				Config: cfg(name, "VISIBILITY_PRIVATE"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_endpoint.e", "name", name),
					resource.TestMatchResourceAttr("togetherai_endpoint.e", "qualified_name", mustRe(`^[^/]+/`+name+`$`)),
					resource.TestCheckResourceAttrSet("togetherai_endpoint.e", "project_id"),
				),
			},
			{
				Config: cfg(name+"-2", "VISIBILITY_INTERNAL"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_endpoint.e", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_endpoint.e", "name", name+"-2"),
					resource.TestCheckResourceAttr("togetherai_endpoint.e", "visibility", "VISIBILITY_INTERNAL"),
				),
			},
			{
				ResourceName:      "togetherai_endpoint.e",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// accDeploymentConfig picks the model and config from the catalog unless
// TOGETHER_ACC_MODEL and TOGETHER_ACC_CONFIG are set.
func accDeploymentConfig(endpoint, deployment string, minR, maxR int, weight float64) string {
	search := envOr("TOGETHER_ACC_MODEL_SEARCH", "gemma-4-E4B-it")
	model := `local.profile.model`
	config := `local.profile.config`
	if v := os.Getenv("TOGETHER_ACC_MODEL"); v != "" {
		model = fmt.Sprintf("%q", v)
	}
	if v := os.Getenv("TOGETHER_ACC_CONFIG"); v != "" {
		config = fmt.Sprintf("%q", v)
	}
	return fmt.Sprintf(`
data "togetherai_supported_models" "m" {
  search  = %q
  product = "PRODUCT_DEDICATED"
}

locals {
  profile = [for p in flatten([for m in data.togetherai_supported_models.m.models : m.deployment_profiles]) : p if p.model != null && p.config != null][0]
}

resource "togetherai_endpoint" "e" {
  name = %q
}

resource "togetherai_deployment" "d" {
  endpoint_id = togetherai_endpoint.e.id
  name        = %q
  model       = %s
  config      = %s
  autoscaling = {
    min_replicas = %d
    max_replicas = %d
  }
  inactive_timeout = 30
  timeouts = {
    create = "45m"
    update = "45m"
  }
}

resource "togetherai_endpoint_traffic_split" "e" {
  endpoint_id = togetherai_endpoint.e.id
  routes = [{
    deployment_id = togetherai_deployment.d.id
    weight        = %g
  }]
}
`, search, endpoint, deployment, model, config, minR, maxR, weight)
}

func deploymentGone() resource.TestCheckFunc {
	return checkGone("togetherai_deployment", func(ctx context.Context, rs *terraform.ResourceState) error {
		a := rs.Primary.Attributes
		_, err := accClient().GetDeployment(ctx, a["project_id"], a["endpoint_id"], rs.Primary.ID)
		return err
	})
}

// TestAccDeploymentStopped creates a deployment with zero replicas, so no
// hardware runs and nothing bills. It covers create, update, traffic split,
// import and destroy against the real API.
func TestAccDeploymentStopped(t *testing.T) {
	ep, dep := accName("tfacc"), accName("dep")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             deploymentGone(),
		Steps: []resource.TestStep{
			{
				Config: accDeploymentConfig(ep, dep, 0, 0, 0),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_deployment.d", "state", "DEPLOYMENT_STATE_STOPPED"),
					resource.TestCheckResourceAttr("togetherai_deployment.d", "ready_replicas", "0"),
					resource.TestCheckResourceAttrSet("togetherai_deployment.d", "model_revision"),
					resource.TestCheckResourceAttrSet("togetherai_deployment.d", "hardware"),
					resource.TestCheckResourceAttr("togetherai_endpoint_traffic_split.e", "routes.#", "1"),
				),
			},
			{
				// Rename and change the idle timeout in place, still stopped.
				Config: strings.Replace(accDeploymentConfig(ep, dep+"-b", 0, 0, 0), "inactive_timeout = 30", "inactive_timeout = 60", 1),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_deployment.d", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_deployment.d", "name", dep+"-b"),
					resource.TestCheckResourceAttr("togetherai_deployment.d", "inactive_timeout", "60"),
				),
			},
			{
				ResourceName:      "togetherai_deployment.d",
				ImportState:       true,
				ImportStateIdFunc: deploymentImportID,
				ImportStateVerify: true,
				// The import sees the pinned revision instead of the
				// catalog's unpinned model name.
				ImportStateVerifyIgnore: []string{"model", "timeouts"},
			},
			{
				ResourceName:      "togetherai_endpoint_traffic_split.e",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func deploymentImportID(s *terraform.State) (string, error) {
	rs := s.RootModule().Resources["togetherai_deployment.d"]
	if rs == nil {
		return "", fmt.Errorf("togetherai_deployment.d not in state")
	}
	a := rs.Primary.Attributes
	return a["project_id"] + "/" + a["endpoint_id"] + "/" + rs.Primary.ID, nil
}

// TestAccDeploymentRunning starts one replica, waits for READY, then stops it
// and destroys everything. One small-model replica bills only while ready.
func TestAccDeploymentRunning(t *testing.T) {
	requireOptIn(t, "TOGETHER_ACC_EXPENSIVE", "TestAccDeploymentRunning (one replica until it is ready)")
	ep, dep := accName("tfacc"), accName("dep")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy:             deploymentGone(),
		Steps: []resource.TestStep{
			{
				Config: accDeploymentConfig(ep, dep, 1, 1, 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_deployment.d", "state", "DEPLOYMENT_STATE_READY"),
					resource.TestCheckResourceAttr("togetherai_deployment.d", "ready_replicas", "1"),
				),
			},
			{
				// Stop it before destroy so the last minutes do not bill.
				Config: accDeploymentConfig(ep, dep, 0, 0, 1),
				Check:  resource.TestCheckResourceAttr("togetherai_deployment.d", "state", "DEPLOYMENT_STATE_STOPPED"),
			},
		},
	})
}

func TestAccSharedVolume(t *testing.T) {
	requireOptIn(t, "TOGETHER_ACC_EXPENSIVE", "TestAccSharedVolume (1-2 TiB of shared storage for a few minutes)")
	name := accName("tfacc")
	region := os.Getenv("TOGETHER_ACC_REGION")
	regionExpr := fmt.Sprintf("%q", region)
	pre := ""
	if region == "" {
		pre = `
data "togetherai_gpu_cluster_regions" "all" {}
`
		regionExpr = `data.togetherai_gpu_cluster_regions.all.regions[0].name`
	}
	cfg := func(size int) string {
		return pre + fmt.Sprintf(`
resource "togetherai_shared_volume" "v" {
  name     = %q
  size_tib = %d
  region   = %s
}
`, name, size, regionExpr)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: checkGone("togetherai_shared_volume", func(ctx context.Context, rs *terraform.ResourceState) error {
			v, err := accClient().GetVolume(ctx, rs.Primary.ID)
			if err == nil && v.Status == client.VolumeStatusDeleting {
				return fmt.Errorf("volume is deleting")
			}
			return err
		}),
		Steps: []resource.TestStep{
			{
				Config: cfg(1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_shared_volume.v", "size_tib", "1"),
					resource.TestCheckResourceAttrSet("togetherai_shared_volume.v", "status"),
				),
			},
			{
				Config: cfg(2),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_shared_volume.v", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttr("togetherai_shared_volume.v", "size_tib", "2"),
			},
			{
				ResourceName:            "togetherai_shared_volume.v",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"region", "timeouts"},
			},
		},
	})
}

// TestAccGPUCluster creates the smallest on-demand cluster (8 GPUs), checks
// the kubeconfig, and destroys it. Provisioning alone can take up to an hour.
func TestAccGPUCluster(t *testing.T) {
	requireOptIn(t, "TOGETHER_ACC_EXPENSIVE", "TestAccGPUCluster (an 8-GPU on-demand cluster for up to about an hour)")
	name := accName("tfacc")
	gpu := envOr("TOGETHER_ACC_GPU_TYPE", "H100_SXM")
	regionFilter := fmt.Sprintf(`r if contains(r.supported_instance_types, %q)`, gpu)
	if v := os.Getenv("TOGETHER_ACC_REGION"); v != "" {
		regionFilter = fmt.Sprintf(`r if r.name == %q`, v)
	}
	cfg := fmt.Sprintf(`
data "togetherai_gpu_cluster_regions" "all" {}

locals {
  region = [for r in data.togetherai_gpu_cluster_regions.all.regions : %s][0]
}

resource "togetherai_gpu_cluster" "c" {
  name              = %q
  cluster_type      = "KUBERNETES"
  region            = local.region.name
  gpu_type          = %q
  billing_type      = "ON_DEMAND"
  num_gpus          = 8
  nvidia_version_id = local.region.driver_versions[0].id

  shared_volume = {
    name     = %q
    size_tib = 1
  }

  timeouts = {
    create = "90m"
    delete = "60m"
  }
}
`, regionFilter, name, gpu, name+"-data")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: checkGone("togetherai_gpu_cluster", func(ctx context.Context, rs *terraform.ResourceState) error {
			c, err := accClient().GetCluster(ctx, rs.Primary.ID)
			if err == nil && c.Status == client.ClusterStatusDeleting {
				return fmt.Errorf("cluster is deleting")
			}
			return err
		}),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "status", "Ready"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "volumes.#", "1"),
					resource.TestCheckResourceAttrSet("togetherai_gpu_cluster.c", "cuda_version"),
					// Only a project admin's key gets the kubeconfig.
					resource.TestCheckResourceAttrSet("togetherai_gpu_cluster.c", "kube_config"),
				),
			},
			{
				ResourceName:            "togetherai_gpu_cluster.c",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"shared_volume", "nvidia_version_id", "timeouts", "status", "control_plane_nodes", "gpu_worker_nodes"},
			},
		},
	})
}

func mustRe(s string) *regexp.Regexp { return regexp.MustCompile(s) }
