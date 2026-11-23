package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// These tests run real terraform plan/apply/import/destroy cycles against the
// in-process mock API. Every apply step is followed by an empty-plan check by
// the test framework, which catches perpetual diffs and inconsistent results.

func TestMockSharedVolume(t *testing.T) {
	m, provider := newMock(t)
	cfg := func(size int) string {
		return provider + fmt.Sprintf(`
resource "togetherai_shared_volume" "data" {
  name     = "tf-data"
  size_tib = %d
  region   = "us-central-8"
}
`, size)
	}
	var id string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			m.Lock()
			defer m.Unlock()
			if len(m.Volumes) != 0 {
				return fmt.Errorf("volumes left after destroy: %v", m.Volumes)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_shared_volume.data", "status", "available"),
					resource.TestCheckResourceAttr("togetherai_shared_volume.data", "size_tib", "1"),
					resource.TestCheckResourceAttrWith("togetherai_shared_volume.data", "id", func(v string) error { id = v; return nil }),
				),
			},
			{
				// Grow in place.
				Config: cfg(2),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_shared_volume.data", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_shared_volume.data", "size_tib", "2"),
					resource.TestCheckResourceAttr("togetherai_shared_volume.data", "status", "available"),
				),
			},
			{
				// Shrinking is refused at plan time, before the API is called.
				Config:      cfg(1),
				ExpectError: regexp.MustCompile(`Cannot shrink a shared volume`),
			},
			{
				ResourceName:            "togetherai_shared_volume.data",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"region", "timeouts"},
			},
			{
				// Deleted in the console: Terraform plans a new volume.
				PreConfig: func() { m.DeleteVolumeNow(id) },
				Config:    cfg(2),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_shared_volume.data", plancheck.ResourceActionCreate),
				}},
				Check: resource.TestCheckResourceAttrWith("togetherai_shared_volume.data", "id", func(v string) error {
					if v == id {
						return fmt.Errorf("id %s was not replaced", v)
					}
					return nil
				}),
			},
		},
	})
}

func TestMockSharedVolumeValidation(t *testing.T) {
	_, provider := newMock(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: provider + `
resource "togetherai_shared_volume" "bad" {
  name     = "Bad_Name"
  size_tib = 1
  region   = "us-central-8"
}
`,
			ExpectError: regexp.MustCompile(`lowercase letters, digits, hyphens or dots`),
		}},
	})
}

const clusterBase = `
resource "togetherai_gpu_cluster" "c" {
  name              = "tf-k8s"
  cluster_type      = "KUBERNETES"
  region            = "us-central-8"
  gpu_type          = "H100_SXM"
  billing_type      = "ON_DEMAND"
  nvidia_version_id = "nv_570_128"
  num_gpus          = %d
  num_preemptible_gpus = %d

  shared_volume = {
    name     = "tf-k8s-data"
    size_tib = 1
  }
}

data "togetherai_gpu_cluster" "c" {
  id = togetherai_gpu_cluster.c.id
}

output "kubeconfig" {
  value     = togetherai_gpu_cluster.c.kube_config
  sensitive = true
}
`

func TestMockGPUCluster(t *testing.T) {
	m, provider := newMock(t)
	cfg := func(gpus, preemptible int) string { return provider + fmt.Sprintf(clusterBase, gpus, preemptible) }
	var id string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			m.Lock()
			defer m.Unlock()
			if len(m.Clusters) != 0 || len(m.Volumes) != 0 {
				return fmt.Errorf("left after destroy: %d clusters, %d volumes", len(m.Clusters), len(m.Volumes))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					// Transient errors while polling must not fail the apply.
					m.FailNext("GET /v1/compute/clusters/", 503, 2)
				},
				Config: cfg(8, 0),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "status", "Ready"),
					resource.TestCheckResourceAttrSet("togetherai_gpu_cluster.c", "kube_config"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "cuda_version", "12.8"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "nvidia_driver_version", "570"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "volumes.#", "1"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "volumes.0.volume_name", "tf-k8s-data"),
					resource.TestCheckResourceAttrPair("togetherai_gpu_cluster.c", "volume_id", "togetherai_gpu_cluster.c", "volumes.0.volume_id"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "gpu_worker_nodes.#", "1"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "control_plane_nodes.#", "1"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "project_id", "proj_mock1"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "num_reserved_gpus", "0"),
					resource.TestCheckResourceAttr("data.togetherai_gpu_cluster.c", "status", "Ready"),
					resource.TestCheckResourceAttr("data.togetherai_gpu_cluster.c", "volume_ids.#", "1"),
					resource.TestCheckResourceAttrSet("data.togetherai_gpu_cluster.c", "kube_config"),
					resource.TestCheckResourceAttrWith("togetherai_gpu_cluster.c", "id", func(v string) error { id = v; return nil }),
				),
			},
			{
				// Scale up and add preemptible capacity in place.
				Config: cfg(16, 8),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_gpu_cluster.c", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "num_gpus", "16"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "current_num_gpus", "16"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "num_preemptible_gpus", "8"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "status", "Ready"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "gpu_worker_nodes.#", "2"),
					resource.TestCheckResourceAttrWith("togetherai_gpu_cluster.c", "id", func(v string) error {
						if v != id {
							return fmt.Errorf("cluster was replaced: %s -> %s", id, v)
						}
						return nil
					}),
				),
			},
			{
				// A GPU count that is not whole nodes fails at plan time.
				Config:      cfg(12, 8),
				ExpectError: regexp.MustCompile(`must be a multiple of 8`),
			},
			{
				ResourceName:      "togetherai_gpu_cluster.c",
				ImportState:       true,
				ImportStateVerify: true,
				// Create-only inputs the API does not echo back.
				ImportStateVerifyIgnore: []string{"shared_volume", "nvidia_version_id", "timeouts"},
			},
			{
				// Changing a create-only field replaces the cluster.
				Config: provider + regexp.MustCompile(`tf-k8s-data`).ReplaceAllString(fmt.Sprintf(clusterBase, 16, 8), "tf-k8s-data2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_gpu_cluster.c", plancheck.ResourceActionDestroyBeforeCreate),
				}},
				Check: resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "volumes.0.volume_name", "tf-k8s-data2"),
			},
			{
				// Deleted in the console: the next plan creates it again.
				PreConfig: func() {
					m.Lock()
					var ids []string
					for cid := range m.Clusters {
						ids = append(ids, cid)
					}
					m.Unlock()
					for _, cid := range ids {
						m.DeleteClusterNow(cid)
					}
				},
				Config: provider + regexp.MustCompile(`tf-k8s-data`).ReplaceAllString(fmt.Sprintf(clusterBase, 16, 8), "tf-k8s-data2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_gpu_cluster.c", plancheck.ResourceActionCreate),
				}},
				Check: resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "status", "Ready"),
			},
		},
	})
}

func TestMockGPUClusterWithStandaloneVolume(t *testing.T) {
	m, provider := newMock(t)
	cfg := provider + `
data "togetherai_gpu_cluster_regions" "all" {}

locals {
  region = [for r in data.togetherai_gpu_cluster_regions.all.regions : r if contains(r.supported_instance_types, "H100_SXM")][0]
}

resource "togetherai_shared_volume" "data" {
  name     = "tf-slurm-data"
  size_tib = 2
  region   = local.region.name
}

resource "togetherai_gpu_cluster" "slurm" {
  name                  = "tf-slurm"
  cluster_type          = "SLURM"
  slurm_shm_size_gib    = 64
  region                = local.region.name
  gpu_type              = "H100_SXM"
  billing_type          = "ON_DEMAND"
  cuda_version          = local.region.driver_versions[0].cuda_version
  nvidia_driver_version = local.region.driver_versions[0].nvidia_driver_version
  num_gpus              = 8
  volume_id             = togetherai_shared_volume.data.id
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			m.Lock()
			defer m.Unlock()
			if len(m.Clusters) != 0 || len(m.Volumes) != 0 {
				return fmt.Errorf("left after destroy: %d clusters, %d volumes", len(m.Clusters), len(m.Volumes))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.slurm", "status", "Ready"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.slurm", "cluster_type", "SLURM"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.slurm", "slurm_shm_size_gib", "64"),
					resource.TestCheckResourceAttrPair("togetherai_gpu_cluster.slurm", "volume_id", "togetherai_shared_volume.data", "id"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.slurm", "volumes.0.status", "bound"),
				),
			},
			{
				// The volume's status moved to "bound"; that is not drift.
				Config:   cfg,
				PlanOnly: true,
			},
			{
				// Switching to Kubernetes is an in-place update in the API.
				Config: regexp.MustCompile(`"SLURM"`).ReplaceAllString(cfg, `"KUBERNETES"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("togetherai_gpu_cluster.slurm", plancheck.ResourceActionUpdate),
				}},
				Check: resource.TestCheckResourceAttr("togetherai_gpu_cluster.slurm", "cluster_type", "KUBERNETES"),
			},
		},
	})
}

func TestMockGPUClusterValidation(t *testing.T) {
	_, provider := newMock(t)
	cases := map[string]struct{ body, err string }{
		"no volume": {`
  billing_type = "ON_DEMAND"
  nvidia_version_id = "nv_570_128"
`, `(?s)volume_id.*shared_volume|shared_volume.*volume_id`},
		"no driver": {`
  billing_type = "ON_DEMAND"
  volume_id = "vol_1"
`, `(?s)nvidia_version_id`},
		"cuda without driver": {`
  billing_type = "ON_DEMAND"
  volume_id = "vol_1"
  cuda_version = "12.8"
`, `(?s)nvidia_driver_version`},
		"slurm without shm": {`
  billing_type = "ON_DEMAND"
  volume_id = "vol_1"
  nvidia_version_id = "nv_570_128"
  cluster_type = "SLURM"
`, `slurm_shm_size_gib is required`},
		"bad gpu": {`
  billing_type = "ON_DEMAND"
  volume_id = "vol_1"
  nvidia_version_id = "nv_570_128"
  gpu_type = "A100"
`, `gpu_type value must be one of`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			gpu := `gpu_type = "H100_SXM"`
			if regexp.MustCompile(`gpu_type`).MatchString(tc.body) {
				gpu = ""
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config: provider + `
resource "togetherai_gpu_cluster" "c" {
  name     = "x"
  region   = "us-central-8"
  num_gpus = 8
  ` + gpu + tc.body + `
}
`,
					ExpectError: regexp.MustCompile(tc.err),
				}},
			})
		})
	}
}

func TestMockGPUClusterAutoscaleLocksGPUCount(t *testing.T) {
	m, provider := newMock(t)
	cfg := func(gpus int) string {
		return provider + fmt.Sprintf(`
resource "togetherai_gpu_cluster" "c" {
  name                = "tf-auto"
  region              = "us-central-8"
  gpu_type            = "H100_SXM"
  billing_type        = "ON_DEMAND"
  nvidia_version_id   = "nv_570_128"
  num_gpus            = %d
  auto_scale          = true
  auto_scale_max_gpus = 32
  shared_volume = {
    name     = "tf-auto"
    size_tib = 1
  }
}
`, gpus)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg(8)},
			{
				// The autoscaler scaled the cluster up: not drift.
				PreConfig: func() {
					m.Lock()
					defer m.Unlock()
					for _, c := range m.Clusters {
						c.Info["num_gpus"] = int64(24)
					}
				},
				Config: cfg(8),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "num_gpus", "8"),
					resource.TestCheckResourceAttr("togetherai_gpu_cluster.c", "current_num_gpus", "24"),
				),
			},
			{
				Config:      cfg(16),
				ExpectError: regexp.MustCompile(`Cannot change num_gpus on an autoscaled cluster`),
			},
		},
	})
}

func TestMockGPUClusterCreateFailureKeepsState(t *testing.T) {
	m, provider := newMock(t)
	m.StepsPerState = 1000000 // never becomes Ready
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			m.Lock()
			defer m.Unlock()
			if len(m.Clusters) != 0 {
				return fmt.Errorf("%d clusters left after destroy", len(m.Clusters))
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config: provider + `
resource "togetherai_gpu_cluster" "c" {
  name              = "tf-slow"
  region            = "us-central-8"
  gpu_type          = "H100_SXM"
  billing_type      = "ON_DEMAND"
  nvidia_version_id = "nv_570_128"
  num_gpus          = 8
  shared_volume = {
    name     = "tf-slow"
    size_tib = 1
  }
  timeouts = {
    create = "1s"
  }
}
`,
			ExpectError: regexp.MustCompile(`(?s)did not reach status Ready.*WaitingForControlPlaneNodes`),
		}},
	})
	// The failed create left the cluster in state (tainted), so the test's
	// destroy sent DELETE for it.
	m.Lock()
	defer m.Unlock()
	if m.Requests["DELETE /v1/compute/clusters/{cluster_id}"] != 1 {
		t.Fatalf("DELETE requests = %d, want 1", m.Requests["DELETE /v1/compute/clusters/{cluster_id}"])
	}
}
