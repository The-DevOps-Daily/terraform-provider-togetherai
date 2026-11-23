package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestMockDataSources(t *testing.T) {
	_, provider := newMock(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: provider + `
data "togetherai_projects" "all" {}
data "togetherai_gpu_cluster_regions" "all" {}
data "togetherai_hardware" "all" {}
data "togetherai_supported_models" "all" {}
data "togetherai_supported_models" "dedicated_text" {
  product  = "PRODUCT_DEDICATED"
  modality = "MODALITY_TEXT"
}
data "togetherai_supported_models" "qwen" {
  search = "QWEN"
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.togetherai_projects.all", "projects.#", "1"),
				resource.TestCheckResourceAttr("data.togetherai_projects.all", "projects.0.slug", "mock-project"),
				resource.TestCheckResourceAttr("data.togetherai_gpu_cluster_regions.all", "regions.#", "2"),
				resource.TestCheckResourceAttr("data.togetherai_gpu_cluster_regions.all", "regions.0.name", "us-central-8"),
				resource.TestCheckResourceAttr("data.togetherai_gpu_cluster_regions.all", "regions.0.supported_instance_types.#", "2"),
				resource.TestCheckResourceAttr("data.togetherai_gpu_cluster_regions.all", "regions.0.driver_versions.0.id", "nv_570_128"),
				resource.TestCheckResourceAttr("data.togetherai_hardware.all", "hardware.#", "2"),
				resource.TestCheckResourceAttr("data.togetherai_hardware.all", "hardware.0.id", "1xnvidia-h100-80gb"),
				resource.TestCheckResourceAttr("data.togetherai_hardware.all", "hardware.0.price_cents_per_hour", "549"),
				resource.TestCheckResourceAttr("data.togetherai_hardware.all", "hardware.0.regions.0", "us-east-1"),
				// The mock pages by 2, so 3 models means the cursor was followed.
				resource.TestCheckResourceAttr("data.togetherai_supported_models.all", "models.#", "3"),
				resource.TestCheckResourceAttr("data.togetherai_supported_models.dedicated_text", "models.#", "2"),
				resource.TestCheckResourceAttr("data.togetherai_supported_models.qwen", "models.#", "1"),
				resource.TestCheckResourceAttr("data.togetherai_supported_models.qwen", "models.0.deployment_profiles.0.config", "projects/proj_public/configs/cr_qwen_fp8"),
				resource.TestCheckResourceAttr("data.togetherai_supported_models.qwen", "models.0.deployment_profiles.0.quantization", "FP8"),
			),
		}},
	})
}

func TestMockSupportedModelsFilterValidation(t *testing.T) {
	_, provider := newMock(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      provider + `data "togetherai_supported_models" "x" { product = "DEDICATED" }`,
			ExpectError: regexp.MustCompile(`value must be one of`),
		}},
	})
}

func TestMockMissingAPIKey(t *testing.T) {
	m, _ := newMock(t)
	noKey := `
provider "togetherai" {
  base_url = "` + m.URL() + `"
}
resource "togetherai_shared_volume" "v" {
  name     = "v"
  size_tib = 1
  region   = "us-central-8"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Planning a new resource does not need the key.
				Config:             noKey,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config:      noKey,
				ExpectError: regexp.MustCompile(`missing Together AI API key`),
			},
		},
	})
}

func TestMockWrongAPIKey(t *testing.T) {
	m, _ := newMock(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
provider "togetherai" {
  api_key  = "wrong"
  base_url = "` + m.URL() + `"
}
data "togetherai_hardware" "all" {}
`,
			ExpectError: regexp.MustCompile(`(?s)HTTP 401.*Invalid API key`),
		}},
	})
}
